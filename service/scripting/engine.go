// Package scripting embeds QuickJS compiled to WebAssembly and evaluates untrusted
// JavaScript under enforceable memory and wall-clock bounds on the wazero runtime.
package scripting

import (
	"context"
	_ "embed"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

//go:embed quickjs.wasm
var quickjsWasm []byte

const WasmPageSize = 65536

// The result envelope qjs_result returns, mirrored from third_party/quickjs/qjs_wasm.c.
const (
	resultHeaderSize       = 5 // [tag u8][len u32 LE][payload]
	tagValue         uint8 = 0
	tagThrow         uint8 = 1
	tagUndefined     uint8 = 2
)

// qjs_eval / qjs_pump status codes, mirrored from qjs_wasm.c.
const (
	statusDone    = 0
	statusPending = 1
	statusError   = 2
)

// ErrInterrupted is returned when a run is cancelled or its deadline elapses; the
// instance it ran on is left dead and must be discarded.
var ErrInterrupted = errors.New("scripting: evaluation interrupted")

// ErrUnsettled is returned when a script's top-level Promise never settles.
var ErrUnsettled = errors.New("scripting: top-level promise did not settle")

// JSError is a JavaScript exception that propagated out of an evaluation.
type JSError struct {
	Message string
	Stack   string
	Line    int
}

func (e *JSError) Error() string { return "scripting: uncaught " + e.Message }

// Runtime is a compiled-once, instantiate-many QuickJS engine; safe for concurrent use.
type Runtime struct {
	rt       wazero.Runtime
	compiled wazero.CompiledModule
}

// New builds a Runtime whose instances may grow linear memory to at most maxPages pages.
func New(ctx context.Context, maxPages uint32) (*Runtime, error) {
	cfg := wazero.NewRuntimeConfig().
		WithCloseOnContextDone(true). // lets ctx cancel/deadline interrupt a running guest
		WithMemoryLimitPages(maxPages)

	rt := wazero.NewRuntimeWithConfig(ctx, cfg)

	// The wasm32-wasi reactor build needs WASI preview1 for libc; no FS/args/env is granted.
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, rt); err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("scripting: instantiate wasi: %w", err)
	}

	// quickjs.wasm statically imports these, so they must be present at every instantiation.
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

// Instance is one live QuickJS wasm instance; not safe for concurrent use, and it must be
// discarded once dead.
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
	dead    bool
}

// Instantiate creates a fresh QuickJS instance granting no FS, args, or env.
func (r *Runtime) Instantiate(ctx context.Context) (*Instance, error) {
	// "_initialize" runs the reactor's libc init; an empty name keeps instances anonymous.
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

func (i *Instance) Close(ctx context.Context) error { return i.mod.Close(ctx) }

// Dead reports whether an interrupt or trap left the instance unusable.
func (i *Instance) Dead() bool { return i.dead }

func (i *Instance) mapErr(ctx context.Context, err error) error {
	i.dead = true
	if ctx.Err() != nil {
		return ErrInterrupted
	}
	return fmt.Errorf("scripting: guest trap: %w", err)
}

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

func (i *Instance) disposeContext(ctx context.Context) {
	if i.dead {
		return
	}
	_, _ = i.dispose.Call(ctx)
}

func (i *Instance) callEval(ctx context.Context, src string, async bool) (int, error) {
	mallocRet, err := i.malloc.Call(ctx, uint64(len(src)))
	if err != nil {
		return 0, i.mapErr(ctx, err)
	}
	srcPtr := uint32(mallocRet[0])
	if len(src) > 0 && srcPtr == 0 {
		// NULL from qjs_malloc: writing at offset 0 would clobber low memory, not fail.
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
	_, _ = i.free.Call(context.WithoutCancel(ctx), uint64(srcPtr))
	if err != nil {
		return 0, i.mapErr(ctx, err)
	}
	return int(int32(rets[0])), nil
}

func (i *Instance) callPump(ctx context.Context) (int, error) {
	rets, err := i.pump.Call(ctx)
	if err != nil {
		return 0, i.mapErr(ctx, err)
	}
	return int(int32(rets[0])), nil
}

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
	// mem.Read returns a view into guest memory that i.free invalidates.
	out := make([]byte, len(payload))
	copy(out, payload)
	return tag, out, nil
}

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
			// Every host call is synchronous, so a drained queue can never advance the promise.
			return 0, nil, ErrUnsettled
		}
	}
	// Do not pump again: a detached spinning `.then` could burn the deadline and lose the
	// value already settled here.
	return i.callResult(ctx, asJSON)
}

