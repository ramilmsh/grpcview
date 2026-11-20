package codegen

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	echov1 "codeberg.org/ramilmsh/grpcview/grpcview/echo/v1"
	"codeberg.org/ramilmsh/grpcview/service/scripting"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/types/descriptorpb"
)

// echoDescriptorSetBytes builds a real, wire-encoded FileDescriptorSet from the compiled
// grpcview/echo/v1 proto registry, the same shape of bytes service/workspace/sources.go
// already produces from uploads/reflection.
func echoDescriptorSetBytes(t *testing.T) []byte {
	t.Helper()
	fd := (&echov1.UnaryRequest{}).ProtoReflect().Descriptor().ParentFile()
	fdProto := protodesc.ToFileDescriptorProto(fd)
	set := &descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{fdProto}}
	b, err := proto.Marshal(set)
	if err != nil {
		t.Fatalf("marshal FileDescriptorSet: %v", err)
	}
	return b
}

func newTestPool(t *testing.T, size int) *Pool {
	t.Helper()
	startCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	p, err := New(startCtx, size)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := p.Close(closeCtx); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return p
}

func wantEchoSubstrings(t *testing.T, content string) {
	t.Helper()
	for _, want := range []string{
		`export type UnaryRequest = Message<"grpcview.echo.v1.UnaryRequest">`,
		`export type UnaryResponse = Message<"grpcview.echo.v1.UnaryResponse">`,
		`export type UnaryRequestJson`, // json_types=true
		`export const EchoService: GenService`,
	} {
		if !strings.Contains(content, want) {
			t.Errorf("generated echo_pb.ts missing expected substring: %s", want)
		}
	}
}

func TestGenerateEchoV1(t *testing.T) {
	p := newTestPool(t, 1)
	files, err := p.Generate(context.Background(), echoDescriptorSetBytes(t), nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	content, ok := files["grpcview/echo/v1/echo_pb.ts"]
	if !ok {
		names := make([]string, 0, len(files))
		for k := range files {
			names = append(names, k)
		}
		t.Fatalf("no grpcview/echo/v1/echo_pb.ts in generated output; got files: %v", names)
	}
	wantEchoSubstrings(t, content)
}

// A second Generate on the same warm worker must not re-Eval the bundle (redeclaration error).
func TestGenerateRunsRepeatedlyOnSameWorker(t *testing.T) {
	p := newTestPool(t, 1)
	fds := echoDescriptorSetBytes(t)
	for i := 0; i < 3; i++ {
		if _, err := p.Generate(context.Background(), fds, nil); err != nil {
			t.Fatalf("Generate #%d: %v", i, err)
		}
	}
}

// More concurrent callers than workers: run under --features=race to confirm no worker's
// Instance is ever touched from two goroutines at once.
func TestGenerateConcurrentQueuesAcrossWorkers(t *testing.T) {
	const size = 2
	const jobs = 8
	p := newTestPool(t, size)
	fds := echoDescriptorSetBytes(t)

	var wg sync.WaitGroup
	errs := make([]error, jobs)
	contents := make([]string, jobs)
	for i := 0; i < jobs; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			files, err := p.Generate(context.Background(), fds, nil)
			if err != nil {
				errs[i] = err
				return
			}
			contents[i] = files["grpcview/echo/v1/echo_pb.ts"]
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("job %d: %v", i, err)
		}
	}
	for _, c := range contents {
		wantEchoSubstrings(t, c)
	}
}

// A JS-level throw (bad input) must not poison the worker's Instance for the next, valid job.
func TestGenerateBadDescriptorSetErrorsThenRecovers(t *testing.T) {
	p := newTestPool(t, 1)
	if _, err := p.Generate(context.Background(), []byte("not a descriptor set"), nil); err == nil {
		t.Fatal("Generate: want error for garbage input, got nil")
	}
	if _, err := p.Generate(context.Background(), echoDescriptorSetBytes(t), nil); err != nil {
		t.Fatalf("Generate after bad input: %v", err)
	}
}

func readTestdata(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Pool output must be byte-identical to node protoc-gen-es (testdata/golden.test.cjs).
func TestGenerateMatchesBrowserGolden(t *testing.T) {
	p := newTestPool(t, 1)
	for _, set := range []string{"example", "all"} {
		t.Run(set, func(t *testing.T) {
			var want map[string]string
			if err := json.Unmarshal(readTestdata(t, set+".golden.json"), &want); err != nil {
				t.Fatal(err)
			}
			got, err := p.Generate(context.Background(), readTestdata(t, set+".binpb"), nil)
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}
			for name, w := range want {
				g, ok := got[name]
				switch {
				case !ok:
					t.Errorf("%s: missing", name)
				case g != w:
					i := 0
					for i < len(g) && i < len(w) && g[i] == w[i] {
						i++
					}
					t.Errorf("%s: differs at byte %d: got %q, want %q", name, i, snippet(g, i), snippet(w, i))
				}
			}
			for name := range got {
				if _, ok := want[name]; !ok {
					t.Errorf("%s: unexpected", name)
				}
			}
		})
	}
}

