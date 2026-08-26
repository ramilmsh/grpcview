package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/store"
	"codeberg.org/ramilmsh/grpcview/service/wsroot"
)

func isolateTrust(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, ".config"))
}

func fakeBazelOnPath(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bazel"), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatalf("write fake bazel: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func bazelWorkspace(t *testing.T, w Workspace) {
	t.Helper()
	writeFile(t, filepath.Join(w.store.Root(), "MODULE.bazel"), []byte("module(name = \"test\")\n"))
}

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
	if addr := hasServiceNamed(coll.GetServices(), "grpc.health.v1.Health").GetSource().GetAddress(); addr != "" {
		t.Errorf("a bazel source must contribute no dial target, got %q", addr)
	}
}

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

func TestRefreshUploadRereadsItsPath(t *testing.T) {
	w := newTestWorkspace(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)

	path := filepath.Join("protos", "image.binpb")
	onDisk := filepath.Join(w.store.Root(), path)
	writeFile(t, onDisk, fileDescriptorSet(t, "grpc/health/v1/health.proto"))

	add := descriptorSetAddReq(fileDescriptorSet(t, "grpc/health/v1/health.proto"))
	add.Path = onDisk
	resp, err := w.AddDescriptorSource(ctx, connect.NewRequest(add))
	if err != nil {
		t.Fatalf("AddDescriptorSource: %v", err)
	}
	src := resp.Msg.GetCollection().GetSources()[0]
	if got, want := src.GetUpload().GetPath(), "protos/image.binpb"; got != want {
		t.Fatalf("stored path = %q, want the root-relative %q", got, want)
	}

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

	_, err = w.RefreshDescriptorSource(ctx, connect.NewRequest(&grpcviewv1.RefreshDescriptorSourceRequest{
		Collection: testWorkspace,
		Id:         coll.GetSources()[0].GetId(),
	}))
	if got := connect.CodeOf(err); got != connect.CodeFailedPrecondition {
		t.Errorf("refresh of an un-refreshable upload = %v (code %s), want FailedPrecondition", err, got)
	}
}

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

func TestBazelRootOutsideTheTrustedWorkspaceIsRefused(t *testing.T) {
	isolateTrust(t)
	w := newTestWorkspace(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	if err := wsroot.Trust(w.store.Root()); err != nil {
		t.Fatalf("Trust: %v", err)
	}

	elsewhere := t.TempDir()
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

func TestListBazelTargets(t *testing.T) {
	isolateTrust(t)
	w := newTestWorkspace(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	bazelWorkspace(t, w)
	if err := wsroot.Trust(w.store.Root()); err != nil {
		t.Fatalf("Trust: %v", err)
	}
	fakeBazelOnPath(t, "printf '%s\\n' //proto/pay:pay_proto //proto/health\nexit 0\n")

	resp, err := w.ListBazelTargets(ctx, connect.NewRequest(&grpcviewv1.ListBazelTargetsRequest{}))
	if err != nil {
		t.Fatalf("ListBazelTargets: %v", err)
	}
	if got, want := strings.Join(resp.Msg.GetLabels(), ","), "//proto/health:health,//proto/pay:pay_proto"; got != want {
		t.Errorf("labels = %q, want %q", got, want)
	}
	if resp.Msg.GetWarning() != "" {
		t.Errorf("warning = %q, want none", resp.Msg.GetWarning())
	}
}

func TestListBazelTargetsRefusedWhenUntrusted(t *testing.T) {
	isolateTrust(t)
	w := newTestWorkspace(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	bazelWorkspace(t, w)

	marker := filepath.Join(w.store.Root(), "bazel-ran")
	fakeBazelOnPath(t, "touch '"+marker+"'\nexit 0\n")

	_, err := w.ListBazelTargets(ctx, connect.NewRequest(&grpcviewv1.ListBazelTargetsRequest{}))
	if err == nil {
		t.Fatal("ListBazelTargets on an untrusted workspace succeeded, want a refusal")
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
}
