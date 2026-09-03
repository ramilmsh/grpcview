// Package codegen runs @bufbuild/protoc-gen-es (vendored under service/scripting/npm, bundled
// with esbuild, executed in QuickJS-WASM) against a FileDescriptorSet to produce the same
// generated TypeScript ui/src/features/workspace/proto-types.ts produces in-browser, but
// server-side.
//
// A Pool holds N warm workers, each a long-lived QuickJS Instance with the bundle already
// Eval'd once, amortizing bundle-eval (the dominant cost of a cold run) across every job. No
// callers yet — built ahead of an endpoint that will use it.
package codegen

import (
	"context"
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
	ctx  context.Context
	fds  []byte
	resp chan<- jobResult
}

type jobResult struct {
	files map[string]string
	err   error
}

// Pool is a fixed-size set of warm QuickJS workers, safe for concurrent Generate calls; jobs
// are load-balanced across workers via a shared channel, one job per worker at a time.
type Pool struct {
	rt          *scripting.Runtime
	bundle      string
	registryDir string

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

	rt, err := scripting.New(ctx, memPages)
	if err != nil {
		return nil, fmt.Errorf("codegen: new runtime: %w", err)
	}
	registryDir, err := scripting.MaterializeNpmRegistry()
	if err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("codegen: materialize npm registry: %w", err)
	}
	bundle, err := scripting.BuildCodegenBundle(registryDir)
	if err != nil {
		_ = rt.Close(ctx)
		_ = os.RemoveAll(registryDir)
		return nil, fmt.Errorf("codegen: build bundle: %w", err)
	}

	p := &Pool{
		rt:          rt,
		bundle:      bundle,
		registryDir: registryDir,
		jobs:        make(chan job),
		done:        make(chan struct{}),
	}

	insts := make([]*scripting.Instance, 0, size)
	for i := 0; i < size; i++ {
		inst, err := newWarmInstance(ctx, rt, bundle)
		if err != nil {
			for _, in := range insts {
				_ = in.Close(context.WithoutCancel(ctx))
			}
			_ = rt.Close(ctx)
			_ = os.RemoveAll(registryDir)
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
// a *descriptorpb.FileDescriptorSet), returning generated-file name -> TypeScript source.
func (p *Pool) Generate(ctx context.Context, fileDescriptorSet []byte) (map[string]string, error) {
	resp := make(chan jobResult, 1)
	select {
	case p.jobs <- job{ctx: ctx, fds: fileDescriptorSet, resp: resp}:
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
// discarded and replaced with a freshly warmed one; a respawn failure shrinks the pool's
// capacity by one rather than wedging it.
func (p *Pool) worker(inst *scripting.Instance) {
	defer p.wg.Done()
	defer func() { _ = inst.Close(context.Background()) }()

	for {
		select {
		case j := <-p.jobs:
			files, err := generate(j.ctx, inst, j.fds)
			j.resp <- jobResult{files: files, err: err}
			if inst.Dead() {
				_ = inst.Close(context.Background())
				fresh, rerr := newWarmInstance(context.Background(), p.rt, p.bundle)
				if rerr != nil {
					return
				}
				inst = fresh
			}
		case <-p.done:
			return
		}
	}
}

func generate(ctx context.Context, inst *scripting.Instance, fds []byte) (map[string]string, error) {
	raw, err := inst.Eval(ctx, scripting.CodegenCallScript(fds))
	if err != nil {
		return nil, err
	}
	var files map[string]string
	if err := json.Unmarshal(raw, &files); err != nil {
		return nil, fmt.Errorf("codegen: decode result: %w", err)
	}
	return files, nil
}

// Close stops accepting new jobs, waits for in-flight ones to finish, and tears down every
// worker's Instance plus the pool's Runtime and materialized npm registry.
func (p *Pool) Close(ctx context.Context) error {
	p.closeOnce.Do(func() { close(p.done) })
	p.wg.Wait()
	err := p.rt.Close(ctx)
	if rmErr := os.RemoveAll(p.registryDir); rmErr != nil && err == nil {
		err = rmErr
	}
	return err
}
