// Package scripting embeds QuickJS compiled to WebAssembly and evaluates untrusted
// JavaScript under hard, enforceable memory and wall-clock bounds using the pure-Go
// wazero runtime — so a hostile or buggy script (new Array(1e9), a runaway loop)
// cannot OOM or wedge the host process the way an in-process interpreter (goja) can.
//
// This is a SPIKE. Eval is the seam where an (already down-levelled) JS source
// string enters the engine. Capability APIs (std/http, console, ...) would later be
// injected as wasm host-function imports and surfaced onto the JS global object; none
// are wired here. The guest exposes a tiny C ABI (see third_party/quickjs/qjs_wasm.c):
//
//	qjs_malloc(n)              -> ptr        allocate n bytes of guest linear memory
//	qjs_free(ptr)                            free a guest / result pointer
//	qjs_eval(ptr, len, limit) -> resultPtr   eval source[ptr:ptr+len]; result buffer is
//	                                         [tag u8][len u32 LE][payload], tag 1 == throw
package scripting

import (
	"context"
	_ "embed"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

//go:embed quickjs.wasm
var quickjsWasm []byte

// WasmPageSize is the WebAssembly linear-memory page size (64 KiB).
const WasmPageSize = 65536

// qjs_eval returns a result buffer laid out as [tag u8][len u32 LE][payload];
// these name that ABI, shared with third_party/quickjs/qjs_wasm.c (tag 0 = value).
const (
	resultHeaderSize       = 5 // tag (1 byte) + payload length (4 bytes, LE)
	tagValue         uint8 = 0 // tag marking the payload as a value / result bytes
	tagThrow         uint8 = 1 // tag marking the payload as an exception message
)

// ErrInterrupted is returned when an evaluation is stopped by the host because its
// context was cancelled or its deadline elapsed (the wall-clock backstop).
var ErrInterrupted = errors.New("scripting: evaluation interrupted")

// JSError is a JavaScript exception that propagated out of an evaluation (including
// QuickJS's own "out of memory" InternalError when JS_SetMemoryLimit is exceeded).
type JSError struct{ Message string }

func (e *JSError) Error() string { return "scripting: uncaught " + e.Message }

// Runtime is a compiled-once, instantiate-many QuickJS engine. Compilation of the
// ~660 KiB module is the expensive step; instances are cheap and disposable, so each
// script run gets a fresh instance (and the shim gives each eval a fresh JSRuntime),
// while long-lived callers can pool instances for warm latency. Safe for concurrent
// Instantiate/EvalIsolated calls.
type Runtime struct {
	rt       wazero.Runtime
	compiled wazero.CompiledModule
}

// New builds a Runtime whose instances may grow linear memory to at most maxPages
// 64 KiB pages. That ceiling is the OUTER backstop: it is enforced by wazero itself,
// independently of (and underneath) QuickJS's own JS_SetMemoryLimit accounting, so it
// still holds even if the guest's allocator bookkeeping were bypassed or buggy.
func New(ctx context.Context, maxPages uint32) (*Runtime, error) {
	cfg := wazero.NewRuntimeConfig().
		WithCloseOnContextDone(true). // lets ctx cancel/deadline interrupt a running guest
		WithMemoryLimitPages(maxPages)

	rt := wazero.NewRuntimeWithConfig(ctx, cfg)

	// QuickJS was built for wasm32-wasi (reactor); it needs WASI preview1 for libc
	// (clock, exit, stubbed stdio). We deliberately grant NO filesystem, args, or env
	// below, so despite linking libc the sandbox cannot touch the host.
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, rt); err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("scripting: instantiate wasi: %w", err)
	}

	// LAYER A: the capability host module. quickjs.wasm statically imports these
	// (see qjs_wasm.c), so they must be present at every instantiation — a fresh
	// per-run instance always resolves them. Whether a call does real I/O or is
	// refused is decided per-invocation from the grant carried on the context
	// (grantFromContext); an ungranted call is refused HERE, in Go, before any
	// syscall. This is the second of the two independent gates.
	if err := registerHostModule(ctx, rt); err != nil {
		_ = rt.Close(ctx)
		return nil, err
	}

	compiled, err := rt.CompileModule(ctx, quickjsWasm)
	if err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("scripting: compile quickjs.wasm: %w", err)
	}
	return &Runtime{rt: rt, compiled: compiled}, nil
}

