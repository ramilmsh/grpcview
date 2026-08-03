package workspace

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"testing"

	"connectrpc.com/connect"
	"github.com/jhump/protoreflect/desc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoregistry"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/scripting"
	"codeberg.org/ramilmsh/grpcview/service/store"
)

const testWorkspace = "default"

func tsBody(obj string) string { return "export default () => (" + obj + ")" }

func startReflectionServer(t *testing.T, withHealth bool) int {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	if withHealth {
		grpc_health_v1.RegisterHealthServer(srv, health.NewServer())
	}
	reflection.Register(srv)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return lis.Addr().(*net.TCPAddr).Port
}

func newTestWorkspace(t *testing.T) Workspace {
	t.Helper()
	return Workspace{store: store.New(t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))}
}

func newTestWorkspaceWithEngine(t *testing.T) Workspace {
	t.Helper()
	eng, err := scripting.NewEngine(context.Background(), scriptingMaxPages)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	t.Cleanup(func() { _ = eng.Close(context.Background()) })
	return Workspace{
		store:  store.New(t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil))),
		engine: eng,
	}
}

func createGenerator(t *testing.T, w Workspace, ctx context.Context, name, source string) {
	t.Helper()
	coll, err := w.store.Open(ctx, testWorkspace)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := coll.CreateScript(ctx, name, grpcviewv1.ScriptKind_SCRIPT_KIND_GENERATOR); err != nil {
		t.Fatalf("CreateScript %q: %v", name, err)
	}
	if err := coll.UpdateScript(ctx, name, store.ScriptPatch{Source: &source}); err != nil {
		t.Fatalf("UpdateScript %q: %v", name, err)
	}
}

func reflectionAddReq(port int) *grpcviewv1.AddDescriptorSourceRequest {
	return &grpcviewv1.AddDescriptorSourceRequest{
		WorkspaceName: testWorkspace,
		Source: &grpcviewv1.AddDescriptorSourceRequest_Reflection{
			Reflection: &grpcviewv1.Server{Address: fmt.Sprintf("127.0.0.1:%d", port)},
		},
	}
}

func removeReq(id string) *grpcviewv1.RemoveDescriptorSourceRequest {
	return &grpcviewv1.RemoveDescriptorSourceRequest{WorkspaceName: testWorkspace, Id: id}
}

const testUploadName = "test.binpb"

func descriptorSetAddReq(set []byte) *grpcviewv1.AddDescriptorSourceRequest {
	return &grpcviewv1.AddDescriptorSourceRequest{
		WorkspaceName: testWorkspace,
		Source:        &grpcviewv1.AddDescriptorSourceRequest_DescriptorSet{DescriptorSet: set},
		FileName:      testUploadName,
	}
}

func fileDescriptorSet(t *testing.T, path string) []byte {
	t.Helper()
	fd, err := protoregistry.GlobalFiles.FindFileByPath(path)
	if err != nil {
		t.Fatalf("find file descriptor %s: %v", path, err)
	}
	wrapped, err := desc.WrapFile(fd)
	if err != nil {
		t.Fatalf("wrap file descriptor %s: %v", path, err)
	}
	raw, err := proto.Marshal(desc.ToFileDescriptorSet(wrapped))
	if err != nil {
		t.Fatalf("marshal descriptor set: %v", err)
	}
	return raw
}

func ensureWorkspace(t *testing.T, w Workspace, ctx context.Context) {
	t.Helper()
	if _, err := w.Get(ctx, connect.NewRequest(&grpcviewv1.GetRequest{WorkspaceName: testWorkspace})); err != nil {
		t.Fatalf("Get (create): %v", err)
	}
}

func hasService(services []*grpcviewv1.Service, name string) bool {
	for _, s := range services {
		if s.GetName() == name {
			return true
		}
	}
	return false
}

