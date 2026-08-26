package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	mcpruntime "github.com/redpanda-data/protoc-gen-go-mcp/pkg/runtime"
	"google.golang.org/protobuf/proto"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/grpcview/v1"
)

func msgFrame(body string) *grpcviewv1.InvokeStreamingResponse {
	return &grpcviewv1.InvokeStreamingResponse{
		Event: &grpcviewv1.InvokeStreamingResponse_Message{Message: []byte(body)},
	}
}

func resultFrame(code int32) *grpcviewv1.InvokeStreamingResponse {
	return &grpcviewv1.InvokeStreamingResponse{
		Event: &grpcviewv1.InvokeStreamingResponse_Result{
			Result: &grpcviewv1.Request_Response{Status: &grpcviewv1.Status{Code: code}},
		},
	}
}

// Stops as soon as its context is cancelled, the way a real drain does, so a cap test can
// assert that the call actually ended rather than that its frames were merely ignored.
type fakeStreamer struct {
	frames  []*grpcviewv1.InvokeStreamingResponse
	err     error
	sent    int
	stopped bool
}

func (f *fakeStreamer) run(ctx context.Context, send sendFunc) error {
	for _, frame := range f.frames {
		if ctx.Err() != nil {
			f.stopped = true
			return ctx.Err()
		}
		if err := send(frame); err != nil {
			return err
		}
		f.sent++
	}
	return f.err
}

func (f *fakeStreamer) InvokeStream(ctx context.Context, _ *grpcviewv1.InvokeStreamRequest, send sendFunc) error {
	return f.run(ctx, send)
}

func (f *fakeStreamer) InvokeSavedStream(ctx context.Context, _ *grpcviewv1.InvokeSavedStreamRequest, send sendFunc) error {
	return f.run(ctx, send)
}

// Goes through the rpcs entry so bindStream's type assertion is exercised too.
func drainFake(t *testing.T, rpc string, req proto.Message, f *fakeStreamer, c caps) (aggregate, error) {
	t.Helper()
	entry, ok := rpcs[rpc]
	if !ok || entry.stream == nil {
		t.Fatalf("rpcs[%q] has no streaming bind", rpc)
	}
	call := entry.stream(f)
	return collect(context.Background(), c, func(ctx context.Context, send sendFunc) error {
		return call(ctx, req, send)
	})
}

func TestCollectFramesThenResult(t *testing.T) {
	f := &fakeStreamer{frames: []*grpcviewv1.InvokeStreamingResponse{
		msgFrame(`{"n":1}`), msgFrame(`{"n":2}`), resultFrame(0),
	}}
	agg, err := drainFake(t, "InvokeStreaming", &grpcviewv1.InvokeStreamRequest{}, f, defaultCaps)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(agg.Messages) != 2 {
		t.Fatalf("collected %d messages, want 2", len(agg.Messages))
	}
	if string(agg.Messages[1]) != `{"n":2}` {
		t.Errorf("message[1] = %s, want the frame spliced in verbatim", agg.Messages[1])
	}
	if !strings.Contains(string(agg.Result), "status") {
		t.Errorf("result = %s, want the terminal frame's protojson", agg.Result)
	}
	if agg.Truncated != nil {
		t.Errorf("truncated = %+v, want absent", agg.Truncated)
	}
}

func TestCollectResultWithNoFrames(t *testing.T) {
	f := &fakeStreamer{frames: []*grpcviewv1.InvokeStreamingResponse{resultFrame(5)}}
	agg, err := drainFake(t, "InvokeSavedStreaming", &grpcviewv1.InvokeSavedStreamRequest{}, f, defaultCaps)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(agg.Messages) != 0 {
		t.Errorf("collected %d messages, want 0", len(agg.Messages))
	}
	// An empty list, not null: an agent should not have to distinguish the two.
	out, err := json.Marshal(agg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(out), `"messages":[]`) {
		t.Errorf("aggregate = %s, want an empty messages list", out)
	}
	if !strings.Contains(string(agg.Result), `"code":5`) {
		t.Errorf("result = %s, want the non-OK status carried through", agg.Result)
	}
}

func TestCollectFramesWithNoResult(t *testing.T) {
	f := &fakeStreamer{frames: []*grpcviewv1.InvokeStreamingResponse{msgFrame(`{"n":1}`)}}
	agg, err := drainFake(t, "InvokeStreaming", &grpcviewv1.InvokeStreamRequest{}, f, defaultCaps)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(agg.Result) != 0 {
		t.Errorf("result = %s, want it absent rather than synthesized", agg.Result)
	}
	if agg.Truncated != nil {
		t.Errorf("truncated = %+v, want absent", agg.Truncated)
	}
}

func TestCollectFrameCap(t *testing.T) {
	f := &fakeStreamer{frames: []*grpcviewv1.InvokeStreamingResponse{
		msgFrame(`{"n":1}`), msgFrame(`{"n":2}`), msgFrame(`{"n":3}`), msgFrame(`{"n":4}`),
	}}
	agg, err := drainFake(t, "InvokeStreaming", &grpcviewv1.InvokeStreamRequest{}, f,
		caps{frames: 2, bytes: 1 << 20, deadline: time.Minute})
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(agg.Messages) != 2 {
		t.Fatalf("collected %d messages, want 2", len(agg.Messages))
	}
	if agg.Truncated == nil || agg.Truncated.Reason != "frames" {
		t.Fatalf("truncated = %+v, want reason %q", agg.Truncated, "frames")
	}
	if !f.stopped {
		t.Error("the cap did not cancel the call")
	}
}

