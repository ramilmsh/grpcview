package mcp

import (
	"context"
	"fmt"
	"strings"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	"codeberg.org/ramilmsh/grpcview/service/workspace"
)

const toolNamePrefix = "grpcview_v1_WorkspaceService_"

// Exactly one of bind and stream is set: a method is either unary or streaming, and the two
// shapes reach the server through different registrations.
type rpcEntry struct {
	tool   string
	bind   func(workspace.Workspace) handlerFunc
	stream func(streamer) streamFunc
}

// One entry per RPC; a missing one fails TestTotality. Tool names are curated, not derived,
// because an agent picks a tool by its name.
var rpcs = map[string]rpcEntry{
	"Get":                       {tool: "get_collection", bind: func(w workspace.Workspace) handlerFunc { return bind(w.Get) }},
	"ListCollections":           {tool: "list_collections", bind: func(w workspace.Workspace) handlerFunc { return bind(w.ListCollections) }},
	"CreateCollection":          {tool: "create_collection", bind: func(w workspace.Workspace) handlerFunc { return bind(w.CreateCollection) }},
	"UpdateCollection":          {tool: "update_collection", bind: func(w workspace.Workspace) handlerFunc { return bind(w.UpdateCollection) }},
	"SetWorkspaceTrust":         {tool: "set_workspace_trust", bind: func(w workspace.Workspace) handlerFunc { return bind(w.SetWorkspaceTrust) }},
	"ListBazelTargets":          {tool: "list_bazel_targets", bind: func(w workspace.Workspace) handlerFunc { return bind(w.ListBazelTargets) }},
	"AddDescriptorSource":       {tool: "add_source", bind: func(w workspace.Workspace) handlerFunc { return bind(w.AddDescriptorSource) }},
	"RemoveDescriptorSource":    {tool: "remove_source", bind: func(w workspace.Workspace) handlerFunc { return bind(w.RemoveDescriptorSource) }},
	"RefreshDescriptorSource":   {tool: "refresh_source", bind: func(w workspace.Workspace) handlerFunc { return bind(w.RefreshDescriptorSource) }},
	"ReorderDescriptorSources":  {tool: "reorder_sources", bind: func(w workspace.Workspace) handlerFunc { return bind(w.ReorderDescriptorSources) }},
	"SetDescriptorSourceCommit": {tool: "set_source_commit", bind: func(w workspace.Workspace) handlerFunc { return bind(w.SetDescriptorSourceCommit) }},
	"CreateFolder":              {tool: "create_folder", bind: func(w workspace.Workspace) handlerFunc { return bind(w.CreateFolder) }},
	"UpdateFolder":              {tool: "update_folder", bind: func(w workspace.Workspace) handlerFunc { return bind(w.UpdateFolder) }},
	"CreateRequest":             {tool: "create_request", bind: func(w workspace.Workspace) handlerFunc { return bind(w.CreateRequest) }},
	"UpdateRequest":             {tool: "update_request", bind: func(w workspace.Workspace) handlerFunc { return bind(w.UpdateRequest) }},
	"DeleteRequest":             {tool: "delete_request", bind: func(w workspace.Workspace) handlerFunc { return bind(w.DeleteRequest) }},
	"MoveItem":                  {tool: "move_item", bind: func(w workspace.Workspace) handlerFunc { return bind(w.MoveItem) }},
	"Invoke":                    {tool: "invoke", bind: func(w workspace.Workspace) handlerFunc { return bind(w.Invoke) }},
	"InvokeSaved":               {tool: "invoke_saved", bind: func(w workspace.Workspace) handlerFunc { return bind(w.InvokeSaved) }},
	"InvokeStreaming":           {tool: "invoke_streaming", stream: func(s streamer) streamFunc { return bindStream(s.InvokeStream) }},
	"InvokeSavedStreaming":      {tool: "invoke_saved_streaming", stream: func(s streamer) streamFunc { return bindStream(s.InvokeSavedStream) }},
	"DescribeMethod":            {tool: "describe_method", bind: func(w workspace.Workspace) handlerFunc { return bind(w.DescribeMethod) }},
	"RunScript":                 {tool: "run_script", bind: func(w workspace.Workspace) handlerFunc { return bind(w.RunScript) }},
	"CreateScript":              {tool: "create_script", bind: func(w workspace.Workspace) handlerFunc { return bind(w.CreateScript) }},
	"UpdateScript":              {tool: "update_script", bind: func(w workspace.Workspace) handlerFunc { return bind(w.UpdateScript) }},
	"DeleteScript":              {tool: "delete_script", bind: func(w workspace.Workspace) handlerFunc { return bind(w.DeleteScript) }},
}

func bareName(generatedName string) string {
	return strings.TrimPrefix(generatedName, toolNamePrefix)
}

func newMessage(md protoreflect.MessageDescriptor) proto.Message {
	mt, err := protoregistry.GlobalTypes.FindMessageByName(md.FullName())
	if err != nil {
		return nil
	}
	return mt.New().Interface()
}

type handlerFunc func(context.Context, proto.Message) (proto.Message, error)

type protoPtr[T any] interface {
	*T
	proto.Message
}

func bind[I any, PI protoPtr[I], O any, PO protoPtr[O]](f func(context.Context, *connect.Request[I]) (*connect.Response[O], error)) handlerFunc {
	return func(ctx context.Context, msg proto.Message) (proto.Message, error) {
		in, ok := msg.(PI)
		if !ok {
			return nil, fmt.Errorf("mcp: unexpected request type %T", msg)
		}
		res, err := f(ctx, connect.NewRequest((*I)(in)))
		if err != nil {
			return nil, err
		}
		return PO(res.Msg), nil
	}
}

func newHandler(ws workspace.Workspace) func(ctx context.Context, method protoreflect.MethodDescriptor, req proto.Message) (proto.Message, error) {
	handlers := make(map[string]handlerFunc, len(rpcs))
	for name, rpc := range rpcs {
		if rpc.bind == nil {
			continue
		}
		handlers[name] = rpc.bind(ws)
	}

	return func(ctx context.Context, method protoreflect.MethodDescriptor, req proto.Message) (proto.Message, error) {
		h, ok := handlers[string(method.Name())]
		if !ok {
			return nil, fmt.Errorf("mcp: no handler registered for method %s", method.FullName())
		}
		return h(ctx, req)
	}
}
