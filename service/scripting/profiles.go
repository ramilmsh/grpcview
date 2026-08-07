package scripting

import (
	"context"
	"time"
)

type Profile struct {
	Name     string
	MemLimit uint64
	Timeout  time.Duration
}

var (
	Generator  = Profile{Name: "generator", MemLimit: 64 << 20, Timeout: 5 * time.Second}
	Middleware = Profile{Name: "middleware", MemLimit: 32 << 20, Timeout: 2 * time.Second}
	Scenario   = Profile{Name: "scenario", MemLimit: 128 << 20, Timeout: 30 * time.Second}
)

type Engine struct {
	rt          *Runtime
	ownsRT      bool
	longLived   bool
	mwPool      *Pool
	bundler     *bundler
	poolMaxIdle int
	resolveDir  string
	nodePaths   []string
	npmDir      string
}

type EngineOption func(*Engine)

func WithLongLivedMiddleware() EngineOption { return func(e *Engine) { e.longLived = true } }

func WithResolveDir(dir string) EngineOption { return func(e *Engine) { e.resolveDir = dir } }

func WithNodePaths(paths ...string) EngineOption { return func(e *Engine) { e.nodePaths = paths } }

func NewEngine(ctx context.Context, maxPages uint32, opts ...EngineOption) (*Engine, error) {
	rt, err := New(ctx, maxPages)
	if err != nil {
		return nil, err
	}
	e := &Engine{rt: rt, ownsRT: true, poolMaxIdle: 8}
	for _, o := range opts {
		o(e)
	}
	if err := e.provisionNpmRegistry(); err != nil {
		_ = rt.Close(ctx)
		return nil, err
	}
	e.initBundlerAndPool()
	return e, nil
}

func (e *Engine) initBundlerAndPool() {
	e.bundler = newBundler(e.resolveDir, e.nodePaths, e.npmDir)
	popts := []PoolOption{WithMaxIdle(e.poolMaxIdle), WithBundler(e.bundler)}
	if e.longLived {
		popts = append(popts, WithLongLivedContext())
	}
	e.mwPool = NewPool(e.rt, Middleware.MemLimit, popts...)
}

func (e *Engine) Close(ctx context.Context) error {
	if e.mwPool != nil {
		e.mwPool.Close(ctx)
	}
	var err error
	if e.ownsRT {
		err = e.rt.Close(ctx)
	}
	if rmErr := e.removeNpmRegistry(); rmErr != nil && err == nil {
		err = rmErr
	}
	return err
}

func withProfileDeadline(ctx context.Context, p Profile) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, p.Timeout)
}

func (e *Engine) runGenerator(ctx context.Context, source string, g Grant, in Input) (Result, error) {
	c, postlude, err := e.compileGenerator(source, g, in.Args)
	if err != nil {
		return Result{}, err
	}
	rctx, cancel := withProfileDeadline(ctx, Generator)
	defer cancel()
	return e.runFresh(rctx, c, g, in, Generator.MemLimit, postlude)
}

func (e *Engine) RunRequestBody(ctx context.Context, body string, gens map[string]string, g Grant, in Input) (Result, error) {
	if len(gens) == 0 {
		return e.runGenerator(ctx, body, g, in)
	}
	c, postlude, err := e.compileRequestBody(body, g, in.Args, gens)
	if err != nil {
		return Result{}, err
	}
	rctx, cancel := withProfileDeadline(ctx, Generator)
	defer cancel()
	return e.runFresh(rctx, c, g, in, Generator.MemLimit, postlude)
}

func (e *Engine) RunMiddleware(ctx context.Context, source string, gens map[string]string, g Grant, in Input) (Result, error) {
	c, postlude, err := e.compileMiddleware(source, g, gens)
	if err != nil {
		return Result{}, err
	}
	rctx, cancel := withProfileDeadline(ctx, Middleware)
	defer cancel()
	return e.mwPool.RunCompiled(rctx, c, g, in, postlude)
}

func (e *Engine) RunScenario(ctx context.Context, source string, g Grant, in Input) (Result, error) {
	c, err := e.bundler.compile(source, g)
	if err != nil {
		return Result{}, err
	}
	rctx, cancel := withProfileDeadline(ctx, Scenario)
	defer cancel()
	return e.runFresh(rctx, c, g, in, Scenario.MemLimit, "")
}

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