// Close releases the runtime and all instances derived from it.
func (r *Runtime) Close(ctx context.Context) error { return r.rt.Close(ctx) }

// Instance is a single live QuickJS wasm instance. Reuse one for warm, low-latency
// back-to-back evals (each eval still runs in a fresh JSRuntime inside the guest, so
// evals do not share JS state); use a fresh Instance per run for maximum isolation.
// An Instance is not safe for concurrent use.
type Instance struct {
	mod    api.Module
	malloc api.Function
	free   api.Function
	eval   api.Function
	mem    api.Memory
}

// Instantiate creates a fresh QuickJS instance. moduleConfig grants no FS/args/env.
func (r *Runtime) Instantiate(ctx context.Context) (*Instance, error) {
	// Empty name => anonymous, so many instances of the one compiled module coexist.
	// WithStartFunctions("_initialize") runs the reactor's libc/ctor init at start.
	cfg := wazero.NewModuleConfig().
		WithName("").
		WithStartFunctions("_initialize")

	mod, err := r.rt.InstantiateModule(ctx, r.compiled, cfg)
	if err != nil {
		return nil, fmt.Errorf("scripting: instantiate: %w", err)
	}

	inst := &Instance{
		mod:    mod,
		malloc: mod.ExportedFunction("qjs_malloc"),
		free:   mod.ExportedFunction("qjs_free"),
		eval:   mod.ExportedFunction("qjs_eval"),
		mem:    mod.Memory(),
	}
	if inst.malloc == nil || inst.free == nil || inst.eval == nil || inst.mem == nil {
		_ = mod.Close(ctx)
		return nil, errors.New("scripting: quickjs.wasm missing expected exports")
	}
	return inst, nil
}

// Close frees the instance's linear memory and resources.
func (i *Instance) Close(ctx context.Context) error { return i.mod.Close(ctx) }

// MemoryPages reports the instance's current linear-memory size in 64 KiB pages —
// used by the smoke test to size the outer ceiling relative to the real footprint.
func (i *Instance) MemoryPages() uint32 { return i.mem.Size() / WasmPageSize }

// Eval evaluates src and returns its result String()-ified. memLimitBytes is the
// INNER bound handed to JS_SetMemoryLimit (0 disables it, leaving only the outer
// wazero page ceiling). A JS exception is returned as *JSError; a context
// cancellation/deadline as ErrInterrupted.
func (i *Instance) Eval(ctx context.Context, src string, memLimitBytes uint64) (string, error) {
	// Copy source into guest memory.
	mallocRet, err := i.malloc.Call(ctx, uint64(len(src)))
	if err != nil {
		if ctx.Err() != nil {
			return "", ErrInterrupted
		}
		return "", err
	}
	srcPtr := uint32(mallocRet[0])
	if len(src) > 0 && !i.mem.WriteString(srcPtr, src) {
		_, _ = i.free.Call(ctx, uint64(srcPtr))
		return "", errors.New("scripting: failed to write source into guest memory")
	}

	rets, err := i.eval.Call(ctx, uint64(srcPtr), uint64(len(src)), memLimitBytes)
	_, _ = i.free.Call(context.WithoutCancel(ctx), uint64(srcPtr)) // best-effort
	if err != nil {
		if ctx.Err() != nil {
			return "", ErrInterrupted
		}
		return "", fmt.Errorf("scripting: eval trap: %w", err)
	}

	resPtr := uint32(rets[0])
	if resPtr == 0 {
		return "", errors.New("scripting: guest returned null (out of memory)")
	}
	defer i.free.Call(context.WithoutCancel(ctx), uint64(resPtr))

	header, ok := i.mem.Read(resPtr, resultHeaderSize)
	if !ok {
		return "", errors.New("scripting: result header out of range")
	}
	tag := header[0]
	n := binary.LittleEndian.Uint32(header[1:resultHeaderSize])
	payload, ok := i.mem.Read(resPtr+resultHeaderSize, n)
	if !ok {
		return "", errors.New("scripting: result payload out of range")
	}
	out := string(payload)
	if tag == tagThrow {
		return "", &JSError{Message: out}
	}
	return out, nil
}

