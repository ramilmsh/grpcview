package store

import (
	"os"
	"testing"

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
			if got := diskSourceID(wireToDiskSource(tc.wire)); got != tc.want {
				t.Errorf("diskSourceID = %q, want %q", got, tc.want)
			}
		})
	}
	if got := SourceID(&grpcviewv1.DescriptorSource{}); got != "" {
		t.Errorf("SourceID of a contentless source = %q, want empty", got)
	}
}
