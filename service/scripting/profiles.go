package scripting

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"sync"
	"time"
)

// Profile bundles the execution bounds for one script kind. The three kinds differ in
// bounds and reuse policy but share the one structured-execution core (runStructured):
//
//   - Generator  — async, result cached by config digest; produces values (tokens,
//     timestamps, signatures) other requests interpolate. Runs on a fresh instance.
//   - Middleware — per-invoke, latency-sensitive, served from a WARM POOL. Runs on the
//     request path, so the pooled instance skips the ~100 µs Instantiate.
//   - Scenario   — long-running, many awaits, larger time budget; a fresh isolated
//     instance each run.
//
// All three evaluate async (top-level await allowed) with a JSON structured result.
type Profile struct {
	Name     string
	MemLimit uint64        // inner QuickJS heap cap in bytes (0 => only the outer ceiling)
	Timeout  time.Duration // default wall-clock budget when the caller's ctx has no deadline
}

// The default profiles. Bounds are starting points to be tuned from the Phase 1
// benchmark; a caller can always pass a ctx with its own deadline to override Timeout.
var (
	Generator  = Profile{Name: "generator", MemLimit: 64 << 20, Timeout: 5 * time.Second}
	Middleware = Profile{Name: "middleware", MemLimit: 32 << 20, Timeout: 2 * time.Second}
	Scenario   = Profile{Name: "scenario", MemLimit: 128 << 20, Timeout: 30 * time.Second}
)

// Engine is the production entry point over a compiled Runtime: it owns the esbuild
// bundler (Gate 1 + TS/dependency compilation), the middleware warm pool, and the
// generator result cache, and runs each profile under its bounds. It is safe for
// concurrent use.
type Engine struct {
	rt          *Runtime
	ownsRT      bool
	longLived   bool
	mwPool      *Pool
	bundler     *bundler
	genCache    sync.Map // config digest (string) -> Result
	poolMaxIdle int
	resolveDir  string
	nodePaths   []string
	npmDir      string // temp dir holding the extracted embedded npm registry; removed in Close
}

// EngineOption configures an Engine.
type EngineOption func(*Engine)

// WithLongLivedMiddleware makes the middleware warm pool reuse one JSContext per
// instance across invokes (warm latency, shared JS globals — see Pool). Off by default
// (fresh context per invoke = full isolation).
func WithLongLivedMiddleware() EngineOption { return func(e *Engine) { e.longLived = true } }

// WithMiddlewarePoolSize caps the middleware pool's idle instances.
func WithMiddlewarePoolSize(n int) EngineOption { return func(e *Engine) { e.poolMaxIdle = n } }

// WithResolveDir anchors RELATIVE imports (`./util`) in a script to dir. Empty (the
// default) means relative imports do not resolve — appropriate for the inline scripts of
// production Phase 2.
func WithResolveDir(dir string) EngineOption { return func(e *Engine) { e.resolveDir = dir } }

// WithNodePaths sets NODE_PATH-style roots for esbuild's own resolver, for BARE (npm)
// imports beyond the embedded registry every Engine self-provisions (npm.go). These compose
// with the embedded registry, which is resolved by a separate plugin: tests use this to
// bundle extra packages (ms, mustache) from their own tree, while dayjs comes from the
// embedded registry regardless. NOTE these roots only take effect alongside a WithResolveDir
// (esbuild consults NodePaths only for an entry with a filesystem home); with no options,
// only the embedded registry (dayjs) and the node:* shims resolve — any other bare import
// fails cleanly, never touching the host FS.
func WithNodePaths(paths ...string) EngineOption { return func(e *Engine) { e.nodePaths = paths } }

// NewEngine compiles a Runtime (outer page ceiling maxPages) and wires the middleware
// pool + generator cache over it. It also self-provisions the embedded npm registry (see
// provisionNpmRegistry) so bare npm imports resolve with no call-site wiring. Close
// releases the runtime and removes the extracted registry.
func NewEngine(ctx context.Context, maxPages uint32, opts ...EngineOption) (*Engine, error) {
	rt, err := New(ctx, maxPages)
	if err != nil {
		return nil, err
	}
	e := &Engine{rt: rt, ownsRT: true, poolMaxIdle: 8}
	for _, o := range opts {
		o(e)
	}
	// Provision the embedded registry AFTER options (so a caller's WithNodePaths composes)
	// and BEFORE initBundlerAndPool (so the added root is folded into the bundler's cache
	// salt). We own the runtime, so close it if provisioning fails — otherwise it leaks.
	if err := e.provisionNpmRegistry(); err != nil {
		_ = rt.Close(ctx)
		return nil, err
	}
	e.initBundlerAndPool()
	return e, nil
}