// EvalIsolated instantiates a fresh instance, evaluates src on it, and tears it down.
// This is the max-isolation, one-instance-per-run path; the cost of the extra
// instantiate/teardown vs a pooled Instance.Eval is measured in the smoke test.
func (r *Runtime) EvalIsolated(ctx context.Context, src string, memLimitBytes uint64) (string, error) {
	inst, err := r.Instantiate(ctx)
	if err != nil {
		return "", err
	}
	defer inst.Close(context.WithoutCancel(ctx))
	return inst.Eval(ctx, src, memLimitBytes)
}

// ---- Capability layer ------------------------------------------------------------
//
// grpcview runs UNTRUSTED scripts, so capabilities are default-DENY and cross into
// real I/O only through two independent gates, both derived from one Grant:
//
//	Gate 1 (bundle time, Bundle): the std/* module for a capability is injected into
//	  the script ONLY if granted. Ungranted => `import fs from "node:fs"` does not
//	  resolve => there is no call site at all. This is the strongest "genuinely absent".
//	Gate 2 (call time, the env host functions): even if a call site existed, the Go
//	  host function refuses an ungranted (or out-of-scope) request BEFORE any syscall.
//
// Either gate alone denies. Enforcement (grant + scope + syscall) is entirely in Go,
// outside the sandbox, so the module never holds a capability the script can reach.

// Grant is the set of capabilities a single script run is allowed. In the real system
// a Grant is pinned to the script's content hash and is off by default; here it is
// passed directly to RunScript. A nil sub-grant means that capability is not granted.
type Grant struct {
	FS  *FSGrant // filesystem read, scoped to an allowlist; nil => no fs
	Net *NetGrant // network (stubbed in this spike); nil => no net
}

// FSGrant scopes the fs capability to an allowlist of paths. Scope lives HERE, in Go:
// an out-of-scope path is refused in the host function, never inside the sandbox.
type FSGrant struct {
	// AllowedPaths permits an exact file or anything under an allowed directory.
	AllowedPaths []string
}

