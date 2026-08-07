package mcp

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/redpanda-data/protoc-gen-go-mcp/pkg/gen"
	mcpruntime "github.com/redpanda-data/protoc-gen-go-mcp/pkg/runtime"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/workspace"
)

func mustLoadService(t *testing.T) protoreflect.ServiceDescriptor {
	t.Helper()
	sd, err := loadWorkspaceService()
	if err != nil {
		t.Fatalf("loadWorkspaceService: %v", err)
	}
	return sd
}

func TestTotality(t *testing.T) {
	sd := mustLoadService(t)
	dispatch := newHandler(workspace.Workspace{})

	for i := 0; i < sd.Methods().Len(); i++ {
		md := sd.Methods().Get(i)
		name := string(md.Name())
		streaming := md.IsStreamingClient() || md.IsStreamingServer()

		_, hasToolName := rpcs[name]

		_, dispatchErr := dispatch(context.Background(), md, nil)
		hasDispatch := dispatchErr == nil || !strings.Contains(dispatchErr.Error(), "no handler registered")

		if streaming {
			if hasToolName {
				t.Errorf("streaming method %s must not have a tool name entry", name)
			}
			if hasDispatch {
				t.Errorf("streaming method %s must not have a dispatch entry", name)
			}
			continue
		}
		if !hasToolName {
			t.Errorf("unary method %s is missing a tool-name entry", name)
		}
		if !hasDispatch {
			t.Errorf("unary method %s is missing a dispatch-map entry", name)
		}
	}
}

var toolNameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)

func TestNameShape(t *testing.T) {
	seen := map[string]bool{}
	for rpc, entry := range rpcs {
		name := entry.tool
		if seen[name] {
			t.Errorf("duplicate tool name %q (from rpc %s)", name, rpc)
		}
		seen[name] = true

		if name != strings.ToLower(name) {
			t.Errorf("tool name %q for rpc %s is not lower_snake_case", name, rpc)
		}
		if !toolNameRe.MatchString(name) {
			t.Errorf("tool name %q for rpc %s does not match go-sdk name pattern", name, rpc)
		}
	}
}

func TestDescriptorLoading(t *testing.T) {
	sd := mustLoadService(t)

	md := sd.Methods().ByName("AddDescriptorSource")
	if md == nil {
		t.Fatal("AddDescriptorSource method not found in loaded service descriptor")
	}

	got := comment(md)
	want := "Appends at LOWEST priority, or refreshes in place when the id already exists."
	if !strings.Contains(got, want) {
		t.Fatalf("comment(AddDescriptorSource) = %q, want it to contain %q", got, want)
	}
}

func TestTrimHeavyFieldsStripsEveryShape(t *testing.T) {
	// Collection.descriptor_set nests under a message field; DescribeMethodResponse's is
	// top-level. Both must go, and nothing else may.
	sd := mustLoadService(t)
	handler := func(res proto.Message) gen.Handler {
		return func(context.Context, protoreflect.MethodDescriptor, proto.Message) (proto.Message, error) {
			return res, nil
		}
	}

	nested := &grpcviewv1.GetResponse{Collection: &grpcviewv1.Collection{
		Name:          "scratch",
		DescriptorSet: []byte("binary"),
	}}
	got, err := trimHeavyFields(handler(nested))(context.Background(), sd.Methods().ByName("Get"), nil)
	if err != nil {
		t.Fatalf("trimHeavyFields (nested): %v", err)
	}
	coll := got.(*grpcviewv1.GetResponse).GetCollection()
	if coll.GetDescriptorSet() != nil {
		t.Errorf("Collection.descriptor_set survived: %q", coll.GetDescriptorSet())
	}
	if coll.GetName() != "scratch" {
		t.Errorf("Collection.name was altered: %q", coll.GetName())
	}

	top := &grpcviewv1.DescribeMethodResponse{
		ProtoText:     "message Foo {}",
		SourceId:      "reflection:localhost:9000",
		DescriptorSet: []byte("binary"),
	}
	got, err = trimHeavyFields(handler(top))(context.Background(), sd.Methods().ByName("DescribeMethod"), nil)
	if err != nil {
		t.Fatalf("trimHeavyFields (top level): %v", err)
	}
	desc := got.(*grpcviewv1.DescribeMethodResponse)
	if desc.GetDescriptorSet() != nil {
		t.Errorf("DescribeMethodResponse.descriptor_set survived: %q", desc.GetDescriptorSet())
	}
	if desc.GetProtoText() != "message Foo {}" || desc.GetSourceId() != "reflection:localhost:9000" {
		t.Errorf("sibling fields were altered: %+v", desc)
	}
}

