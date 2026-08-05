package workspace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
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
	port, _ := startStoppableReflectionServer(t, withHealth)
	return port
}

// startStoppableReflectionServer also hands back the stop function, for tests that have to take
// the target away mid-test to prove an operation does not dial.
func startStoppableReflectionServer(t *testing.T, withHealth bool) (int, func()) {
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
	return lis.Addr().(*net.TCPAddr).Port, srv.Stop
}

func newTestWorkspace(t *testing.T) Workspace {
	t.Helper()
	return Workspace{
		store: store.New(t.TempDir(), t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil))),
		defs:  newDefinitionsCache(),
	}
}

func newTestWorkspaceWithEngine(t *testing.T) Workspace {
	t.Helper()
	eng, err := scripting.NewEngine(context.Background(), scriptingMaxPages)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	t.Cleanup(func() { _ = eng.Close(context.Background()) })
	return Workspace{
		store:  store.New(t.TempDir(), t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil))),
		engine: eng,
		defs:   newDefinitionsCache(),
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
		Collection: testWorkspace,
		Source: &grpcviewv1.AddDescriptorSourceRequest_Reflection{
			Reflection: &grpcviewv1.Server{Address: fmt.Sprintf("127.0.0.1:%d", port)},
		},
	}
}

func removeReq(id string) *grpcviewv1.RemoveDescriptorSourceRequest {
	return &grpcviewv1.RemoveDescriptorSourceRequest{Collection: testWorkspace, Id: id}
}

const testUploadName = "test.binpb"

