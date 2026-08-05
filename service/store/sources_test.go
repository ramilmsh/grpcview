package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	grpcviewstorev1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/store/v1"
	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
)

func TestNormalizeSourcesDropsAmbiguity(t *testing.T) {
	coll, ctx := newTestCollection(t)
	manifest := `{
  "schemaVersion": 1,
  "name": "test",
  "sources": [
    {"reflection": {"address": "localhost:8080"}},
    {},
    {"reflection": {"address": "localhost:8080"}},
    {"reflection": {"address": "localhost:9090"}}
  ]
}
`
	if err := os.WriteFile(coll.collectionFilePath(), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	ws, err := coll.Load(ctx)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(ws.GetSources()) != 2 {
		t.Fatalf("sources = %d, want 2 (the duplicate and the empty entry dropped): %v",
			len(ws.GetSources()), ws.GetSources())
	}
	if id := ws.GetSources()[0].GetId(); id != "reflection:localhost:8080" {
		t.Errorf("kept the wrong copy of the duplicate: %q", id)
	}
	if id := ws.GetSources()[1].GetId(); id != "reflection:localhost:9090" {
		t.Errorf("source[1] id = %q, want reflection:localhost:9090", id)
	}
}

func TestSourceIDAgreesAcrossShapes(t *testing.T) {
	cases := []struct {
		name string
		wire *grpcviewv1.DescriptorSource
		want string
	}{
		{
			name: "reflection",
			wire: &grpcviewv1.DescriptorSource{
				Source: &grpcviewv1.DescriptorSource_Reflection{
					Reflection: &grpcviewv1.Server{Address: "localhost:8080"},
				},
			},
			want: "reflection:localhost:8080",
		},
		{
			name: "reflection over tls is a distinct source",
			wire: &grpcviewv1.DescriptorSource{
				Source: &grpcviewv1.DescriptorSource_Reflection{
					Reflection: &grpcviewv1.Server{Address: "localhost:8080", Tls: &grpcviewv1.Server_TLS{}},
				},
			},
			want: "reflection:localhost:8080+tls",
		},
		{
			name: "upload",
			wire: &grpcviewv1.DescriptorSource{
				Source: &grpcviewv1.DescriptorSource_Upload{
					Upload: &grpcviewv1.Upload{FileName: "buf_image.binpb"},
				},
			},
			want: "upload:buf_image.binpb",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SourceID(tc.wire); got != tc.want {
				t.Errorf("SourceID = %q, want %q", got, tc.want)
			}
			if got := diskSourceID(wireToDiskSource(tc.wire, nil)); got != tc.want {
				t.Errorf("diskSourceID = %q, want %q", got, tc.want)
			}
		})
	}
	if got := SourceID(&grpcviewv1.DescriptorSource{}); got != "" {
		t.Errorf("SourceID of a contentless source = %q, want empty", got)
	}
}

// writeSharedDefinitions writes a workspace manifest holding shared reflection DEFINITIONS (and
// optionally the defaults a new collection is seeded from). It never lists collections, so
// discovery still scans — the two halves of grpcview.work.json are independent.
func writeSharedDefinitions(t *testing.T, root string, defaults []string, addresses ...string) {
	t.Helper()
	ws := &grpcviewstorev1.Workspace{SchemaVersion: schemaVersion, Name: "acme"}
	for _, addr := range addresses {
		ws.Sources = append(ws.Sources, &grpcviewstorev1.DescriptorSource{
			Source: &grpcviewstorev1.DescriptorSource_Reflection{
				Reflection: &grpcviewstorev1.Reflection{Address: addr},
			},
		})
	}
	if len(defaults) > 0 {
		ws.Defaults = &grpcviewstorev1.Defaults{Sources: defaults}
	}
	if err := writeMessage(filepath.Join(root, WorkspaceFileName), ws); err != nil {
		t.Fatalf("write workspace manifest: %v", err)
	}
}

