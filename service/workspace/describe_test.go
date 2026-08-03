package workspace

import (
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/jhump/protoreflect/desc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
)

// describeMethodCall runs the DescribeMethod RPC against the test workspace, failing the test
// on a Connect error — the shape every success-path assertion here starts from.
func describeMethodCall(t *testing.T, w Workspace, ctx context.Context, service, method string) *grpcviewv1.DescribeMethodResponse {
	t.Helper()
	resp, err := w.DescribeMethod(ctx, connect.NewRequest(&grpcviewv1.DescribeMethodRequest{
		WorkspaceName: testWorkspace,
		Service:       service,
		Method:        method,
	}))
	if err != nil {
		t.Fatalf("DescribeMethod(%s/%s): %v", service, method, err)
	}
	return resp.Msg
}

// linkDescribedSet is the round-trip assertion the format decision rests on: the returned
// bytes must parse as a FileDescriptorSet and LINK on their own (so they carry their
// transitive imports), yielding descriptors any protobuf library could use.
func linkDescribedSet(t *testing.T, raw []byte) map[string]*desc.FileDescriptor {
	t.Helper()
	if len(raw) == 0 {
		t.Fatalf("descriptor_set is empty")
	}
	fds := &descriptorpb.FileDescriptorSet{}
	if err := proto.Unmarshal(raw, fds); err != nil {
		t.Fatalf("unmarshal descriptor_set: %v", err)
	}
	files, err := desc.CreateFileDescriptorsFromSet(fds)
	if err != nil {
		t.Fatalf("link descriptor_set: %v", err)
	}
	return files
}

// findDescribedMessage locates a message by fully-qualified name in a linked set, failing the
// test when it is absent — the "did the closure pull this type in?" assertion.
func findDescribedMessage(t *testing.T, files map[string]*desc.FileDescriptor, name string) *desc.MessageDescriptor {
	t.Helper()
	for _, fd := range files {
		if md := fd.FindMessage(name); md != nil {
			return md
		}
	}
	t.Fatalf("message %q is not in the returned descriptor set", name)
	return nil
}

// fieldNames lists a message's field names in declaration order.
func fieldNames(md *desc.MessageDescriptor) []string {
	out := make([]string, 0, len(md.GetFields()))
	for _, fd := range md.GetFields() {
		out = append(out, fd.GetName())
	}
	return out
}

// wonBy returns the id of the source the workspace credits with a service, read straight off
// the persisted resolve summaries — the independent check that DescribeMethod's source_id is
// the real winner and not, say, always the first source.
func wonBy(t *testing.T, ws *grpcviewv1.Workspace, service string) string {
	t.Helper()
	for _, src := range ws.GetSources() {
		for _, name := range src.GetResolved().GetWonServiceNames() {
			if name == service {
				return src.GetId()
			}
		}
	}
	t.Fatalf("no source claims service %q", service)
	return ""
}

// describeEchoWorkspace stands up an echo server, points a reflection source at it, and
// returns the workspace plus the resolved snapshot. This is the C2b setup in miniature: the
// schema is on disk afterwards, and describe reads it without dialing again.
func describeEchoWorkspace(t *testing.T, ctx context.Context) (Workspace, *grpcviewv1.Workspace) {
	t.Helper()
	w := newTestWorkspace(t)
	ensureWorkspace(t, w, ctx)
	port := startEchoServer(t)
	resp, err := w.AddDescriptorSource(ctx, connect.NewRequest(reflectionAddReq(port)))
	if err != nil {
		t.Fatalf("AddDescriptorSource (echo reflection): %v", err)
	}
	return w, resp.Msg.GetWorkspace()
}