// A recorded response can carry a descriptor set of its own, so history is what actually
// blows the per-result cap. get_collection is an agent's only access to it and keeps it;
// every other RPC drops it.
func TestTrimHeavyFieldsKeepsHistoryOnlyForGet(t *testing.T) {
	sd := mustLoadService(t)
	withHistory := func() *grpcviewv1.Collection {
		return &grpcviewv1.Collection{
			Name: "scratch",
			Item: &grpcviewv1.Item{Content: &grpcviewv1.Item_Folder{Folder: &grpcviewv1.Folder{
				Items: []*grpcviewv1.Item{{
					Name: "Unary",
					Content: &grpcviewv1.Item_Request{Request: &grpcviewv1.Request{
						Name: "Unary",
						History: []*grpcviewv1.History{{
							Response: &grpcviewv1.History_Response{Response: []byte(`{"big":"payload"}`)},
						}},
					}},
				}},
			}}},
		}
	}
	handler := func(res proto.Message) gen.Handler {
		return func(context.Context, protoreflect.MethodDescriptor, proto.Message) (proto.Message, error) {
			return res, nil
		}
	}
	historyOf := func(item *grpcviewv1.Item) []*grpcviewv1.History {
		return item.GetFolder().GetItems()[0].GetRequest().GetHistory()
	}

	got, err := trimHeavyFields(handler(&grpcviewv1.GetResponse{Collection: withHistory()}))(
		context.Background(), sd.Methods().ByName("Get"), nil)
	if err != nil {
		t.Fatalf("trimHeavyFields (Get): %v", err)
	}
	if n := len(historyOf(got.(*grpcviewv1.GetResponse).GetCollection().GetItem())); n != 1 {
		t.Errorf("get_collection returned %d history entries, want 1", n)
	}

	got, err = trimHeavyFields(handler(&grpcviewv1.CreateRequestResponse{Collection: withHistory()}))(
		context.Background(), sd.Methods().ByName("CreateRequest"), nil)
	if err != nil {
		t.Fatalf("trimHeavyFields (CreateRequest): %v", err)
	}
	mutated := got.(*grpcviewv1.CreateRequestResponse).GetCollection()
	if n := len(historyOf(mutated.GetItem())); n != 0 {
		t.Errorf("create_request returned %d history entries, want 0", n)
	}
	if mutated.GetItem().GetFolder().GetItems()[0].GetRequest().GetName() != "Unary" {
		t.Errorf("sibling fields were altered: %+v", mutated)
	}
}

// collectionPath's depth bound is an assumption about the schema, so pin it to the schema:
// every request carrying a collection field anywhere must be reachable within the bound,
// or that tool silently loses its default-collection convenience.
func TestCollectionPathReachesEveryCollectionField(t *testing.T) {
	sd := mustLoadService(t)

	for i := 0; i < sd.Methods().Len(); i++ {
		md := sd.Methods().Get(i)
		if md.IsStreamingClient() || md.IsStreamingServer() {
			continue
		}
		in := md.Input()
		if !carriesCollection(in, collectionSearchDepth+2) {
			continue
		}
		if path := collectionPath(in, collectionSearchDepth); path == nil {
			t.Errorf("%s carries a collection field that collectionPath cannot reach within depth %d",
				md.Name(), collectionSearchDepth)
		}
	}
}

func carriesCollection(md protoreflect.MessageDescriptor, depth int) bool {
	if md == nil || depth == 0 {
		return false
	}
	fields := md.Fields()
	for i := 0; i < fields.Len(); i++ {
		fd := fields.Get(i)
		if fd.Name() == "collection" && fd.Kind() == protoreflect.StringKind && !fd.IsList() {
			return true
		}
		if fd.Kind() == protoreflect.MessageKind && !fd.IsMap() && carriesCollection(fd.Message(), depth-1) {
			return true
		}
	}
	return false
}

func TestDefaultCollectionFillsNestedAndTopLevel(t *testing.T) {
	pass := func(context.Context, *mcpruntime.CallToolRequest) (*mcpruntime.CallToolResult, error) {
		return &mcpruntime.CallToolResult{}, nil
	}

	for _, tc := range []struct {
		name string
		path []string
		args map[string]any
		want func(map[string]any) string
	}{
		{
			name: "top level, absent",
			path: []string{"collection"},
			args: map[string]any{},
			want: func(a map[string]any) string { return a["collection"].(string) },
		},
		{
			name: "top level, present but empty",
			path: []string{"collection"},
			args: map[string]any{"collection": ""},
			want: func(a map[string]any) string { return a["collection"].(string) },
		},
		{
			name: "nested, spec absent entirely",
			path: []string{"spec", "collection"},
			args: map[string]any{},
			want: func(a map[string]any) string { return a["spec"].(map[string]any)["collection"].(string) },
		},
		{
			name: "nested, spec present without collection",
			path: []string{"spec", "collection"},
			args: map[string]any{"spec": map[string]any{"service": "x"}},
			want: func(a map[string]any) string { return a["spec"].(map[string]any)["collection"].(string) },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := &mcpruntime.CallToolRequest{Arguments: tc.args}
			if _, err := defaultCollection(pass, "svc", tc.path)(context.Background(), req); err != nil {
				t.Fatalf("defaultCollection: %v", err)
			}
			if got := tc.want(req.Arguments); got != "svc" {
				t.Errorf("collection = %q, want %q", got, "svc")
			}
		})
	}

	// An explicit collection is never overwritten.
	req := &mcpruntime.CallToolRequest{Arguments: map[string]any{"collection": "mine"}}
	if _, err := defaultCollection(pass, "svc", []string{"collection"})(context.Background(), req); err != nil {
		t.Fatalf("defaultCollection: %v", err)
	}
	if got := req.Arguments["collection"]; got != "mine" {
		t.Errorf("explicit collection was overwritten with %q", got)
	}
}
