package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/redpanda-data/protoc-gen-go-mcp/pkg/gen"
	mcpruntime "github.com/redpanda-data/protoc-gen-go-mcp/pkg/runtime"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/grpcview/v1"
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

		entry, hasToolName := rpcs[name]
		if _, excluded := notTools[name]; excluded {
			if hasToolName {
				t.Errorf("method %s is both excluded and registered as a tool", name)
			}
			continue
		}
		if !hasToolName {
			t.Errorf("method %s is missing a tool-name entry", name)
			continue
		}

		_, dispatchErr := dispatch(context.Background(), md, nil)
		hasDispatch := dispatchErr == nil || !strings.Contains(dispatchErr.Error(), "no handler registered")

		if entry.bind != nil && entry.stream != nil {
			t.Errorf("method %s has both a unary and a streaming bind", name)
		}
		if streaming {
			if entry.stream == nil {
				t.Errorf("streaming method %s is missing a streaming bind", name)
			}
			if hasDispatch {
				t.Errorf("streaming method %s must not reach the unary dispatch map", name)
			}
			continue
		}
		if entry.bind == nil {
			t.Errorf("unary method %s is missing a unary bind", name)
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

// The hoisted names must survive the whole plugin path, not just look right in the schema:
// arguments are re-marshalled to JSON and read back with protojson, so a hoisted member only
// works because it is a real proto field name.
func TestHoistedOneofMembersReachTheHandler(t *testing.T) {
	sd := mustLoadService(t)

	var got proto.Message
	record := func(_ context.Context, _ protoreflect.MethodDescriptor, req proto.Message) (proto.Message, error) {
		got = req
		return &grpcviewv1.AddDescriptorSourceResponse{}, nil
	}

	rec := &recordingServer{}
	s := &shim{MCPServer: rec, service: sd}
	gen.RegisterService(s, sd, record, gen.RegisterServiceOptions{NewMessage: newMessage, CommentProvider: comment})

	handler, ok := rec.handlers["add_source"]
	if !ok {
		t.Fatal("add_source was never registered")
	}

	call := func(args map[string]any) error {
		got = nil
		res, err := handler(context.Background(), &mcpruntime.CallToolRequest{Arguments: args})
		if err != nil {
			return err
		}
		if res != nil && res.IsError {
			return errors.New("tool error: " + res.Text)
		}
		return nil
	}

	if err := call(map[string]any{
		"collection": "example",
		"bazel":      map[string]any{"label": "//grpcview/v1:grpcviewv1_proto"},
	}); err != nil {
		t.Fatalf("add_source with bazel: %v", err)
	}
	req := got.(*grpcviewv1.AddDescriptorSourceRequest)
	if req.GetBazel().GetLabel() != "//grpcview/v1:grpcviewv1_proto" {
		t.Fatalf("bazel did not reach the handler as the bazel oneof case: %+v", req)
	}

	if err := call(map[string]any{
		"collection": "example",
		"reflection": map[string]any{"address": "localhost:10000"},
	}); err != nil {
		t.Fatalf("add_source with reflection: %v", err)
	}
	req = got.(*grpcviewv1.AddDescriptorSourceRequest)
	if req.GetReflection().GetAddress() != "localhost:10000" {
		t.Fatalf("reflection did not reach the handler as the reflection oneof case: %+v", req)
	}

	err := call(map[string]any{
		"collection": "example",
		"bazel":      map[string]any{"label": "//x:y"},
		"reflection": map[string]any{"address": "localhost:10000"},
	})
	if err == nil {
		t.Fatal("setting two members of one oneof succeeded, want an error")
	}
}

// Measured on `example`: a request edit's response is 19.8 KB, of which `services` is 7.0 KB and
// `scripts` 5.5 KB that the edit could not have changed. Each field survives only on the RPCs
// that own it, and on the read.
func TestTrimHeavyFieldsKeepsDerivedListsOnlyForTheirOwners(t *testing.T) {
	sd := mustLoadService(t)
	full := func() *grpcviewv1.Collection {
		return &grpcviewv1.Collection{
			Name:     "scratch",
			Services: []*grpcviewv1.Service{{Package: "a.v1", Name: "S"}},
			Scripts:  []*grpcviewv1.Script{{Path: "scripts/smoke.ts"}},
		}
	}
	handler := func(res proto.Message) gen.Handler {
		return func(context.Context, protoreflect.MethodDescriptor, proto.Message) (proto.Message, error) {
			return res, nil
		}
	}

	for _, tc := range []struct {
		method                 string
		res                    func() proto.Message
		read                   func(proto.Message) *grpcviewv1.Collection
		wantServices, wantScrs int
	}{
		{
			method: "Get", wantServices: 1, wantScrs: 1,
			res:  func() proto.Message { return &grpcviewv1.GetResponse{Collection: full()} },
			read: func(m proto.Message) *grpcviewv1.Collection { return m.(*grpcviewv1.GetResponse).GetCollection() },
		},
		{
			method: "UpdateRequest", wantServices: 0, wantScrs: 0,
			res: func() proto.Message { return &grpcviewv1.UpdateRequestResponse{Collection: full()} },
			read: func(m proto.Message) *grpcviewv1.Collection {
				return m.(*grpcviewv1.UpdateRequestResponse).GetCollection()
			},
		},
		{
			method: "AddDescriptorSource", wantServices: 1, wantScrs: 0,
			res: func() proto.Message { return &grpcviewv1.AddDescriptorSourceResponse{Collection: full()} },
			read: func(m proto.Message) *grpcviewv1.Collection {
				return m.(*grpcviewv1.AddDescriptorSourceResponse).GetCollection()
			},
		},
		{
			method: "UpdateScript", wantServices: 0, wantScrs: 1,
			res: func() proto.Message { return &grpcviewv1.UpdateScriptResponse{Collection: full()} },
			read: func(m proto.Message) *grpcviewv1.Collection {
				return m.(*grpcviewv1.UpdateScriptResponse).GetCollection()
			},
		},
	} {
		t.Run(tc.method, func(t *testing.T) {
			got, err := trimHeavyFields(handler(tc.res()))(context.Background(), sd.Methods().ByName(protoreflect.Name(tc.method)), nil)
			if err != nil {
				t.Fatalf("trimHeavyFields: %v", err)
			}
			coll := tc.read(got)
			if n := len(coll.GetServices()); n != tc.wantServices {
				t.Errorf("services = %d, want %d", n, tc.wantServices)
			}
			if n := len(coll.GetScripts()); n != tc.wantScrs {
				t.Errorf("scripts = %d, want %d", n, tc.wantScrs)
			}
			if coll.GetName() != "scratch" {
				t.Errorf("sibling fields were altered: %+v", coll)
			}
		})
	}
}

// The descriptor set that overflowed the tool-result cap was base64 inside a JSON body inside a
// bytes field, which the proto walk cannot reach. The guard is generic: any oversized string in
// the body goes, and everything around it is left byte-for-byte alone.
func TestShrinkResponseBodyElidesOversizedStrings(t *testing.T) {
	sd := mustLoadService(t)
	handler := func(res proto.Message) gen.Handler {
		return func(context.Context, protoreflect.MethodDescriptor, proto.Message) (proto.Message, error) {
			return res, nil
		}
	}

	huge := strings.Repeat("A", maxResponseStringBytes+1)
	body, err := json.Marshal(map[string]any{
		"protoText":     "message Foo {}",
		"sourceId":      "bazel://proto:proto",
		"descriptorSet": huge,
	})
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}

	got, err := trimHeavyFields(handler(&grpcviewv1.InvokeResponse{
		Response: &grpcviewv1.Request_Response{Response: body},
	}))(context.Background(), sd.Methods().ByName("Invoke"), nil)
	if err != nil {
		t.Fatalf("trimHeavyFields: %v", err)
	}

	var out map[string]any
	if err := json.Unmarshal(got.(*grpcviewv1.InvokeResponse).GetResponse().GetResponse(), &out); err != nil {
		t.Fatalf("the shrunk body is not JSON: %v", err)
	}
	elided, _ := out["descriptorSet"].(string)
	if strings.Contains(elided, "AAAA") || !strings.Contains(elided, "elided") {
		t.Fatalf("descriptorSet = %q, want an elision marker", truncate(elided))
	}
	if !strings.Contains(elided, "8193 bytes") {
		t.Fatalf("marker does not name the elided size: %q", elided)
	}
	if out["protoText"] != "message Foo {}" || out["sourceId"] != "bazel://proto:proto" {
		t.Fatalf("sibling fields were altered: %+v", out)
	}

	// A recorded response is the other shape carrying a body, and `get_collection` replays every
	// one of them: 13 recorded DescribeMethod calls made one read 396 KB.
	got, err = trimHeavyFields(handler(&grpcviewv1.GetResponse{
		Collection: &grpcviewv1.Collection{
			Item: &grpcviewv1.Item{Name: "root", Content: &grpcviewv1.Item_Request{Request: &grpcviewv1.Request{
				Name:    "one",
				History: []*grpcviewv1.History{{Response: &grpcviewv1.History_Response{Response: body}}},
			}}},
		},
	}))(context.Background(), sd.Methods().ByName("Get"), nil)
	if err != nil {
		t.Fatalf("trimHeavyFields: %v", err)
	}
	replayed := got.(*grpcviewv1.GetResponse).GetCollection().GetItem().GetRequest().GetHistory()
	if len(replayed) != 1 {
		t.Fatalf("history = %d entries, want 1", len(replayed))
	}
	if err := json.Unmarshal(replayed[0].GetResponse().GetResponse(), &out); err != nil {
		t.Fatalf("the shrunk history body is not JSON: %v", err)
	}
	if elided, _ := out["descriptorSet"].(string); !strings.Contains(elided, "elided") {
		t.Fatalf("history descriptorSet = %q, want an elision marker", truncate(elided))
	}
}

