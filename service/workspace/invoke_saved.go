package workspace

import (
	"context"
	"errors"
	"strings"

	"connectrpc.com/connect"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
)

type savedInvoke struct {
	workspaceName string
	parent        []string
	itemName      string
	params        map[string]any
	target        *grpcviewv1.Server
	messages      []string
	recordHistory bool
}

type savedRun struct {
	spec     invokeSpec
	messages []string
}

// The fields live inside `spec`, and a caller that passes them flat sends none of them: without
// this the empty collection name reaches the store and comes back as "collection not found",
// which sends the caller looking at the collection rather than at the request.
func checkSavedSpec(spec *grpcviewv1.SavedInvokeSpec) error {
	if spec == nil {
		return connect.NewError(connect.CodeInvalidArgument,
			errors.New("spec is required: collection, path, item_name and params all nest inside it"))
	}
	if spec.GetCollection() == "" {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("spec.collection is required"))
	}
	if spec.GetItemName() == "" {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("spec.item_name is required"))
	}
	return nil
}

func (w Workspace) resolveSavedRun(ctx context.Context, in savedInvoke) (savedRun, error) {
	coll, err := w.store.Open(ctx, in.workspaceName)
	if err != nil {
		return savedRun{}, err
	}
	saved, bodyFile, metadataFile, err := coll.ResolveRequestFiles(ctx, in.parent, in.itemName)
	if err != nil {
		return savedRun{}, toConnectError(err)
	}

	// bodyFile is only attributed when the body actually came from disk: a caller that
	// supplies in.messages is evaluating its own bytes, not body.ts, and must not have an
	// error blamed on a file that was never read.
	fromDisk := len(in.messages) == 0

	messages := in.messages
	if fromDisk {
		messages = []string{saved.GetDraftBody()}
	}
	messages = normalizeBodies(messages)

	target := saved.GetTarget()
	if in.target != nil {
		target = in.target
	}

	if !fromDisk {
		bodyFile = ""
	}

	return savedRun{
		spec: invokeSpec{
			workspaceName:  in.workspaceName,
			path:           in.parent,
			itemName:       in.itemName,
			service:        saved.GetService(),
			method:         saved.GetMethod(),
			target:         target,
			body:           messages[0],
			metadataScript: saved.GetDraftMetadataScript(),
			params:         in.params,
			recordHistory:  in.recordHistory,
			bodyFile:       bodyFile,
			metadataFile:   metadataFile,
		},
		messages: messages,
	}, nil
}

func normalizeBodies(messages []string) []string {
	out := make([]string, len(messages))
	for i, m := range messages {
		if strings.TrimSpace(m) == "" {
			m = emptyBody
		}
		out[i] = m
	}
	return out
}

func (w Workspace) InvokeSaved(ctx context.Context, request *connect.Request[grpcviewv1.InvokeSavedRequest]) (*connect.Response[grpcviewv1.InvokeSavedResponse], error) {
	msg := request.Msg
	if err := checkSavedSpec(msg.GetSpec()); err != nil {
		return nil, err
	}
	run, err := w.resolveSavedRun(ctx, savedInvokeFrom(msg.GetSpec()))
	if err != nil {
		return nil, err
	}
	if msg.GetDryRun() {
		resolved, err := w.dryRun(ctx, run)
		if err != nil {
			return nil, err
		}
		return connect.NewResponse(&grpcviewv1.InvokeSavedResponse{Resolved: resolved}), nil
	}
	out, err := w.invokeUnary(ctx, run.spec)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&grpcviewv1.InvokeSavedResponse{Response: out}), nil
}

func (w Workspace) InvokeSavedStreaming(ctx context.Context, request *connect.Request[grpcviewv1.InvokeSavedStreamRequest], stream *connect.ServerStream[grpcviewv1.InvokeStreamingResponse]) error {
	return w.invokeSavedStream(ctx, request.Msg, stream.Send)
}

func (w Workspace) InvokeSavedStream(ctx context.Context, msg *grpcviewv1.InvokeSavedStreamRequest, send func(*grpcviewv1.InvokeStreamingResponse) error) error {
	return w.invokeSavedStream(ctx, msg, send)
}

func (w Workspace) InvokeStream(ctx context.Context, msg *grpcviewv1.InvokeStreamRequest, send func(*grpcviewv1.InvokeStreamingResponse) error) error {
	spec := specFrom(msg.GetSpec())
	spec.recordHistory = true
	return w.streamInvoke(ctx, spec, msg.GetMessages(), send)
}

func (w Workspace) invokeSavedStream(ctx context.Context, msg *grpcviewv1.InvokeSavedStreamRequest, send func(*grpcviewv1.InvokeStreamingResponse) error) error {
	if err := checkSavedSpec(msg.GetSpec()); err != nil {
		return err
	}
	run, err := w.resolveSavedRun(ctx, savedInvokeFrom(msg.GetSpec()))
	if err != nil {
		return err
	}
	return w.streamInvoke(ctx, run.spec, run.messages, send)
}

func savedInvokeFrom(spec *grpcviewv1.SavedInvokeSpec) savedInvoke {
	recordHistory := true
	if spec != nil && spec.RecordHistory != nil {
		recordHistory = spec.GetRecordHistory()
	}
	return savedInvoke{
		workspaceName: spec.GetCollection(),
		parent:        spec.GetPath(),
		itemName:      spec.GetItemName(),
		params:        spec.GetParams().AsMap(),
		target:        spec.GetTarget(),
		messages:      spec.GetMessages(),
		recordHistory: recordHistory,
	}
}

func (w Workspace) dryRun(ctx context.Context, run savedRun) (*grpcviewv1.ResolvedRequest, error) {
	messages, md, err := w.resolvePreSend(ctx, run.spec, run.messages)
	if err != nil {
		return nil, err
	}
	target, err := w.resolveTarget(ctx, run.spec.target, run.spec.workspaceName, run.spec.service)
	if err != nil {
		return nil, err
	}
	return &grpcviewv1.ResolvedRequest{
		Service:  run.spec.service,
		Method:   run.spec.method,
		Target:   target,
		Messages: messages,
		Metadata: md,
	}, nil
}
