package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/store"
	"codeberg.org/ramilmsh/grpcview/service/wsroot"
)

// isolateTrust points wsroot's trust file at a temp directory: HOME covers darwin
// (~/Library/Application Support), XDG_CONFIG_HOME covers linux. Without it these tests would read
// — and write — the developer's real trust list.
func isolateTrust(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, ".config"))
}

// fakeBazelOnPath puts an executable `bazel` shell script on PATH, so the resolve path exercises
// exactly what it does in production — Builder.Binary is empty and the binary is looked up — rather
// than a test-only injection point. The real PATH is kept behind it so the script's own interpreter
// and any tool it calls still resolve.
func fakeBazelOnPath(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bazel"), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatalf("write fake bazel: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// bazelWorkspace makes w's root a bazel root too (the common case: one repo, one MODULE.bazel), so
// bazelBuilder's discovery finds it without any bazel.root config.
func bazelWorkspace(t *testing.T, w Workspace) {
	t.Helper()
	writeFile(t, filepath.Join(w.store.Root(), "MODULE.bazel"), []byte("module(name = \"test\")\n"))
}

// cqueryPrinting is a fake bazel that succeeds and, for cquery, prints the given output paths.
func cqueryPrinting(paths ...string) string {
	return "if [ \"$1\" = cquery ]; then\n" +
		"  printf '%s\\n' " + strings.Join(quoteAll(paths), " ") + "\n" +
		"fi\nexit 0\n"
}

func quoteAll(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		out = append(out, "'"+strings.ReplaceAll(s, "'", `'\''`)+"'")
	}
	return out
}

func bazelAddReq(label string) *grpcviewv1.AddDescriptorSourceRequest {
	return &grpcviewv1.AddDescriptorSourceRequest{
		Collection: testWorkspace,
		Source:     &grpcviewv1.AddDescriptorSourceRequest_Bazel{Bazel: &grpcviewv1.Bazel{Label: label}},
	}
}