func descriptorSetAddReq(set []byte) *grpcviewv1.AddDescriptorSourceRequest {
	return &grpcviewv1.AddDescriptorSourceRequest{
		Collection: testWorkspace,
		Source:     &grpcviewv1.AddDescriptorSourceRequest_DescriptorSet{DescriptorSet: set},
		FileName:   testUploadName,
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

// ensureWorkspace explicitly creates the test collection: Get no longer does this on
// demand (see TestGetMissingCollectionIsNotFound), so every test that needs the collection
// to already exist must ask for that itself.
func ensureWorkspace(t *testing.T, w Workspace, ctx context.Context) {
	t.Helper()
	coll, err := w.store.Open(ctx, testWorkspace)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := coll.Create(ctx, ""); err != nil {
		t.Fatalf("Create: %v", err)
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
	ws := addB.Msg.GetCollection()
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
	ws = remResp.Msg.GetCollection()
	if len(ws.GetSources()) != 1 {
		t.Fatalf("want 1 source after remove, got %d", len(ws.GetSources()))
	}
	if hasService(ws.GetServices(), "Health") {
		t.Fatalf("Health should be gone after removing source A")
	}
	if len(ws.GetServices()) == 0 {
		t.Fatalf("expected B's reflection services to remain after remove")
	}

	reloaded, err := w.Get(ctx, connect.NewRequest(&grpcviewv1.GetRequest{Collection: testWorkspace}))
	if err != nil {
		t.Fatalf("Get after remove: %v", err)
	}
	if got := reloaded.Msg.GetCollection(); len(got.GetSources()) != 1 || hasService(got.GetServices(), "Health") {
		t.Fatalf("removal not persisted: %d sources, Health present=%v",
			len(got.GetSources()), hasService(got.GetServices(), "Health"))
	}

	idB := ws.GetSources()[0].GetId()
	if _, err := w.RemoveDescriptorSource(ctx, connect.NewRequest(removeReq(idB))); err != nil {
		t.Fatalf("RemoveDescriptorSource (last): %v", err)
	}
	final, err := w.Get(ctx, connect.NewRequest(&grpcviewv1.GetRequest{Collection: testWorkspace}))
	if err != nil {
		t.Fatalf("Get final: %v", err)
	}
	if got := final.Msg.GetCollection(); len(got.GetSources()) != 0 || len(got.GetServices()) != 0 {
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
	ws := resp.Msg.GetCollection()
	if len(ws.GetSources()) != 1 {
		t.Fatalf("want 1 source, got %d", len(ws.GetSources()))
	}
	if got := ws.GetSources()[0].GetUpload().GetFileName(); got != testUploadName {
		t.Fatalf("stored source is not the upload: %+v", ws.GetSources()[0])
	}
	if !hasService(ws.GetServices(), "Health") {
		t.Fatalf("Health service missing after adding descriptor-set source")
	}

	reloaded, err := w.Get(ctx, connect.NewRequest(&grpcviewv1.GetRequest{Collection: testWorkspace}))
	if err != nil {
		t.Fatalf("Get after add: %v", err)
	}
	got := reloaded.Msg.GetCollection()
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
	ws := addResp.Msg.GetCollection()
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
	ws = remResp.Msg.GetCollection()
	if len(ws.GetSources()) != 1 || ws.GetSources()[0].GetUpload().GetFileName() != testUploadName {
		t.Fatalf("want 1 descriptor-set source after remove, got %+v", ws.GetSources())
	}
	if !hasService(ws.GetServices(), "Health") {
		t.Fatalf("Health should survive: descriptor-set source must re-resolve on remove")
	}
	if hasService(ws.GetServices(), "ServerReflection") {
		t.Fatalf("ServerReflection should be gone after removing the reflection source")
	}

	final, err := w.Get(ctx, connect.NewRequest(&grpcviewv1.GetRequest{Collection: testWorkspace}))
	if err != nil {
		t.Fatalf("Get after remove: %v", err)
	}
	if got := final.Msg.GetCollection(); len(got.GetSources()) != 1 || !hasService(got.GetServices(), "Health") {
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

// TestGetMissingCollectionIsNotFound is the point of removing the implicit-create behavior
// (see AGENTS.md): a stale query for a collection that was never created must fail cleanly
// and leave nothing behind on disk, not materialize one.
func TestGetMissingCollectionIsNotFound(t *testing.T) {
	root := t.TempDir()
	w := Workspace{
		store: store.New(root, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil))),
		defs:  newDefinitionsCache(),
	}
	ctx := context.Background()

	_, err := w.Get(ctx, connect.NewRequest(&grpcviewv1.GetRequest{Collection: testWorkspace}))
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("Get on a missing collection = %v, want NotFound", err)
	}

	entries, rerr := os.ReadDir(root)
	if rerr != nil {
		t.Fatalf("ReadDir workspace root: %v", rerr)
	}
	if len(entries) != 0 {
		t.Errorf("Get must create nothing on disk, found %v", entries)
	}
}

// TestMutateOnMissingCollectionIsNotFound is Get's counterpart for the write side: a
// mutating RPC against a collection that was never created must fail the same way, not
// stand one up on demand.
func TestMutateOnMissingCollectionIsNotFound(t *testing.T) {
	root := t.TempDir()
	w := Workspace{
		store: store.New(root, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil))),
		defs:  newDefinitionsCache(),
	}
	ctx := context.Background()

	_, err := w.CreateFolder(ctx, connect.NewRequest(&grpcviewv1.CreateFolderRequest{
		Collection: testWorkspace,
		ItemName:   "First",
	}))
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("CreateFolder on a missing collection = %v, want NotFound", err)
	}

	entries, rerr := os.ReadDir(root)
	if rerr != nil {
		t.Fatalf("ReadDir workspace root: %v", rerr)
	}
	if len(entries) != 0 {
		t.Errorf("CreateFolder must create nothing on disk, found %v", entries)
	}
}