func TestRemoveDescriptorSourceReResolves(t *testing.T) {
	w := newTestWorkspace(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)

	portA := startReflectionServer(t, true)
	portB := startReflectionServer(t, false)

	if _, err := w.AddDescriptorSource(ctx, connect.NewRequest(reflectionAddReq(portA))); err != nil {
		t.Fatalf("AddDescriptorSource A: %v", err)
	}
	addB, err := w.AddDescriptorSource(ctx, connect.NewRequest(reflectionAddReq(portB)))
	if err != nil {
		t.Fatalf("AddDescriptorSource B: %v", err)
	}
	ws := addB.Msg.GetWorkspace()
	if len(ws.GetSources()) != 2 {
		t.Fatalf("want 2 sources, got %d", len(ws.GetSources()))
	}
	if !hasService(ws.GetServices(), "Health") {
		t.Fatalf("Health service missing after adding source A")
	}

	idA := ws.GetSources()[0].GetId()
	remResp, err := w.RemoveDescriptorSource(ctx, connect.NewRequest(removeReq(idA)))
	if err != nil {
		t.Fatalf("RemoveDescriptorSource: %v", err)
	}
	ws = remResp.Msg.GetWorkspace()
	if len(ws.GetSources()) != 1 {
		t.Fatalf("want 1 source after remove, got %d", len(ws.GetSources()))
	}
	if hasService(ws.GetServices(), "Health") {
		t.Fatalf("Health should be gone after removing source A")
	}
	if len(ws.GetServices()) == 0 {
		t.Fatalf("expected B's reflection services to remain after remove")
	}

	reloaded, err := w.Get(ctx, connect.NewRequest(&grpcviewv1.GetRequest{WorkspaceName: testWorkspace}))
	if err != nil {
		t.Fatalf("Get after remove: %v", err)
	}
	if got := reloaded.Msg.GetWorkspace(); len(got.GetSources()) != 1 || hasService(got.GetServices(), "Health") {
		t.Fatalf("removal not persisted: %d sources, Health present=%v",
			len(got.GetSources()), hasService(got.GetServices(), "Health"))
	}

	idB := ws.GetSources()[0].GetId()
	if _, err := w.RemoveDescriptorSource(ctx, connect.NewRequest(removeReq(idB))); err != nil {
		t.Fatalf("RemoveDescriptorSource (last): %v", err)
	}
	final, err := w.Get(ctx, connect.NewRequest(&grpcviewv1.GetRequest{WorkspaceName: testWorkspace}))
	if err != nil {
		t.Fatalf("Get final: %v", err)
	}
	if got := final.Msg.GetWorkspace(); len(got.GetSources()) != 0 || len(got.GetServices()) != 0 {
		t.Fatalf("want empty sources+services, got %d sources / %d services",
			len(got.GetSources()), len(got.GetServices()))
	}
}

func TestAddDescriptorSetSource(t *testing.T) {
	w := newTestWorkspace(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)

	set := fileDescriptorSet(t, "grpc/health/v1/health.proto")
	resp, err := w.AddDescriptorSource(ctx, connect.NewRequest(descriptorSetAddReq(set)))
	if err != nil {
		t.Fatalf("AddDescriptorSource (descriptor set): %v", err)
	}
	ws := resp.Msg.GetWorkspace()
	if len(ws.GetSources()) != 1 {
		t.Fatalf("want 1 source, got %d", len(ws.GetSources()))
	}
	if got := ws.GetSources()[0].GetUpload().GetFileName(); got != testUploadName {
		t.Fatalf("stored source is not the upload: %+v", ws.GetSources()[0])
	}
	if !hasService(ws.GetServices(), "Health") {
		t.Fatalf("Health service missing after adding descriptor-set source")
	}

	reloaded, err := w.Get(ctx, connect.NewRequest(&grpcviewv1.GetRequest{WorkspaceName: testWorkspace}))
	if err != nil {
		t.Fatalf("Get after add: %v", err)
	}
	got := reloaded.Msg.GetWorkspace()
	if len(got.GetSources()) != 1 || got.GetSources()[0].GetUpload().GetFileName() != testUploadName {
		t.Fatalf("descriptor-set source not persisted: %+v", got.GetSources())
	}
	if !hasService(got.GetServices(), "Health") {
		t.Fatalf("Health service missing after reload")
	}
}

