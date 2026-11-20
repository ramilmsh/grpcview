// Package codegen runs @bufbuild/protoc-gen-es (vendored under service/scripting/npm, bundled
// with esbuild, executed in QuickJS-WASM) against a FileDescriptorSet, producing exactly what
// node protoc-gen-es does (testdata/golden.cjs).
//
// A Pool holds N warm workers, each a long-lived QuickJS Instance with the bundle already
// Eval'd once, amortizing bundle-eval (the dominant cost of a cold run) across every job.
package codegen

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"

	"codeberg.org/ramilmsh/grpcview/service/scripting"
)

const (
	memPages uint32 = 1024 // 64MiB wasm linear memory
	memLimit uint64 = 64 << 20
)

// ErrClosed is returned by Generate once the pool has been Closed.
var ErrClosed = errors.New("codegen: pool closed")

type job struct {
	ctx   context.Context
	fds   []byte
	files []string
	resp  chan<- jobResult
}

type jobResult struct {
	files map[string]string
	err   error
}

// Pool is a fixed-size set of warm QuickJS workers, safe for concurrent Generate calls; jobs
// are load-balanced across workers via a shared channel, one job per worker at a time.
type Pool struct {
	rt   *scripting.Runtime
	warm func(context.Context) (*scripting.Instance, error) // tests swap it

	jobs chan job
	done chan struct{}
	wg   sync.WaitGroup

	closeOnce sync.Once
}

// New starts a pool of size warm workers (size <= 0 defaults to 1).
func New(ctx context.Context, size int) (*Pool, error) {
	if size <= 0 {
		size = 1
	}

	code, err := bundleCode.get()
	if err != nil {
		return nil, err
	}
	rt, err := scripting.New(ctx, memPages, scripting.WithoutHardKill()) // trusted bundle
	if err != nil {
		return nil, fmt.Errorf("codegen: new runtime: %w", err)
	}

	p := &Pool{
		rt:   rt,
		jobs: make(chan job),
		done: make(chan struct{}),
	}
	p.warm = func(ctx context.Context) (*scripting.Instance, error) { return newWarmInstance(ctx, rt, code) }

	insts := make([]*scripting.Instance, 0, size)
	for i := 0; i < size; i++ {
		inst, err := p.warm(ctx)
		if err != nil {
			for _, in := range insts {
				_ = in.Close(context.WithoutCancel(ctx))
			}
			_ = rt.Close(ctx)
			return nil, fmt.Errorf("codegen: start worker %d: %w", i, err)
		}
		insts = append(insts, inst)
	}
	for _, inst := range insts {
		p.wg.Add(1)
		go p.worker(inst)
	}
	return p, nil
}

// memo caches f's first success; a failure (say, a full /tmp) is retried on the next call.
type memo[T any] struct {
	mu sync.Mutex
	ok bool
	v  T
	f  func() (T, error)
}

func (m *memo[T]) get() (T, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ok {
		return m.v, nil
	}
	v, err := m.f()
	if err != nil {
		return v, err
	}
	m.v, m.ok = v, true
	return v, nil
}

var materialize = scripting.MaterializeNpmRegistry // tests swap it

// bundleCode builds the bundle once per process; the npm registry it's built from is
// scratch, removed as soon as esbuild is done with it.
var bundleCode = &memo[string]{f: func() (string, error) {
	dir, err := materialize()
	if err != nil {
		return "", fmt.Errorf("codegen: materialize npm registry: %w", err)
	}
	defer os.RemoveAll(dir)
	code, err := scripting.BuildCodegenBundle(dir)
	if err != nil {
		return "", fmt.Errorf("codegen: build bundle: %w", err)
	}
	return code, nil
}}

var bundleHash = &memo[[sha256.Size]byte]{f: scripting.CodegenBundleHash}

// BundleHash identifies the generator: it changes whenever the vendored npm packages, the
// bundle's patches, its entry point, polyfill, build options or esbuild version do. Hashes
// those inputs; builds neither the bundle nor a Pool.
func BundleHash() ([sha256.Size]byte, error) { return bundleHash.get() }

// newWarmInstance instantiates one QuickJS context and loads the polyfill (see
// scripting.CodegenPolyfillJS), then the bundle.
func newWarmInstance(ctx context.Context, rt *scripting.Runtime, bundle string) (*scripting.Instance, error) {
	inst, err := rt.Instantiate(ctx)
	if err != nil {
		return nil, err
	}
	if err := inst.NewContext(ctx, memLimit); err != nil {
		_ = inst.Close(context.WithoutCancel(ctx))
		return nil, err
	}
	if _, err := inst.Eval(ctx, scripting.CodegenPolyfillJS); err != nil {
		_ = inst.Close(context.WithoutCancel(ctx))
		return nil, fmt.Errorf("eval polyfill: %w", err)
	}
	if _, err := inst.Eval(ctx, bundle); err != nil {
		_ = inst.Close(context.WithoutCancel(ctx))
		return nil, fmt.Errorf("eval bundle: %w", err)
	}
	return inst, nil
}

// Generate runs the codegen bundle against a wire-encoded FileDescriptorSet (proto.Marshal of
// a *descriptorpb.FileDescriptorSet), returning generated-file name -> TypeScript source for
// each of fileToGenerate (nil = every non-WKT file). The set must hold fileToGenerate's
// transitive closure, in dependency order; nothing outside it affects the output.
func (p *Pool) Generate(ctx context.Context, fileDescriptorSet []byte, fileToGenerate []string) (map[string]string, error) {
	resp := make(chan jobResult, 1)
	select {
	case p.jobs <- job{ctx: ctx, fds: fileDescriptorSet, files: fileToGenerate, resp: resp}:
	case <-p.done:
		return nil, ErrClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case r := <-resp:
		return r.files, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// worker is the only goroutine allowed to touch inst. A trapped instance (Dead()) is
// discarded and replaced with a freshly warmed one; if that fails, each later job retries
// the respawn and gets its error, so the pool degrades per job but never wedges.
func (p *Pool) worker(inst *scripting.Instance) {
	defer p.wg.Done()
	defer func() {
		if inst != nil {
			_ = inst.Close(context.Background())
		}
	}()

	for {
		select {
		case j := <-p.jobs:
			if inst == nil {
				fresh, err := p.warm(j.ctx)
				if err != nil {
					j.resp <- jobResult{err: fmt.Errorf("codegen: respawn worker: %w", err)}
					continue
				}
				inst = fresh
			}
			files, err := generate(j.ctx, inst, j.fds, j.files)
			j.resp <- jobResult{files: files, err: err}
			if inst.Dead() {
				_ = inst.Close(context.Background())
				inst, _ = p.warm(context.Background()) // nil on failure: the next job retries
			}
		case <-p.done:
			return
		}
	}
}

func generate(ctx context.Context, inst *scripting.Instance, fds []byte, files []string) (map[string]string, error) {
	if err := inst.SetBytes(ctx, scripting.CodegenInputGlobal, fds); err != nil {
		return nil, err
	}
	raw, err := inst.Eval(ctx, scripting.CodegenCallScript(files))
	if err != nil {
		return nil, err
	}
	var out map[string]string
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("codegen: decode result: %w", err)
	}
	return out, nil
}

// Close stops accepting new jobs, waits for in-flight ones to finish, and tears down every
// worker's Instance plus the pool's Runtime.
func (p *Pool) Close(ctx context.Context) error {
	p.closeOnce.Do(func() { close(p.done) })
	p.wg.Wait()
	return p.rt.Close(ctx)
}
