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

// One entry per unary RPC; a missing one fails TestTotality. Tool names are curated, not
// derived, because an agent picks a tool by its name.
var rpcs = map[string]struct {
	tool string
	bind func(workspace.Workspace) handlerFunc
}{
	"Get":                       {"get_collection", func(w workspace.Workspace) handlerFunc { return bind(w.Get) }},
	"ListCollections":           {"list_collections", func(w workspace.Workspace) handlerFunc { return bind(w.ListCollections) }},
	"CreateCollection":          {"create_collection", func(w workspace.Workspace) handlerFunc { return bind(w.CreateCollection) }},
	"UpdateCollection":          {"update_collection", func(w workspace.Workspace) handlerFunc { return bind(w.UpdateCollection) }},
	"SetWorkspaceTrust":         {"set_workspace_trust", func(w workspace.Workspace) handlerFunc { return bind(w.SetWorkspaceTrust) }},
	"ListBazelTargets":          {"list_bazel_targets", func(w workspace.Workspace) handlerFunc { return bind(w.ListBazelTargets) }},
	"AddDescriptorSource":       {"add_source", func(w workspace.Workspace) handlerFunc { return bind(w.AddDescriptorSource) }},
	"RemoveDescriptorSource":    {"remove_source", func(w workspace.Workspace) handlerFunc { return bind(w.RemoveDescriptorSource) }},
	"RefreshDescriptorSource":   {"refresh_source", func(w workspace.Workspace) handlerFunc { return bind(w.RefreshDescriptorSource) }},
	"ReorderDescriptorSources":  {"reorder_sources", func(w workspace.Workspace) handlerFunc { return bind(w.ReorderDescriptorSources) }},
	"SetDescriptorSourceCommit": {"set_source_commit", func(w workspace.Workspace) handlerFunc { return bind(w.SetDescriptorSourceCommit) }},
	"CreateFolder":              {"create_folder", func(w workspace.Workspace) handlerFunc { return bind(w.CreateFolder) }},
	"UpdateFolder":              {"update_folder", func(w workspace.Workspace) handlerFunc { return bind(w.UpdateFolder) }},
	"CreateRequest":             {"create_request", func(w workspace.Workspace) handlerFunc { return bind(w.CreateRequest) }},
	"UpdateRequest":             {"update_request", func(w workspace.Workspace) handlerFunc { return bind(w.UpdateRequest) }},
	"DeleteRequest":             {"delete_request", func(w workspace.Workspace) handlerFunc { return bind(w.DeleteRequest) }},
	"MoveItem":                  {"move_item", func(w workspace.Workspace) handlerFunc { return bind(w.MoveItem) }},
	"Invoke":                    {"invoke", func(w workspace.Workspace) handlerFunc { return bind(w.Invoke) }},
	"InvokeSaved":               {"invoke_saved", func(w workspace.Workspace) handlerFunc { return bind(w.InvokeSaved) }},
	"DescribeMethod":            {"describe_method", func(w workspace.Workspace) handlerFunc { return bind(w.DescribeMethod) }},
	"RunScript":                 {"run_script", func(w workspace.Workspace) handlerFunc { return bind(w.RunScript) }},
	"CreateScript":              {"create_script", func(w workspace.Workspace) handlerFunc { return bind(w.CreateScript) }},
	"UpdateScript":              {"update_script", func(w workspace.Workspace) handlerFunc { return bind(w.UpdateScript) }},
	"DeleteScript":              {"delete_script", func(w workspace.Workspace) handlerFunc { return bind(w.DeleteScript) }},
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
