// Package scripting embeds QuickJS compiled to WebAssembly and evaluates untrusted
// JavaScript under hard, enforceable memory and wall-clock bounds using the pure-Go
// wazero runtime — so a hostile or buggy script (new Array(1e9), a runaway loop)
// cannot OOM or wedge the host process the way an in-process interpreter (goja) can.
//
// The guest exposes a small STATE-MACHINE ABI (see third_party/quickjs/qjs_wasm.c) so
// the host can drive async execution: create a context, evaluate, pump the microtask
// queue until the top-level Promise settles (or the wall-clock deadline fires), then
// marshal the settled value. Three layers build on this seam:
//
//   - the low-level ABI calls (newContext/callEval/callPump/callResult/disposeContext)
//     and the evalRaw pump loop, here;
//   - the capability system (Grant + Bundle + the narrow Go host functions), here;
//   - structured I/O, the three execution profiles, and the warm pool, in the sibling
//     marshal.go / profiles.go / pool.go files.
//
// The guest C ABI:
//
//	qjs_malloc(n) -> ptr / qjs_free(ptr)          guest linear-memory alloc/free
//	qjs_new(memLimit) -> status                   create this instance's runtime+context
//	qjs_dispose()                                 tear it down
//	qjs_eval(ptr,len,async) -> status             evaluate; status 0 DONE / 1 PENDING / 2 ERROR
//	qjs_pump() -> status                          drain the job queue; re-check the promise
//	qjs_result(asJSON) -> resultPtr               [tag u8][len u32 LE][payload]
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

// The result-buffer ABI: qjs_result returns [tag u8][len u32 LE][payload]; these name
// that layout, shared with third_party/quickjs/qjs_wasm.c.
const (
	resultHeaderSize       = 5 // tag (1 byte) + payload length (4 bytes, LE)
	tagValue         uint8 = 0 // payload is the value (JSON text or String()-ified)
	tagThrow         uint8 = 1 // payload is "message" or "message\nstack"
	tagUndefined     uint8 = 2 // no payload; the value had no JSON representation
)

// qjs_eval / qjs_pump status codes, mirrored from qjs_wasm.c.
const (
	statusDone    = 0 // a settled value is held; fetch it with qjs_result
	statusPending = 1 // the top-level promise is unsettled; pump again
	statusError   = 2 // an exception / rejection is held
)

// ErrInterrupted is returned when an evaluation is stopped by the host because its
// context was cancelled or its deadline elapsed (the wall-clock backstop). The
// instance it ran on is left dead and must be discarded, not reused.
var ErrInterrupted = errors.New("scripting: evaluation interrupted")

// ErrUnsettled is returned when a script's top-level Promise never settles and no
// outstanding host work could advance it — the job queue drained with the promise
// still pending, so waiting for the deadline would be pointless.
var ErrUnsettled = errors.New("scripting: top-level promise did not settle")

// JSError is a JavaScript exception that propagated out of an evaluation (including
// QuickJS's own "out of memory" InternalError when JS_SetMemoryLimit is exceeded).
// Message is the first line (the error's toString); Stack is the raw guest backtrace
// when present, and Line is the source line. For the structured profiles, Line is
// remapped through the compiled script's source map back to the AUTHOR's original line
// (remapJSError, sourcemap.go), undoing the input-prelude offset and esbuild's bundling.
// For the legacy Eval/RunScript path (no source map) Line is the raw line in the evaluated
// string.
type JSError struct {
	Message string
	Stack   string
	Line    int
}

func (e *JSError) Error() string { return "scripting: uncaught " + e.Message }

// Runtime is a compiled-once, instantiate-many QuickJS engine. Compilation of the
// ~660 KiB module is the expensive step; instances are cheap and disposable. Safe for
// concurrent Instantiate/EvalIsolated calls.
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

	// The capability host module. quickjs.wasm statically imports these (see
	// qjs_wasm.c), so they must be present at every instantiation. Whether a call does
	// real I/O or is refused is decided per-invocation from the grant carried on the
	// context; an ungranted call is refused HERE, in Go, before any syscall.
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