func TestShrinkResponseBodyLeavesOrdinaryBodiesAlone(t *testing.T) {
	small := []byte(`{"value":"hi"}`)
	if got := shrinkResponseBody(small); string(got) != string(small) {
		t.Fatalf("small body was rewritten: %s", got)
	}

	// Big but with no single oversized string: the shape survives untouched.
	many := make([]any, 4096)
	for i := range many {
		many[i] = "chunk"
	}
	raw, err := json.Marshal(many)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(raw) <= maxResponseStringBytes {
		t.Fatalf("fixture is only %d bytes, it must exceed the threshold", len(raw))
	}
	if got := shrinkResponseBody(raw); string(got) != string(raw) {
		t.Fatalf("a body of many small strings was rewritten: %s", truncate(string(got)))
	}

	// Not JSON at all: capped whole, with the marker naming what went.
	blob := []byte(strings.Repeat("x", maxResponseStringBytes*2))
	got := shrinkResponseBody(blob)
	if len(got) >= len(blob) {
		t.Fatalf("a non-JSON body of %d bytes came back as %d", len(blob), len(got))
	}
	if !strings.Contains(string(got), "elided") {
		t.Fatalf("non-JSON body kept no marker: %s", truncate(string(got)))
	}
}

func truncate(s string) string {
	if len(s) <= 120 {
		return s
	}
	return s[:120] + "…"
}

// collectionPath's depth bound is an assumption about the schema, so pin it to the schema:
// every request carrying a collection field anywhere must be reachable within the bound,
// or that tool silently loses its default-collection convenience.
func TestCollectionPathReachesEveryCollectionField(t *testing.T) {
	sd := mustLoadService(t)

	for i := 0; i < sd.Methods().Len(); i++ {
		md := sd.Methods().Get(i)
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
