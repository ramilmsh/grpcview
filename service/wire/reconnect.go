package wire

import (
	"context"

	"connectrpc.com/connect"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/grpcview/v1"
)

// One line per unary RPC, naming the generated client's method. The only thing that varies is
// whether running it twice is safe, and the two helpers below are that decision written down
// once per RPC instead of once per failure: call for everything that changes something (on disk
// here, or on the user's server), read for the handful that do not.
type method[I, O any] func(remote, context.Context, *connect.Request[I]) (*connect.Response[O], error)

func call[I, O any](c *reconnecting, ctx context.Context, m method[I, O], req *connect.Request[I]) (*connect.Response[O], error) {
	return attempt(c, ctx, m, req, false)
}

// read is replayable after a connection breaks mid-flight. "No effect" means none the caller
// can observe twice: these resolve and cache descriptors, and a second resolve returns what
// the first one did.
func read[I, O any](c *reconnecting, ctx context.Context, m method[I, O], req *connect.Request[I]) (*connect.Response[O], error) {
	return attempt(c, ctx, m, req, true)
}

func attempt[I, O any](c *reconnecting, ctx context.Context, m method[I, O], req *connect.Request[I], replayable bool) (*connect.Response[O], error) {
	var res *connect.Response[O]
	err := c.retry(ctx, func(r remote) error {
		var err error
		res, err = m(r, ctx, req)
		return err
	}, nil, replayable)
	if err != nil {
		return nil, err
	}
	return res, nil
}

func (c *reconnecting) Get(ctx context.Context, req *connect.Request[grpcviewv1.GetRequest]) (*connect.Response[grpcviewv1.GetResponse], error) {
	return read(c, ctx, remote.Get, req)
}

func (c *reconnecting) ListCollections(ctx context.Context, req *connect.Request[grpcviewv1.ListCollectionsRequest]) (*connect.Response[grpcviewv1.ListCollectionsResponse], error) {
	return read(c, ctx, remote.ListCollections, req)
}

func (c *reconnecting) CreateCollection(ctx context.Context, req *connect.Request[grpcviewv1.CreateCollectionRequest]) (*connect.Response[grpcviewv1.CreateCollectionResponse], error) {
	return call(c, ctx, remote.CreateCollection, req)
}

func (c *reconnecting) UpdateCollection(ctx context.Context, req *connect.Request[grpcviewv1.UpdateCollectionRequest]) (*connect.Response[grpcviewv1.UpdateCollectionResponse], error) {
	return call(c, ctx, remote.UpdateCollection, req)
}

func (c *reconnecting) SetWorkspaceTrust(ctx context.Context, req *connect.Request[grpcviewv1.SetWorkspaceTrustRequest]) (*connect.Response[grpcviewv1.SetWorkspaceTrustResponse], error) {
	return call(c, ctx, remote.SetWorkspaceTrust, req)
}

func (c *reconnecting) ListBazelTargets(ctx context.Context, req *connect.Request[grpcviewv1.ListBazelTargetsRequest]) (*connect.Response[grpcviewv1.ListBazelTargetsResponse], error) {
	return read(c, ctx, remote.ListBazelTargets, req)
}

func (c *reconnecting) AddDescriptorSource(ctx context.Context, req *connect.Request[grpcviewv1.AddDescriptorSourceRequest]) (*connect.Response[grpcviewv1.AddDescriptorSourceResponse], error) {
	return call(c, ctx, remote.AddDescriptorSource, req)
}

func (c *reconnecting) RemoveDescriptorSource(ctx context.Context, req *connect.Request[grpcviewv1.RemoveDescriptorSourceRequest]) (*connect.Response[grpcviewv1.RemoveDescriptorSourceResponse], error) {
	return call(c, ctx, remote.RemoveDescriptorSource, req)
}

func (c *reconnecting) RefreshDescriptorSource(ctx context.Context, req *connect.Request[grpcviewv1.RefreshDescriptorSourceRequest]) (*connect.Response[grpcviewv1.RefreshDescriptorSourceResponse], error) {
	return call(c, ctx, remote.RefreshDescriptorSource, req)
}

func (c *reconnecting) ReorderDescriptorSources(ctx context.Context, req *connect.Request[grpcviewv1.ReorderDescriptorSourcesRequest]) (*connect.Response[grpcviewv1.ReorderDescriptorSourcesResponse], error) {
	return call(c, ctx, remote.ReorderDescriptorSources, req)
}

