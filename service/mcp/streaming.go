package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redpanda-data/protoc-gen-go-mcp/pkg/gen"
	mcpruntime "github.com/redpanda-data/protoc-gen-go-mcp/pkg/runtime"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
)

// An alias, not a defined type: workspace.Workspace's methods spell the parameter out, and a
// defined type would stop them satisfying streamer.
type sendFunc = func(*grpcviewv1.InvokeStreamingResponse) error

// The send-func form of the two streaming RPCs, which is what an in-process caller wants:
// connect cannot build a *connect.ServerStream outside a served request. Satisfied by
// workspace.Workspace; the seam that makes collect testable without a target.
type streamer interface {
	InvokeStream(context.Context, *grpcviewv1.InvokeStreamRequest, sendFunc) error
	InvokeSavedStream(context.Context, *grpcviewv1.InvokeSavedStreamRequest, sendFunc) error
}

type streamFunc func(context.Context, proto.Message, sendFunc) error

func bindStream[I any, PI protoPtr[I]](f func(context.Context, *I, sendFunc) error) streamFunc {
	return func(ctx context.Context, msg proto.Message, send sendFunc) error {
		in, ok := msg.(PI)
		if !ok {
			return fmt.Errorf("mcp: unexpected request type %T", msg)
		}
		return f(ctx, (*I)(in), send)
	}
}

// A tool call is request/response, so the only faithful collapse of a stream is to drain it —
// and unbounded that is a context bomb an agent cannot interrupt, because there is no Ctrl-C
// inside a tool call. Every drain runs under all three of these.
type caps struct {
	frames   int
	bytes    int
	deadline time.Duration
}

var defaultCaps = caps{frames: 200, bytes: 256 << 10, deadline: 60 * time.Second}

func (c caps) describe() string {
	return fmt.Sprintf(
		"Runs the method to completion and returns it all at once: {\"messages\": [each response "+
			"frame as raw JSON], \"result\": {status, metadata, latency}, \"truncated\": {…}}. "+
			"The invoked call's gRPC status is in `result`, not in this tool's error channel. "+
			"The drain is capped at %d frames, %d bytes of message payload and %s; whichever "+
			"bites first cancels the call, sets `truncated` and returns everything collected so "+
			"far. Note the deliberate inconsistency with `invoke`, whose `response` is base64.",
		c.frames, c.bytes, c.deadline)
}

// Registered separately from gen.RegisterService, which skips every streaming method
// unconditionally. Only the registration and the handler are hand-written: the schema still
// comes from the plugin, and s is the shim, so the rename map, annotateSchema and
// defaultCollection all apply as they do for a unary tool.
func registerStreaming(s mcpruntime.MCPServer, sd protoreflect.ServiceDescriptor, str streamer, c caps) {
	for name, rpc := range rpcs {
		if rpc.stream == nil {
			continue
		}
		md := sd.Methods().ByName(protoreflect.Name(name))
		if md == nil {
			panic(fmt.Sprintf("mcp: no method descriptor for streaming rpc %q", name))
		}
		tool, _ := gen.ToolForMethod(md, comment(md))
		tool.Description = strings.TrimSpace(tool.Description + "\n\n" + c.describe())
		s.AddTool(tool, streamHandler(md, rpc.stream(str), c))
	}
}

func streamHandler(md protoreflect.MethodDescriptor, call streamFunc, c caps) mcpruntime.ToolHandler {
	return func(ctx context.Context, request *mcpruntime.CallToolRequest) (*mcpruntime.CallToolResult, error) {
		marshaled, err := json.Marshal(request.Arguments)
		if err != nil {
			return nil, err
		}
		req := newMessage(md.Input())
		if req == nil {
			return nil, fmt.Errorf("mcp: no registered message type for %s", md.Input().FullName())
		}
		if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(marshaled, req); err != nil {
			return nil, err
		}

		agg, err := collect(ctx, c, func(ctx context.Context, send sendFunc) error {
			return call(ctx, req, send)
		})
		if err != nil {
			return mcpruntime.HandleError(err)
		}

		out, err := json.Marshal(agg)
		if err != nil {
			return nil, err
		}
		return mcpruntime.NewToolResultJSON(out), nil
	}
}

type aggregate struct {
	Messages  []json.RawMessage `json:"messages"`
	Result    json.RawMessage   `json:"result,omitempty"`
	Truncated *truncation       `json:"truncated,omitempty"`
}

// Absent when nothing was dropped. frames and bytes are what was kept, not the caps.
type truncation struct {
	Reason string `json:"reason"`
	Frames int    `json:"frames"`
	Bytes  int    `json:"bytes"`
}

// call gets the deadline-bearing context, because a cap that bites cancels it from inside the
// send func and the RPC has to actually stop. The send func then returns nil so the drain
// unwinds normally and we still report everything collected.
func collect(ctx context.Context, c caps, call func(context.Context, sendFunc) error) (aggregate, error) {
	ctx, cancel := context.WithTimeout(ctx, c.deadline)
	defer cancel()

	var (
		agg   = aggregate{Messages: []json.RawMessage{}}
		bytes int
		trunc *truncation
	)

	err := call(ctx, func(frame *grpcviewv1.InvokeStreamingResponse) error {
		switch ev := frame.GetEvent().(type) {
		case *grpcviewv1.InvokeStreamingResponse_Message:
			if trunc != nil {
				return nil
			}
			switch {
			case len(agg.Messages)+1 > c.frames:
				trunc = &truncation{Reason: "frames", Frames: len(agg.Messages), Bytes: bytes}
				cancel()
				return nil
			case bytes+len(ev.Message) > c.bytes:
				trunc = &truncation{Reason: "bytes", Frames: len(agg.Messages), Bytes: bytes}
				cancel()
				return nil
			}
			bytes += len(ev.Message)
			agg.Messages = append(agg.Messages, rawOrString(ev.Message))

		// Kept even after a cap bit: cancelling makes the terminal frame arrive carrying the
		// Canceled status, which is more honest than dropping it.
		case *grpcviewv1.InvokeStreamingResponse_Result:
			out, merr := (protojson.MarshalOptions{UseProtoNames: true, EmitDefaultValues: true}).Marshal(ev.Result)
			if merr != nil {
				return merr
			}
			agg.Result = out
		}
		return nil
	})

	// The deadline reaches the drain as a cancelled RPC, so the call itself usually returns
	// nil and the context is the only witness.
	if trunc == nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		trunc = &truncation{Reason: "deadline", Frames: len(agg.Messages), Bytes: bytes}
	}
	if err != nil && trunc == nil {
		return agg, err
	}
	agg.Truncated = trunc
	return agg, nil
}

// A frame that is not valid JSON is passed through as a string rather than dropped: it should
// never happen, and swallowing it would hide a backend bug.
func rawOrString(b []byte) json.RawMessage {
	if json.Valid(b) {
		return json.RawMessage(b)
	}
	quoted, err := json.Marshal(string(b))
	if err != nil {
		return json.RawMessage(`""`)
	}
	return json.RawMessage(quoted)
}
