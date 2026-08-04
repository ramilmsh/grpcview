package workspace

// The ADDRESSED invoke: run the request saved at a display-name path, resolved server-side, for
// a caller with no editor buffer of its own.

import (
	"context"
	"strings"

	"connectrpc.com/connect"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
)

type savedInvoke struct {
	workspaceName string
	parent        []string // PARENT-folder display-name path, NOT including itemName
	itemName      string
	params        map[string]any
	target        *grpcviewv1.Server
	messages      []string
	recordHistory bool
}

type savedRun struct {
	spec     invokeSpec
	messages []string // every message, in send order; spec.body is just the first
}

func (w Workspace) resolveSavedRun(ctx context.Context, in savedInvoke) (savedRun, error) {
	coll, err := w.store.Open(ctx, in.workspaceName)
	if err != nil {
		return savedRun{}, err
	}
	saved, err := coll.ResolveRequest(ctx, in.parent, in.itemName)
	if err != nil {
		return savedRun{}, toConnectError(err)
	}

	messages := in.messages
	if len(messages) == 0 {
		messages = []string{saved.GetDraftBody()}
	}
	messages = normalizeBodies(messages)

	target := saved.GetTarget()
	if in.target != nil {
		target = in.target
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
		},
		messages: messages,
	}, nil
}

// normalizeBodies replaces every blank message with emptyBody: an empty source would not parse.
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

// InvokeSaved runs the request saved at path/item_name with its own stored body, metadata
// script, middleware and target, under Invoke's error policy.
func (w Workspace) InvokeSaved(ctx context.Context, request *connect.Request[grpcviewv1.InvokeSavedRequest]) (*connect.Response[grpcviewv1.InvokeSavedResponse], error) {
	msg := request.Msg
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

// InvokeSavedStreaming adapts the Connect server-streaming handler onto invokeSavedStream.
func (w Workspace) InvokeSavedStreaming(ctx context.Context, request *connect.Request[grpcviewv1.InvokeSavedStreamRequest], stream *connect.ServerStream[grpcviewv1.InvokeStreamingResponse]) error {
	return w.invokeSavedStream(ctx, request.Msg, stream.Send)
}

// InvokeSavedStream is the send-func form of InvokeSavedStreaming, for an in-process caller:
// connect exposes no way to build a *connect.ServerStream outside a served request.
func (w Workspace) InvokeSavedStream(ctx context.Context, msg *grpcviewv1.InvokeSavedStreamRequest, send func(*grpcviewv1.InvokeStreamingResponse) error) error {
	return w.invokeSavedStream(ctx, msg, send)
}

// InvokeStream is the send-func form of InvokeStreaming, for the same in-process reason.
func (w Workspace) InvokeStream(ctx context.Context, msg *grpcviewv1.InvokeStreamRequest, send func(*grpcviewv1.InvokeStreamingResponse) error) error {
	spec := specFrom(msg.GetSpec())
	spec.recordHistory = true
	return w.streamInvoke(ctx, spec, msg.GetMessages(), send)
}

func (w Workspace) invokeSavedStream(ctx context.Context, msg *grpcviewv1.InvokeSavedStreamRequest, send func(*grpcviewv1.InvokeStreamingResponse) error) error {
	run, err := w.resolveSavedRun(ctx, savedInvokeFrom(msg.GetSpec()))
	if err != nil {
		return err
	}
	return w.streamInvoke(ctx, run.spec, run.messages, send)
}

// savedInvokeFrom is the one place the wire SavedInvokeSpec becomes the internal one, shared by
// both saved RPCs. savedInvoke survives as its own type because gv.invoke builds one directly
// and carries params as a map, not a Struct.
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