func (c *reconnecting) SetDescriptorSourceCommit(ctx context.Context, req *connect.Request[grpcviewv1.SetDescriptorSourceCommitRequest]) (*connect.Response[grpcviewv1.SetDescriptorSourceCommitResponse], error) {
	return call(c, ctx, remote.SetDescriptorSourceCommit, req)
}

func (c *reconnecting) CreateFolder(ctx context.Context, req *connect.Request[grpcviewv1.CreateFolderRequest]) (*connect.Response[grpcviewv1.CreateFolderResponse], error) {
	return call(c, ctx, remote.CreateFolder, req)
}

func (c *reconnecting) UpdateFolder(ctx context.Context, req *connect.Request[grpcviewv1.UpdateFolderRequest]) (*connect.Response[grpcviewv1.UpdateFolderResponse], error) {
	return call(c, ctx, remote.UpdateFolder, req)
}

func (c *reconnecting) CreateRequest(ctx context.Context, req *connect.Request[grpcviewv1.CreateRequestRequest]) (*connect.Response[grpcviewv1.CreateRequestResponse], error) {
	return call(c, ctx, remote.CreateRequest, req)
}

func (c *reconnecting) UpdateRequest(ctx context.Context, req *connect.Request[grpcviewv1.UpdateRequestRequest]) (*connect.Response[grpcviewv1.UpdateRequestResponse], error) {
	return call(c, ctx, remote.UpdateRequest, req)
}

func (c *reconnecting) DeleteRequest(ctx context.Context, req *connect.Request[grpcviewv1.DeleteRequestRequest]) (*connect.Response[grpcviewv1.DeleteRequestResponse], error) {
	return call(c, ctx, remote.DeleteRequest, req)
}

func (c *reconnecting) MoveItem(ctx context.Context, req *connect.Request[grpcviewv1.MoveItemRequest]) (*connect.Response[grpcviewv1.MoveItemResponse], error) {
	return call(c, ctx, remote.MoveItem, req)
}

func (c *reconnecting) Invoke(ctx context.Context, req *connect.Request[grpcviewv1.InvokeRequest]) (*connect.Response[grpcviewv1.InvokeResponse], error) {
	return call(c, ctx, remote.Invoke, req)
}

func (c *reconnecting) InvokeSaved(ctx context.Context, req *connect.Request[grpcviewv1.InvokeSavedRequest]) (*connect.Response[grpcviewv1.InvokeSavedResponse], error) {
	return call(c, ctx, remote.InvokeSaved, req)
}

func (c *reconnecting) DescribeMethod(ctx context.Context, req *connect.Request[grpcviewv1.DescribeMethodRequest]) (*connect.Response[grpcviewv1.DescribeMethodResponse], error) {
	return read(c, ctx, remote.DescribeMethod, req)
}

func (c *reconnecting) RunScript(ctx context.Context, req *connect.Request[grpcviewv1.RunScriptRequest]) (*connect.Response[grpcviewv1.RunScriptResponse], error) {
	return call(c, ctx, remote.RunScript, req)
}

func (c *reconnecting) CreateScript(ctx context.Context, req *connect.Request[grpcviewv1.CreateScriptRequest]) (*connect.Response[grpcviewv1.CreateScriptResponse], error) {
	return call(c, ctx, remote.CreateScript, req)
}

func (c *reconnecting) UpdateScript(ctx context.Context, req *connect.Request[grpcviewv1.UpdateScriptRequest]) (*connect.Response[grpcviewv1.UpdateScriptResponse], error) {
	return call(c, ctx, remote.UpdateScript, req)
}

func (c *reconnecting) DeleteScript(ctx context.Context, req *connect.Request[grpcviewv1.DeleteScriptRequest]) (*connect.Response[grpcviewv1.DeleteScriptResponse], error) {
	return call(c, ctx, remote.DeleteScript, req)
}

func (c *reconnecting) ServerInfo(ctx context.Context, req *connect.Request[grpcviewv1.ServerInfoRequest]) (*connect.Response[grpcviewv1.ServerInfoResponse], error) {
	return read(c, ctx, remote.ServerInfo, req)
}

func (c *reconnecting) Shutdown(ctx context.Context, req *connect.Request[grpcviewv1.ShutdownRequest]) (*connect.Response[grpcviewv1.ShutdownResponse], error) {
	return call(c, ctx, remote.Shutdown, req)
}