// Instance is a single live QuickJS wasm instance. Its JS context lifecycle is driven
// explicitly: newContext creates a fresh JSRuntime+JSContext, disposeContext tears it
// down. Reuse an instance (skip the ~100 µs Instantiate) with a fresh context per run
// for isolation, or hold one long-lived context across runs for warm middleware
// latency (at the cost of shared JS globals). An Instance is not safe for concurrent
// use, and once dead (interrupted or trapped) it must be discarded.
type Instance struct {
	mod     api.Module
	malloc  api.Function
	free    api.Function
	newCtx  api.Function
	dispose api.Function
	eval    api.Function
	pump    api.Function
	result  api.Function
	mem     api.Memory
	dead    bool // true once an interrupt/trap left the module closed or undefined
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
		mod:     mod,
		malloc:  mod.ExportedFunction("qjs_malloc"),
		free:    mod.ExportedFunction("qjs_free"),
		newCtx:  mod.ExportedFunction("qjs_new"),
		dispose: mod.ExportedFunction("qjs_dispose"),
		eval:    mod.ExportedFunction("qjs_eval"),
		pump:    mod.ExportedFunction("qjs_pump"),
		result:  mod.ExportedFunction("qjs_result"),
		mem:     mod.Memory(),
	}
	if inst.malloc == nil || inst.free == nil || inst.newCtx == nil || inst.dispose == nil ||
		inst.eval == nil || inst.pump == nil || inst.result == nil || inst.mem == nil {
		_ = mod.Close(ctx)
		return nil, errors.New("scripting: quickjs.wasm missing expected exports")
	}
	return inst, nil
}

// Close frees the instance's linear memory and resources.
func (i *Instance) Close(ctx context.Context) error { return i.mod.Close(ctx) }

// Dead reports whether the instance was left unusable by an interrupt or trap; the
// warm pool uses this to discard rather than reuse it.
func (i *Instance) Dead() bool { return i.dead }

// MemoryPages reports the instance's current linear-memory size in 64 KiB pages —
// used by the tests to size the outer ceiling relative to the real footprint.
func (i *Instance) MemoryPages() uint32 { return i.mem.Size() / WasmPageSize }

// ---- Low-level guest ABI calls ---------------------------------------------------

// mapErr classifies a wazero Call error. A context deadline/cancel surfaces as
// ErrInterrupted; any other trap (wasm stack exhaustion, a closed module) also leaves
// the instance in an undefined state. Either way the instance is marked dead.
func (i *Instance) mapErr(ctx context.Context, err error) error {
	i.dead = true
	if ctx.Err() != nil {
		return ErrInterrupted
	}
	return fmt.Errorf("scripting: guest trap: %w", err)
}

// newContext creates this instance's JSRuntime+JSContext with the given inner heap cap
// (0 => only the outer wazero page ceiling applies).
func (i *Instance) newContext(ctx context.Context, memLimitBytes uint64) error {
	rets, err := i.newCtx.Call(ctx, memLimitBytes)
	if err != nil {
		return i.mapErr(ctx, err)
	}
	if int32(rets[0]) != 0 {
		i.dead = true
		return errors.New("scripting: qjs_new failed (cannot create runtime/context)")
	}
	return nil
}

// disposeContext tears down the current JS context. Best-effort: a dead instance has
// already had its module closed, so there is nothing (and nothing safe) to dispose.
func (i *Instance) disposeContext(ctx context.Context) {
	if i.dead {
		return
	}
	_, _ = i.dispose.Call(ctx)
}

// callEval copies src into guest memory and evaluates it, returning the status.
func (i *Instance) callEval(ctx context.Context, src string, async bool) (int, error) {
	mallocRet, err := i.malloc.Call(ctx, uint64(len(src)))
	if err != nil {
		return 0, i.mapErr(ctx, err)
	}
	srcPtr := uint32(mallocRet[0])
	if len(src) > 0 && srcPtr == 0 {
		// qjs_malloc returned NULL (guest OOM). Writing to offset 0 would silently
		// clobber low linear memory instead of failing, so reject it here.
		return 0, errors.New("scripting: guest out of memory allocating source")
	}
	if len(src) > 0 && !i.mem.WriteString(srcPtr, src) {
		_, _ = i.free.Call(context.WithoutCancel(ctx), uint64(srcPtr))
		return 0, errors.New("scripting: failed to write source into guest memory")
	}
	var asyncArg uint64
	if async {
		asyncArg = 1
	}
	rets, err := i.eval.Call(ctx, uint64(srcPtr), uint64(len(src)), asyncArg)
	_, _ = i.free.Call(context.WithoutCancel(ctx), uint64(srcPtr)) // best-effort
	if err != nil {
		return 0, i.mapErr(ctx, err)
	}
	return int(int32(rets[0])), nil
}