// TestDescribeMethodRendersEchoUnary is the headline case: describing a method a shell caller
// is about to invoke prints the rpc and the messages on both sides, names the source the
// schema came from, and hands back a descriptor set that links on its own.
func TestDescribeMethodRendersEchoUnary(t *testing.T) {
	ctx := context.Background()
	w, ws := describeEchoWorkspace(t, ctx)

	got := describeMethodCall(t, w, ctx, echoService, "Unary")
	t.Logf("proto_text for %s/Unary:\n%s", echoService, got.GetProtoText())

	for _, want := range []string{
		"rpc Unary",
		"message EchoRequest",
		"string message = 1;",
		"int32 count = 2;",
		"message EchoResponse",
		"int32 index = 2;",
	} {
		if !strings.Contains(got.GetProtoText(), want) {
			t.Errorf("proto_text is missing %q:\n%s", want, got.GetProtoText())
		}
	}

	// The machine view: parse and link it, then find the input message by name and check
	// its fields — asserted through the descriptor API, not by matching the text.
	files := linkDescribedSet(t, got.GetDescriptorSet())
	input := findDescribedMessage(t, files, "echo.v1.EchoRequest")
	if names := fieldNames(input); strings.Join(names, ",") != "message,count" {
		t.Errorf("echo.v1.EchoRequest fields = %v, want [message count]", names)
	}
	findDescribedMessage(t, files, "echo.v1.EchoResponse")

	if want := wonBy(t, ws, echoService); got.GetSourceId() != want {
		t.Errorf("source_id = %q, want %q (the source that won the service)", got.GetSourceId(), want)
	}
	if got.GetSourceId() != ws.GetSources()[0].GetId() {
		t.Errorf("source_id = %q, want the reflection source %q", got.GetSourceId(), ws.GetSources()[0].GetId())
	}
	if got.GetClientStreaming() || got.GetServerStreaming() {
		t.Errorf("Unary reported as streaming: client=%v server=%v", got.GetClientStreaming(), got.GetServerStreaming())
	}
}

// TestDescribeMethodStreamingFlags asserts the two flags carry the method's real call shape
// for all four kinds — the CLI picks the unary or the streaming invoke off them, so a wrong
// flag sends the wrong RPC.
func TestDescribeMethodStreamingFlags(t *testing.T) {
	ctx := context.Background()
	w, _ := describeEchoWorkspace(t, ctx)

	cases := []struct {
		method             string
		client, serverWant bool
	}{
		{"Unary", false, false},
		{"ServerStream", false, true},
		{"ClientStream", true, false},
		{"BidiStream", true, true},
	}
	for _, tc := range cases {
		t.Run(tc.method, func(t *testing.T) {
			got := describeMethodCall(t, w, ctx, echoService, tc.method)
			if got.GetClientStreaming() != tc.client || got.GetServerStreaming() != tc.serverWant {
				t.Errorf("%s: client_streaming=%v server_streaming=%v, want %v/%v",
					tc.method, got.GetClientStreaming(), got.GetServerStreaming(), tc.client, tc.serverWant)
			}
		})
	}
}

// TestDescribeMethodPullsInReferencedTypes describes a method whose input references other
// messages — including one from another file — and asserts every referenced type is in both
// views. This is the whole point of the transitive closure: a body author who is only shown
// the top-level message cannot fill in a nested one.
//
// grpcview's own service.proto is the fixture because it is registered in this test binary
// and InvokeSavedRequest references a sibling message (Server, in another file) and a
// well-known type (google.protobuf.Struct). Struct also makes this the cycle case: it holds
// a map<string, Value> and Value holds a Struct back, so a closure walk that did not remember
// what it had visited would not return.
func TestDescribeMethodPullsInReferencedTypes(t *testing.T) {
	ctx := context.Background()
	w := newTestWorkspace(t)
	ensureWorkspace(t, w, ctx)

	set := fileDescriptorSet(t, "proto/grpcview/v1/service.proto")
	addResp, err := w.AddDescriptorSource(ctx, connect.NewRequest(descriptorSetAddReq(set)))
	if err != nil {
		t.Fatalf("AddDescriptorSource (upload): %v", err)
	}

	const service = "grpcview.v1.WorkspaceService"
	got := describeMethodCall(t, w, ctx, service, "InvokeSaved")

	files := linkDescribedSet(t, got.GetDescriptorSet())
	for _, name := range []string{
		"grpcview.v1.InvokeSavedRequest",  // the input
		"grpcview.v1.InvokeSavedResponse", // the output
		"grpcview.v1.Server",              // referenced by the input, from another file
		"grpcview.v1.ResolvedRequest",     // referenced by the output
		"google.protobuf.Struct",          // referenced by the input, a well-known type
	} {
		findDescribedMessage(t, files, name)
		if !strings.Contains(got.GetProtoText(), name) {
			t.Errorf("proto_text never mentions %s:\n%s", name, got.GetProtoText())
		}
	}

	if want := wonBy(t, addResp.Msg.GetWorkspace(), service); got.GetSourceId() != want {
		t.Errorf("source_id = %q, want %q", got.GetSourceId(), want)
	}
	if got.GetSourceId() != "upload:"+testUploadName {
		t.Errorf("source_id = %q, want the upload's id", got.GetSourceId())
	}
}