func TestCollectByteCap(t *testing.T) {
	f := &fakeStreamer{frames: []*grpcviewv1.InvokeStreamingResponse{
		msgFrame(`{"n":1}`), msgFrame(`{"n":2}`), msgFrame(`{"n":3}`),
	}}
	agg, err := drainFake(t, "InvokeStreaming", &grpcviewv1.InvokeStreamRequest{}, f,
		caps{frames: 100, bytes: 10, deadline: time.Minute})
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(agg.Messages) != 1 {
		t.Fatalf("collected %d messages, want 1 (7 bytes each, cap 10)", len(agg.Messages))
	}
	if agg.Truncated == nil || agg.Truncated.Reason != "bytes" {
		t.Fatalf("truncated = %+v, want reason %q", agg.Truncated, "bytes")
	}
	if agg.Truncated.Bytes != 7 {
		t.Errorf("truncated.bytes = %d, want the 7 kept", agg.Truncated.Bytes)
	}
	if !f.stopped {
		t.Error("the cap did not cancel the call")
	}
}

func TestCollectDeadline(t *testing.T) {
	agg, err := collect(context.Background(), caps{frames: 100, bytes: 1 << 20, deadline: 20 * time.Millisecond},
		func(ctx context.Context, send sendFunc) error {
			if err := send(msgFrame(`{"n":1}`)); err != nil {
				return err
			}
			<-ctx.Done()
			return nil
		})
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if agg.Truncated == nil || agg.Truncated.Reason != "deadline" {
		t.Fatalf("truncated = %+v, want reason %q", agg.Truncated, "deadline")
	}
	if agg.Truncated.Frames != 1 {
		t.Errorf("truncated.frames = %d, want the 1 kept", agg.Truncated.Frames)
	}
}

func TestCollectPassesNonJSONFrameThrough(t *testing.T) {
	f := &fakeStreamer{frames: []*grpcviewv1.InvokeStreamingResponse{msgFrame("not json at all")}}
	agg, err := drainFake(t, "InvokeStreaming", &grpcviewv1.InvokeStreamRequest{}, f, defaultCaps)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(agg.Messages) != 1 {
		t.Fatalf("collected %d messages, want the bad frame kept", len(agg.Messages))
	}
	if string(agg.Messages[0]) != `"not json at all"` {
		t.Errorf("message[0] = %s, want it quoted as a string", agg.Messages[0])
	}
	if _, err := json.Marshal(agg); err != nil {
		t.Fatalf("a bad frame made the aggregate unmarshalable: %v", err)
	}
}

func TestCollectSurfacesStreamerError(t *testing.T) {
	want := errors.New("target refused the connection")
	f := &fakeStreamer{frames: []*grpcviewv1.InvokeStreamingResponse{msgFrame(`{"n":1}`)}, err: want}
	if _, err := drainFake(t, "InvokeStreaming", &grpcviewv1.InvokeStreamRequest{}, f, defaultCaps); !errors.Is(err, want) {
		t.Fatalf("collect error = %v, want %v", err, want)
	}
}

type recordingServer struct {
	tools    map[string]mcpruntime.Tool
	handlers map[string]mcpruntime.ToolHandler
}

func (r *recordingServer) AddTool(t mcpruntime.Tool, h mcpruntime.ToolHandler) {
	if r.tools == nil {
		r.tools = map[string]mcpruntime.Tool{}
		r.handlers = map[string]mcpruntime.ToolHandler{}
	}
	r.tools[t.Name] = t
	r.handlers[t.Name] = h
}

// The schema is still the plugin's, and it still goes through the shim: proving both is the
// point, since a hand-built tool registered around the shim would silently lose the rename
// map, the comments and the default collection.
func TestStreamingToolSchema(t *testing.T) {
	sd := mustLoadService(t)
	rec := &recordingServer{}
	registerStreaming(&shim{MCPServer: rec, service: sd}, sd, &fakeStreamer{}, defaultCaps)

	for _, name := range []string{"invoke_streaming", "invoke_saved_streaming"} {
		if _, ok := rec.tools[name]; !ok {
			t.Fatalf("tool %q was not registered; got %v", name, rec.tools)
		}
	}

	tool := rec.tools["invoke_streaming"]
	if tool.RawOutputSchema != nil {
		t.Error("output schema survived the shim")
	}
	if !strings.Contains(tool.Description, "truncated") {
		t.Errorf("description does not state the caps: %q", tool.Description)
	}

	var schema struct {
		Properties map[string]struct {
			Description string `json:"description"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(tool.RawInputSchema, &schema); err != nil {
		t.Fatalf("input schema is not JSON: %v", err)
	}
	for _, field := range []string{"spec", "messages"} {
		if _, ok := schema.Properties[field]; !ok {
			t.Fatalf("input schema has no %q property; got %v", field, schema.Properties)
		}
	}
	if want := "Client bodies as JSON text"; !strings.Contains(schema.Properties["messages"].Description, want) {
		t.Errorf("messages description = %q, want it to carry the .proto comment %q",
			schema.Properties["messages"].Description, want)
	}
}