func TestRemoveReResolvesDescriptorSetSource(t *testing.T) {
	w := newTestWorkspace(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)

	port := startReflectionServer(t, false)
	if _, err := w.AddDescriptorSource(ctx, connect.NewRequest(reflectionAddReq(port))); err != nil {
		t.Fatalf("AddDescriptorSource (reflection): %v", err)
	}
	set := fileDescriptorSet(t, "grpc/health/v1/health.proto")
	addResp, err := w.AddDescriptorSource(ctx, connect.NewRequest(descriptorSetAddReq(set)))
	if err != nil {
		t.Fatalf("AddDescriptorSource (descriptor set): %v", err)
	}
	ws := addResp.Msg.GetWorkspace()
	if len(ws.GetSources()) != 2 {
		t.Fatalf("want 2 sources, got %d", len(ws.GetSources()))
	}
	if !hasService(ws.GetServices(), "Health") || !hasService(ws.GetServices(), "ServerReflection") {
		t.Fatalf("want both Health (descriptor set) and ServerReflection (reflection): %v", ws.GetServices())
	}

	remResp, err := w.RemoveDescriptorSource(ctx, connect.NewRequest(removeReq(ws.GetSources()[0].GetId())))
	if err != nil {
		t.Fatalf("RemoveDescriptorSource: %v", err)
	}
	ws = remResp.Msg.GetWorkspace()
	if len(ws.GetSources()) != 1 || ws.GetSources()[0].GetUpload().GetFileName() != testUploadName {
		t.Fatalf("want 1 descriptor-set source after remove, got %+v", ws.GetSources())
	}
	if !hasService(ws.GetServices(), "Health") {
		t.Fatalf("Health should survive: descriptor-set source must re-resolve on remove")
	}
	if hasService(ws.GetServices(), "ServerReflection") {
		t.Fatalf("ServerReflection should be gone after removing the reflection source")
	}

	final, err := w.Get(ctx, connect.NewRequest(&grpcviewv1.GetRequest{WorkspaceName: testWorkspace}))
	if err != nil {
		t.Fatalf("Get after remove: %v", err)
	}
	if got := final.Msg.GetWorkspace(); len(got.GetSources()) != 1 || !hasService(got.GetServices(), "Health") {
		t.Fatalf("re-resolved descriptor-set state not persisted: %d sources, Health present=%v",
			len(got.GetSources()), hasService(got.GetServices(), "Health"))
	}
}

func TestRemoveDescriptorSourceUnknownID(t *testing.T) {
	w := newTestWorkspace(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)

	for _, id := range []string{"", "reflection:nope:1", "upload:missing.binpb"} {
		_, err := w.RemoveDescriptorSource(ctx, connect.NewRequest(removeReq(id)))
		if err == nil {
			t.Fatalf("id %q: want error, got nil", id)
		}
		if got := connect.CodeOf(err); got != connect.CodeNotFound {
			t.Fatalf("id %q: want NotFound, got %v (%v)", id, got, err)
		}
	}
}

func TestMutateCreatesTheCollection(t *testing.T) {
	ctx := context.Background()

	t.Run("a tree mutation", func(t *testing.T) {
		w := newTestWorkspace(t)
		if _, err := w.CreateFolder(ctx, connect.NewRequest(&grpcviewv1.CreateFolderRequest{
			WorkspaceName: testWorkspace,
			ItemName:      "First",
		})); err != nil {
			t.Fatalf("CreateFolder on an unread workspace: %v", err)
		}
		got, err := w.Get(ctx, connect.NewRequest(&grpcviewv1.GetRequest{WorkspaceName: testWorkspace}))
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if items := got.Msg.GetWorkspace().GetItem().GetFolder().GetItems(); len(items) != 1 || items[0].GetName() != "First" {
			t.Fatalf("items = %v, want the created folder to have persisted", items)
		}
	})

	t.Run("a source mutation", func(t *testing.T) {
		w := newTestWorkspace(t)
		if _, err := w.ReorderDescriptorSources(ctx, connect.NewRequest(&grpcviewv1.ReorderDescriptorSourcesRequest{
			WorkspaceName: testWorkspace,
		})); err != nil {
			t.Fatalf("ReorderDescriptorSources on an unread workspace: %v", err)
		}
	})
}

