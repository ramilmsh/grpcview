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
	"google.golang.org/protobuf/reflect/protoreflect"

	"codeberg.org/ramilmsh/grpcview/service/wire"
)

type Options struct {
	Collection string
	Version    string
}

// Sent as `instructions` in MCP's initialize response.
const instructions = "grpcview is a gRPC client: browse reflected services and their schemas, " +
	"manage a workspace tree of collections, folders and requests, and invoke calls (unary, " +
	"saved, or streaming). A scripting system supports creating, updating, deleting and running " +
	"scripts, with the grpcview:invoke, grpcview:assert, grpcview:metadata and grpcview:request " +
	"modules importable from scripts and request bodies. Call get_collection first to see the " +
	"current tree, services and scripts."

// Run takes its backend rather than opening one, because which process owns the collection is
// the CLI's decision and it is the same decision for every verb: normally the workspace daemon,
// so an agent's writes and the UI's go through one process and one Collection.mu. The MCP
// server itself is never discovered — its client launches it over stdio — so it publishes
// nothing and only ever acts as a client.
func Run(ctx context.Context, opts Options, ws wire.Workspace) error {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))

	sd, err := loadWorkspaceService()
	if err != nil {
		return fmt.Errorf("mcp: load service descriptor: %w", err)
	}

	server := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "grpcview", Version: opts.Version}, &mcpsdk.ServerOptions{Instructions: instructions})

	s := &shim{MCPServer: gosdk.Wrap(server), collection: opts.Collection, service: sd}

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
	if _, skip := notTools[bare]; skip {
		return
	}
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
