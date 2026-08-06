package cli

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"connectrpc.com/connect"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/workspace"
	"codeberg.org/ramilmsh/grpcview/service/wsroot"
)

type Client interface {
	Get(context.Context, *connect.Request[grpcviewv1.GetRequest]) (*connect.Response[grpcviewv1.GetResponse], error)
	ListCollections(context.Context, *connect.Request[grpcviewv1.ListCollectionsRequest]) (*connect.Response[grpcviewv1.ListCollectionsResponse], error)
	SetWorkspaceTrust(context.Context, *connect.Request[grpcviewv1.SetWorkspaceTrustRequest]) (*connect.Response[grpcviewv1.SetWorkspaceTrustResponse], error)
	Invoke(context.Context, *connect.Request[grpcviewv1.InvokeRequest]) (*connect.Response[grpcviewv1.InvokeResponse], error)
	InvokeSaved(context.Context, *connect.Request[grpcviewv1.InvokeSavedRequest]) (*connect.Response[grpcviewv1.InvokeSavedResponse], error)

	InvokeStream(ctx context.Context, msg *grpcviewv1.InvokeStreamRequest, send func(*grpcviewv1.InvokeStreamingResponse) error) error
	InvokeSavedStream(ctx context.Context, msg *grpcviewv1.InvokeSavedStreamRequest, send func(*grpcviewv1.InvokeStreamingResponse) error) error

	DescribeMethod(context.Context, *connect.Request[grpcviewv1.DescribeMethodRequest]) (*connect.Response[grpcviewv1.DescribeMethodResponse], error)

	AddDescriptorSource(context.Context, *connect.Request[grpcviewv1.AddDescriptorSourceRequest]) (*connect.Response[grpcviewv1.AddDescriptorSourceResponse], error)
	RefreshDescriptorSource(context.Context, *connect.Request[grpcviewv1.RefreshDescriptorSourceRequest]) (*connect.Response[grpcviewv1.RefreshDescriptorSourceResponse], error)
	RemoveDescriptorSource(context.Context, *connect.Request[grpcviewv1.RemoveDescriptorSourceRequest]) (*connect.Response[grpcviewv1.RemoveDescriptorSourceResponse], error)
	ReorderDescriptorSources(context.Context, *connect.Request[grpcviewv1.ReorderDescriptorSourcesRequest]) (*connect.Response[grpcviewv1.ReorderDescriptorSourcesResponse], error)
	SetDescriptorSourceCommit(context.Context, *connect.Request[grpcviewv1.SetDescriptorSourceCommitRequest]) (*connect.Response[grpcviewv1.SetDescriptorSourceCommitResponse], error)

	CreateCollection(context.Context, *connect.Request[grpcviewv1.CreateCollectionRequest]) (*connect.Response[grpcviewv1.CreateCollectionResponse], error)
	CreateFolder(context.Context, *connect.Request[grpcviewv1.CreateFolderRequest]) (*connect.Response[grpcviewv1.CreateFolderResponse], error)
	CreateRequest(context.Context, *connect.Request[grpcviewv1.CreateRequestRequest]) (*connect.Response[grpcviewv1.CreateRequestResponse], error)
	UpdateRequest(context.Context, *connect.Request[grpcviewv1.UpdateRequestRequest]) (*connect.Response[grpcviewv1.UpdateRequestResponse], error)
	DeleteRequest(context.Context, *connect.Request[grpcviewv1.DeleteRequestRequest]) (*connect.Response[grpcviewv1.DeleteRequestResponse], error)
	MoveItem(context.Context, *connect.Request[grpcviewv1.MoveItemRequest]) (*connect.Response[grpcviewv1.MoveItemResponse], error)

	RunScript(context.Context, *connect.Request[grpcviewv1.RunScriptRequest]) (*connect.Response[grpcviewv1.RunScriptResponse], error)
}

type inProcess struct {
	workspace.Workspace
}

type remote struct {
	grpcviewv1.WorkspaceServiceClient
}

func (r remote) InvokeStream(ctx context.Context, msg *grpcviewv1.InvokeStreamRequest, send func(*grpcviewv1.InvokeStreamingResponse) error) error {
	stream, err := r.WorkspaceServiceClient.InvokeStreaming(ctx, connect.NewRequest(msg))
	if err != nil {
		return err
	}
	return drain(stream, send)
}

func (r remote) InvokeSavedStream(ctx context.Context, msg *grpcviewv1.InvokeSavedStreamRequest, send func(*grpcviewv1.InvokeStreamingResponse) error) error {
	stream, err := r.WorkspaceServiceClient.InvokeSavedStreaming(ctx, connect.NewRequest(msg))
	if err != nil {
		return err
	}
	return drain(stream, send)
}

func drain(stream *connect.ServerStreamForClient[grpcviewv1.InvokeStreamingResponse], send func(*grpcviewv1.InvokeStreamingResponse) error) error {
	defer stream.Close()
	for stream.Receive() {
		if err := send(stream.Msg()); err != nil {
			return err
		}
	}
	return stream.Err()
}

var (
	_ Client = inProcess{}
	_ Client = remote{}
)

type session struct {
	Client
	close func(context.Context) error
}

type clientFactory func(ctx context.Context, g *globalFlags) (session, error)

func openClient(ctx context.Context, g *globalFlags, s Streams) (session, error) {
	if g != nil && g.Server != "" {
		return session{
			Client: remote{grpcviewv1.NewWorkspaceServiceClient(http.DefaultClient, g.Server)},
			close:  func(context.Context) error { return nil },
		}, nil
	}

	var override string
	if g != nil {
		override = g.Workspace
	}
	cwd, err := os.Getwd()
	if err != nil {
		return session{}, fmt.Errorf("failed to resolve the current directory: %w", err)
	}
	root, warn, err := wsroot.Discover(override, cwd)
	if err != nil {
		return session{}, err
	}
	if warn != "" {
		fmt.Fprintln(s.Err, warn)
	}

	ws, err := workspace.New(ctx, root)
	if err != nil {
		return session{}, fmt.Errorf("failed to open workspace: %w", err)
	}
	return session{Client: inProcess{ws}, close: ws.Close}, nil
}
