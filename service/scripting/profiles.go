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

// Profile bundles the execution bounds for one script kind.
type Profile struct {
	Name     string
	MemLimit uint64 // inner QuickJS heap cap in bytes; 0 => only the outer page ceiling
	Timeout  time.Duration
}

var (
	Generator  = Profile{Name: "generator", MemLimit: 64 << 20, Timeout: 5 * time.Second}
	Middleware = Profile{Name: "middleware", MemLimit: 32 << 20, Timeout: 2 * time.Second}
	Scenario   = Profile{Name: "scenario", MemLimit: 128 << 20, Timeout: 30 * time.Second}
)

// Engine runs each profile under its bounds over one compiled Runtime; safe for concurrent use.
type Engine struct {
	rt          *Runtime
	ownsRT      bool
	longLived   bool
	mwPool      *Pool
	bundler     *bundler
	genCache    sync.Map // config digest -> Result
	poolMaxIdle int
	resolveDir  string
	nodePaths   []string
	npmDir      string
}

// EngineOption configures an Engine.
type EngineOption func(*Engine)

// WithLongLivedMiddleware makes pooled middleware instances reuse one JSContext across
// invokes — warm latency, shared JS globals. Off by default.
func WithLongLivedMiddleware() EngineOption { return func(e *Engine) { e.longLived = true } }

// WithResolveDir anchors relative imports to dir; empty (the default) resolves none.
func WithResolveDir(dir string) EngineOption { return func(e *Engine) { e.resolveDir = dir } }

// WithNodePaths sets NODE_PATH-style roots for bare imports beyond the embedded registry.
// esbuild consults them only alongside a WithResolveDir.
func WithNodePaths(paths ...string) EngineOption { return func(e *Engine) { e.nodePaths = paths } }

// NewEngine compiles a Runtime (outer page ceiling maxPages) and wires the middleware pool,
// generator cache and embedded npm registry over it.
func NewEngine(ctx context.Context, maxPages uint32, opts ...EngineOption) (*Engine, error) {
	rt, err := New(ctx, maxPages)
	if err != nil {
		return nil, err
	}
	e := &Engine{rt: rt, ownsRT: true, poolMaxIdle: 8}
	for _, o := range opts {
		o(e)
	}
	// After the options (so WithNodePaths composes) and before the bundler (cache salt).
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

// Close releases the pool, the Runtime if the Engine created it, and the extracted registry.
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

// RunGenerator runs a generator script, caching the result by a digest of the configuration
// (profile, source, grant, vars/secrets/env/args) — never the per-invoke request.
func (e *Engine) RunGenerator(ctx context.Context, source string, g Grant, in Input) (Result, error) {
	key, kerr := configDigest(Generator.Name, source, g, in)
	if kerr == nil {
		if v, ok := e.genCache.Load(key); ok {
			return v.(Result).clone(), nil // clone: never share the cache's slices
		}
	}
	res, err := e.runGenerator(ctx, source, g, in)
	if err != nil {
		return res, err
	}
	if kerr == nil {
		e.genCache.Store(key, res.clone())
	}
	return res, nil
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

// RunRequestBody runs a TypeScript request body, exposing gens (name -> source) as ambient
// globals so the body can compose them. Uncached: a body may vary per invoke.
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

// RunMiddleware runs a middleware invoke through the warm instance pool.
func (e *Engine) RunMiddleware(ctx context.Context, source string, g Grant, in Input) (Result, error) {
	c, postlude, err := e.compileMiddleware(source, g)
	if err != nil {
		return Result{}, err
	}
	rctx, cancel := withProfileDeadline(ctx, Middleware)
	defer cancel()
	return e.mwPool.RunCompiled(rctx, c, g, in, postlude)
}

// RunScenario runs a scenario on a fresh, isolated instance; no entry-point convention.
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

// A digest error must never become a fallback key: distinct configs would collide and one
// run could return another's secrets.
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