// TestCreateCollection covers the RPC that replaces the implicit auto-create: it must
// default the display name to the directory's own base name, honor an explicit one, and
// refuse to silently reuse an address that already holds a collection.
func TestCreateCollection(t *testing.T) {
	root := t.TempDir()
	w := Workspace{
		store: store.New(root, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil))),
		defs:  newDefinitionsCache(),
	}
	ctx := context.Background()

	resp, err := w.CreateCollection(ctx, connect.NewRequest(&grpcviewv1.CreateCollectionRequest{Collection: "requests"}))
	if err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}
	if got := resp.Msg.GetCollection().GetName(); got != "requests" {
		t.Errorf("default name = %q, want the directory base name %q", got, "requests")
	}

	if _, err := w.CreateCollection(ctx, connect.NewRequest(&grpcviewv1.CreateCollectionRequest{Collection: "requests"})); connect.CodeOf(err) != connect.CodeAlreadyExists {
		t.Fatalf("second CreateCollection at the same address = %v, want AlreadyExists", err)
	}

	named, err := w.CreateCollection(ctx, connect.NewRequest(&grpcviewv1.CreateCollectionRequest{
		Collection: "other",
		Name:       "My Collection",
	}))
	if err != nil {
		t.Fatalf("CreateCollection with an explicit name: %v", err)
	}
	if got := named.Msg.GetCollection().GetName(); got != "My Collection" {
		t.Errorf("name = %q, want the explicit %q", got, "My Collection")
	}
}

func folderNamed(t *testing.T, ws *grpcviewv1.Collection, name string) *grpcviewv1.Folder {
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
		Collection: testWorkspace,
		ItemName:   "Users",
	})); err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	if _, err := w.CreateRequest(ctx, connect.NewRequest(&grpcviewv1.CreateRequestRequest{
		Collection: testWorkspace,
		ItemName:   "Leaf",
		Service:    "s",
		Method:     "m",
	})); err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}

	script := "export default () => ({ ...gv.metadata.inherit(), team: ['users'] })"
	updResp, err := w.UpdateFolder(ctx, connect.NewRequest(&grpcviewv1.UpdateFolderRequest{
		Collection:          testWorkspace,
		ItemName:            "Users",
		DraftMetadataScript: &script,
	}))
	if err != nil {
		t.Fatalf("UpdateFolder: %v", err)
	}
	if got := folderNamed(t, updResp.Msg.GetCollection(), "Users").GetDraftMetadataScript(); got != script {
		t.Fatalf("folder script in mutate response = %q, want %q", got, script)
	}

	getResp, err := w.Get(ctx, connect.NewRequest(&grpcviewv1.GetRequest{Collection: testWorkspace}))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got := folderNamed(t, getResp.Msg.GetCollection(), "Users").GetDraftMetadataScript(); got != script {
		t.Fatalf("folder script after Get = %q, want %q", got, script)
	}

	empty := ""
	if _, err := w.UpdateFolder(ctx, connect.NewRequest(&grpcviewv1.UpdateFolderRequest{
		Collection:          testWorkspace,
		ItemName:            "Users",
		DraftMetadataScript: &empty,
	})); err != nil {
		t.Fatalf("UpdateFolder clear: %v", err)
	}
	cleared, err := w.Get(ctx, connect.NewRequest(&grpcviewv1.GetRequest{Collection: testWorkspace}))
	if err != nil {
		t.Fatalf("Get after clear: %v", err)
	}
	if got := folderNamed(t, cleared.Msg.GetCollection(), "Users").GetDraftMetadataScript(); got != "" {
		t.Fatalf("folder script after clear = %q, want empty", got)
	}

	if _, err := w.UpdateFolder(ctx, connect.NewRequest(&grpcviewv1.UpdateFolderRequest{
		Collection:          testWorkspace,
		ItemName:            "Ghost",
		DraftMetadataScript: &script,
	})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("UpdateFolder missing item = %v, want NotFound", err)
	}

	if _, err := w.UpdateFolder(ctx, connect.NewRequest(&grpcviewv1.UpdateFolderRequest{
		Collection:          testWorkspace,
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
			Collection: testWorkspace,
			ItemName:   name,
		})); err != nil {
			t.Fatalf("CreateFolder %s: %v", name, err)
		}
	}

	newName := "People"
	updResp, err := w.UpdateFolder(ctx, connect.NewRequest(&grpcviewv1.UpdateFolderRequest{
		Collection: testWorkspace,
		ItemName:   "Users",
		Name:       &newName,
	}))
	if err != nil {
		t.Fatalf("UpdateFolder rename: %v", err)
	}
	folderNamed(t, updResp.Msg.GetCollection(), newName)

	getResp, err := w.Get(ctx, connect.NewRequest(&grpcviewv1.GetRequest{Collection: testWorkspace}))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	folderNamed(t, getResp.Msg.GetCollection(), newName)
	for _, it := range getResp.Msg.GetCollection().GetItem().GetFolder().GetItems() {
		if it.GetName() == "Users" {
			t.Fatalf("old folder name survived the rename")
		}
	}

	collide := "Admin"
	if _, err := w.UpdateFolder(ctx, connect.NewRequest(&grpcviewv1.UpdateFolderRequest{
		Collection: testWorkspace,
		ItemName:   newName,
		Name:       &collide,
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
			Collection: testWorkspace,
			ItemName:   name,
		})); err != nil {
			t.Fatalf("CreateFolder %s: %v", name, err)
		}
	}
	if _, err := w.CreateRequest(ctx, connect.NewRequest(&grpcviewv1.CreateRequestRequest{
		Collection: testWorkspace,
		Path:       []string{"Src"},
		ItemName:   "Leaf",
		Service:    "s",
		Method:     "m",
	})); err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}

	moveResp, err := w.MoveItem(ctx, connect.NewRequest(&grpcviewv1.MoveItemRequest{
		Collection: testWorkspace,
		Path:       []string{"Src"},
		ItemName:   "Leaf",
		NewPath:    []string{"Dst"},
	}))
	if err != nil {
		t.Fatalf("MoveItem: %v", err)
	}
	if got := itemNames(folderNamed(t, moveResp.Msg.GetCollection(), "Dst").GetItems()); len(got) != 1 || got[0] != "Leaf" {
		t.Fatalf("Dst in mutate response = %v, want [Leaf]", got)
	}
	if got := itemNames(folderNamed(t, moveResp.Msg.GetCollection(), "Src").GetItems()); len(got) != 0 {
		t.Fatalf("Src in mutate response = %v, want empty", got)
	}

	getResp, err := w.Get(ctx, connect.NewRequest(&grpcviewv1.GetRequest{Collection: testWorkspace}))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got := itemNames(folderNamed(t, getResp.Msg.GetCollection(), "Dst").GetItems()); len(got) != 1 || got[0] != "Leaf" {
		t.Fatalf("Dst after Get = %v, want [Leaf]", got)
	}

	if _, err := w.CreateFolder(ctx, connect.NewRequest(&grpcviewv1.CreateFolderRequest{
		Collection: testWorkspace,
		Path:       []string{"Dst"},
		ItemName:   "Inner",
	})); err != nil {
		t.Fatalf("CreateFolder Inner: %v", err)
	}
	if _, err := w.MoveItem(ctx, connect.NewRequest(&grpcviewv1.MoveItemRequest{
		Collection: testWorkspace,
		ItemName:   "Dst",
		NewPath:    []string{"Dst", "Inner"},
	})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("move into own descendant = %v, want FailedPrecondition", err)
	}
}

