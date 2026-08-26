package workspace

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/structpb"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/store"
)

const inheritImport = "import { inherit } from \"grpcview:metadata\";\n"

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
	createFolder(t, w, ctx, nil, "a", inheritImport+`export default () => ({ ...inherit(), fromA: ["1"] })`)

	md, err := w.resolveInvokeMetadata(ctx, testWorkspace, []string{"a"},
		inheritImport+`export default () => ({ ...inherit(), own: ["2"] })`, nil, nil, "")
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

func TestResolveInvokeMetadataEmptyScriptStillInherits(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	createFolder(t, w, ctx, nil, "a", `export default () => ({ authorization: ["Bearer tkn"] })`)

	fallback := structFromMetadataLists(map[string][]string{"authorization": {"explicit"}, "x-own": {"1"}})
	md, err := w.resolveInvokeMetadata(ctx, testWorkspace, []string{"a"}, "", fallback, nil, "")
	if err != nil {
		t.Fatalf("resolveInvokeMetadata: %v", err)
	}
	if got := mdValues(md, "authorization"); len(got) != 1 || got[0] != "explicit" {
		t.Errorf("authorization = %v, want [explicit] (an explicit key must beat the inherited one)", got)
	}
	if got := mdValues(md, "x-own"); len(got) != 1 || got[0] != "1" {
		t.Errorf("x-own = %v, want [1] (fallback key dropped)", got)
	}

	md, err = w.resolveInvokeMetadata(ctx, testWorkspace, []string{"a"}, "", nil, nil, "")
	if err != nil {
		t.Fatalf("resolveInvokeMetadata (no fallback): %v", err)
	}
	if got := mdValues(md, "authorization"); len(got) != 1 || got[0] != "Bearer tkn" {
		t.Errorf("authorization = %v, want [Bearer tkn] (an unsaved request must still inherit its folder)", got)
	}
}

// The removed gate only ever decided whether the Go side computed the ancestor fold at all; it
// never controlled whether the fold's result reached the returned metadata — that has always been
// up to the leaf script itself, via spreading inherit(). So a leaf script that never mentions
// inherit() must still see its own keys, with nothing from ancestors merged in for it.
func TestResolveInvokeMetadataLeafNotMentioningInheritGetsNothingFromAncestors(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	createFolder(t, w, ctx, nil, "a", inheritImport+`export default () => ({ ...inherit(), fromA: ["1"] })`)

	md, err := w.resolveInvokeMetadata(ctx, testWorkspace, []string{"a"},
		`export default () => ({ own: ["2"] })`, nil, nil, "")
	if err != nil {
		t.Fatalf("resolveInvokeMetadata: %v", err)
	}
	if got := mdValues(md, "fromA"); got != nil {
		t.Errorf("fromA = %v, want absent (a leaf that never calls inherit() must not see ancestor keys)", got)
	}
	if got := mdValues(md, "own"); len(got) != 1 || got[0] != "2" {
		t.Errorf("own = %v, want [2]", got)
	}
}

func TestResolveInvokeMetadataInheritanceNestedTransitive(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	createFolder(t, w, ctx, nil, "a", inheritImport+`export default () => ({ ...inherit(), fromA: ["1"] })`)
	createFolder(t, w, ctx, []string{"a"}, "b", inheritImport+`export default () => ({ ...inherit(), fromB: ["2"] })`)
	createFolder(t, w, ctx, []string{"a", "b"}, "c", inheritImport+`export default () => ({ ...inherit(), fromC: ["3"] })`)

	md, err := w.resolveInvokeMetadata(ctx, testWorkspace, []string{"a", "b", "c"},
		inheritImport+`export default () => ({ ...inherit(), own: ["4"] })`, nil, nil, "")
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
	createFolder(t, w, ctx, nil, "a", inheritImport+`export default () => ({ ...inherit(), fromA: ["1"] })`)
	createFolder(t, w, ctx, []string{"a"}, "b", `export default () => ({ fromB: ["2"] })`)

	md, err := w.resolveInvokeMetadata(ctx, testWorkspace, []string{"a", "b"},
		inheritImport+`export default () => ({ ...inherit(), own: ["3"] })`, nil, nil, "")
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
	createFolder(t, w, ctx, nil, "a", inheritImport+`export default () => ({ ...inherit(), shared: ["from-a-1", "from-a-2"] })`)
	createFolder(t, w, ctx, []string{"a"}, "b", inheritImport+`export default () => ({ ...inherit(), shared: ["from-b"] })`)

	md, err := w.resolveInvokeMetadata(ctx, testWorkspace, []string{"a", "b"},
		inheritImport+`export default () => ({ ...inherit() })`, nil, nil, "")
	if err != nil {
		t.Fatalf("resolveInvokeMetadata: %v", err)
	}
	if got := mdValues(md, "shared"); len(got) != 1 || got[0] != "from-b" {
		t.Errorf("shared = %v, want [from-b] (redefined key must whole-replace, not merge)", got)
	}
}

