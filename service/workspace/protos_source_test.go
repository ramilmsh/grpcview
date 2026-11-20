package workspace

import (
	"context"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/grpcview/v1"
)

const protosCommon = `syntax = "proto3";
package acme.v1;

// Ping is the request.
message Ping { string id = 1; }
`

const protosSvc = `syntax = "proto3";
package acme.v1;

import "acme/v1/common.proto";
import "google/protobuf/empty.proto";

// Pinger pings.
service Pinger {
  rpc Ping(acme.v1.Ping) returns (google.protobuf.Empty);
}
`

func addProtos(t *testing.T, w Workspace, ctx context.Context, protos *grpcviewv1.Protos, commit bool) (*grpcviewv1.Collection, error) {
	t.Helper()
	resp, err := w.AddDescriptorSource(ctx, connect.NewRequest(&grpcviewv1.AddDescriptorSourceRequest{
		Collection:        testWorkspace,
		Source:            &grpcviewv1.AddDescriptorSourceRequest_Protos{Protos: protos},
		CommitDescriptors: commit,
	}))
	if err != nil {
		return nil, err
	}
	return resp.Msg.GetCollection(), nil
}

func TestAddProtosSource(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "cached", true: "committed"}[commit], func(t *testing.T) {
			w := newTestWorkspace(t)
			ctx := context.Background()
			ensureWorkspace(t, w, ctx)
			root := w.store.Root()
			writeFile(t, filepath.Join(root, "proto/acme/v1/common.proto"), []byte(protosCommon))
			writeFile(t, filepath.Join(root, "proto/acme/v1/svc.proto"), []byte(protosSvc))

			// Absolute path in, root-relative stored; untrusted workspace is fine.
			_, err := addProtos(t, w, ctx, &grpcviewv1.Protos{Path: filepath.Join(root, "proto/acme")}, commit)
			if err == nil {
				t.Fatal("import names relative to proto/acme should not resolve acme/v1/common.proto")
			}
			coll, err := addProtos(t, w, ctx, &grpcviewv1.Protos{
				Path:        filepath.Join(root, "proto/acme"),
				ImportPaths: []string{"proto"},
			}, commit)
			if err != nil {
				t.Fatalf("AddDescriptorSource: %v", err)
			}
			if hasServiceNamed(coll.GetServices(), "acme.v1.Pinger") == nil {
				t.Fatalf("services = %v, want acme.v1.Pinger", coll.GetServices())
			}
			src := coll.GetSources()[0]
			if src.GetId() != "protos:proto/acme" || src.GetProtos().GetImportPaths()[0] != "proto" {
				t.Errorf("source = %v", src)
			}
			if n := src.GetResolved().GetFileCount(); n != 3 {
				t.Errorf("file_count = %d, want 3 (two files + empty.proto)", n)
			}

			// Originals survive the store and come back through resolveConfigured.
			c, err := w.store.Open(ctx, testWorkspace)
			if err != nil {
				t.Fatal(err)
			}
			sources, err := c.Sources(ctx)
			if err != nil {
				t.Fatal(err)
			}
			resolved, err := w.resolveConfigured(ctx, c, sources, nil)
			if err != nil {
				t.Fatal(err)
			}
			originals := resolved[0].originals
			if text := originals["acme/v1/svc.proto"]; text != protosSvc {
				t.Errorf("svc.proto original = %q", text)
			}
			if got := strings.Join(slices.Sorted(maps.Keys(originals)), ","); got != "acme/v1/common.proto,acme/v1/svc.proto" {
				t.Errorf("originals = %v", got)
			}

			// Refresh re-reads the disk.
			writeFile(t, filepath.Join(root, "proto/acme/v1/more.proto"),
				[]byte("syntax = \"proto3\";\npackage acme.v1;\nimport \"acme/v1/common.proto\";\nservice Echo { rpc Echo(Ping) returns (Ping); }\n"))
			resp, err := w.RefreshDescriptorSource(ctx, connect.NewRequest(&grpcviewv1.RefreshDescriptorSourceRequest{
				Collection: testWorkspace,
				Id:         src.GetId(),
			}))
			if err != nil {
				t.Fatalf("RefreshDescriptorSource: %v", err)
			}
			if hasServiceNamed(resp.Msg.GetCollection().GetServices(), "acme.v1.Echo") == nil {
				t.Errorf("refresh did not pick up acme.v1.Echo")
			}
		})
	}
}

func TestAddProtosSourceErrors(t *testing.T) {
	w := newTestWorkspace(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	root := w.store.Root()
	writeFile(t, filepath.Join(root, "bad/x.proto"), []byte("syntax = \"proto3\";\nmessage X { Nope n = 1; }\n"))
	writeFile(t, filepath.Join(root, "elsewhere/y.proto"), []byte("syntax = \"proto3\";\n"))

	for _, tc := range []struct {
		name   string
		protos *grpcviewv1.Protos
		want   string
	}{
		{"compile error", &grpcviewv1.Protos{Path: "bad"}, "x.proto:2:13:"},
		{"file under no import path", &grpcviewv1.Protos{Path: "elsewhere/y.proto", ImportPaths: []string{"bad"}}, "under none of the import paths"},
		{"outside the workspace", &grpcviewv1.Protos{Path: t.TempDir()}, "outside the workspace"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := addProtos(t, w, ctx, tc.protos, false)
			if connect.CodeOf(err) != connect.CodeInvalidArgument || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want InvalidArgument containing %q", err, tc.want)
			}
		})
	}
}