func (i *Instance) runCompiled(ctx context.Context, c compiled, g Grant, in Input, postlude string) (Result, error) {
	if strings.TrimSpace(c.code) == "" && postlude == "" {
		return Result{}, nil
	}

	prelude := buildInputPrelude(in)
	full := prelude + c.code
	if postlude != "" {
		full += "\n;\n" + postlude + "\n"
	}
	preludeLines := strings.Count(prelude, "\n")

	sink := &logCollector{}
	rctx := withSink(WithGrant(ctx, g), sink)

	tag, payload, err := i.evalRaw(rctx, full, true, true)
	if err != nil {
		return Result{Logs: sink.lines}, err
	}
	val, derr := decodeResult(tag, payload)
	var je *JSError
	if errors.As(derr, &je) {
		remapJSError(je, c.sourceMap, preludeLines, c.authorPreludeLines)
	}
	return Result{Value: val, Logs: sink.lines}, derr
}

// Grant is the set of capabilities a run is allowed; a nil sub-grant denies that capability.
type Grant struct {
	FS *FSGrant
}

// FSGrant scopes the fs capability to an allowlist of paths.
type FSGrant struct {
	AllowedPaths []string
}

// NOTE: prefix containment on cleaned paths only; symlinks are not resolved.
func (f *FSGrant) allows(cleaned string) bool {
	for _, p := range f.AllowedPaths {
		pc := filepath.Clean(p)
		if cleaned == pc || strings.HasPrefix(cleaned, pc+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

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

type grantCtxKey struct{}

// WithGrant returns a context carrying g for the host functions to enforce against.
func WithGrant(ctx context.Context, g Grant) context.Context {
	return context.WithValue(ctx, grantCtxKey{}, g)
}

func grantFromContext(ctx context.Context) Grant {
	if g, ok := ctx.Value(grantCtxKey{}).(Grant); ok {
		return g
	}
	return Grant{}
}

// Invoker is the ctx-carried bridge gv.invoke calls into: a JSON {path, params} request in,
// the InvokeResult envelope JSON out.
type Invoker func(ctx context.Context, req []byte) ([]byte, error)

type invokerCtxKey struct{}

// WithInvoker returns a context carrying inv for hostInvoke to call.
func WithInvoker(ctx context.Context, inv Invoker) context.Context {
	return context.WithValue(ctx, invokerCtxKey{}, inv)
}

func invokerFromContext(ctx context.Context) Invoker {
	inv, _ := ctx.Value(invokerCtxKey{}).(Invoker)
	return inv
}

type invokeDepthCtxKey struct{}

// WithInvokeDepth returns a context carrying depth as the current gv.invoke nesting count.
func WithInvokeDepth(ctx context.Context, depth int) context.Context {
	return context.WithValue(ctx, invokeDepthCtxKey{}, depth)
}

func invokeDepthFromContext(ctx context.Context) int {
	d, _ := ctx.Value(invokeDepthCtxKey{}).(int)
	return d
}

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

func registerHostModule(ctx context.Context, rt wazero.Runtime) error {
	reqParams := []api.ValueType{api.ValueTypeI32, api.ValueTypeI32}
	ptrResult := []api.ValueType{api.ValueTypeI32}
	_, err := rt.NewHostModuleBuilder("env").
		NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(hostFSRead), reqParams, ptrResult).
		Export("host_fs_read").
		NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(hostNetFetch), reqParams, ptrResult).
		Export("host_net_fetch").
		NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(hostInvoke), reqParams, ptrResult).
		Export("host_invoke").
		NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(hostConsole),
			[]api.ValueType{api.ValueTypeI32, api.ValueTypeI32, api.ValueTypeI32},
			[]api.ValueType{}).
		Export("host_console").
		Instantiate(ctx)
	if err != nil {
		return fmt.Errorf("scripting: instantiate host module: %w", err)
	}
	return nil
}

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

// writeResult allocates the result envelope in GUEST memory via qjs_malloc; the guest reads
// and frees it. Returns 0 on failure, which the guest turns into an error.
func writeResult(ctx context.Context, mod api.Module, tag uint8, payload []byte) uint32 {
	malloc := mod.ExportedFunction("qjs_malloc")
	if malloc == nil {
		return 0
	}
	total := resultHeaderSize + len(payload)
	rets, err := malloc.Call(ctx, uint64(total))
	if err != nil || len(rets) == 0 {
		return 0
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