func folderNamed(t *testing.T, ws *grpcviewv1.Workspace, name string) *grpcviewv1.Folder {
	t.Helper()
	for _, it := range ws.GetItem().GetFolder().GetItems() {
		if it.GetName() == name {
			if it.GetFolder() == nil {
				t.Fatalf("item %q is not a folder", name)
			}
			return it.GetFolder()
		}
	}
	t.Fatalf("folder %q not found", name)
	return nil
}

func TestUpdateFolderRPC(t *testing.T) {
	w := newTestWorkspace(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)

	if _, err := w.CreateFolder(ctx, connect.NewRequest(&grpcviewv1.CreateFolderRequest{
		WorkspaceName: testWorkspace,
		ItemName:      "Users",
	})); err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	if _, err := w.CreateRequest(ctx, connect.NewRequest(&grpcviewv1.CreateRequestRequest{
		WorkspaceName: testWorkspace,
		ItemName:      "Leaf",
		Service:       "s",
		Method:        "m",
	})); err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}

	script := "export default () => ({ ...gv.metadata.inherit(), team: ['users'] })"
	updResp, err := w.UpdateFolder(ctx, connect.NewRequest(&grpcviewv1.UpdateFolderRequest{
		WorkspaceName:       testWorkspace,
		ItemName:            "Users",
		DraftMetadataScript: &script,
	}))
	if err != nil {
		t.Fatalf("UpdateFolder: %v", err)
	}
	if got := folderNamed(t, updResp.Msg.GetWorkspace(), "Users").GetDraftMetadataScript(); got != script {
		t.Fatalf("folder script in mutate response = %q, want %q", got, script)
	}

	getResp, err := w.Get(ctx, connect.NewRequest(&grpcviewv1.GetRequest{WorkspaceName: testWorkspace}))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got := folderNamed(t, getResp.Msg.GetWorkspace(), "Users").GetDraftMetadataScript(); got != script {
		t.Fatalf("folder script after Get = %q, want %q", got, script)
	}

	empty := ""
	if _, err := w.UpdateFolder(ctx, connect.NewRequest(&grpcviewv1.UpdateFolderRequest{
		WorkspaceName:       testWorkspace,
		ItemName:            "Users",
		DraftMetadataScript: &empty,
	})); err != nil {
		t.Fatalf("UpdateFolder clear: %v", err)
	}
	cleared, err := w.Get(ctx, connect.NewRequest(&grpcviewv1.GetRequest{WorkspaceName: testWorkspace}))
	if err != nil {
		t.Fatalf("Get after clear: %v", err)
	}
	if got := folderNamed(t, cleared.Msg.GetWorkspace(), "Users").GetDraftMetadataScript(); got != "" {
		t.Fatalf("folder script after clear = %q, want empty", got)
	}

	if _, err := w.UpdateFolder(ctx, connect.NewRequest(&grpcviewv1.UpdateFolderRequest{
		WorkspaceName:       testWorkspace,
		ItemName:            "Ghost",
		DraftMetadataScript: &script,
	})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("UpdateFolder missing item = %v, want NotFound", err)
	}

	if _, err := w.UpdateFolder(ctx, connect.NewRequest(&grpcviewv1.UpdateFolderRequest{
		WorkspaceName:       testWorkspace,
		ItemName:            "Leaf",
		DraftMetadataScript: &script,
	})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("UpdateFolder on a request = %v, want FailedPrecondition", err)
	}
}

