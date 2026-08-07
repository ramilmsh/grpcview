package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/redpanda-data/protoc-gen-go-mcp/pkg/gen"
	mcpruntime "github.com/redpanda-data/protoc-gen-go-mcp/pkg/runtime"
	"github.com/redpanda-data/protoc-gen-go-mcp/pkg/runtime/gosdk"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"codeberg.org/ramilmsh/grpcview/service/workspace"
	"codeberg.org/ramilmsh/grpcview/service/wsroot"
)

type Options struct {
	Root       string
	Collection string
	Version    string
}

func Run(ctx context.Context, opts Options) error {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))

	cwd, err := wsroot.InvocationDir()
	if err != nil {
		return fmt.Errorf("mcp: resolve the invocation directory: %w", err)
	}
	root, warn, err := wsroot.Discover(opts.Root, cwd)
	if err != nil {
		return fmt.Errorf("mcp: discover workspace root: %w", err)
	}
	if warn != "" {
		fmt.Fprintln(os.Stderr, warn)
	}

	ws, err := workspace.New(ctx, root)
	if err != nil {
		return fmt.Errorf("mcp: open workspace: %w", err)
	}
	defer ws.Close(ctx)

	sd, err := loadWorkspaceService()
	if err != nil {
		return fmt.Errorf("mcp: load service descriptor: %w", err)
	}

	server, raw := gosdk.NewServer("grpcview", opts.Version)

	s := &shim{MCPServer: raw, collection: opts.Collection, service: sd}

	gen.RegisterService(s, sd, trimHeavyFields(newHandler(ws)), gen.RegisterServiceOptions{
		NewMessage:      newMessage,
		CommentProvider: comment,
	})
	registerStreaming(s, sd, ws, defaultCaps)

	return server.Run(ctx, &mcpsdk.StdioTransport{})
}

type shim struct {
	mcpruntime.MCPServer
	collection string
	service    protoreflect.ServiceDescriptor
}

func (s *shim) AddTool(t mcpruntime.Tool, handler mcpruntime.ToolHandler) {
	bare := bareName(t.Name)
	md := s.service.Methods().ByName(protoreflect.Name(bare))
	if md == nil {
		panic(fmt.Sprintf("mcp: no method descriptor for generated tool name %q", t.Name))
	}

	rpc, ok := rpcs[bare]
	if !ok {
		panic(fmt.Sprintf("mcp: no short tool name registered for %q", t.Name))
	}
	t.Name = rpc.tool

	t.RawOutputSchema = nil
	t.RawInputSchema = annotateSchema(t.RawInputSchema, md.Input())

	if s.collection != "" {
		if path := collectionPath(md.Input(), collectionSearchDepth); path != nil {
			handler = defaultCollection(handler, s.collection, path)
		}
	}

	s.MCPServer.AddTool(t, handler)
}

// The one RPC whose response is allowed to carry `history`: it is an agent's ONLY access to
// it, so stripping it here would remove a capability rather than trim a payload.
const historyBearingMethod = "Get"

// Mutates the response, which is safe only because every RPC rebuilds its own from the
// store: this is never a cached message.
//
// Both fields blow the MCP client's per-result token cap on a collection of ordinary size —
// a recorded response holding a descriptor set is 160 KB of a 186 KB `Collection`, and every
// write RPC returns the whole collection, so without this every mutation comes back as an
// overflow error.
func trimHeavyFields(h gen.Handler) gen.Handler {
	return func(ctx context.Context, method protoreflect.MethodDescriptor, req proto.Message) (proto.Message, error) {
		res, err := h(ctx, method, req)
		if err != nil || res == nil {
			return res, err
		}
		clearHeavyFields(res.ProtoReflect(), string(method.Name()) != historyBearingMethod)
		return res, nil
	}
}

func clearHeavyFields(m protoreflect.Message, dropHistory bool) {
	var clear []protoreflect.FieldDescriptor
	m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		switch {
		case fd.Name() == "descriptor_set" && fd.Kind() == protoreflect.BytesKind && !fd.IsList():
			clear = append(clear, fd)
		case dropHistory && fd.Name() == "history" && fd.IsList() && fd.Kind() == protoreflect.MessageKind:
			clear = append(clear, fd)
		case fd.IsMap():
			if fd.MapValue().Kind() == protoreflect.MessageKind {
				v.Map().Range(func(_ protoreflect.MapKey, mv protoreflect.Value) bool {
					clearHeavyFields(mv.Message(), dropHistory)
					return true
				})
			}
		case fd.IsList():
			if fd.Kind() == protoreflect.MessageKind {
				l := v.List()
				for i := 0; i < l.Len(); i++ {
					clearHeavyFields(l.Get(i).Message(), dropHistory)
				}
			}
		case fd.Kind() == protoreflect.MessageKind:
			clearHeavyFields(v.Message(), dropHistory)
		}
		return true
	})
	for _, fd := range clear {
		m.Clear(fd)
	}
}

// Invoke and InvokeSaved nest theirs inside `spec`; everything else is top-level.
const collectionSearchDepth = 2

func collectionPath(md protoreflect.MessageDescriptor, depth int) []string {
	if md == nil || depth == 0 {
		return nil
	}
	fields := md.Fields()
	for i := 0; i < fields.Len(); i++ {
		fd := fields.Get(i)
		if fd.Name() == "collection" && fd.Kind() == protoreflect.StringKind && !fd.IsList() {
			return []string{"collection"}
		}
	}
	for i := 0; i < fields.Len(); i++ {
		fd := fields.Get(i)
		if fd.Kind() != protoreflect.MessageKind || fd.IsList() || fd.IsMap() {
			continue
		}
		if sub := collectionPath(fd.Message(), depth-1); sub != nil {
			return append([]string{string(fd.Name())}, sub...)
		}
	}
	return nil
}

func defaultCollection(h mcpruntime.ToolHandler, collection string, path []string) mcpruntime.ToolHandler {
	return func(ctx context.Context, req *mcpruntime.CallToolRequest) (*mcpruntime.CallToolResult, error) {
		if req.Arguments == nil {
			req.Arguments = map[string]any{}
		}
		node := req.Arguments
		for _, key := range path[:len(path)-1] {
			child, ok := node[key].(map[string]any)
			if !ok {
				child = map[string]any{}
				node[key] = child
			}
			node = child
		}
		leaf := path[len(path)-1]
		if v, ok := node[leaf]; !ok || v == "" {
			node[leaf] = collection
		}
		return h(ctx, req)
	}
}