// writeSourceEntries writes a collection manifest whose source list is exactly entries, so a test
// can author a bare REFERENCE — the shape no converter produces from a wire message.
func writeSourceEntries(t *testing.T, coll *Collection, entries ...*grpcviewstorev1.DescriptorSource) {
	t.Helper()
	if err := writeMessage(coll.collectionFilePath(), &grpcviewstorev1.Collection{
		SchemaVersion: schemaVersion,
		Name:          "test",
		Sources:       entries,
	}); err != nil {
		t.Fatalf("write collection manifest: %v", err)
	}
}

func reference(id string, commit bool) *grpcviewstorev1.DescriptorSource {
	return &grpcviewstorev1.DescriptorSource{Id: id, CommitDescriptors: commit}
}

func inlineReflection(address string) *grpcviewstorev1.DescriptorSource {
	return &grpcviewstorev1.DescriptorSource{
		Source: &grpcviewstorev1.DescriptorSource_Reflection{
			Reflection: &grpcviewstorev1.Reflection{Address: address},
		},
	}
}

func diskSources(t *testing.T, coll *Collection) []*grpcviewstorev1.DescriptorSource {
	t.Helper()
	col := &grpcviewstorev1.Collection{}
	mustRead(t, coll.collectionFilePath(), col)
	return col.GetSources()
}