// TestDescribeMethodSourceIsThePriorityWinner puts an upload of the echo protos AHEAD of the
// reflection source serving the same service, so the two sources disagree about who provides
// the schema. source_id must name the one whose descriptors were actually used — the upload —
// even though the dial target still comes from reflection. Reporting the wrong id here would
// make an empty-comment result unattributable, which is the reason the field exists.
func TestDescribeMethodSourceIsThePriorityWinner(t *testing.T) {
	ctx := context.Background()
	w := newTestWorkspace(t)
	ensureWorkspace(t, w, ctx)

	// Upload first (highest priority), reflection second.
	if _, err := w.AddDescriptorSource(ctx, connect.NewRequest(descriptorSetAddReq(fileDescriptorSet(t, "proto/echo/v1/echo.proto")))); err != nil {
		t.Fatalf("AddDescriptorSource (upload): %v", err)
	}
	port := startEchoServer(t)
	addResp, err := w.AddDescriptorSource(ctx, connect.NewRequest(reflectionAddReq(port)))
	if err != nil {
		t.Fatalf("AddDescriptorSource (reflection): %v", err)
	}
	ws := addResp.Msg.GetWorkspace()
	if len(ws.GetSources()) != 2 {
		t.Fatalf("want 2 sources, got %d", len(ws.GetSources()))
	}

	got := describeMethodCall(t, w, ctx, echoService, "Unary")
	if want := wonBy(t, ws, echoService); got.GetSourceId() != want {
		t.Errorf("source_id = %q, want %q (the priority winner)", got.GetSourceId(), want)
	}
	if got.GetSourceId() != "upload:"+testUploadName {
		t.Errorf("source_id = %q, want the higher-priority upload", got.GetSourceId())
	}
}

// TestDescribeMethodNotFound covers the two typos a caller actually makes. Both are NotFound,
// and only the message tells them apart — so the method case must name the service it looked
// in, or a mistyped method reads as a missing service.
func TestDescribeMethodNotFound(t *testing.T) {
	ctx := context.Background()
	w, _ := describeEchoWorkspace(t, ctx)

	_, err := w.DescribeMethod(ctx, connect.NewRequest(&grpcviewv1.DescribeMethodRequest{
		WorkspaceName: testWorkspace,
		Service:       "echo.v1.NoSuchService",
		Method:        "Unary",
	}))
	if code := connect.CodeOf(err); code != connect.CodeNotFound {
		t.Fatalf("unknown service: code = %v (%v), want NotFound", code, err)
	}

	_, err = w.DescribeMethod(ctx, connect.NewRequest(&grpcviewv1.DescribeMethodRequest{
		WorkspaceName: testWorkspace,
		Service:       echoService,
		Method:        "Unray",
	}))
	if code := connect.CodeOf(err); code != connect.CodeNotFound {
		t.Fatalf("unknown method: code = %v (%v), want NotFound", code, err)
	}
	if err == nil || !strings.Contains(err.Error(), echoService) {
		t.Errorf("unknown-method error %v does not name the service %q", err, echoService)
	}
}

// TestDescribeMethodWithoutResolvedSource asserts a workspace nobody has added a source to
// reports FailedPrecondition — the situation named — rather than dereferencing an empty
// descriptor set.
func TestDescribeMethodWithoutResolvedSource(t *testing.T) {
	ctx := context.Background()
	w := newTestWorkspace(t)
	ensureWorkspace(t, w, ctx)

	_, err := w.DescribeMethod(ctx, connect.NewRequest(&grpcviewv1.DescribeMethodRequest{
		WorkspaceName: testWorkspace,
		Service:       echoService,
		Method:        "Unary",
	}))
	if code := connect.CodeOf(err); code != connect.CodeFailedPrecondition {
		t.Fatalf("code = %v (%v), want FailedPrecondition", code, err)
	}
}