// With the textual gate gone, folding is unconditional: a broken ancestor folder script now
// errors regardless of whether the current script even mentions inherit().
func TestResolveInvokeMetadataBrokenFolderErrorsEvenWithoutInherit(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	createFolder(t, w, ctx, nil, "a", `export default () => ({ unterminated`)

	_, err := w.resolveInvokeMetadata(ctx, testWorkspace, []string{"a"},
		`export default () => ({ plain: ["x"] })`, nil, nil, "")
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("code = %v, want FailedPrecondition (err=%v): folding is unconditional now, "+
			"a broken ancestor folder must surface even when the leaf script never calls inherit()", connect.CodeOf(err), err)
	}
	if !strings.Contains(err.Error(), `"a"`) {
		t.Fatalf("error = %v, want it to name the offending folder \"a\"", err)
	}
}

func TestResolveInvokeMetadataInheritanceBrokenFolderNamesItself(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	createFolder(t, w, ctx, nil, "a", `export default () => ({ unterminated`)

	_, err := w.resolveInvokeMetadata(ctx, testWorkspace, []string{"a"},
		inheritImport+`export default () => ({ ...inherit() })`, nil, nil, "")
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
		inheritImport+`export default () => ({ ...inherit() })`, nil, nil, "")
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("code = %v, want FailedPrecondition (err=%v)", connect.CodeOf(err), err)
	}
}

func TestResolveInvokeMetadataInheritanceMissingFolderTolerated(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)

	md, err := w.resolveInvokeMetadata(ctx, testWorkspace, []string{"ghost"},
		inheritImport+`export default () => ({ ...inherit(), x: ["1"] })`, nil, nil, "")
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
		inheritImport+`export default () => ({ ...inherit(), x: ["1"] })`, nil, nil, "")
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
	createFolder(t, w, ctx, nil, "Folder", inheritImport+`export default () => ({ ...inherit(), "x-from-folder": ["yes"] })`)

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
			Collection:     testWorkspace,
			Path:           []string{"Folder"},
			ItemName:       "Echo",
			Service:        echoService,
			Method:         "Unary",
			MetadataScript: inheritImport + `export default () => ({ ...inherit(), "x-own": ["mine"] })`,
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

// The MCP/CLI/hand-edited case: nothing wraps the script on its way in, so a bare object
// literal must evaluate at both metadata seams exactly as it does in a body.
func TestBareObjectMetadataEvaluatesAtBothSeams(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	createFolder(t, w, ctx, nil, "Echo", `{ "x-demo-suite": ["echo"] }`)

	md, err := w.resolveInvokeMetadata(ctx, testWorkspace, []string{"Echo"},
		`{ ...require("grpcview:metadata").inherit(), "x-own": ["mine"] }`, nil, nil, "")
	if err != nil {
		t.Fatalf("resolveInvokeMetadata: %v", err)
	}
	if got := mdValues(md, "x-demo-suite"); len(got) != 1 || got[0] != "echo" {
		t.Errorf("x-demo-suite = %v, want [echo] (bare folder metadata did not evaluate)", got)
	}
	if got := mdValues(md, "x-own"); len(got) != 1 || got[0] != "mine" {
		t.Errorf("x-own = %v, want [mine] (bare request metadata did not evaluate)", got)
	}
}

func TestBareMetadataSyntaxErrorReportsTheAuthorLine(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)

	_, err := w.resolveInvokeMetadata(ctx, testWorkspace, nil, "{\n  \"a\": [\"1\"],\n  oops oops,\n}", nil, nil, "")
	if err == nil {
		t.Fatal("want an error for a metadata script that does not parse, got nil")
	}
	if !strings.Contains(err.Error(), "script.ts:3") {
		t.Fatalf("error = %v, want it to name the author's line 3", err)
	}
}