func commitReq(id string, commit bool) *grpcviewv1.SetDescriptorSourceCommitRequest {
	return &grpcviewv1.SetDescriptorSourceCommitRequest{Collection: testWorkspace, Id: id, Commit: commit}
}

// sourceByID finds a row in the response's source list; the flag is round-tripped through the
// manifest, so reading it back off a handler's Collection is the assertion that it persisted.
func sourceByID(ws *grpcviewv1.Collection, id string) *grpcviewv1.DescriptorSource {
	for _, src := range ws.GetSources() {
		if src.GetId() == id {
			return src
		}
	}
	return nil
}

func collectionDir(t *testing.T, w Workspace) string {
	t.Helper()
	coll, err := w.store.Open(context.Background(), testWorkspace)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return coll.Root()
}

func committedSidecars(t *testing.T, w Workspace) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(collectionDir(t, w), "descriptors"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatalf("ReadDir descriptors: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// TestSetDescriptorSourceCommitNeverDials is the promise the RPC exists for: the toggle moves the
// bytes the store already holds between the two locations, so it still works with the reflection
// target gone — a dial would fail the call outright.
func TestSetDescriptorSourceCommitNeverDials(t *testing.T) {
	w := newTestWorkspace(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)

	port, stop := startStoppableReflectionServer(t, true)
	added, err := w.AddDescriptorSource(ctx, connect.NewRequest(reflectionAddReq(port)))
	if err != nil {
		t.Fatalf("AddDescriptorSource: %v", err)
	}
	id := added.Msg.GetCollection().GetSources()[0].GetId()
	if got := committedSidecars(t, w); len(got) != 0 {
		t.Fatalf("a source added without the flag must commit nothing, got %v", got)
	}
	stop()

	on, err := w.SetDescriptorSourceCommit(ctx, connect.NewRequest(commitReq(id, true)))
	if err != nil {
		t.Fatalf("SetDescriptorSourceCommit(on) with the target down: %v", err)
	}
	if got := committedSidecars(t, w); len(got) != 1 {
		t.Fatalf("committing must write one sidecar, got %v", got)
	}
	if !sourceByID(on.Msg.GetCollection(), id).GetCommitDescriptors() {
		t.Errorf("commit_descriptors did not come back set: %+v", on.Msg.GetCollection().GetSources())
	}
	if !hasService(on.Msg.GetCollection().GetServices(), "Health") {
		t.Errorf("committing lost the resolved services: %v", on.Msg.GetCollection().GetServices())
	}

	off, err := w.SetDescriptorSourceCommit(ctx, connect.NewRequest(commitReq(id, false)))
	if err != nil {
		t.Fatalf("SetDescriptorSourceCommit(off) with the target down: %v", err)
	}
	if got := committedSidecars(t, w); len(got) != 0 {
		t.Errorf("un-committing must delete the sidecar, got %v", got)
	}
	if sourceByID(off.Msg.GetCollection(), id).GetCommitDescriptors() {
		t.Errorf("commit_descriptors still set after --off: %+v", off.Msg.GetCollection().GetSources())
	}
	if !hasService(off.Msg.GetCollection().GetServices(), "Health") {
		t.Errorf("un-committing lost the resolved services: %v", off.Msg.GetCollection().GetServices())
	}
}

// TestSetDescriptorSourceCommitRejectsUnresolved answers the question the plan left open: an
// unresolved source is refused, because resolve-then-commit would make a config change acquire.
func TestSetDescriptorSourceCommitRejectsUnresolved(t *testing.T) {
	w := newTestWorkspace(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)

	coll, err := w.store.Open(ctx, testWorkspace)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// Listed but never resolved — the state a fresh clone of an uncommitted source is in.
	const id = "reflection:127.0.0.1:1"
	if err := coll.PutDescriptorState(ctx, store.DescriptorState{
		Sources: []*grpcviewv1.DescriptorSource{{
			Id:     id,
			Source: &grpcviewv1.DescriptorSource_Reflection{Reflection: &grpcviewv1.Server{Address: "127.0.0.1:1"}},
		}},
	}); err != nil {
		t.Fatalf("PutDescriptorState: %v", err)
	}

	_, err = w.SetDescriptorSourceCommit(ctx, connect.NewRequest(commitReq(id, true)))
	if code := connect.CodeOf(err); code != connect.CodeInvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument (err=%v)", code, err)
	}
	if got := committedSidecars(t, w); len(got) != 0 {
		t.Errorf("a refused commit must write nothing, got %v", got)
	}
	if _, err := w.SetDescriptorSourceCommit(ctx, connect.NewRequest(commitReq("reflection:nope:1", true))); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("an unknown id = %v, want NotFound", err)
	}
}

