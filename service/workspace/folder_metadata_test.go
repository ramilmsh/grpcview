package workspace

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/structpb"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/store"
)

func createFolder(t *testing.T, w Workspace, ctx context.Context, parent []string, name, metadataScript string) {
	t.Helper()
	coll, err := w.store.Open(ctx, testWorkspace)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := coll.CreateFolder(ctx, parent, name); err != nil {
		t.Fatalf("CreateFolder %q: %v", name, err)
	}
	if err := coll.UpdateFolder(ctx, parent, name, store.FolderPatch{DraftMetadataScript: &metadataScript}); err != nil {
		t.Fatalf("UpdateFolder %q: %v", name, err)
	}
}

func mdValues(md *structpb.Struct, key string) []string {
	v, ok := md.GetFields()[key]
	if !ok {
		return nil
	}
	return valueToStrings(v)
}

func TestResolveInvokeMetadataInheritanceAdditive(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	createFolder(t, w, ctx, nil, "a", `export default () => ({ ...gv.metadata.inherit(), fromA: ["1"] })`)

	md, err := w.resolveInvokeMetadata(ctx, testWorkspace, []string{"a"},
		`export default () => ({ ...gv.metadata.inherit(), own: ["2"] })`, nil, nil)
	if err != nil {
		t.Fatalf("resolveInvokeMetadata: %v", err)
	}
	if got := mdValues(md, "fromA"); len(got) != 1 || got[0] != "1" {
		t.Errorf("fromA = %v, want [1] (folder key not inherited)", got)
	}
	if got := mdValues(md, "own"); len(got) != 1 || got[0] != "2" {
		t.Errorf("own = %v, want [2]", got)
	}
}

func TestResolveInvokeMetadataInheritanceNestedTransitive(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	createFolder(t, w, ctx, nil, "a", `export default () => ({ ...gv.metadata.inherit(), fromA: ["1"] })`)
	createFolder(t, w, ctx, []string{"a"}, "b", `export default () => ({ ...gv.metadata.inherit(), fromB: ["2"] })`)
	createFolder(t, w, ctx, []string{"a", "b"}, "c", `export default () => ({ ...gv.metadata.inherit(), fromC: ["3"] })`)

	md, err := w.resolveInvokeMetadata(ctx, testWorkspace, []string{"a", "b", "c"},
		`export default () => ({ ...gv.metadata.inherit(), own: ["4"] })`, nil, nil)
	if err != nil {
		t.Fatalf("resolveInvokeMetadata: %v", err)
	}
	for key, want := range map[string]string{"fromA": "1", "fromB": "2", "fromC": "3", "own": "4"} {
		if got := mdValues(md, key); len(got) != 1 || got[0] != want {
			t.Errorf("%s = %v, want [%s] (3-level transitive inheritance broken)", key, got, want)
		}
	}
}

func TestResolveInvokeMetadataInheritanceBarrier(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	createFolder(t, w, ctx, nil, "a", `export default () => ({ ...gv.metadata.inherit(), fromA: ["1"] })`)
	createFolder(t, w, ctx, []string{"a"}, "b", `export default () => ({ fromB: ["2"] })`)

	md, err := w.resolveInvokeMetadata(ctx, testWorkspace, []string{"a", "b"},
		`export default () => ({ ...gv.metadata.inherit(), own: ["3"] })`, nil, nil)
	if err != nil {
		t.Fatalf("resolveInvokeMetadata: %v", err)
	}
	if got := mdValues(md, "fromA"); got != nil {
		t.Errorf("fromA = %v, want absent (non-spread folder b must be a barrier)", got)
	}
	if got := mdValues(md, "fromB"); len(got) != 1 || got[0] != "2" {
		t.Errorf("fromB = %v, want [2]", got)
	}
	if got := mdValues(md, "own"); len(got) != 1 || got[0] != "3" {
		t.Errorf("own = %v, want [3]", got)
	}
}

func TestResolveInvokeMetadataInheritanceOverride(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	createFolder(t, w, ctx, nil, "a", `export default () => ({ ...gv.metadata.inherit(), shared: ["from-a-1", "from-a-2"] })`)
	createFolder(t, w, ctx, []string{"a"}, "b", `export default () => ({ ...gv.metadata.inherit(), shared: ["from-b"] })`)

	md, err := w.resolveInvokeMetadata(ctx, testWorkspace, []string{"a", "b"},
		`export default () => ({ ...gv.metadata.inherit() })`, nil, nil)
	if err != nil {
		t.Fatalf("resolveInvokeMetadata: %v", err)
	}
	if got := mdValues(md, "shared"); len(got) != 1 || got[0] != "from-b" {
		t.Errorf("shared = %v, want [from-b] (redefined key must whole-replace, not merge)", got)
	}
}