// callPump drains the guest job queue and re-checks the top-level promise.
func (i *Instance) callPump(ctx context.Context) (int, error) {
	rets, err := i.pump.Call(ctx)
	if err != nil {
		return 0, i.mapErr(ctx, err)
	}
	return int(int32(rets[0])), nil
}

// callResult fetches and copies the settled result envelope out of guest memory.
func (i *Instance) callResult(ctx context.Context, asJSON bool) (uint8, []byte, error) {
	var jsonArg uint64
	if asJSON {
		jsonArg = 1
	}
	rets, err := i.result.Call(ctx, jsonArg)
	if err != nil {
		return 0, nil, i.mapErr(ctx, err)
	}
	resPtr := uint32(rets[0])
	if resPtr == 0 {
		return 0, nil, errors.New("scripting: guest returned null (out of memory)")
	}
	defer i.free.Call(context.WithoutCancel(ctx), uint64(resPtr))

	header, ok := i.mem.Read(resPtr, resultHeaderSize)
	if !ok {
		return 0, nil, errors.New("scripting: result header out of range")
	}
	tag := header[0]
	n := binary.LittleEndian.Uint32(header[1:resultHeaderSize])
	payload, ok := i.mem.Read(resPtr+resultHeaderSize, n)
	if !ok {
		return 0, nil, errors.New("scripting: result payload out of range")
	}
	// Copy: mem.Read returns a view into guest memory that i.free invalidates.
	out := make([]byte, len(payload))
	copy(out, payload)
	return tag, out, nil
}

// evalRaw evaluates src in the instance's CURRENT context and drives the async job
// pump until the top-level promise settles, the deadline fires, or the queue drains
// with the promise still pending. The caller manages context lifecycle (newContext
// before / disposeContext after, or long-lived reuse) and sets any grant + log sink
// on ctx. It returns the raw result envelope: a JS exception is a normal (tag, payload)
// result, NOT a Go error — only interrupts/traps/unsettled promises are Go errors.
func (i *Instance) evalRaw(ctx context.Context, src string, async, asJSON bool) (uint8, []byte, error) {
	st, err := i.callEval(ctx, src, async)
	if err != nil {
		return 0, nil, err
	}
	for st == statusPending {
		if ctx.Err() != nil {
			i.dead = true
			return 0, nil, ErrInterrupted
		}
		st, err = i.callPump(ctx)
		if err != nil {
			return 0, nil, err
		}
		if st == statusPending {
			// The queue drained but the top-level promise is still pending, and there
			// are no outstanding host tickets to advance it (net is stubbed). Waiting
			// for the deadline would be pointless — report it now.
			return 0, nil, ErrUnsettled
		}
	}
	// st is statusDone or statusError: the settled value/error is held guest-side. We do
	// NOT pump again here — the async pending-loop above already drains the queue before
	// the top-level settles, so a trailing pump would only run detached fire-and-forget
	// microtasks, and a hostile one (a spinning `.then`) could burn the deadline and lose
	// this already-computed result to a spurious interrupt.
	return i.callResult(ctx, asJSON)
}

