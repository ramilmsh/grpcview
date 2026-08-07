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

func describeMethodCall(t *testing.T, w Workspace, ctx context.Context, service, method string) *grpcviewv1.DescribeMethodResponse {
	t.Helper()
	resp, err := w.DescribeMethod(ctx, connect.NewRequest(&grpcviewv1.DescribeMethodRequest{
		Collection: testWorkspace,
		Service:    service,
		Method:     method,
	}))
	if err != nil {
		t.Fatalf("DescribeMethod(%s/%s): %v", service, method, err)
	}
	return resp.Msg
}

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

func fieldNames(md *desc.MessageDescriptor) []string {
	out := make([]string, 0, len(md.GetFields()))
	for _, fd := range md.GetFields() {
		out = append(out, fd.GetName())
	}
	return out
}

func wonBy(t *testing.T, ws *grpcviewv1.Collection, service string) string {
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

func describeEchoWorkspace(t *testing.T, ctx context.Context) (Workspace, *grpcviewv1.Collection) {
	t.Helper()
	w := newTestWorkspace(t)
	ensureWorkspace(t, w, ctx)
	port := startEchoServer(t)
	resp, err := w.AddDescriptorSource(ctx, connect.NewRequest(reflectionAddReq(port)))
	if err != nil {
		t.Fatalf("AddDescriptorSource (echo reflection): %v", err)
	}
	return w, resp.Msg.GetCollection()
}

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
			streaming := tc.client || tc.serverWant
			if reason := got.GetNotInvocableReason(); (reason != "") != streaming {
				t.Errorf("%s: not_invocable_reason = %q, want it set = %v", tc.method, reason, streaming)
			}
		})
	}
}

// An agent authoring against this API would otherwise find out at invoke time, after saving.
func TestCreateRequestWarnsOnAStreamingMethod(t *testing.T) {
	ctx := context.Background()
	w, _ := describeEchoWorkspace(t, ctx)

	create := func(name, method string) []string {
		t.Helper()
		res, err := w.CreateRequest(ctx, connect.NewRequest(&grpcviewv1.CreateRequestRequest{
			Collection: testWorkspace,
			ItemName:   name,
			Service:    echoService,
			Method:     method,
		}))
		if err != nil {
			t.Fatalf("CreateRequest %q: %v", name, err)
		}
		return res.Msg.GetWarnings()
	}

	if got := create("unary", "Unary"); len(got) != 0 {
		t.Errorf("warnings for a unary method = %v, want none", got)
	}
	got := create("stream", "ServerStream")
	if len(got) != 1 || !strings.Contains(got[0], "ServerStream") {
		t.Errorf("warnings for a streaming method = %v, want one naming the method", got)
	}
}

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
		"grpcview.v1.InvokeSavedRequest",
		"grpcview.v1.SavedInvokeSpec",
		"grpcview.v1.InvokeSavedResponse",
		"grpcview.v1.Server",
		"grpcview.v1.ResolvedRequest",
		"google.protobuf.Struct",
	} {
		findDescribedMessage(t, files, name)
		if !strings.Contains(got.GetProtoText(), name) {
			t.Errorf("proto_text never mentions %s:\n%s", name, got.GetProtoText())
		}
	}

	if want := wonBy(t, addResp.Msg.GetCollection(), service); got.GetSourceId() != want {
		t.Errorf("source_id = %q, want %q", got.GetSourceId(), want)
	}
	if got.GetSourceId() != "upload:"+testUploadName {
		t.Errorf("source_id = %q, want the upload's id", got.GetSourceId())
	}
}

func TestDescribeMethodSourceIsThePriorityWinner(t *testing.T) {
	ctx := context.Background()
	w := newTestWorkspace(t)
	ensureWorkspace(t, w, ctx)

	if _, err := w.AddDescriptorSource(ctx, connect.NewRequest(descriptorSetAddReq(fileDescriptorSet(t, "proto/echo/v1/echo.proto")))); err != nil {
		t.Fatalf("AddDescriptorSource (upload): %v", err)
	}
	port := startEchoServer(t)
	addResp, err := w.AddDescriptorSource(ctx, connect.NewRequest(reflectionAddReq(port)))
	if err != nil {
		t.Fatalf("AddDescriptorSource (reflection): %v", err)
	}
	ws := addResp.Msg.GetCollection()
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

func TestDescribeMethodNotFound(t *testing.T) {
	ctx := context.Background()
	w, _ := describeEchoWorkspace(t, ctx)

	_, err := w.DescribeMethod(ctx, connect.NewRequest(&grpcviewv1.DescribeMethodRequest{
		Collection: testWorkspace,
		Service:    "echo.v1.NoSuchService",
		Method:     "Unary",
	}))
	if code := connect.CodeOf(err); code != connect.CodeNotFound {
		t.Fatalf("unknown service: code = %v (%v), want NotFound", code, err)
	}

	_, err = w.DescribeMethod(ctx, connect.NewRequest(&grpcviewv1.DescribeMethodRequest{
		Collection: testWorkspace,
		Service:    echoService,
		Method:     "Unray",
	}))
	if code := connect.CodeOf(err); code != connect.CodeNotFound {
		t.Fatalf("unknown method: code = %v (%v), want NotFound", code, err)
	}
	if err == nil || !strings.Contains(err.Error(), echoService) {
		t.Errorf("unknown-method error %v does not name the service %q", err, echoService)
	}
}

func TestDescribeMethodWithoutResolvedSource(t *testing.T) {
	ctx := context.Background()
	w := newTestWorkspace(t)
	ensureWorkspace(t, w, ctx)

	_, err := w.DescribeMethod(ctx, connect.NewRequest(&grpcviewv1.DescribeMethodRequest{
		Collection: testWorkspace,
		Service:    echoService,
		Method:     "Unary",
	}))
	if code := connect.CodeOf(err); code != connect.CodeFailedPrecondition {
		t.Fatalf("code = %v (%v), want FailedPrecondition", code, err)
	}
}