// The per-file cache's soundness gate: each _pb.ts must depend only on its own descriptor and
// its transitive imports — generated alone, from just that closure, it matches the whole-set
// golden byte for byte.
func TestGeneratePerFileMatchesGolden(t *testing.T) {
	p := newTestPool(t, 1)
	for _, name := range []string{"example", "all"} {
		var want map[string]string
		if err := json.Unmarshal(readTestdata(t, name+".golden.json"), &want); err != nil {
			t.Fatal(err)
		}
		var set descriptorpb.FileDescriptorSet
		if err := proto.Unmarshal(readTestdata(t, name+".binpb"), &set); err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, f := range set.GetFile() {
			if WKT(f.GetName()) {
				continue
			}
			n++
			t.Run(name+"/"+f.GetName(), func(t *testing.T) {
				closure, err := Closure(set.GetFile(), []string{f.GetName()})
				if err != nil {
					t.Fatal(err)
				}
				raw, err := proto.Marshal(&descriptorpb.FileDescriptorSet{File: closure})
				if err != nil {
					t.Fatal(err)
				}
				got, err := p.Generate(context.Background(), raw, []string{f.GetName()})
				if err != nil {
					t.Fatalf("Generate: %v", err)
				}
				out := OutputName(f.GetName())
				if len(got) != 1 {
					t.Fatalf("got %d files, want just %s", len(got), out)
				}
				g, w := got[out], want[out]
				if g != w {
					i := 0
					for i < len(g) && i < len(w) && g[i] == w[i] {
						i++
					}
					t.Errorf("differs at byte %d: got %q, want %q", i, snippet(g, i), snippet(w, i))
				}
			})
		}
		if n != len(want) {
			t.Errorf("%s: %d non-WKT files, golden has %d", name, n, len(want))
		}
	}
}

func snippet(s string, i int) string {
	return s[max(0, i-40):min(len(s), i+40)]
}

// bazel test //service/scripting/codegen:codegen_test --test_arg=-test.bench=. --test_arg=-test.run=^$ --test_output=all
func BenchmarkNew(b *testing.B) {
	for b.Loop() {
		p, err := New(context.Background(), 1)
		if err != nil {
			b.Fatal(err)
		}
		_ = p.Close(context.Background())
	}
}

func BenchmarkGenerate(b *testing.B) {
	fds := readTestdata(b, "all.binpb")
	p, err := New(context.Background(), 1)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = p.Close(context.Background()) })
	for b.Loop() {
		if _, err := p.Generate(context.Background(), fds, nil); err != nil {
			b.Fatal(err)
		}
	}
}

func TestNewDefaultsSizeToOne(t *testing.T) {
	p := newTestPool(t, 0)
	if _, err := p.Generate(context.Background(), echoDescriptorSetBytes(t), nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}
}

func TestRuntimeHardKillOff(t *testing.T) {
	if newTestPool(t, 1).rt.HardKill() {
		t.Fatal("codegen runtime: hard kill on, want off (trusted input, ~3.4x faster)")
	}
}

// resetMemos forgets the process-wide bundle and hash, restoring them (and materialize) after.
func resetMemos(t *testing.T) {
	t.Helper()
	code, hash, mat := *bundleCode, *bundleHash, materialize
	bundleCode.ok, bundleHash.ok = false, false
	t.Cleanup(func() {
		bundleCode.ok, bundleCode.v = code.ok, code.v
		bundleHash.ok, bundleHash.v = hash.ok, hash.v
		materialize = mat
	})
}

// A failed bundle build (say, a full /tmp) is retried, not memoized.
func TestBundleBuildFailureRetried(t *testing.T) {
	resetMemos(t)
	real := materialize
	materialize = func() (string, error) { return "", errors.New("disk full") }
	if _, err := bundleCode.get(); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("err = %v, want disk full", err)
	}
	materialize = real
	if _, err := bundleCode.get(); err != nil {
		t.Fatalf("retry: %v", err)
	}
}

// BundleHash hashes the bundle's inputs; it never materializes the registry or runs esbuild.
func TestBundleHashDoesNotBuild(t *testing.T) {
	resetMemos(t)
	materialize = func() (string, error) {
		t.Error("BundleHash built the bundle")
		return "", errors.New("unreachable")
	}
	h, err := BundleHash()
	if err != nil {
		t.Fatal(err)
	}
	if h == ([32]byte{}) {
		t.Fatal("zero hash")
	}
	if again, _ := BundleHash(); again != h {
		t.Fatal("hash unstable")
	}
}

// A failed respawn must not wedge the pool: each later job retries it, erroring meanwhile.
func TestRespawnFailureDoesNotWedge(t *testing.T) {
	p := newTestPool(t, 1)
	real := p.warm
	var fail atomic.Bool
	fail.Store(true)
	p.warm = func(ctx context.Context) (*scripting.Instance, error) { // ordered before the worker's read by p.jobs
		if fail.Load() {
			return nil, errors.New("respawn boom")
		}
		return real(ctx)
	}
	fds := echoDescriptorSetBytes(t)

	// A done ctx interrupts the job, retiring the instance.
	dead, cancel := context.WithCancel(context.Background())
	cancel()
	resp := make(chan jobResult, 1)
	p.jobs <- job{ctx: dead, fds: fds, resp: resp}
	if r := <-resp; r.err == nil {
		t.Fatal("interrupted job: want error")
	}

	ctx, cancel2 := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel2()
	if _, err := p.Generate(ctx, fds, nil); err == nil || !strings.Contains(err.Error(), "respawn boom") {
		t.Fatalf("Generate with respawn failing: err = %v, want respawn boom", err)
	}
	fail.Store(false)
	if _, err := p.Generate(ctx, fds, nil); err != nil {
		t.Fatalf("Generate after respawn recovers: %v", err)
	}
}
