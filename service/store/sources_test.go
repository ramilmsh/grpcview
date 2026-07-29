package store

import (
	"os"
	"testing"

	"google.golang.org/protobuf/types/descriptorpb"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
)

// legacyManifest is grpcview.json exactly as the pre-identity code wrote it: no
// source ids, and an upload's descriptors stored directly on the source under the
// "descriptorSet" key. Written as raw JSON on purpose — the point of the test is
// that a file already on a user's disk still loads, and only the bytes prove that.
const legacyManifest = `{
  "schemaVersion": 1,
  "name": "test",
  "sources": [
    {"reflection": {"address": "localhost:8080"}},
    {"descriptorSet": {"file": [{"name": "legacy/v1/legacy.proto", "package": "legacy.v1"}]}},
    {"reflection": {"address": "secure.example.com:443", "tls": true}}
  ]
}
`

// TestLoadLegacyManifestGainsIdentities is the upgrade path. Every id-keyed
// operation — refresh, remove, reorder — addresses a source by an id that older
// manifests do not carry, so loading one without repairing it leaves a workspace
// whose rows all share the empty id: removing any one of them removes the first.
// A legacy upload additionally has to survive at all, since the manifest holds the
// only committed copy of its descriptors (the resolve cache is gitignored).
func TestLoadLegacyManifestGainsIdentities(t *testing.T) {
	coll, ctx := newTestCollection(t)
	if err := os.WriteFile(coll.collectionFilePath(), []byte(legacyManifest), 0o644); err != nil {
		t.Fatalf("write legacy manifest: %v", err)
	}

	ws, err := coll.Load(ctx)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := make([]string, 0, len(ws.GetSources()))
	for _, s := range ws.GetSources() {
		got = append(got, s.GetId())
	}
	want := []string{
		"reflection:localhost:8080",
		"upload:uploaded bytes",
		"reflection:secure.example.com:443+tls",
	}
	if len(got) != len(want) {
		t.Fatalf("sources = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("source[%d] id = %q, want %q", i, got[i], want[i])
		}
	}
	// Priority order is the manifest's order, untouched by the repair.
	if up := ws.GetSources()[1].GetUpload(); up.GetFileName() != legacyUploadFileName {
		t.Errorf("legacy upload not recognized as an upload: %v", ws.GetSources()[1])
	}

	// The descriptors have to be reachable BY THE NEW ID, because that is how the
	// merge re-parses an upload and how a write re-commits it.
	fds, err := coll.UploadDescriptors(ctx, "upload:uploaded bytes")
	if err != nil {
		t.Fatalf("UploadDescriptors: %v", err)
	}
	if len(fds.GetFile()) != 1 || fds.GetFile()[0].GetName() != "legacy/v1/legacy.proto" {
		t.Fatalf("legacy descriptors lost: %v", fds)
	}

	// And they have to survive the next write, which re-commits the manifest from
	// the wire sources (where an upload carries only its name).
	if err := coll.PutDescriptorState(ctx, DescriptorState{
		Sources: ws.GetSources(),
		Uploads: map[string]*descriptorpb.FileDescriptorSet{"upload:uploaded bytes": fds},
	}); err != nil {
		t.Fatalf("PutDescriptorState: %v", err)
	}
	again, err := coll.UploadDescriptors(ctx, "upload:uploaded bytes")
	if err != nil {
		t.Fatalf("UploadDescriptors after write: %v", err)
	}
	if len(again.GetFile()) != 1 {
		t.Fatalf("legacy descriptors lost on re-commit: %v", again)
	}
	// The read-only migration field is never written back, so the migration is a
	// one-time rewrite and not a permanent second copy of the bytes.
	raw, err := os.ReadFile(coll.collectionFilePath())
	if err != nil {
		t.Fatal(err)
	}
	col, err := coll.readCollection()
	if err != nil {
		t.Fatal(err)
	}
	for _, ds := range col.GetSources() {
		if ds.GetLegacyDescriptorSet() != nil {
			t.Errorf("legacy_descriptor_set still populated after re-commit: %s", raw)
		}
	}
}

// TestNormalizeSourcesDropsAmbiguity covers manifests that would leave the source
// list unaddressable: a pre-identity add could append the same target twice (it had
// no notion of "the same source"), and a hand-edited file can contain an entry with
// no content at all. Either way two rows would answer to one id.
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

// TestSourceIDAgreesAcrossShapes pins the invariant that keeps a source's identity
// stable across a save/load: the id derived from the wire shape and the id derived
// from the on-disk shape are the same string. If they diverged, every reload would
// rename its sources and orphan their caches.
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
			disk, err := wireToDiskSource(tc.wire, func(string) *descriptorpb.FileDescriptorSet {
				return &descriptorpb.FileDescriptorSet{}
			})
			if err != nil {
				t.Fatalf("wireToDiskSource: %v", err)
			}
			if got := diskSourceID(disk); got != tc.want {
				t.Errorf("diskSourceID = %q, want %q", got, tc.want)
			}
		})
	}
	if got := SourceID(&grpcviewv1.DescriptorSource{}); got != "" {
		t.Errorf("SourceID of a contentless source = %q, want empty", got)
	}
}
