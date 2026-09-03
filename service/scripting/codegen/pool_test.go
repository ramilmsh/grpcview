package codegen

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	echov1 "codeberg.org/ramilmsh/grpcview/grpcview/echo/v1"
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
	files, err := p.Generate(context.Background(), echoDescriptorSetBytes(t))
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
		if _, err := p.Generate(context.Background(), fds); err != nil {
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
			files, err := p.Generate(context.Background(), fds)
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
	if _, err := p.Generate(context.Background(), []byte("not a descriptor set")); err == nil {
		t.Fatal("Generate: want error for garbage input, got nil")
	}
	if _, err := p.Generate(context.Background(), echoDescriptorSetBytes(t)); err != nil {
		t.Fatalf("Generate after bad input: %v", err)
	}
}

func TestNewDefaultsSizeToOne(t *testing.T) {
	p := newTestPool(t, 0)
	if _, err := p.Generate(context.Background(), echoDescriptorSetBytes(t)); err != nil {
		t.Fatalf("Generate: %v", err)
	}
}