// initBundlerAndPool builds the shared bundler from the resolved options and wires it into
// the middleware pool, so a middleware run and a generator/scenario run share one compile
// cache. Called by both constructors after options are applied.
func (e *Engine) initBundlerAndPool() {
	e.bundler = newBundler(e.resolveDir, e.nodePaths, e.npmDir)
	popts := []PoolOption{WithMaxIdle(e.poolMaxIdle), WithBundler(e.bundler)}
	if e.longLived {
		popts = append(popts, WithLongLivedContext())
	}
	e.mwPool = NewPool(e.rt, Middleware.MemLimit, popts...)
}

// NewEngineWithRuntime wires an Engine over an existing Runtime (which the caller keeps
// ownership of — Close will not close it). Useful when several engines share one
// compiled module. Like NewEngine it self-provisions the embedded npm registry, so it
// returns an error (extraction can fail); the runtime is the caller's, so a failure does
// NOT close it here.
func NewEngineWithRuntime(rt *Runtime, opts ...EngineOption) (*Engine, error) {
	e := &Engine{rt: rt, ownsRT: false, poolMaxIdle: 8}
	for _, o := range opts {
		o(e)
	}
	if err := e.provisionNpmRegistry(); err != nil {
		return nil, err
	}
	e.initBundlerAndPool()
	return e, nil
}

// Close releases the middleware pool and, if the Engine created it, the Runtime, and
// removes the temp dir the embedded npm registry was extracted to.
func (e *Engine) Close(ctx context.Context) error {
	if e.mwPool != nil {
		e.mwPool.Close(ctx)
	}
	var err error
	if e.ownsRT {
		err = e.rt.Close(ctx)
	}
	// Remove the extracted registry regardless of runtime ownership; report it only if
	// nothing more important already failed.
	if rmErr := e.removeNpmRegistry(); rmErr != nil && err == nil {
		err = rmErr
	}
	return err
}

// withProfileDeadline ensures the run is time-bounded: if the caller's ctx already has
// a deadline it wins; otherwise the profile's default Timeout is applied.
func withProfileDeadline(ctx context.Context, p Profile) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, p.Timeout)
}

// RunGenerator runs a generator script. The result is cached by a config digest of
// (profile + source + grant + vars/secrets/env) — NOT the per-invoke request, so a
// varying request.body cannot thrash or unbound the cache. Repeated resolves of the
// same configuration reuse the value instead of re-executing.
//
// Caveats (future work when generators are wired to real inputs): (1) this assumes the
// generator is a pure function of that configuration — a generator that must run every
// time (e.g. one using a live clock) should not be cached (a per-script "no-cache"
// flag); (2) the cache is unbounded — a size cap / LRU is needed for a long-lived
// service with many distinct configs. If the config can't be digested (an input that
// won't JSON-marshal), the run is executed uncached rather than risking a key collision.
func (e *Engine) RunGenerator(ctx context.Context, source string, g Grant, in Input) (Result, error) {
	key, kerr := configDigest(Generator.Name, source, g, in)
	if kerr == nil {
		if v, ok := e.genCache.Load(key); ok {
			return v.(Result).clone(), nil // clone: never hand callers the cache's slices
		}
	}
	res, err := e.runGenerator(ctx, source, g, in)
	if err != nil {
		return res, err
	}
	if kerr == nil {
		e.genCache.Store(key, res.clone()) // clone: detach the cache from this caller's slices
	}
	return res, nil
}

// RunGeneratorUncached runs a generator WITHOUT consulting or populating the generator
// cache, so a caller that must re-run every time gets a fresh value. Token resolution on
// the invoke path uses this (scripting-ui-plan §S2): `uuid()`/`now()` must vary per invoke,
// so the config-digest cache — which assumes the generator is a pure function of its
// configuration — would hand back a stale value. The cache stays intact for a future
// opt-in "by inputs" caching policy; RunGenerator remains the cached entry point.
func (e *Engine) RunGeneratorUncached(ctx context.Context, source string, g Grant, in Input) (Result, error) {
	return e.runGenerator(ctx, source, g, in)
}