// TestCommittedDescriptorsResolveInAFreshClone is the whole point of the flag: the same collection
// directory, opened with a state root that has never seen it — a colleague's clone — resolves with
// no refresh and no network.
func TestCommittedDescriptorsResolveInAFreshClone(t *testing.T) {
	w := newTestWorkspace(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)

	add := descriptorSetAddReq(fileDescriptorSet(t, "grpc/health/v1/health.proto"))
	add.CommitDescriptors = true
	if _, err := w.AddDescriptorSource(ctx, connect.NewRequest(add)); err != nil {
		t.Fatalf("AddDescriptorSource (committed upload): %v", err)
	}
	if got := committedSidecars(t, w); len(got) != 1 {
		t.Fatalf("want the upload's sidecar committed, got %v", got)
	}

	clone := Workspace{
		store: store.New(w.store.Root(), t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil))),
		defs:  newDefinitionsCache(),
	}
	got, err := clone.Get(ctx, connect.NewRequest(&grpcviewv1.GetRequest{Collection: testWorkspace}))
	if err != nil {
		t.Fatalf("Get in a fresh clone: %v", err)
	}
	ws := got.Msg.GetCollection()
	if !hasService(ws.GetServices(), "Health") {
		t.Fatalf("a fresh clone did not resolve from the committed sidecar: %v", ws.GetServices())
	}
	if got := ws.GetSources()[0].GetResolved().GetError(); got != "" {
		t.Errorf("the committed source reports an error in a clone: %q", got)
	}
}

