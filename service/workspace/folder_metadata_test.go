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

// folder_metadata_test.go covers gv-features-plan.md Feature 1 Phase 3, "Inheritance fold in
// invoke": resolveInvokeMetadata's path/params plumbing, the mentionsInherit efficiency gate,
// and foldAncestorMetadata's D2 "spread-driven replace" semantics (additive spread, nested
// transitive inheritance, the non-spread barrier, key override, the gate itself, per-folder
// error naming, the depth cap, and store-error tolerance).

// createFolder creates a folder named name inside parent and sets its draft metadata script —
// the workspace-test-package analogue of createMiddleware/createGenerator, built on the
// already-landed store.Collection.CreateFolder + UpdateFolder(FolderPatch).
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

// mdValues returns the string values a resolveInvokeMetadata Struct carries for key, via the
// same valueToStrings invoke.go itself uses to flatten a Struct field (a scalar collapses to a
// single-element slice, a ListValue to its elements); a missing field returns nil.
func mdValues(md *structpb.Struct, key string) []string {
	v, ok := md.GetFields()[key]
	if !ok {
		return nil
	}
	return valueToStrings(v)
}

// TestResolveInvokeMetadataInheritanceAdditive is the additive-spread case: a root folder's
// script spreads gv.metadata.inherit() (a no-op — it has no ancestors) and adds its own key; the
// request living inside it also spreads inherit() and adds a key of its own. Both keys are
// present in the final result.
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

// TestResolveInvokeMetadataInheritanceNestedTransitive covers 3 levels of ancestor folders
// (a -> a/b -> a/b/c), each additively spreading gv.metadata.inherit() and contributing its own
// key, plus the request's own script at path ["a","b","c"] doing the same: all four keys must
// survive the fold, proving transitivity compounds correctly across more than one hop.
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

// TestResolveInvokeMetadataInheritanceBarrier is the required barrier case (D2's stated
// footgun): folder "a" contributes fromA; folder "a/b" is NON-EMPTY but deliberately omits the
// `...gv.metadata.inherit()` spread, so it whole-replaces rather than carrying "a"'s key
// forward — fromA must be absent from the final result even though "a" is a real ancestor.
func TestResolveInvokeMetadataInheritanceBarrier(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	createFolder(t, w, ctx, nil, "a", `export default () => ({ ...gv.metadata.inherit(), fromA: ["1"] })`)
	createFolder(t, w, ctx, []string{"a"}, "b", `export default () => ({ fromB: ["2"] })`) // no spread: a barrier

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

// TestResolveInvokeMetadataInheritanceOverride is the required override case: folder "a" sets
// a multi-valued "shared" key; folder "a/b" spreads inherit() (so it is NOT a barrier) but
// redefines "shared" — standard JS spread means the later key wins outright, not a merge/append
// of the two arrays.
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

// TestResolveInvokeMetadataInheritanceGateSkipsFold is the required gate case: the request's own
// metadata script never calls inherit(...), so mentionsInherit must prevent foldAncestorMetadata
// from ever running — proven OBSERVABLY by giving the ancestor folder a script that would fail
// to even compile if it were evaluated. A passing (non-erroring) resolve proves the gate held.
func TestResolveInvokeMetadataInheritanceGateSkipsFold(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	createFolder(t, w, ctx, nil, "a", `export default () => ({ unterminated`) // would fail if evaluated

	md, err := w.resolveInvokeMetadata(ctx, testWorkspace, []string{"a"},
		`export default () => ({ plain: ["x"] })`, nil, nil) // no inherit( call anywhere
	if err != nil {
		t.Fatalf("resolveInvokeMetadata: %v (the gate should have skipped the broken folder script entirely)", err)
	}
	if got := mdValues(md, "plain"); len(got) != 1 || got[0] != "x" {
		t.Errorf("plain = %v, want [x]", got)
	}
}

// TestResolveInvokeMetadataInheritanceBrokenFolderNamesItself is the required broken-folder
// case: when the gate DOES fire (the request's script calls inherit()), a folder script that
// fails to evaluate must surface as FailedPrecondition and the error text must name the
// offending folder's path — otherwise a broken ancestor script is unactionable to debug.
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

// TestResolveInvokeMetadataInheritanceDepthCap is the required depth-cap case: a parent-folder
// path longer than MaxFolderMetadataDepth is rejected as FailedPrecondition before any store I/O
// or QuickJS instantiation happens (none of the named folders even exist) — a Connect error,
// never a hang.
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

// TestResolveInvokeMetadataInheritanceMissingFolderTolerated is the required stale-path case: a
// renamed/deleted folder segment (FolderMetadataChain propagates ErrItemNotFound rather than
// swallowing it) must degrade to "no inheritance" — an empty accumulator, no error — exactly
// like applyRequestMiddleware tolerates a missing target request, NOT fail the invoke.
func TestResolveInvokeMetadataInheritanceMissingFolderTolerated(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx) // the collection exists; folder "ghost" inside it does not

	md, err := w.resolveInvokeMetadata(ctx, testWorkspace, []string{"ghost"},
		`export default () => ({ ...gv.metadata.inherit(), x: ["1"] })`, nil, nil)
	if err != nil {
		t.Fatalf("resolveInvokeMetadata: %v (a missing folder path must degrade to no inheritance, not fail)", err)
	}
	if got := mdValues(md, "x"); len(got) != 1 || got[0] != "1" {
		t.Errorf("x = %v, want [1]", got)
	}
}

// TestResolveInvokeMetadataInheritanceNotAFolderTolerated covers the fold's OTHER tolerated
// sentinel: a path segment that resolves to a REQUEST rather than a folder (FolderMetadataChain
// propagates ErrNotAFolder). It must be tolerated identically to a missing path — "no
// inheritance", not a failure — so both halves of the errors.Is(...) || errors.Is(...) gate in
// foldAncestorMetadata are actually exercised.
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

// TestInvokeFolderMetadataInheritanceEndToEnd is the end-to-end must-pass: a saved request
// living inside a folder is invoked against the echo server, and the actually-sent request
// metadata carries BOTH the folder-provided header and the request's own — proving the whole
// pipeline (store -> foldAncestorMetadata -> gv.metadata.inherit() -> RunRequestBody ->
// structFromMetadataLists -> structToMetadata -> the wire) works together, not just at the
// resolveInvokeMetadata unit level.
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

	port := startEchoServer(t)
	resp, err := w.Invoke(ctx, connect.NewRequest(&grpcviewv1.InvokeRequest{
		WorkspaceName:  testWorkspace,
		Path:           []string{"Folder"},
		ItemName:       "Echo",
		Service:        echoService,
		Method:         "Unary",
		Body:           tsBody(`{"message":"hi"}`),
		MetadataScript: `export default () => ({ ...gv.metadata.inherit(), "x-own": ["mine"] })`,
		Target:         &grpcviewv1.Server{Address: fmt.Sprintf("127.0.0.1:%d", port)},
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