// runGenerator is the shared compile-and-run core of the cached (RunGenerator) and uncached
// (RunGeneratorUncached) paths. It applies the entry-point convention (§2.5) — a generator
// that declares `export default` is called with in.Args, one that doesn't falls back to
// last-expression eval — and runs on a fresh instance under the Generator bounds.
func (e *Engine) runGenerator(ctx context.Context, source string, g Grant, in Input) (Result, error) {
	c, postlude, err := e.compileGenerator(source, g, in.Args)
	if err != nil {
		return Result{}, err
	}
	rctx, cancel := withProfileDeadline(ctx, Generator)
	defer cancel()
	return e.runFresh(rctx, c, g, in, Generator.MemLimit, postlude)
}

// RunMiddleware runs a middleware invoke through the warm instance pool. A middleware that
// declares a `handle`/default export is called with a ctx built from in.Request (§2.5);
// otherwise it falls back to last-expression eval.
func (e *Engine) RunMiddleware(ctx context.Context, source string, g Grant, in Input) (Result, error) {
	c, postlude, err := e.compileMiddleware(source, g)
	if err != nil {
		return Result{}, err
	}
	rctx, cancel := withProfileDeadline(ctx, Middleware)
	defer cancel()
	return e.mwPool.RunCompiled(rctx, c, g, in, postlude)
}

// RunScenario runs a long-running scenario on a fresh, isolated instance + context. It is
// the ad-hoc scratchpad path: last-expression eval, no entry-point convention.
func (e *Engine) RunScenario(ctx context.Context, source string, g Grant, in Input) (Result, error) {
	c, err := e.bundler.compile(source, g)
	if err != nil {
		return Result{}, err
	}
	rctx, cancel := withProfileDeadline(ctx, Scenario)
	defer cancel()
	return e.runFresh(rctx, c, g, in, Scenario.MemLimit, "")
}

// runFresh executes an already-compiled blob on a brand-new instance + context, torn down
// after — maximum isolation, the price being the full instantiate + context bootstrap per
// run. The caller compiles (Gate 1, TS, dependency inlining) before this is reached, so a
// compile failure — including an ungranted-capability denial — never allocates an instance.
// postlude is the entry-point call site (empty for last-expression eval).
func (e *Engine) runFresh(ctx context.Context, c compiled, g Grant, in Input, memLimit uint64, postlude string) (Result, error) {
	inst, err := e.rt.Instantiate(ctx)
	if err != nil {
		return Result{}, err
	}
	defer inst.Close(context.WithoutCancel(ctx))

	if err := inst.newContext(ctx, memLimit); err != nil {
		return Result{}, err
	}
	defer inst.disposeContext(context.WithoutCancel(ctx))

	return inst.runCompiled(ctx, c, g, in, postlude)
}

// configDigest is the generator cache key: a stable hash over the profile, source, the
// grant (capabilities + scope), the environment inputs (vars/secrets/env), and the
// generator's positional args — but NOT the per-invoke request. Maps are marshalled with
// sorted keys by encoding/json, so the
// digest is deterministic. It returns an error if the grant or inputs cannot be JSON-
// marshalled, in which case the caller must NOT cache (a "" fallback key would collide
// distinct configs and could return one run's output — including secrets — for another).
func configDigest(profile, source string, g Grant, in Input) (string, error) {
	h := sha256.New()
	writeField(h, profile)
	writeField(h, source)
	grantJSON, err := json.Marshal(g)
	if err != nil {
		return "", err
	}
	writeField(h, string(grantJSON))
	envJSON, err := json.Marshal(struct {
		Vars, Secrets, Env map[string]any
		Args               []any
	}{in.Vars, in.Secrets, in.Env, in.Args})
	if err != nil {
		return "", err
	}
	writeField(h, string(envJSON))
	return hex.EncodeToString(h.Sum(nil)), nil
}

// clone returns a deep copy of r so a cached Result never shares its Value bytes or Logs
// backing array with the caller (the Engine is used concurrently; a cache entry must be
// immutable from a caller's perspective).
func (r Result) clone() Result {
	out := Result{}
	if r.Value != nil {
		out.Value = append(json.RawMessage(nil), r.Value...)
	}
	if r.Logs != nil {
		out.Logs = append([]LogLine(nil), r.Logs...)
	}
	return out
}

func writeField(h io.Writer, s string) {
	_, _ = io.WriteString(h, s)
	_, _ = h.Write([]byte{0}) // separator so field boundaries can't be forged
}