// TestUploadsAreNotStoredInTheManifest pins the deletion of upload's special case: the bytes go into
// the descriptor store like every other kind's, so grpcview.json stays a small file that a request
// reorder can rewrite.
func TestUploadsAreNotStoredInTheManifest(t *testing.T) {
	w := newTestWorkspace(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)

	set := fileDescriptorSet(t, "proto/grpcview/v1/service.proto")
	if len(set) < 10_000 {
		t.Fatalf("test descriptor set is only %d bytes; it must be big enough to notice inline", len(set))
	}
	if _, err := w.AddDescriptorSource(ctx, connect.NewRequest(descriptorSetAddReq(set))); err != nil {
		t.Fatalf("AddDescriptorSource: %v", err)
	}

	manifest := filepath.Join(collectionDir(t, w), store.CollectionFileName)
	info, err := os.Stat(manifest)
	if err != nil {
		t.Fatalf("Stat manifest: %v", err)
	}
	if info.Size() > 1024 {
		t.Errorf("grpcview.json is %d bytes for a %d-byte upload; descriptors must not be inline",
			info.Size(), len(set))
	}
	raw, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if strings.Contains(string(raw), "descriptorSet") || strings.Contains(string(raw), "\"file\"") {
		t.Errorf("grpcview.json still carries descriptor content:\n%s", raw)
	}
}

// TestRefreshAnUploadFails: an upload is the null-pointer kind, so there is nothing to re-resolve
// from. Failing is deliberate — a silent success would report a refresh that re-fetched nothing.
func TestRefreshAnUploadFails(t *testing.T) {
	w := newTestWorkspace(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)

	added, err := w.AddDescriptorSource(ctx, connect.NewRequest(descriptorSetAddReq(fileDescriptorSet(t, "grpc/health/v1/health.proto"))))
	if err != nil {
		t.Fatalf("AddDescriptorSource: %v", err)
	}
	id := added.Msg.GetCollection().GetSources()[0].GetId()

	_, err = w.RefreshDescriptorSource(ctx, connect.NewRequest(&grpcviewv1.RefreshDescriptorSourceRequest{
		Collection: testWorkspace,
		Id:         id,
	}))
	if code := connect.CodeOf(err); code != connect.CodeFailedPrecondition {
		t.Fatalf("code = %v, want FailedPrecondition (err=%v)", code, err)
	}
	if !strings.Contains(err.Error(), "uploading the file again") {
		t.Errorf("the error must name the fix, got %v", err)
	}

	// The failed refresh changed nothing: the upload still resolves from what the add stored.
	got, err := w.Get(ctx, connect.NewRequest(&grpcviewv1.GetRequest{Collection: testWorkspace}))
	if err != nil {
		t.Fatalf("Get after the failed refresh: %v", err)
	}
	if !hasService(got.Msg.GetCollection().GetServices(), "Health") {
		t.Errorf("the upload lost its descriptors: %v", got.Msg.GetCollection().GetServices())
	}
}