// allows reports whether cleaned (an already filepath.Clean'd path) is in scope.
// NOTE: this is prefix containment on cleaned paths; a hardened build would also
// filepath.EvalSymlinks and confirm the result stays under a real root (see the doc).
func (f *FSGrant) allows(cleaned string) bool {
	for _, p := range f.AllowedPaths {
		pc := filepath.Clean(p)
		if cleaned == pc || strings.HasPrefix(cleaned, pc+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// NetGrant marks the (stubbed) net capability as granted; its presence is the grant.
type NetGrant struct{}

// fsRead is the actual privileged operation: grant check, scope check, then the
// syscall — all in Go. Returned errors become catchable JS exceptions in the guest.
func (g Grant) fsRead(path string) ([]byte, error) {
	if g.FS == nil {
		return nil, errors.New(`capability "fs" not granted`)
	}
	cleaned := filepath.Clean(path)
	if !g.FS.allows(cleaned) {
		return nil, fmt.Errorf("fs: path %q not in allowlist", path)
	}
	return os.ReadFile(cleaned)
}

// grantCtxKey carries the running script's Grant on the context. The host functions
// are shared across instances (one runtime), so per-run scope rides on ctx — the
// script cannot forge it. Combined with a fresh instance per run, this is per-run
// wiring in effect.
type grantCtxKey struct{}

// WithGrant returns a context carrying g for the host functions to enforce against.
// RunScript sets this automatically; it is exported so tests can drive Eval directly.
func WithGrant(ctx context.Context, g Grant) context.Context {
	return context.WithValue(ctx, grantCtxKey{}, g)
}

func grantFromContext(ctx context.Context) Grant {
	if g, ok := ctx.Value(grantCtxKey{}).(Grant); ok {
		return g
	}
	return Grant{} // default deny: no sub-grants
}

// registerHostModule installs the "env" module quickjs.wasm imports. Both functions
// share the ONE uniform ABI: request bytes at (ptr,len); a [tag|len|payload] result
// written back into guest memory via the guest's own qjs_malloc. See qjs_wasm.c.
func registerHostModule(ctx context.Context, rt wazero.Runtime) error {
	params := []api.ValueType{api.ValueTypeI32, api.ValueTypeI32} // reqPtr, reqLen
	results := []api.ValueType{api.ValueTypeI32}                  // resultPtr
	_, err := rt.NewHostModuleBuilder("env").
		NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(hostFSRead), params, results).
		Export("host_fs_read").
		NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(hostNetFetch), params, results).
		Export("host_net_fetch").
		Instantiate(ctx)
	if err != nil {
		return fmt.Errorf("scripting: instantiate host module: %w", err)
	}
	return nil
}

// hostFSRead is Layer A for fs: read the request path, enforce the grant + scope in
// Go, then read the file — or write back an error the guest re-throws as a JS exception.
func hostFSRead(ctx context.Context, mod api.Module, stack []uint64) {
	req, ok := mod.Memory().Read(uint32(stack[0]), uint32(stack[1]))
	if !ok {
		stack[0] = uint64(writeResult(ctx, mod, tagThrow, []byte("fs: bad request pointer")))
		return
	}
	data, err := grantFromContext(ctx).fsRead(string(req))
	if err != nil {
		stack[0] = uint64(writeResult(ctx, mod, tagThrow, []byte(err.Error())))
		return
	}
	stack[0] = uint64(writeResult(ctx, mod, tagValue, data))
}

// hostNetFetch is a STUB proving the same ABI carries a second capability. Granted, it
// echoes; ungranted, it refuses. A real net cap would do the request off-thread and
// resolve a JS Promise via a host-driven job pump (see the spike doc's async note).
func hostNetFetch(ctx context.Context, mod api.Module, stack []uint64) {
	req, _ := mod.Memory().Read(uint32(stack[0]), uint32(stack[1]))
	if grantFromContext(ctx).Net == nil {
		stack[0] = uint64(writeResult(ctx, mod, tagThrow, []byte(`capability "net" not granted`)))
		return
	}
	stack[0] = uint64(writeResult(ctx, mod, tagValue, []byte("stub-fetched:"+string(req))))
}

// writeResult places a [tag u8][len u32 LE][payload] buffer into GUEST memory by
// calling the guest's own qjs_malloc (the component-model cabi_realloc pattern) and
// returns its pointer, which the guest reads and frees. Returns 0 on host-side OOM,
// which the guest turns into an InternalError. This is the single marshalling helper
// every capability reuses.
func writeResult(ctx context.Context, mod api.Module, tag uint8, payload []byte) uint32 {
	malloc := mod.ExportedFunction("qjs_malloc")
	if malloc == nil {
		return 0
	}
	total := resultHeaderSize + len(payload)
	rets, err := malloc.Call(ctx, uint64(total))
	if err != nil || len(rets) == 0 {
		return 0 // e.g. context cancelled mid-call, or guest OOM
	}
	ptr := uint32(rets[0])
	if ptr == 0 {
		return 0
	}
	buf := make([]byte, total)
	buf[0] = tag
	binary.LittleEndian.PutUint32(buf[1:resultHeaderSize], uint32(len(payload)))
	copy(buf[resultHeaderSize:], payload)
	if !mod.Memory().Write(ptr, buf) {
		return 0
	}
	return ptr
}

// stdModule is one entry in the (deliberately tiny) module registry Bundle resolves
// against. A nil `granted` means the module is INERT (pure computation, no capability):
// it is always injected and needs no grant. A non-nil `granted` means the module is a
// capability: injected only when the grant permits it (Gate 1).
type stdModule struct {
	shim    string
	granted func(Grant) bool
}

// The vendored node:* shims. Inert modules are pure JS. Capability modules are the
// ergonomic JS surface (Layer B's JS side) over the __grpcview_* marshallers that
// qjs_wasm.c registers; they do no I/O themselves. Node names/shapes are used so
// third-party libraries importing these builtins resolve against our shims.
const (
	// node:path — INERT: pure string ops, no capability. (Demonstrates "works with no grant".)
	pathShim = `({join:function(){return Array.prototype.slice.call(arguments).join("/").replace(/\/+/g,"/");},` +
		`basename:function(p){p=String(p);var i=p.lastIndexOf("/");return i<0?p:p.slice(i+1);}})`
	// node:fs — CAPABILITY: readFileSync marshals to the fs import. Returns a string
	// (the spike proves the seam; a full shim would honour the encoding arg / return a Buffer).
	fsShim = `({readFileSync:function(p,enc){return globalThis.__grpcview_fs_read(String(p));}})`
	// node:net — CAPABILITY (stubbed): same shape, different import.
	netShim = `({fetch:function(u){return globalThis.__grpcview_net_fetch(String(u));}})`
)

var stdModules = map[string]stdModule{
	"node:path": {shim: pathShim},                                              // inert
	"node:fs":   {shim: fsShim, granted: func(g Grant) bool { return g.FS != nil }},
	"node:net":  {shim: netShim, granted: func(g Grant) bool { return g.Net != nil }},
}

// importRe matches a default import of a bare module, e.g. `import fs from "node:fs"`.
// The import statement is what makes a capability request STATICALLY VISIBLE — Bundle
// (and, in production, the consent scanner) can enumerate exactly what a script asks
// for. Inter-token spacing is horizontal-only ([ \t]) so a match never spans lines; the
// statement ends at an optional ';' (any trailing code on the same line is left intact).
// A real bundler parses the module graph; this scan is the spike stand-in and does not
// see imports hidden in comments/strings — noted in the design doc.
var importRe = regexp.MustCompile(`import[ \t]+(\w+)[ \t]+from[ \t]+["']([^"']+)["'][ \t]*;?`)

// Bundle is GATE 1: a deliberately tiny stand-in for esbuild's grant-gated resolver
// (NOT the real bundler — npm resolution, the content-addressed store, tree-shaking
// and sourcemaps are out of scope). It scans the static `import ... from "..."` lines,
// injects the vendored shim for each INERT module and each GRANTED capability module,
// and REFUSES to resolve a capability module that is not granted (or an unknown module).
// A refusal means the script cannot even be assembled — there is no call site.
func Bundle(userSrc string, g Grant) (string, error) {
	var prelude strings.Builder
	var bundleErr error
	body := importRe.ReplaceAllStringFunc(userSrc, func(line string) string {
		m := importRe.FindStringSubmatch(line)
		local, name := m[1], m[2]
		mod, ok := stdModules[name]
		if !ok {
			if bundleErr == nil {
				bundleErr = fmt.Errorf("scripting: cannot resolve %q: unknown module", name)
			}
			return ""
		}
		if mod.granted != nil && !mod.granted(g) {
			if bundleErr == nil {
				bundleErr = fmt.Errorf("scripting: cannot resolve %q: capability not granted", name)
			}
			return ""
		}
		fmt.Fprintf(&prelude, "const %s = %s;\n", local, mod.shim)
		return "" // strip the import line; the shim is inlined into the prelude
	})
	if bundleErr != nil {
		return "", bundleErr
	}
	return prelude.String() + body, nil
}

// RunScript is the capability entry point: bundle the script against the grant (Gate 1),
// instantiate a fresh isolated instance, and evaluate with the grant on the context so
// the host functions enforce it (Gate 2). memLimitBytes is the inner QuickJS heap cap
// (0 disables it; the outer wazero page ceiling from New still applies).
func (r *Runtime) RunScript(ctx context.Context, userSrc string, g Grant, memLimitBytes uint64) (string, error) {
	bundled, err := Bundle(userSrc, g)
	if err != nil {
		return "", err // Gate 1 denial: ungranted/unknown module did not resolve
	}
	inst, err := r.Instantiate(ctx)
	if err != nil {
		return "", err
	}
	defer inst.Close(context.WithoutCancel(ctx))
	return inst.EvalWithGrant(ctx, bundled, g, memLimitBytes)
}

// EvalWithGrant evaluates already-bundled source with g in force for the host functions.
// RunScript uses it; tests use it directly to exercise Gate 2 in isolation (e.g. bundle
// with a capability granted, then evaluate under a grant that lacks it).
func (i *Instance) EvalWithGrant(ctx context.Context, src string, g Grant, memLimitBytes uint64) (string, error) {
	return i.Eval(WithGrant(ctx, g), src, memLimitBytes)
}
