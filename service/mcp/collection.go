package mcp

import (
	"context"

	mcpruntime "github.com/redpanda-data/protoc-gen-go-mcp/pkg/runtime"
	"google.golang.org/protobuf/reflect/protoreflect"
)

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