// runCompiled is the shared structured-execution core the three profiles build on. The
// caller has already compiled the source (bundler.compile — Gate 1, TS, dependency
// inlining); this prepends the console + frozen-input prelude and evaluates the blob in
// the instance's CURRENT context (async, JSON result) with the grant (Gate 2) + a fresh
// log sink on the context. The caller owns context lifecycle (fresh per run for
// isolation, or long-lived reuse for warm latency). Console logs are returned even when
// the run errors, so an interrupted or throwing script still surfaces what it logged
// before failing. A JS exception comes back as a *JSError (line remapped to the author's
// source via the compiled script's source map) in the second return.
func (i *Instance) runCompiled(ctx context.Context, c compiled, g Grant, in Input, postlude string) (Result, error) {
	// A script that compiled to nothing — only comments, or the no-op `undefined` that
	// esbuild elides — has no value and nothing to run. An entry-point run always has a
	// postlude (the call site), so it never trips this even for a tiny module.
	if strings.TrimSpace(c.code) == "" && postlude == "" {
		return Result{}, nil
	}

	prelude := buildInputPrelude(in)
	full := prelude + c.code
	// The postlude (entry-point calling convention) is appended AFTER the user code so
	// its final expression — the awaited entry-point return — is the run's value. It sits
	// past the author's lines, so error line-remapping of user code is unaffected.
	if postlude != "" {
		full += "\n;\n" + postlude + "\n"
	}
	preludeLines := strings.Count(prelude, "\n")

	sink := &logCollector{}
	rctx := withSink(WithGrant(ctx, g), sink)

	tag, payload, err := i.evalRaw(rctx, full, true /*async*/, true /*asJSON*/)
	if err != nil {
		return Result{Logs: sink.lines}, err // interrupt / trap / unsettled
	}
	val, derr := decodeResult(tag, payload) // derr is *JSError when tag == tagThrow
	var je *JSError
	if errors.As(derr, &je) {
		remapJSError(je, c.sourceMap, preludeLines, c.authorPreludeLines)
	}
	return Result{Value: val, Logs: sink.lines}, derr
}

// ---- Legacy string-mode entry points (the spike's API, kept green) ---------------

// Eval evaluates src in a FRESH context and returns its result String()-ified.
// memLimitBytes is the INNER bound handed to JS_SetMemoryLimit (0 disables it, leaving
// only the outer wazero page ceiling). A JS exception is returned as *JSError; a
// context cancellation/deadline as ErrInterrupted. Reuse an Instance across Eval calls
// for warm latency — each Eval still runs in a fresh JSRuntime, so evals never share
// JS state.
func (i *Instance) Eval(ctx context.Context, src string, memLimitBytes uint64) (string, error) {
	if err := i.newContext(ctx, memLimitBytes); err != nil {
		return "", err
	}
	defer i.disposeContext(context.WithoutCancel(ctx))

	tag, payload, err := i.evalRaw(ctx, src, false /*async*/, false /*asJSON*/)
	if err != nil {
		return "", err
	}
	if tag == tagThrow {
		return "", parseJSError(payload)
	}
	return string(payload), nil
}

// EvalIsolated instantiates a fresh instance, evaluates src on it, and tears it down —
// the max-isolation, one-instance-per-run path.
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

// Grant is the set of capabilities a single script run is allowed. A nil sub-grant
// means that capability is not granted.
type Grant struct {
	FS  *FSGrant  // filesystem read, scoped to an allowlist; nil => no fs
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
// script cannot forge it.
type grantCtxKey struct{}

// WithGrant returns a context carrying g for the host functions to enforce against.
func WithGrant(ctx context.Context, g Grant) context.Context {
	return context.WithValue(ctx, grantCtxKey{}, g)
}

func grantFromContext(ctx context.Context) Grant {
	if g, ok := ctx.Value(grantCtxKey{}).(Grant); ok {
		return g
	}
	return Grant{} // default deny: no sub-grants
}

// sinkCtxKey carries the running script's console sink on the context, the same way
// the grant rides on ctx — shared host functions, per-run state on ctx.
type sinkCtxKey struct{}

func withSink(ctx context.Context, s *logCollector) context.Context {
	return context.WithValue(ctx, sinkCtxKey{}, s)
}

func sinkFromContext(ctx context.Context) *logCollector {
	if s, ok := ctx.Value(sinkCtxKey{}).(*logCollector); ok {
		return s
	}
	return nil
}

// registerHostModule installs the "env" module quickjs.wasm imports. fs/net share the
// uniform request/result ABI; console is a fire-and-forget sink (no result envelope).
func registerHostModule(ctx context.Context, rt wazero.Runtime) error {
	reqParams := []api.ValueType{api.ValueTypeI32, api.ValueTypeI32} // reqPtr, reqLen
	ptrResult := []api.ValueType{api.ValueTypeI32}                   // resultPtr
	_, err := rt.NewHostModuleBuilder("env").
		NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(hostFSRead), reqParams, ptrResult).
		Export("host_fs_read").
		NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(hostNetFetch), reqParams, ptrResult).
		Export("host_net_fetch").
		NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(hostConsole),
			[]api.ValueType{api.ValueTypeI32, api.ValueTypeI32, api.ValueTypeI32}, // level, msgPtr, msgLen
			[]api.ValueType{}).
		Export("host_console").
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
// resolve a JS Promise via the host-driven job pump (evalRaw already drives it).
func hostNetFetch(ctx context.Context, mod api.Module, stack []uint64) {
	req, _ := mod.Memory().Read(uint32(stack[0]), uint32(stack[1]))
	if grantFromContext(ctx).Net == nil {
		stack[0] = uint64(writeResult(ctx, mod, tagThrow, []byte(`capability "net" not granted`)))
		return
	}
	stack[0] = uint64(writeResult(ctx, mod, tagValue, []byte("stub-fetched:"+string(req))))
}