func TestUpdateFolderRenameRPC(t *testing.T) {
	w := newTestWorkspace(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)

	for _, name := range []string{"Users", "Admin"} {
		if _, err := w.CreateFolder(ctx, connect.NewRequest(&grpcviewv1.CreateFolderRequest{
			WorkspaceName: testWorkspace,
			ItemName:      name,
		})); err != nil {
			t.Fatalf("CreateFolder %s: %v", name, err)
		}
	}

	newName := "People"
	updResp, err := w.UpdateFolder(ctx, connect.NewRequest(&grpcviewv1.UpdateFolderRequest{
		WorkspaceName: testWorkspace,
		ItemName:      "Users",
		Name:          &newName,
	}))
	if err != nil {
		t.Fatalf("UpdateFolder rename: %v", err)
	}
	folderNamed(t, updResp.Msg.GetWorkspace(), newName)

	getResp, err := w.Get(ctx, connect.NewRequest(&grpcviewv1.GetRequest{WorkspaceName: testWorkspace}))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	folderNamed(t, getResp.Msg.GetWorkspace(), newName)
	for _, it := range getResp.Msg.GetWorkspace().GetItem().GetFolder().GetItems() {
		if it.GetName() == "Users" {
			t.Fatalf("old folder name survived the rename")
		}
	}

	collide := "Admin"
	if _, err := w.UpdateFolder(ctx, connect.NewRequest(&grpcviewv1.UpdateFolderRequest{
		WorkspaceName: testWorkspace,
		ItemName:      newName,
		Name:          &collide,
	})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("colliding rename = %v, want FailedPrecondition", err)
	}
}

func itemNames(items []*grpcviewv1.Item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.GetName()
	}
	return out
}

func TestMoveItemRPC(t *testing.T) {
	w := newTestWorkspace(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)

	for _, name := range []string{"Src", "Dst"} {
		if _, err := w.CreateFolder(ctx, connect.NewRequest(&grpcviewv1.CreateFolderRequest{
			WorkspaceName: testWorkspace,
			ItemName:      name,
		})); err != nil {
			t.Fatalf("CreateFolder %s: %v", name, err)
		}
	}
	if _, err := w.CreateRequest(ctx, connect.NewRequest(&grpcviewv1.CreateRequestRequest{
		WorkspaceName: testWorkspace,
		Path:          []string{"Src"},
		ItemName:      "Leaf",
		Service:       "s",
		Method:        "m",
	})); err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}

	moveResp, err := w.MoveItem(ctx, connect.NewRequest(&grpcviewv1.MoveItemRequest{
		WorkspaceName: testWorkspace,
		Path:          []string{"Src"},
		ItemName:      "Leaf",
		NewPath:       []string{"Dst"},
	}))
	if err != nil {
		t.Fatalf("MoveItem: %v", err)
	}
	if got := itemNames(folderNamed(t, moveResp.Msg.GetWorkspace(), "Dst").GetItems()); len(got) != 1 || got[0] != "Leaf" {
		t.Fatalf("Dst in mutate response = %v, want [Leaf]", got)
	}
	if got := itemNames(folderNamed(t, moveResp.Msg.GetWorkspace(), "Src").GetItems()); len(got) != 0 {
		t.Fatalf("Src in mutate response = %v, want empty", got)
	}

	getResp, err := w.Get(ctx, connect.NewRequest(&grpcviewv1.GetRequest{WorkspaceName: testWorkspace}))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got := itemNames(folderNamed(t, getResp.Msg.GetWorkspace(), "Dst").GetItems()); len(got) != 1 || got[0] != "Leaf" {
		t.Fatalf("Dst after Get = %v, want [Leaf]", got)
	}

	if _, err := w.CreateFolder(ctx, connect.NewRequest(&grpcviewv1.CreateFolderRequest{
		WorkspaceName: testWorkspace,
		Path:          []string{"Dst"},
		ItemName:      "Inner",
	})); err != nil {
		t.Fatalf("CreateFolder Inner: %v", err)
	}
	if _, err := w.MoveItem(ctx, connect.NewRequest(&grpcviewv1.MoveItemRequest{
		WorkspaceName: testWorkspace,
		ItemName:      "Dst",
		NewPath:       []string{"Dst", "Inner"},
	})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("move into own descendant = %v, want FailedPrecondition", err)
	}
}