// TestWorkspaceReferenceJoinsTheDefinition is the read half of the definition/list split: the
// collection's manifest says only "this id, at this position", and the config comes from the
// workspace.
func TestWorkspaceReferenceJoinsTheDefinition(t *testing.T) {
	coll, ctx := newTestCollection(t)
	writeSharedDefinitions(t, coll.store.root, nil, "shared.example:50051")
	writeSourceEntries(t, coll,
		reference("reflection:shared.example:50051", true),
		inlineReflection("own.example:50051"),
	)

	got, err := coll.Sources(ctx)
	if err != nil {
		t.Fatalf("Sources: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("sources = %d, want 2 (a reference and an inline source): %v", len(got), got)
	}
	if addr := got[0].GetReflection().GetAddress(); addr != "shared.example:50051" {
		t.Errorf("reference did not pick up the definition's address: %q", addr)
	}
	if got[0].GetOrigin() != grpcviewv1.SourceOrigin_SOURCE_ORIGIN_WORKSPACE {
		t.Errorf("reference origin = %v, want WORKSPACE", got[0].GetOrigin())
	}
	// The referencing ENTRY owns commit_descriptors; a definition never does.
	if !got[0].GetCommitDescriptors() {
		t.Errorf("reference lost its own commit_descriptors")
	}
	if got[1].GetOrigin() != grpcviewv1.SourceOrigin_SOURCE_ORIGIN_COLLECTION {
		t.Errorf("inline source origin = %v, want COLLECTION", got[1].GetOrigin())
	}
}

// TestReferenceSurvivesAListRewrite is the invariant the whole tier rests on: every mutation
// round-trips the list through the wire form, so a reorder must leave the manifest holding a bare
// reference rather than a copy of the shared config.
func TestReferenceSurvivesAListRewrite(t *testing.T) {
	coll, ctx := newTestCollection(t)
	const id = "reflection:shared.example:50051"
	writeSharedDefinitions(t, coll.store.root, nil, "shared.example:50051")
	writeSourceEntries(t, coll, reference(id, false), inlineReflection("own.example:50051"))

	wire, err := coll.Sources(ctx)
	if err != nil {
		t.Fatalf("Sources: %v", err)
	}
	if err := coll.PutDescriptorState(ctx, DescriptorState{
		Sources: []*grpcviewv1.DescriptorSource{wire[1], wire[0]},
	}); err != nil {
		t.Fatalf("PutDescriptorState (reorder): %v", err)
	}

	entries := diskSources(t, coll)
	if len(entries) != 2 {
		t.Fatalf("manifest sources = %d, want 2: %v", len(entries), entries)
	}
	if entries[1].GetId() != id || entries[1].GetSource() != nil {
		t.Errorf("reorder inlined the shared definition instead of keeping a bare reference: %+v", entries[1])
	}
	if entries[0].GetReflection().GetAddress() != "own.example:50051" {
		t.Errorf("the collection's own source lost its config: %+v", entries[0])
	}
}

// TestInlineSourceTheWorkspaceDefinesCollapses is the dedup an add relies on: identity is
// config-derived, so an entry whose id the workspace already declares carries the same config and
// is written as a reference to it. It also pins origin as read-only — the input here claims
// COLLECTION, there is no disk field to hold that claim, and the read says WORKSPACE.
func TestInlineSourceTheWorkspaceDefinesCollapses(t *testing.T) {
	coll, ctx := newTestCollection(t)
	writeSharedDefinitions(t, coll.store.root, nil, "shared.example:50051")

	claimed := reflectionSourceAt("shared.example:50051")
	claimed.Origin = grpcviewv1.SourceOrigin_SOURCE_ORIGIN_COLLECTION
	if err := coll.PutDescriptorState(ctx, DescriptorState{
		Sources: []*grpcviewv1.DescriptorSource{claimed},
	}); err != nil {
		t.Fatalf("PutDescriptorState: %v", err)
	}
	entries := diskSources(t, coll)
	if len(entries) != 1 || entries[0].GetSource() != nil {
		t.Fatalf("an inline source the workspace defines must be stored as a reference: %v", entries)
	}
	got, err := coll.Sources(ctx)
	if err != nil {
		t.Fatalf("Sources: %v", err)
	}
	if got[0].GetOrigin() != grpcviewv1.SourceOrigin_SOURCE_ORIGIN_WORKSPACE {
		t.Errorf("origin = %v, want WORKSPACE: the client's claim must not survive a write", got[0].GetOrigin())
	}
}

// TestWorkspaceOriginAloneWritesABareReference covers the dangling case: a reference whose
// definition has gone must survive a rewrite as a reference rather than being erased or inlined,
// which is what keeps it visible and removable.
func TestWorkspaceOriginAloneWritesABareReference(t *testing.T) {
	got := wireToDiskSource(&grpcviewv1.DescriptorSource{
		Id:     "reflection:gone.example:50051",
		Origin: grpcviewv1.SourceOrigin_SOURCE_ORIGIN_WORKSPACE,
		Source: &grpcviewv1.DescriptorSource_Reflection{
			Reflection: &grpcviewv1.Server{Address: "gone.example:50051"},
		},
	}, nil)
	if got.GetId() != "reflection:gone.example:50051" || got.GetSource() != nil {
		t.Errorf("want a bare reference, got %+v", got)
	}
}

// TestWorkspaceDefinitionRejectsAnUpload: an upload has no pointer and its bytes belong to the
// collection that supplied them, so a workspace-level one is skipped — and a collection
// referencing it gets a row with no kind, which the layer above reports.
func TestWorkspaceDefinitionRejectsAnUpload(t *testing.T) {
	coll, ctx := newTestCollection(t)
	if err := writeMessage(filepath.Join(coll.store.root, WorkspaceFileName), &grpcviewstorev1.Workspace{
		SchemaVersion: schemaVersion,
		Sources: []*grpcviewstorev1.DescriptorSource{{
			Source: &grpcviewstorev1.DescriptorSource_Upload{
				Upload: &grpcviewstorev1.Upload{FileName: "image.binpb"},
			},
		}},
	}); err != nil {
		t.Fatalf("write workspace manifest: %v", err)
	}

	defs, err := coll.store.workspaceDefinitions()
	if err != nil {
		t.Fatalf("workspaceDefinitions: %v", err)
	}
	if len(defs) != 0 {
		t.Fatalf("an upload must not be usable as a workspace definition: %v", defs)
	}

	writeSourceEntries(t, coll, reference("upload:image.binpb", false))
	got, err := coll.Sources(ctx)
	if err != nil {
		t.Fatalf("Sources: %v", err)
	}
	if len(got) != 1 || got[0].GetSource() != nil {
		t.Fatalf("want one row with no kind, got %v", got)
	}
}

// TestWorkspaceDefinitionIgnoresCommitDescriptors: a sidecar can only live in a collection, so the
// flag is meaningless on a shared definition and never leaks into the collections referencing it.
func TestWorkspaceDefinitionIgnoresCommitDescriptors(t *testing.T) {
	s := newTestStore(t)
	if err := writeMessage(filepath.Join(s.root, WorkspaceFileName), &grpcviewstorev1.Workspace{
		SchemaVersion: schemaVersion,
		Sources: []*grpcviewstorev1.DescriptorSource{{
			CommitDescriptors: true,
			Source: &grpcviewstorev1.DescriptorSource_Reflection{
				Reflection: &grpcviewstorev1.Reflection{Address: "shared.example:50051"},
			},
		}},
	}); err != nil {
		t.Fatalf("write workspace manifest: %v", err)
	}
	defs, err := s.workspaceDefinitions()
	if err != nil {
		t.Fatalf("workspaceDefinitions: %v", err)
	}
	def := defs["reflection:shared.example:50051"]
	if def == nil {
		t.Fatalf("definition missing: %v", defs)
	}
	if def.GetCommitDescriptors() {
		t.Errorf("commit_descriptors on a workspace definition must be ignored")
	}
}

// TestDefaultsSeedANewCollection: defaults.sources gives a new collection a list of POINTERS, in
// order, skipping ids nothing defines. Creating it acquires nothing.
func TestDefaultsSeedANewCollection(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	writeSharedDefinitions(t, s.root,
		[]string{"reflection:b.example:50051", "reflection:nope.example:50051", "reflection:a.example:50051"},
		"a.example:50051", "b.example:50051")

	coll, err := s.Open(ctx, "services/payments/requests")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := coll.Create(ctx, ""); err != nil {
		t.Fatalf("Create: %v", err)
	}

	entries := diskSources(t, coll)
	want := []string{"reflection:b.example:50051", "reflection:a.example:50051"}
	if len(entries) != len(want) {
		t.Fatalf("seeded sources = %v, want %v", entries, want)
	}
	for i, id := range want {
		if entries[i].GetId() != id {
			t.Errorf("seeded[%d] = %q, want %q", i, entries[i].GetId(), id)
		}
		if entries[i].GetSource() != nil {
			t.Errorf("seeded[%d] is a copy, not a reference: %+v", i, entries[i])
		}
	}
	if resolves, err := coll.DescriptorResolves(ctx); err != nil || len(resolves) != 0 {
		t.Errorf("seeding must acquire nothing: %v, %v", resolves, err)
	}
}

// TestOneBlobServesTwoReferencingCollections is the sharing the definition tier promises: the CAS
// is per workspace, so two collections referencing one definition hold one copy of its bytes with
// no id-keyed scheme of its own.
func TestOneBlobServesTwoReferencingCollections(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	const id = "reflection:shared.example:50051"
	writeSharedDefinitions(t, s.root, []string{id}, "shared.example:50051")

	for _, name := range []string{"services/payments/requests", "services/ledger/requests"} {
		coll, err := s.Open(ctx, name)
		if err != nil {
			t.Fatalf("Open %s: %v", name, err)
		}
		if err := coll.Create(ctx, ""); err != nil {
			t.Fatalf("Create %s: %v", name, err)
		}
		wire, err := coll.Sources(ctx)
		if err != nil {
			t.Fatalf("Sources %s: %v", name, err)
		}
		if len(wire) != 1 || wire[0].GetReflection().GetAddress() != "shared.example:50051" {
			t.Fatalf("%s was not seeded with a working reference: %v", name, wire)
		}
		if err := coll.PutDescriptorState(ctx, DescriptorState{
			Sources: wire,
			Resolves: map[string]*grpcviewstorev1.ResolvedSource{
				// Independently built, byte-identical: two collections resolved the one target the
				// workspace defines.
				id: {Id: id, DescriptorSet: fdsNamed("acme/v1/user.proto"), ServiceNames: []string{"acme.v1.UserService"}},
			},
		}); err != nil {
			t.Fatalf("PutDescriptorState %s: %v", name, err)
		}
		if entries := diskSources(t, coll); len(entries) != 1 || entries[0].GetSource() != nil {
			t.Fatalf("%s inlined the shared definition: %v", name, entries)
		}
	}

	if got := blobNames(t, s); len(got) != 1 {
		t.Errorf("two collections referencing one definition must share one blob, got %v", got)
	}
}
