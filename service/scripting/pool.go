package scripting

import (
	"context"
	"sync"
)

// Pool is a warm pool of QuickJS instances for latency-sensitive reuse (the middleware
// profile). Reusing an instance skips the ~100 µs Instantiate. Two context policies:
//
//   - fresh context per run (default): each Run creates a JSContext, evaluates, and
//     disposes it. The wasm instance is warm but every run is fully isolated — no JS
//     state leaks between invokes. Cost per run ≈ the ~800 µs QuickJS context bootstrap.
//   - long-lived context (WithLongLivedContext): the instance keeps one JSContext across
//     runs, so the bootstrap is paid once and warm runs are far cheaper — at the cost of
//     SHARED JS GLOBAL STATE. Re-evaluating a script in the same context throws a
//     redeclaration error whenever the compiled blob has a top-level `const`/`let` — which
//     the esbuild output has (user lexical declarations, and esbuild names inlined
//     dependencies). So this mode still only suits scripts that are re-entrant by
//     construction; making it generally usable needs the bundler to emit a re-callable
//     entry (wrap the blob in one reusable function) rather than a fresh top-level
//     program per run. Measure the trade-off, then choose per the design's warm-pool
//     decision.
//
// An instance left dead by an interrupt or trap (its module closed) is discarded on
// return, never reused — the pool self-heals by instantiating a fresh one on demand.
// A Pool is safe for concurrent use.
type Pool struct {
	rt        *Runtime
	memLimit  uint64
	longLived bool
	maxIdle   int
	bundler   *bundler

	mu     sync.Mutex
	idle   []*Instance
	closed bool
}

// PoolOption configures a Pool.
type PoolOption func(*Pool)

// WithLongLivedContext makes pooled instances reuse one JSContext across runs (warm
// latency, shared JS globals). See the Pool doc for the trade-off.
func WithLongLivedContext() PoolOption { return func(p *Pool) { p.longLived = true } }

// WithMaxIdle caps how many idle instances the pool retains (default 8). Excess
// returned instances are closed rather than pooled.
func WithMaxIdle(n int) PoolOption {
	return func(p *Pool) {
		if n > 0 {
			p.maxIdle = n
		}
	}
}

// WithBundler gives the pool the shared bundler used to compile scripts (so a middleware
// run shares its compile cache with generator/scenario runs). If unset, the pool builds a
// default bundler with no module resolution — fine for import-free scripts.
func WithBundler(b *bundler) PoolOption { return func(p *Pool) { p.bundler = b } }

// NewPool builds a warm pool over rt. memLimit is the inner QuickJS heap cap applied to
// each context (0 => only the outer wazero page ceiling).
func NewPool(rt *Runtime, memLimit uint64, opts ...PoolOption) *Pool {
	p := &Pool{rt: rt, memLimit: memLimit, maxIdle: 8}
	for _, o := range opts {
		o(p)
	}
	if p.bundler == nil {
		p.bundler = newBundler("", nil, "")
	}
	return p
}

// get checks out a ready instance. For a long-lived pool the returned instance already
// holds a live context; for a fresh pool the caller creates the context per run.
func (p *Pool) get(ctx context.Context) (*Instance, error) {
	p.mu.Lock()
	if n := len(p.idle); n > 0 {
		inst := p.idle[n-1]
		p.idle = p.idle[:n-1]
		p.mu.Unlock()
		return inst, nil
	}
	p.mu.Unlock()

	inst, err := p.rt.Instantiate(ctx)
	if err != nil {
		return nil, err
	}
	if p.longLived {
		if err := inst.newContext(ctx, p.memLimit); err != nil {
			_ = inst.Close(context.WithoutCancel(ctx))
			return nil, err
		}
	}
	return inst, nil
}

// put returns inst to the pool, or closes it if it is dead, the pool is full, or the
// pool has been closed (a run that finishes after Close must not re-populate idle).
func (p *Pool) put(inst *Instance) {
	if inst.Dead() {
		_ = inst.Close(context.WithoutCancel(context.Background()))
		return
	}
	p.mu.Lock()
	if p.closed || len(p.idle) >= p.maxIdle {
		p.mu.Unlock()
		_ = inst.Close(context.WithoutCancel(context.Background()))
		return
	}
	p.idle = append(p.idle, inst)
	p.mu.Unlock()
}

// Close closes every idle instance and marks the pool closed, so any run still in
// flight closes its instance on return (put) instead of re-pooling it. Callers should
// still quiesce in-flight runs before Close; this only prevents the leak if they don't.
func (p *Pool) Close(ctx context.Context) {
	p.mu.Lock()
	p.closed = true
	idle := p.idle
	p.idle = nil
	p.mu.Unlock()
	for _, inst := range idle {
		_ = inst.Close(ctx)
	}
}

// Run executes one invoke through the pool: compile the script (Gate 1, TS, dependency
// inlining — cached, so the warm path recompiles nothing after the first time), check out
// an instance, run the compiled blob (async, JSON result), and check it back in
// (discarding it if the run left it dead). A compile failure never checks out an instance.
func (p *Pool) Run(ctx context.Context, source string, g Grant, in Input) (Result, error) {
	c, err := p.bundler.compile(source, g)
	if err != nil {
		return Result{}, err
	}

	inst, err := p.get(ctx)
	if err != nil {
		return Result{}, err
	}

	if p.longLived {
		// Reuse the live context; do NOT dispose between runs. defer so a panic in the
		// script path still returns the instance (or discards it if it died).
		defer p.put(inst)
		return inst.runCompiled(ctx, c, g, in)
	}

	// Fresh context per run: create, run, dispose. If newContext fails the instance is
	// marked dead, so put discards it. The defers (LIFO: dispose then put) keep the
	// instance from leaking even if runCompiled panics.
	if err := inst.newContext(ctx, p.memLimit); err != nil {
		p.put(inst)
		return Result{}, err
	}
	defer p.put(inst)
	defer inst.disposeContext(context.WithoutCancel(ctx))
	return inst.runCompiled(ctx, c, g, in)
}