// hostConsole is the fire-and-forget log sink: read the formatted line out of guest
// memory and append it to the per-run collector on ctx. It allocates nothing in guest
// memory and returns nothing, so it is safe even as a deadline is firing.
func hostConsole(ctx context.Context, mod api.Module, stack []uint64) {
	level := int32(stack[0])
	msg, ok := mod.Memory().Read(uint32(stack[1]), uint32(stack[2]))
	if !ok {
		return
	}
	if sink := sinkFromContext(ctx); sink != nil {
		sink.add(levelName(level), string(msg)) // string() copies out of guest memory
	}
}

// writeResult places a [tag u8][len u32 LE][payload] buffer into GUEST memory by
// calling the guest's own qjs_malloc (the component-model cabi_realloc pattern) and
// returns its pointer, which the guest reads and frees. Returns 0 on host-side OOM,
// which the guest turns into an InternalError.
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
// ergonomic JS surface over the __grpcview_* marshallers that qjs_wasm.c registers;
// they do no I/O themselves. Node names/shapes are used so third-party libraries
// importing these builtins resolve against our shims.
const (
	// node:path — INERT: pure string ops, no capability.
	pathShim = `({join:function(){return Array.prototype.slice.call(arguments).join("/").replace(/\/+/g,"/");},` +
		`basename:function(p){p=String(p);var i=p.lastIndexOf("/");return i<0?p:p.slice(i+1);}})`
	// node:fs — CAPABILITY: readFileSync marshals to the fs import (returns a string).
	fsShim = `({readFileSync:function(p,enc){return globalThis.__grpcview_fs_read(String(p));}})`
	// node:net — CAPABILITY (stubbed): same shape, different import.
	netShim = `({fetch:function(u){return globalThis.__grpcview_net_fetch(String(u));}})`
)

var stdModules = map[string]stdModule{
	"node:path": {shim: pathShim}, // inert
	"node:fs":   {shim: fsShim, granted: func(g Grant) bool { return g.FS != nil }},
	"node:net":  {shim: netShim, granted: func(g Grant) bool { return g.Net != nil }},
}

// importRe matches a default import of a bare module, e.g. `import fs from "node:fs"`.
// The import statement is what makes a capability request STATICALLY VISIBLE. A real
// bundler parses the module graph; this scan is the spike stand-in and does not see
// imports hidden in comments/strings — noted in the design doc.
var importRe = regexp.MustCompile(`import[ \t]+(\w+)[ \t]+from[ \t]+["']([^"']+)["'][ \t]*;?`)

// Bundle is GATE 1: a deliberately tiny stand-in for esbuild's grant-gated resolver.
// It scans the static `import ... from "..."` lines, injects the vendored shim for each
// INERT module and each GRANTED capability module, and REFUSES to resolve a capability
// module that is not granted (or an unknown module). A refusal means the script cannot
// even be assembled — there is no call site.
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

// RunScript is the legacy capability entry point: bundle the script against the grant
// (Gate 1), instantiate a fresh isolated instance, and evaluate with the grant on the
// context so the host functions enforce it (Gate 2). Returns the result String()-ified;
// the structured profiles (profiles.go) are the production entry points.
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
func (i *Instance) EvalWithGrant(ctx context.Context, src string, g Grant, memLimitBytes uint64) (string, error) {
	return i.Eval(WithGrant(ctx, g), src, memLimitBytes)
}
