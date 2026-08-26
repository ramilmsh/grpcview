package workspace

import (
	"bytes"
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
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoregistry"

	grpcviewstorev1 "codeberg.org/ramilmsh/grpcview/grpcview/store/v1"
	grpcviewv1 "codeberg.org/ramilmsh/grpcview/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/scripting"
	"codeberg.org/ramilmsh/grpcview/service/store"
)

const testWorkspace = "default"

func tsBody(obj string) string { return "export default () => (" + obj + ")" }

func startReflectionServer(t *testing.T, withHealth bool) int {
	port, _ := startStoppableReflectionServer(t, withHealth)
	return port
}

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

// newTestWorkspaceWithEngine wires the engine's workspace root to the SAME directory the store
// addresses, mirroring what New does in production: without it, `@/...` imports never resolve.
func newTestWorkspaceWithEngine(t *testing.T) Workspace {
	t.Helper()
	root := t.TempDir()
	eng, err := scripting.NewEngine(context.Background(), scriptingMaxPages, scripting.WithWorkspaceRoot(root))
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	t.Cleanup(func() { _ = eng.Close(context.Background()) })
	return Workspace{
		store:  store.New(root, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil))),
		engine: eng,
		defs:   newDefinitionsCache(),
		root:   root,
	}
}

// writeScript creates (or overwrites) a script at a collection-relative path under scripts/,
// e.g. "scripts/mkid.ts". Scripts have no kind any more — identity is the path.
func writeScript(t *testing.T, w Workspace, ctx context.Context, path, source string) {
	t.Helper()
	coll, err := w.store.Open(ctx, testWorkspace)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := coll.CreateScript(ctx, path); err != nil {
		t.Fatalf("CreateScript %q: %v", path, err)
	}
	if err := coll.UpdateScript(ctx, path, store.ScriptPatch{Source: &source}); err != nil {
		t.Fatalf("UpdateScript %q: %v", path, err)
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

	script := "import { inherit } from \"grpcview:metadata\";\nexport default () => ({ ...inherit(), team: ['users'] })"
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

func TestSetDescriptorSourceCommitRejectsUnresolved(t *testing.T) {
	w := newTestWorkspace(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)

	coll, err := w.store.Open(ctx, testWorkspace)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
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

func TestReAddNeverUnCommits(t *testing.T) {
	w := newTestWorkspace(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)

	set := fileDescriptorSet(t, "grpc/health/v1/health.proto")
	add := descriptorSetAddReq(set)
	add.CommitDescriptors = true
	added, err := w.AddDescriptorSource(ctx, connect.NewRequest(add))
	if err != nil {
		t.Fatalf("AddDescriptorSource (committed): %v", err)
	}
	id := added.Msg.GetCollection().GetSources()[0].GetId()
	if got := committedSidecars(t, w); len(got) != 1 {
		t.Fatalf("want one sidecar after a committed add, got %v", got)
	}

	again, err := w.AddDescriptorSource(ctx, connect.NewRequest(descriptorSetAddReq(set)))
	if err != nil {
		t.Fatalf("AddDescriptorSource (re-add): %v", err)
	}
	if !sourceByID(again.Msg.GetCollection(), id).GetCommitDescriptors() {
		t.Errorf("a re-add un-committed the source: %+v", again.Msg.GetCollection().GetSources())
	}
	if got := committedSidecars(t, w); len(got) != 1 {
		t.Errorf("a re-add deleted the committed sidecar, got %v", got)
	}

	off, err := w.SetDescriptorSourceCommit(ctx, connect.NewRequest(commitReq(id, false)))
	if err != nil {
		t.Fatalf("SetDescriptorSourceCommit(off): %v", err)
	}
	if sourceByID(off.Msg.GetCollection(), id).GetCommitDescriptors() {
		t.Errorf("commit_descriptors still set after --off: %+v", off.Msg.GetCollection().GetSources())
	}
	if got := committedSidecars(t, w); len(got) != 0 {
		t.Errorf("--off must delete the sidecar, got %v", got)
	}

	on := descriptorSetAddReq(set)
	on.CommitDescriptors = true
	backOn, err := w.AddDescriptorSource(ctx, connect.NewRequest(on))
	if err != nil {
		t.Fatalf("AddDescriptorSource (re-add with the flag on): %v", err)
	}
	if !sourceByID(backOn.Msg.GetCollection(), id).GetCommitDescriptors() {
		t.Errorf("a re-add could not turn committing on: %+v", backOn.Msg.GetCollection().GetSources())
	}
}

func TestUploadsAreNotStoredInTheManifest(t *testing.T) {
	w := newTestWorkspace(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)

	set := fileDescriptorSet(t, "grpcview/v1/service.proto")
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
	for _, want := range []string{"no path", "handing the file over again"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error must say why (%q) and name the fix, got %v", want, err)
		}
	}

	got, err := w.Get(ctx, connect.NewRequest(&grpcviewv1.GetRequest{Collection: testWorkspace}))
	if err != nil {
		t.Fatalf("Get after the failed refresh: %v", err)
	}
	if !hasService(got.Msg.GetCollection().GetServices(), "Health") {
		t.Errorf("the upload lost its descriptors: %v", got.Msg.GetCollection().GetServices())
	}
}

func writeWorkspaceDefinitions(t *testing.T, root string, port int) string {
	t.Helper()
	id := fmt.Sprintf("reflection:127.0.0.1:%d", port)
	manifest := fmt.Sprintf(`{
  "schemaVersion": 1,
  "name": "acme",
  "sources": [{"reflection": {"address": "127.0.0.1:%d"}}],
  "defaults": {"sources": [%q]}
}
`, port, id)
	if err := os.WriteFile(filepath.Join(root, store.WorkspaceFileName), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write %s: %v", store.WorkspaceFileName, err)
	}
	return id
}

func manifestText(t *testing.T, w Workspace, collectionID string) string {
	t.Helper()
	coll, err := w.store.Open(context.Background(), collectionID)
	if err != nil {
		t.Fatalf("Open %s: %v", collectionID, err)
	}
	data, err := os.ReadFile(filepath.Join(coll.Root(), store.CollectionFileName))
	if err != nil {
		t.Fatalf("read %s manifest: %v", collectionID, err)
	}
	return string(data)
}

func assertBareReference(t *testing.T, w Workspace, collectionID, id string) {
	t.Helper()
	col := &grpcviewstorev1.Collection{}
	if err := protojson.Unmarshal([]byte(manifestText(t, w, collectionID)), col); err != nil {
		t.Fatalf("parse %s manifest: %v", collectionID, err)
	}
	for _, entry := range col.GetSources() {
		if entry.GetId() != id {
			continue
		}
		if entry.GetSource() != nil {
			t.Errorf("%s inlined the shared definition %q instead of referencing it: %+v", collectionID, id, entry)
		}
		return
	}
	t.Errorf("%s does not list %q at all:\n%s", collectionID, id, manifestText(t, w, collectionID))
}

func workspaceBlobNames(t *testing.T, w Workspace, collectionID string) []string {
	t.Helper()
	coll, err := w.store.Open(context.Background(), collectionID)
	if err != nil {
		t.Fatalf("Open %s: %v", collectionID, err)
	}
	entries, err := os.ReadDir(filepath.Join(filepath.Dir(filepath.Dir(coll.State())), "blobs"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatalf("ReadDir blobs: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func refreshSource(t *testing.T, w Workspace, ctx context.Context, collectionID, id string) *grpcviewv1.Collection {
	t.Helper()
	resp, err := w.RefreshDescriptorSource(ctx, connect.NewRequest(&grpcviewv1.RefreshDescriptorSourceRequest{
		Collection: collectionID,
		Id:         id,
	}))
	if err != nil {
		t.Fatalf("RefreshDescriptorSource %s/%s: %v", collectionID, id, err)
	}
	return resp.Msg.GetCollection()
}

func createCollectionAt(t *testing.T, w Workspace, ctx context.Context, collectionID string) *grpcviewv1.Collection {
	t.Helper()
	resp, err := w.CreateCollection(ctx, connect.NewRequest(&grpcviewv1.CreateCollectionRequest{Collection: collectionID}))
	if err != nil {
		t.Fatalf("CreateCollection %s: %v", collectionID, err)
	}
	return resp.Msg.GetCollection()
}

func TestSharedDefinitionServesTwoCollections(t *testing.T) {
	root := t.TempDir()
	w := newWorkspaceAt(t, root)
	ctx := context.Background()
	port := startReflectionServer(t, true)
	id := writeWorkspaceDefinitions(t, root, port)

	ids := []string{"services/payments/requests", "services/ledger/requests"}
	for _, cid := range ids {
		created := createCollectionAt(t, w, ctx, cid)
		sources := created.GetSources()
		if len(sources) != 1 || sources[0].GetId() != id {
			t.Fatalf("%s was not seeded from defaults.sources: %v", cid, sources)
		}
		if sources[0].GetOrigin() != grpcviewv1.SourceOrigin_SOURCE_ORIGIN_WORKSPACE {
			t.Errorf("%s seeded origin = %v, want WORKSPACE", cid, sources[0].GetOrigin())
		}
		if sources[0].GetResolved().GetError() == "" {
			t.Errorf("%s: seeding writes pointers, so the seeded source must read as unresolved", cid)
		}
		if len(created.GetServices()) != 0 {
			t.Errorf("%s resolved something on creation: %v", cid, created.GetServices())
		}
		assertBareReference(t, w, cid, id)

		if got := refreshSource(t, w, ctx, cid, id); !hasService(got.GetServices(), "Health") {
			t.Fatalf("%s did not resolve the shared definition: %v", cid, got.GetServices())
		}
		assertBareReference(t, w, cid, id)
	}

	for _, cid := range ids {
		got, err := w.Get(ctx, connect.NewRequest(&grpcviewv1.GetRequest{Collection: cid}))
		if err != nil {
			t.Fatalf("Get %s: %v", cid, err)
		}
		if !hasService(got.Msg.GetCollection().GetServices(), "Health") {
			t.Errorf("%s lost the shared definition's services on reload: %v", cid, got.Msg.GetCollection().GetServices())
		}
	}
	if got := workspaceBlobNames(t, w, ids[0]); len(got) != 1 {
		t.Errorf("two collections referencing one definition must share one blob, got %v", got)
	}
}

func TestRemovingAReferenceLeavesTheDefinition(t *testing.T) {
	root := t.TempDir()
	w := newWorkspaceAt(t, root)
	ctx := context.Background()
	port := startReflectionServer(t, true)
	id := writeWorkspaceDefinitions(t, root, port)

	const payments, ledger = "services/payments/requests", "services/ledger/requests"
	for _, cid := range []string{payments, ledger} {
		createCollectionAt(t, w, ctx, cid)
		refreshSource(t, w, ctx, cid, id)
	}
	manifestPath := filepath.Join(root, store.WorkspaceFileName)
	before, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read %s: %v", store.WorkspaceFileName, err)
	}

	removed, err := w.RemoveDescriptorSource(ctx, connect.NewRequest(&grpcviewv1.RemoveDescriptorSourceRequest{
		Collection: payments,
		Id:         id,
	}))
	if err != nil {
		t.Fatalf("RemoveDescriptorSource: %v", err)
	}
	if len(removed.Msg.GetCollection().GetSources()) != 0 {
		t.Errorf("the reference survived its removal: %v", removed.Msg.GetCollection().GetSources())
	}

	after, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("re-read %s: %v", store.WorkspaceFileName, err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("%s was rewritten by a collection-level removal:\n%s\n---\n%s",
			store.WorkspaceFileName, before, after)
	}

	still, err := w.Get(ctx, connect.NewRequest(&grpcviewv1.GetRequest{Collection: ledger}))
	if err != nil {
		t.Fatalf("Get %s: %v", ledger, err)
	}
	if !hasService(still.Msg.GetCollection().GetServices(), "Health") {
		t.Errorf("%s lost the shared definition when %s dropped its reference: %v",
			ledger, payments, still.Msg.GetCollection().GetServices())
	}
}

func TestAddingASourceTheWorkspaceDefinesReferencesIt(t *testing.T) {
	root := t.TempDir()
	w := newWorkspaceAt(t, root)
	ctx := context.Background()
	port := startReflectionServer(t, true)
	id := writeWorkspaceDefinitions(t, root, port)

	const cid = "services/payments/requests"
	createCollectionAt(t, w, ctx, cid)
	if _, err := w.RemoveDescriptorSource(ctx, connect.NewRequest(&grpcviewv1.RemoveDescriptorSourceRequest{
		Collection: cid,
		Id:         id,
	})); err != nil {
		t.Fatalf("RemoveDescriptorSource (seeded): %v", err)
	}

	added, err := w.AddDescriptorSource(ctx, connect.NewRequest(&grpcviewv1.AddDescriptorSourceRequest{
		Collection: cid,
		Source: &grpcviewv1.AddDescriptorSourceRequest_Reflection{
			Reflection: &grpcviewv1.Server{Address: fmt.Sprintf("127.0.0.1:%d", port)},
		},
	}))
	if err != nil {
		t.Fatalf("AddDescriptorSource: %v", err)
	}
	sources := added.Msg.GetCollection().GetSources()
	if len(sources) != 1 || sources[0].GetId() != id {
		t.Fatalf("sources = %v, want the one shared id", sources)
	}
	if sources[0].GetOrigin() != grpcviewv1.SourceOrigin_SOURCE_ORIGIN_WORKSPACE {
		t.Errorf("origin = %v, want WORKSPACE: an add of a defined id is a reference", sources[0].GetOrigin())
	}
	if !hasService(added.Msg.GetCollection().GetServices(), "Health") {
		t.Errorf("the add resolved nothing: %v", added.Msg.GetCollection().GetServices())
	}
	assertBareReference(t, w, cid, id)
}

func TestReferenceWithNoDefinitionKeepsItsRow(t *testing.T) {
	w := newTestWorkspace(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)

	const id = "reflection:missing.example:50051"
	coll, err := w.store.Open(ctx, testWorkspace)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	manifest := fmt.Sprintf(`{"schemaVersion": 1, "name": "test", "sources": [{"id": %q}]}`, id)
	if err := os.WriteFile(filepath.Join(coll.Root(), store.CollectionFileName), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write collection manifest: %v", err)
	}

	got, err := w.Get(ctx, connect.NewRequest(&grpcviewv1.GetRequest{Collection: testWorkspace}))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	sources := got.Msg.GetCollection().GetSources()
	if len(sources) != 1 || sources[0].GetId() != id {
		t.Fatalf("sources = %v, want the dangling reference's row", sources)
	}
	if sources[0].GetOrigin() != grpcviewv1.SourceOrigin_SOURCE_ORIGIN_WORKSPACE {
		t.Errorf("origin = %v, want WORKSPACE", sources[0].GetOrigin())
	}
	reason := sources[0].GetResolved().GetError()
	if !strings.Contains(reason, store.WorkspaceFileName) || !strings.Contains(reason, id) {
		t.Errorf("error = %q, want it to name both %s and the id", reason, store.WorkspaceFileName)
	}

	_, err = w.RefreshDescriptorSource(ctx, connect.NewRequest(&grpcviewv1.RefreshDescriptorSourceRequest{
		Collection: testWorkspace,
		Id:         id,
	}))
	if err == nil || !strings.Contains(err.Error(), store.WorkspaceFileName) {
		t.Errorf("RefreshDescriptorSource error = %v, want it to name %s", err, store.WorkspaceFileName)
	}
}

func TestCommitAndRefreshWorkOnAReference(t *testing.T) {
	root := t.TempDir()
	w := newWorkspaceAt(t, root)
	ctx := context.Background()
	port := startReflectionServer(t, true)
	id := writeWorkspaceDefinitions(t, root, port)

	const cid = "services/payments/requests"
	createCollectionAt(t, w, ctx, cid)
	refreshSource(t, w, ctx, cid, id)

	on, err := w.SetDescriptorSourceCommit(ctx, connect.NewRequest(&grpcviewv1.SetDescriptorSourceCommitRequest{
		Collection: cid,
		Id:         id,
		Commit:     true,
	}))
	if err != nil {
		t.Fatalf("SetDescriptorSourceCommit(on) on a reference: %v", err)
	}
	src := on.Msg.GetCollection().GetSources()[0]
	if !src.GetCommitDescriptors() || src.GetOrigin() != grpcviewv1.SourceOrigin_SOURCE_ORIGIN_WORKSPACE {
		t.Errorf("want a committed WORKSPACE source, got %+v", src)
	}
	coll, err := w.store.Open(ctx, cid)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(coll.Root(), "descriptors"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("committing a reference must write one sidecar in the collection: %v, %v", entries, err)
	}
	assertBareReference(t, w, cid, id)

	if got := refreshSource(t, w, ctx, cid, id); !hasService(got.GetServices(), "Health") {
		t.Errorf("refreshing a committed reference lost its services: %v", got.GetServices())
	}
	off, err := w.SetDescriptorSourceCommit(ctx, connect.NewRequest(&grpcviewv1.SetDescriptorSourceCommitRequest{
		Collection: cid,
		Id:         id,
	}))
	if err != nil {
		t.Fatalf("SetDescriptorSourceCommit(off) on a reference: %v", err)
	}
	if off.Msg.GetCollection().GetSources()[0].GetCommitDescriptors() {
		t.Errorf("the flag did not come back off: %+v", off.Msg.GetCollection().GetSources()[0])
	}
	assertBareReference(t, w, cid, id)
}

func TestReorderKeepsAReferenceShared(t *testing.T) {
	root := t.TempDir()
	w := newWorkspaceAt(t, root)
	ctx := context.Background()
	shared := startReflectionServer(t, true)
	own := startReflectionServer(t, false)
	id := writeWorkspaceDefinitions(t, root, shared)

	const cid = "services/payments/requests"
	createCollectionAt(t, w, ctx, cid)
	refreshSource(t, w, ctx, cid, id)
	added, err := w.AddDescriptorSource(ctx, connect.NewRequest(&grpcviewv1.AddDescriptorSourceRequest{
		Collection: cid,
		Source: &grpcviewv1.AddDescriptorSourceRequest_Reflection{
			Reflection: &grpcviewv1.Server{Address: fmt.Sprintf("127.0.0.1:%d", own)},
		},
	}))
	if err != nil {
		t.Fatalf("AddDescriptorSource: %v", err)
	}
	ownID := added.Msg.GetCollection().GetSources()[1].GetId()

	reordered, err := w.ReorderDescriptorSources(ctx, connect.NewRequest(&grpcviewv1.ReorderDescriptorSourcesRequest{
		Collection: cid,
		Ids:        []string{ownID, id},
	}))
	if err != nil {
		t.Fatalf("ReorderDescriptorSources: %v", err)
	}
	if got := sourceIDsOf(reordered.Msg.GetCollection().GetSources()); got[0] != ownID || got[1] != id {
		t.Errorf("order = %v, want [%s %s]", got, ownID, id)
	}
	assertBareReference(t, w, cid, id)
	if text := manifestText(t, w, cid); !strings.Contains(text, fmt.Sprintf("127.0.0.1:%d", own)) {
		t.Errorf("the collection's own source lost its config:\n%s", text)
	}
}