// The whole bazel path end to end: a trusted workspace, a build, cquery naming two outputs, and a
// proto file appearing in BOTH of them — which a merging rule produces and which the linker rejects
// unless the resolve deduped by file name first.
func TestAddBazelSourceResolvesAndDedupes(t *testing.T) {
	isolateTrust(t)
	w := newTestWorkspace(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	bazelWorkspace(t, w)
	if err := wsroot.Trust(w.store.Root()); err != nil {
		t.Fatalf("Trust: %v", err)
	}

	health := fileDescriptorSet(t, "grpc/health/v1/health.proto")
	writeFile(t, filepath.Join(w.store.Root(), "out", "one.bin"), health)
	// The same file name again, in a second output. Deduping is what keeps this a resolve instead
	// of a "duplicate file" link failure.
	writeFile(t, filepath.Join(w.store.Root(), "out", "two.bin"), health)
	fakeBazelOnPath(t, cqueryPrinting("out/one.bin", "out/two.bin"))

	resp, err := w.AddDescriptorSource(ctx, connect.NewRequest(bazelAddReq("//proto/health:health_descriptor_set")))
	if err != nil {
		t.Fatalf("AddDescriptorSource(bazel): %v", err)
	}
	coll := resp.Msg.GetCollection()
	if len(coll.GetSources()) != 1 {
		t.Fatalf("want 1 source, got %d", len(coll.GetSources()))
	}
	src := coll.GetSources()[0]
	if got, want := src.GetId(), "bazel://proto/health:health_descriptor_set"; got != want {
		t.Errorf("source id = %q, want %q", got, want)
	}
	if got := src.GetResolved().GetError(); got != "" {
		t.Errorf("resolved error = %q, want none", got)
	}
	if !hasService(coll.GetServices(), "Health") {
		t.Fatalf("Health missing after a bazel resolve: %v", coll.GetServices())
	}
	// A build has no dial target of its own, so the service it wins has no address until some
	// reflection source supplies one.
	if addr := hasServiceNamed(coll.GetServices(), "grpc.health.v1.Health").GetSource().GetAddress(); addr != "" {
		t.Errorf("a bazel source must contribute no dial target, got %q", addr)
	}
}

// Re-adding the OTHER spelling of one label refreshes that source in place: canonicalizing before
// the id is derived is what makes "//pkg" and "//pkg:pkg" the same source.
func TestAddBazelSourceCanonicalizesTheID(t *testing.T) {
	isolateTrust(t)
	w := newTestWorkspace(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	bazelWorkspace(t, w)
	if err := wsroot.Trust(w.store.Root()); err != nil {
		t.Fatalf("Trust: %v", err)
	}
	writeFile(t, filepath.Join(w.store.Root(), "out.bin"), fileDescriptorSet(t, "grpc/health/v1/health.proto"))
	fakeBazelOnPath(t, cqueryPrinting("out.bin"))

	if _, err := w.AddDescriptorSource(ctx, connect.NewRequest(bazelAddReq("//proto/health"))); err != nil {
		t.Fatalf("AddDescriptorSource(//proto/health): %v", err)
	}
	resp, err := w.AddDescriptorSource(ctx, connect.NewRequest(bazelAddReq("//proto/health:health")))
	if err != nil {
		t.Fatalf("AddDescriptorSource(//proto/health:health): %v", err)
	}
	sources := resp.Msg.GetCollection().GetSources()
	if len(sources) != 1 {
		t.Fatalf("the two spellings of one label must be one source, got %v", sourceIDsOf(sources))
	}
	if got, want := sources[0].GetId(), "bazel://proto/health:health"; got != want {
		t.Errorf("source id = %q, want %q", got, want)
	}
}

// An untrusted workspace refuses to build at all — and says trust is the fix.
func TestAddBazelSourceRefusedWhenUntrusted(t *testing.T) {
	isolateTrust(t)
	w := newTestWorkspace(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	bazelWorkspace(t, w)

	marker := filepath.Join(w.store.Root(), "bazel-ran")
	fakeBazelOnPath(t, "touch '"+marker+"'\nexit 0\n")

	_, err := w.AddDescriptorSource(ctx, connect.NewRequest(bazelAddReq("//proto/health:health")))
	if err == nil {
		t.Fatal("AddDescriptorSource on an untrusted workspace succeeded, want a refusal")
	}
	if got := connect.CodeOf(err); got != connect.CodeFailedPrecondition {
		t.Errorf("code = %s, want FailedPrecondition", got)
	}
	if !strings.Contains(err.Error(), "not trusted") {
		t.Errorf("error %q does not say the workspace is untrusted", err)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("bazel was exec'd for an untrusted workspace")
	}

	// Trusting it is the whole difference.
	writeFile(t, filepath.Join(w.store.Root(), "out.bin"), fileDescriptorSet(t, "grpc/health/v1/health.proto"))
	fakeBazelOnPath(t, cqueryPrinting("out.bin"))
	if err := wsroot.Trust(w.store.Root()); err != nil {
		t.Fatalf("Trust: %v", err)
	}
	resp, err := w.AddDescriptorSource(ctx, connect.NewRequest(bazelAddReq("//proto/health:health")))
	if err != nil {
		t.Fatalf("AddDescriptorSource after Trust: %v", err)
	}
	if !hasService(resp.Msg.GetCollection().GetServices(), "Health") {
		t.Fatalf("Health missing after trusting: %v", resp.Msg.GetCollection().GetServices())
	}
}

// A bazel label that is really a flag never reaches a build.
func TestAddBazelSourceRejectsABadLabel(t *testing.T) {
	isolateTrust(t)
	w := newTestWorkspace(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	bazelWorkspace(t, w)
	if err := wsroot.Trust(w.store.Root()); err != nil {
		t.Fatalf("Trust: %v", err)
	}
	marker := filepath.Join(w.store.Root(), "bazel-ran")
	fakeBazelOnPath(t, "touch '"+marker+"'\nexit 0\n")

	_, err := w.AddDescriptorSource(ctx, connect.NewRequest(bazelAddReq("//x --output_base=/tmp")))
	if err == nil {
		t.Fatal("AddDescriptorSource with a flag for a label succeeded, want InvalidArgument")
	}
	if got := connect.CodeOf(err); got != connect.CodeInvalidArgument {
		t.Errorf("code = %s, want InvalidArgument", got)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("bazel was exec'd for an invalid label")
	}
}

// A build that fails fails the ADD, exactly as an undialable reflection target does, and the
// bazel error text (which names the fix) survives the trip through connect.
func TestAddBazelSourceBuildFailureFailsTheAdd(t *testing.T) {
	isolateTrust(t)
	w := newTestWorkspace(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	bazelWorkspace(t, w)
	if err := wsroot.Trust(w.store.Root()); err != nil {
		t.Fatalf("Trust: %v", err)
	}
	fakeBazelOnPath(t, "echo 'ERROR: no such package' >&2\nexit 1\n")

	_, err := w.AddDescriptorSource(ctx, connect.NewRequest(bazelAddReq("//proto/health:health")))
	if err == nil {
		t.Fatal("AddDescriptorSource with a failing build succeeded, want an error")
	}
	if got := connect.CodeOf(err); got != connect.CodeFailedPrecondition {
		t.Errorf("code = %s, want FailedPrecondition", got)
	}
	if !strings.Contains(err.Error(), "ERROR: no such package") {
		t.Errorf("error %q lost bazel's own stderr", err)
	}
}

// An unbuildable (here: untrusted) bazel source must still LOAD, with the reason on its row —
// nothing on the read path may build, so a colleague's committed label cannot stop the collection
// from opening.
func TestBazelSourceLoadsUnresolvedWhenUntrusted(t *testing.T) {
	isolateTrust(t)
	w := newTestWorkspace(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	bazelWorkspace(t, w)
	if err := wsroot.Trust(w.store.Root()); err != nil {
		t.Fatalf("Trust: %v", err)
	}
	writeFile(t, filepath.Join(w.store.Root(), "out.bin"), fileDescriptorSet(t, "grpc/health/v1/health.proto"))
	fakeBazelOnPath(t, cqueryPrinting("out.bin"))
	if _, err := w.AddDescriptorSource(ctx, connect.NewRequest(bazelAddReq("//proto/health:health"))); err != nil {
		t.Fatalf("AddDescriptorSource: %v", err)
	}

	// Revoke, drop the memo, and re-read: the stored resolve is still served, and nothing rebuilds.
	if err := wsroot.Revoke(w.store.Root()); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	w.defs.invalidate("default")
	marker := filepath.Join(w.store.Root(), "bazel-ran")
	fakeBazelOnPath(t, "touch '"+marker+"'\nexit 0\n")

	got, err := w.Get(ctx, connect.NewRequest(&grpcviewv1.GetRequest{Collection: testWorkspace}))
	if err != nil {
		t.Fatalf("Get after Revoke: %v", err)
	}
	if !hasService(got.Msg.GetCollection().GetServices(), "Health") {
		t.Errorf("revoking trust must not un-resolve what is already stored: %v", got.Msg.GetCollection().GetServices())
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("a read rebuilt a bazel source; nothing on the read path may build")
	}
}

func TestSetWorkspaceTrustFlipsTheListing(t *testing.T) {
	isolateTrust(t)
	w := newTestWorkspace(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)

	listing, err := w.ListCollections(ctx, connect.NewRequest(&grpcviewv1.ListCollectionsRequest{}))
	if err != nil {
		t.Fatalf("ListCollections: %v", err)
	}
	if listing.Msg.GetTrusted() {
		t.Fatal("a fresh workspace must not be trusted")
	}

	set, err := w.SetWorkspaceTrust(ctx, connect.NewRequest(&grpcviewv1.SetWorkspaceTrustRequest{Trusted: true}))
	if err != nil {
		t.Fatalf("SetWorkspaceTrust(true): %v", err)
	}
	if !set.Msg.GetTrusted() {
		t.Error("SetWorkspaceTrust(true) reported not trusted")
	}
	if listing, err = w.ListCollections(ctx, connect.NewRequest(&grpcviewv1.ListCollectionsRequest{})); err != nil {
		t.Fatalf("ListCollections: %v", err)
	}
	if !listing.Msg.GetTrusted() {
		t.Error("ListCollections.trusted = false after SetWorkspaceTrust(true)")
	}

	if set, err = w.SetWorkspaceTrust(ctx, connect.NewRequest(&grpcviewv1.SetWorkspaceTrustRequest{})); err != nil {
		t.Fatalf("SetWorkspaceTrust(false): %v", err)
	}
	if set.Msg.GetTrusted() {
		t.Error("SetWorkspaceTrust(false) reported trusted")
	}
	if listing, err = w.ListCollections(ctx, connect.NewRequest(&grpcviewv1.ListCollectionsRequest{})); err != nil {
		t.Fatalf("ListCollections: %v", err)
	}
	if listing.Msg.GetTrusted() {
		t.Error("ListCollections.trusted = true after SetWorkspaceTrust(false)")
	}
}

// An upload WITH a path has a real refresh recipe: the file is re-read and the new schema is what
// resolves.
func TestRefreshUploadRereadsItsPath(t *testing.T) {
	w := newTestWorkspace(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)

	path := filepath.Join("protos", "image.binpb")
	onDisk := filepath.Join(w.store.Root(), path)
	writeFile(t, onDisk, fileDescriptorSet(t, "grpc/health/v1/health.proto"))

	add := descriptorSetAddReq(fileDescriptorSet(t, "grpc/health/v1/health.proto"))
	add.Path = onDisk // an absolute path is accepted and stored root-relative
	resp, err := w.AddDescriptorSource(ctx, connect.NewRequest(add))
	if err != nil {
		t.Fatalf("AddDescriptorSource: %v", err)
	}
	src := resp.Msg.GetCollection().GetSources()[0]
	if got, want := src.GetUpload().GetPath(), "protos/image.binpb"; got != want {
		t.Fatalf("stored path = %q, want the root-relative %q", got, want)
	}

	// Rebuild the image with a different schema; a refresh must pick THAT up.
	writeFile(t, onDisk, fileDescriptorSet(t, "grpc/reflection/v1/reflection.proto"))
	refreshed, err := w.RefreshDescriptorSource(ctx, connect.NewRequest(&grpcviewv1.RefreshDescriptorSourceRequest{
		Collection: testWorkspace,
		Id:         src.GetId(),
	}))
	if err != nil {
		t.Fatalf("RefreshDescriptorSource: %v", err)
	}
	coll := refreshed.Msg.GetCollection()
	if !hasService(coll.GetServices(), "ServerReflection") {
		t.Errorf("the re-read schema is missing: %v", coll.GetServices())
	}
	if hasService(coll.GetServices(), "Health") {
		t.Errorf("the old schema survived the refresh: %v", coll.GetServices())
	}
}

// Identity is the file name, so re-adding the same upload from a MOVED file edits the recipe rather
// than spawning a second source — which is the whole reason the path is not part of the id.
func TestAddUploadUpdatesTheRecipeInPlace(t *testing.T) {
	w := newTestWorkspace(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)

	set := fileDescriptorSet(t, "grpc/health/v1/health.proto")
	writeFile(t, filepath.Join(w.store.Root(), "old", "image.binpb"), set)
	writeFile(t, filepath.Join(w.store.Root(), "new", "image.binpb"), set)

	add := descriptorSetAddReq(set)
	add.Path = "old/image.binpb"
	if _, err := w.AddDescriptorSource(ctx, connect.NewRequest(add)); err != nil {
		t.Fatalf("AddDescriptorSource: %v", err)
	}
	add.Path = "new/image.binpb"
	resp, err := w.AddDescriptorSource(ctx, connect.NewRequest(add))
	if err != nil {
		t.Fatalf("AddDescriptorSource after the move: %v", err)
	}
	sources := resp.Msg.GetCollection().GetSources()
	if len(sources) != 1 {
		t.Fatalf("re-adding the same upload must not duplicate it, got %v", sourceIDsOf(sources))
	}
	if got := sources[0].GetUpload().GetPath(); got != "new/image.binpb" {
		t.Errorf("recorded path = %q, want the new one", got)
	}
}

// A path that does not confine costs the RECIPE and nothing else: the bytes are already in the
// request and already valid, so the ordinary "add the buf image from ~/Downloads" (or a bazel-bin/
// path, which is a symlink out of the repo) still lands — it is simply not refreshable. The strict
// confinement lives on the READ side, which TestRefreshRefusesAnUnconfinedRecipe pins.
func TestAddUploadOutsideTheWorkspaceRecordsNoRecipe(t *testing.T) {
	w := newTestWorkspace(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)

	outside := filepath.Join(t.TempDir(), "downloaded.binpb")
	writeFile(t, outside, fileDescriptorSet(t, "grpc/health/v1/health.proto"))

	add := descriptorSetAddReq(fileDescriptorSet(t, "grpc/health/v1/health.proto"))
	add.Path = outside
	resp, err := w.AddDescriptorSource(ctx, connect.NewRequest(add))
	if err != nil {
		t.Fatalf("AddDescriptorSource with a path outside the workspace: %v", err)
	}
	coll := resp.Msg.GetCollection()
	if !hasService(coll.GetServices(), "Health") {
		t.Fatalf("the uploaded bytes did not resolve: %v", coll.GetServices())
	}
	if got := coll.GetSources()[0].GetUpload().GetPath(); got != "" {
		t.Errorf("recorded recipe = %q, want none for a file outside the workspace", got)
	}

	// With no recipe there is nothing to re-read, which is the refusal a pathless upload gets.
	_, err = w.RefreshDescriptorSource(ctx, connect.NewRequest(&grpcviewv1.RefreshDescriptorSourceRequest{
		Collection: testWorkspace,
		Id:         coll.GetSources()[0].GetId(),
	}))
	if got := connect.CodeOf(err); got != connect.CodeFailedPrecondition {
		t.Errorf("refresh of an un-refreshable upload = %v (code %s), want FailedPrecondition", err, got)
	}
}

// The read side stays strict: a recipe a hand-edited manifest points out of the workspace is
// refused rather than read, which is where the untrusted-path hazard actually lives.
func TestRefreshRefusesAnUnconfinedRecipe(t *testing.T) {
	w := newTestWorkspace(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)

	set := fileDescriptorSet(t, "grpc/health/v1/health.proto")
	writeFile(t, filepath.Join(w.store.Root(), "image.binpb"), set)
	add := descriptorSetAddReq(set)
	add.Path = "image.binpb"
	resp, err := w.AddDescriptorSource(ctx, connect.NewRequest(add))
	if err != nil {
		t.Fatalf("AddDescriptorSource: %v", err)
	}
	src := resp.Msg.GetCollection().GetSources()[0]

	// Re-point the recorded recipe out of the workspace, the way an editor can.
	outside := filepath.Join(t.TempDir(), "secret.binpb")
	writeFile(t, outside, set)
	coll, err := w.store.Open(ctx, testWorkspace)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	sources, err := coll.Sources(ctx)
	if err != nil {
		t.Fatalf("Sources: %v", err)
	}
	sources[0].GetUpload().Path = outside
	if err := coll.PutDescriptorState(ctx, store.DescriptorState{Sources: sources}); err != nil {
		t.Fatalf("PutDescriptorState: %v", err)
	}

	_, err = w.RefreshDescriptorSource(ctx, connect.NewRequest(&grpcviewv1.RefreshDescriptorSourceRequest{
		Collection: testWorkspace,
		Id:         src.GetId(),
	}))
	if err == nil {
		t.Fatal("a refresh through a recipe outside the workspace succeeded, want InvalidArgument")
	}
	if got := connect.CodeOf(err); got != connect.CodeInvalidArgument {
		t.Errorf("code = %s, want InvalidArgument", got)
	}
}

// writeBazelRoot hand-writes grpcview.work.json's bazel block, which is the repo state a colleague
// commits — and therefore the input the confinement in bazelBuilder is about.
func writeBazelRoot(t *testing.T, root, bazelRoot string) {
	t.Helper()
	manifest := fmt.Sprintf(`{
  "schemaVersion": 1,
  "name": "acme",
  "bazel": {"root": %q}
}
`, bazelRoot)
	if err := os.WriteFile(filepath.Join(root, store.WorkspaceFileName), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write %s: %v", store.WorkspaceFileName, err)
	}
}

// The case bazel.root exists for: a grpcview workspace opened at a subdirectory of a monorepo, whose
// bazel root is ABOVE it. An ancestor is on the same line of descent as the trusted root, so it is
// accepted.
func TestBazelRootMayBeAnAncestorOfTheWorkspace(t *testing.T) {
	isolateTrust(t)
	w := newTestWorkspace(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	if err := wsroot.Trust(w.store.Root()); err != nil {
		t.Fatalf("Trust: %v", err)
	}

	monorepo := filepath.Dir(w.store.Root())
	writeFile(t, filepath.Join(monorepo, "MODULE.bazel"), []byte("module(name = \"acme\")\n"))
	writeBazelRoot(t, w.store.Root(), monorepo)
	// cquery prints paths relative to the bazel root, so the output lands there.
	writeFile(t, filepath.Join(monorepo, "out.bin"), fileDescriptorSet(t, "grpc/health/v1/health.proto"))
	fakeBazelOnPath(t, cqueryPrinting("out.bin"))

	resp, err := w.AddDescriptorSource(ctx, connect.NewRequest(bazelAddReq("//proto/health:health")))
	if err != nil {
		t.Fatalf("AddDescriptorSource with bazel.root above the workspace: %v", err)
	}
	if !hasService(resp.Msg.GetCollection().GetServices(), "Health") {
		t.Fatalf("Health missing: %v", resp.Msg.GetCollection().GetServices())
	}
}

// A bazel.root pointing at an UNRELATED tree is refused: trust covers one root, and a build whose
// cwd is somewhere else would run that repo's BUILD files on the strength of this repo's grant.
func TestBazelRootOutsideTheTrustedWorkspaceIsRefused(t *testing.T) {
	isolateTrust(t)
	w := newTestWorkspace(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	if err := wsroot.Trust(w.store.Root()); err != nil {
		t.Fatalf("Trust: %v", err)
	}

	elsewhere := t.TempDir() // a sibling: neither inside the workspace nor above it
	writeFile(t, filepath.Join(elsewhere, "MODULE.bazel"), []byte("module(name = \"other\")\n"))
	writeBazelRoot(t, w.store.Root(), elsewhere)
	marker := filepath.Join(w.store.Root(), "bazel-ran")
	fakeBazelOnPath(t, "touch '"+marker+"'\nexit 0\n")

	_, err := w.AddDescriptorSource(ctx, connect.NewRequest(bazelAddReq("//proto/health:health")))
	if err == nil {
		t.Fatal("AddDescriptorSource with bazel.root in an unrelated repo succeeded, want a refusal")
	}
	if got := connect.CodeOf(err); got != connect.CodeFailedPrecondition {
		t.Errorf("code = %s, want FailedPrecondition", got)
	}
	if !strings.Contains(err.Error(), "bazel.root") || !strings.Contains(err.Error(), store.WorkspaceFileName) {
		t.Errorf("error %q must name the manifest field and the file it is in", err)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("bazel was exec'd with a cwd outside the trusted workspace")
	}
}

// An upload with NO path records none, which is what keeps its refresh a refusal
// (TestRefreshAnUploadFails) rather than a read of some other file.
func TestAddPathlessUploadRecordsNoRecipe(t *testing.T) {
	w := newTestWorkspace(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)

	resp, err := w.AddDescriptorSource(ctx, connect.NewRequest(
		descriptorSetAddReq(fileDescriptorSet(t, "grpc/health/v1/health.proto"))))
	if err != nil {
		t.Fatalf("AddDescriptorSource: %v", err)
	}
	if got := resp.Msg.GetCollection().GetSources()[0].GetUpload().GetPath(); got != "" {
		t.Fatalf("an upload with no path must record none, got %q", got)
	}
}