func TestResolveInvokeMetadataInheritanceGateSkipsFold(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	createFolder(t, w, ctx, nil, "a", `export default () => ({ unterminated`)

	md, err := w.resolveInvokeMetadata(ctx, testWorkspace, []string{"a"},
		`export default () => ({ plain: ["x"] })`, nil, nil)
	if err != nil {
		t.Fatalf("resolveInvokeMetadata: %v (the gate should have skipped the broken folder script entirely)", err)
	}
	if got := mdValues(md, "plain"); len(got) != 1 || got[0] != "x" {
		t.Errorf("plain = %v, want [x]", got)
	}
}

func TestResolveInvokeMetadataInheritanceBrokenFolderNamesItself(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	createFolder(t, w, ctx, nil, "a", `export default () => ({ unterminated`)

	_, err := w.resolveInvokeMetadata(ctx, testWorkspace, []string{"a"},
		`export default () => ({ ...gv.metadata.inherit() })`, nil, nil)
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("code = %v, want FailedPrecondition (err=%v)", connect.CodeOf(err), err)
	}
	if err == nil || !strings.Contains(err.Error(), `"a"`) {
		t.Fatalf("error = %v, want it to name the offending folder \"a\"", err)
	}
}

func TestResolveInvokeMetadataInheritanceDepthCap(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)

	longPath := make([]string, MaxFolderMetadataDepth+1)
	for i := range longPath {
		longPath[i] = fmt.Sprintf("f%d", i)
	}
	_, err := w.resolveInvokeMetadata(ctx, testWorkspace, longPath,
		`export default () => ({ ...gv.metadata.inherit() })`, nil, nil)
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("code = %v, want FailedPrecondition (err=%v)", connect.CodeOf(err), err)
	}
}

func TestResolveInvokeMetadataInheritanceMissingFolderTolerated(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)

	md, err := w.resolveInvokeMetadata(ctx, testWorkspace, []string{"ghost"},
		`export default () => ({ ...gv.metadata.inherit(), x: ["1"] })`, nil, nil)
	if err != nil {
		t.Fatalf("resolveInvokeMetadata: %v (a missing folder path must degrade to no inheritance, not fail)", err)
	}
	if got := mdValues(md, "x"); len(got) != 1 || got[0] != "1" {
		t.Errorf("x = %v, want [1]", got)
	}
}

func TestResolveInvokeMetadataInheritanceNotAFolderTolerated(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	coll, err := w.store.Open(ctx, testWorkspace)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := coll.CreateRequest(ctx, nil, "Leaf", echoService, "Unary"); err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}

	md, err := w.resolveInvokeMetadata(ctx, testWorkspace, []string{"Leaf"},
		`export default () => ({ ...gv.metadata.inherit(), x: ["1"] })`, nil, nil)
	if err != nil {
		t.Fatalf("resolveInvokeMetadata: %v (ErrNotAFolder must degrade to no inheritance, not fail)", err)
	}
	if got := mdValues(md, "x"); len(got) != 1 || got[0] != "1" {
		t.Errorf("x = %v, want [1]", got)
	}
}

func TestInvokeFolderMetadataInheritanceEndToEnd(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	createFolder(t, w, ctx, nil, "Folder", `export default () => ({ ...gv.metadata.inherit(), "x-from-folder": ["yes"] })`)

	coll, err := w.store.Open(ctx, testWorkspace)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := coll.CreateRequest(ctx, []string{"Folder"}, "Echo", echoService, "Unary"); err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}

	port := echoTarget(t, w, ctx, startEchoServer)
	resp, err := w.Invoke(ctx, connect.NewRequest(&grpcviewv1.InvokeRequest{
		Spec: &grpcviewv1.InvokeSpec{
			WorkspaceName:  testWorkspace,
			Path:           []string{"Folder"},
			ItemName:       "Echo",
			Service:        echoService,
			Method:         "Unary",
			MetadataScript: `export default () => ({ ...gv.metadata.inherit(), "x-own": ["mine"] })`,
			Target:         &grpcviewv1.Server{Address: fmt.Sprintf("127.0.0.1:%d", port)},
		},
		Body: tsBody(`{"message":"hi"}`),
	}))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if code := resp.Msg.GetResponse().GetStatus().GetCode(); code != int32(codeOK) {
		t.Fatalf("status = %d (%s)", code, resp.Msg.GetResponse().GetStatus().GetMessage())
	}
	fields := resp.Msg.GetResponse().GetRequestMetadata().GetFields()
	if got := fields["x-from-folder"].GetStringValue(); got != "yes" {
		t.Fatalf("x-from-folder = %q, want yes (folder metadata not inherited into the sent request)", got)
	}
	if got := fields["x-own"].GetStringValue(); got != "mine" {
		t.Fatalf("x-own = %q, want mine (request's own metadata missing from the sent request)", got)
	}
}
