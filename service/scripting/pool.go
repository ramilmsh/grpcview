package scripting

import (
	"context"
	"sync"
)

// Pool is a warm pool of QuickJS instances for the middleware profile. An instance left dead
// by an interrupt or trap is discarded on return, never reused. Safe for concurrent use.
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

// WithLongLivedContext makes pooled instances reuse one JSContext across runs. Re-evaluating
// a blob with top-level `const`/`let` — which esbuild output has — then throws.
func WithLongLivedContext() PoolOption { return func(p *Pool) { p.longLived = true } }

// WithMaxIdle caps how many idle instances the pool retains (default 8).
func WithMaxIdle(n int) PoolOption {
	return func(p *Pool) {
		if n > 0 {
			p.maxIdle = n
		}
	}
}

// WithBundler gives the pool the shared bundler, so its compile cache is shared.
func WithBundler(b *bundler) PoolOption { return func(p *Pool) { p.bundler = b } }

// NewPool builds a warm pool over rt. memLimit 0 => only the outer wazero page ceiling.
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

// Close closes every idle instance and marks the pool closed, so an in-flight run closes its
// instance on return instead of re-pooling it.
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

// Run compiles source and executes one invoke through the pool.
func (p *Pool) Run(ctx context.Context, source string, g Grant, in Input) (Result, error) {
	c, err := p.bundler.compile(source, g)
	if err != nil {
		return Result{}, err
	}
	return p.RunCompiled(ctx, c, g, in, "")
}

// RunCompiled is Run for an already-compiled blob plus an optional entry-point postlude.
func (p *Pool) RunCompiled(ctx context.Context, c compiled, g Grant, in Input, postlude string) (Result, error) {
	inst, err := p.get(ctx)
	if err != nil {
		return Result{}, err
	}

	if p.longLived {
		defer p.put(inst)
		return inst.runCompiled(ctx, c, g, in, postlude)
	}

	if err := inst.newContext(ctx, p.memLimit); err != nil {
		p.put(inst)
		return Result{}, err
	}
	// LIFO: dispose the context before the instance goes back to the pool.
	defer p.put(inst)
	defer inst.disposeContext(context.WithoutCancel(ctx))
	return inst.runCompiled(ctx, c, g, in, postlude)
}
