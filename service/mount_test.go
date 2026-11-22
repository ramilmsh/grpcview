package service

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"connectrpc.com/connect"
	"connectrpc.com/grpcreflect"
	"google.golang.org/protobuf/reflect/protoreflect"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/wire"
)

type stubWorkspace struct {
	grpcviewv1.UnimplementedWorkspaceServiceHandler
	grpcviewv1.UnimplementedFileServiceHandler
}

func (stubWorkspace) Stat(
	_ context.Context,
	req *connect.Request[grpcviewv1.StatRequest],
) (*connect.Response[grpcviewv1.StatResponse], error) {
	return connect.NewResponse(&grpcviewv1.StatResponse{
		Stat: &grpcviewv1.Stat{Type: grpcviewv1.FileType_FILE_TYPE_DIRECTORY, Label: req.Msg.GetPath()},
	}), nil
}

func serveMounted(t *testing.T) string {
	t.Helper()
	mux := http.NewServeMux()
	mountWorkspace(mux, stubWorkspace{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv := httptest.NewUnstartedServer(mux)
	srv.Config.Protocols = new(http.Protocols)
	srv.Config.Protocols.SetHTTP1(true)
	srv.Config.Protocols.SetUnencryptedHTTP2(true)
	srv.Start()
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestMountWorkspace_reflectsFileService(t *testing.T) {
	// Reflection is a bidi stream, so it needs HTTP/2.
	h2c := new(http.Protocols)
	h2c.SetUnencryptedHTTP2(true)
	client := &http.Client{Transport: &http.Transport{Protocols: h2c}}
	stream := grpcreflect.NewClient(client, serveMounted(t)).NewStream(t.Context())
	defer stream.Close()
	names, err := stream.ListServices()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(names, protoreflect.FullName(grpcviewv1.FileServiceName)) {
		t.Fatalf("reflection lists %v, want %s", names, grpcviewv1.FileServiceName)
	}
}

func TestMountWorkspace_routesFileServiceThroughWire(t *testing.T) {
	res, err := wire.Remote(serveMounted(t)).Stat(t.Context(), connect.NewRequest(&grpcviewv1.StatRequest{Path: "a/b"}))
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Msg.GetStat(); got.GetType() != grpcviewv1.FileType_FILE_TYPE_DIRECTORY || got.GetLabel() != "a/b" {
		t.Fatalf("Stat = %v", got)
	}
}
