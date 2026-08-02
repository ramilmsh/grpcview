package workspace

// invoke_saved.go — the ADDRESSED invoke: "run the request saved at this path, with these
// params" (cli-generator-exploration.md C1a). The public Invoke/InvokeStreaming RPCs carry the
// caller's own body and metadata script — the UI's live editor buffers — so a caller with no
// editor (a CLI, an MCP tool, a script) would have to read the whole workspace, walk the tree
// and echo the saved draft back to invoke it. InvokeSaved/InvokeSavedStreaming instead take a
// display-name path and resolve everything server-side.
//
// It is not new machinery: Collection.ResolveRequest → invokeUnary is exactly what gv.invoke
// already does (gvinvoke.go). resolveSavedRun is that middle, factored out, so the script
// re-entry and the two RPCs cannot drift — the ONE place a saved request becomes an invokeSpec.

import (
	"context"
	"errors"
	"strings"

	"connectrpc.com/connect"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
)

// savedInvoke addresses a saved request and carries the per-run overrides its caller may apply.
// workspaceName/parent/itemName are the address (parent is the PARENT-folder display-name path,
// NOT including itemName — the same split splitInvokePath produces); params backs
// gv.request.params for every script the run evaluates; target overrides the saved target when
// non-nil; messages override the saved body when non-empty; recordHistory gates the history
// append (false for gv.invoke's fan-out — D6 — true by default for an addressed RPC run — D7).
type savedInvoke struct {
	workspaceName string
	parent        []string
	itemName      string
	params        map[string]any
	target        *grpcviewv1.Server
	messages      []string
	recordHistory bool
}

// savedRun is a saved request resolved and ready to run: the invokeSpec invokeUnary takes, plus
// the FULL ordered message list. The two are not redundant — spec.body is the first message
// (all a unary target sends), while messages carries every one of them, which the streaming
// form composes up-front for a client-streaming or bidi target and a dry run reports in send
// order.
type savedRun struct {
	spec     invokeSpec
	messages []string
}

// resolveSavedRun resolves the saved request in addresses and returns what it takes to run it.
// This is the shared middle of gv.invoke and both InvokeSaved RPCs: it opens the collection,
// resolves the request by (parent, item name), applies the per-run target/messages overrides,
// and builds the invokeSpec. It deliberately does NOT invoke — the streaming form needs an
// InvokeStreamRequest instead of a unary send, and a dry run needs the spec without any send at
// all.
//
// A blank body (a request whose draft body was never authored, or an explicit empty override)
// becomes emptyBody here rather than reaching the engine, where an empty expression would not
// even parse.
func (w Workspace) resolveSavedRun(ctx context.Context, in savedInvoke) (savedRun, error) {
	coll, err := w.store.Open(ctx, in.workspaceName)
	if err != nil {
		return savedRun{}, err
	}
	saved, err := coll.ResolveRequest(ctx, in.parent, in.itemName)
	if err != nil {
		// ResolveRequest speaks the store's transport-agnostic sentinels, so map them here —
		// the ONE place both callers resolve an address — into the codes a client can act on:
		// no such item is NotFound, a path naming a folder is FailedPrecondition. The CLI's
		// exit 2 covers both, but the code is what tells the two apart. gv.invoke wraps this
		// with its path-naming text, which toConnectError survives (it matches with errors.Is).
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

// normalizeBodies replaces every blank message with emptyBody, so a saved request with no
// authored body (or a caller that sent an empty override) evaluates to the empty object instead
// of handing the engine a source that cannot parse.
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

// InvokeSaved runs the request saved at path/item_name using its own stored body, metadata
// script, attached middleware and target, and returns the same Request.Response shape Invoke
// does. record_history defaults to TRUE (D7): an addressed run is a real user-initiated one,
// unlike gv.invoke's fan-out, and an explicit false opts out.
//
// The error policy is Invoke's, unchanged, and the CLI's exit codes depend on it (D9): a gRPC
// status the target returned comes back as a RESPONSE with that status inside, never as a
// Connect error, while a failure grpcview itself can't get past (unknown path, a folder, no
// target, a body that won't evaluate, a streaming method) is a Connect error carrying no
// response.
func (w Workspace) InvokeSaved(ctx context.Context, request *connect.Request[grpcviewv1.InvokeSavedRequest]) (*connect.Response[grpcviewv1.InvokeSavedResponse], error) {
	msg := request.Msg
	run, err := w.resolveSavedRun(ctx, savedInvokeFrom(msg))
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

// InvokeSavedStreaming adapts the Connect server-streaming handler onto invokeSavedStream, the
// same thin seam InvokeStreaming uses: the logic lives behind a plain send func so it is
// testable without a real connect.ServerStream.
func (w Workspace) InvokeSavedStreaming(ctx context.Context, request *connect.Request[grpcviewv1.InvokeSavedRequest], stream *connect.ServerStream[grpcviewv1.InvokeStreamResponse]) error {
	return w.invokeSavedStream(ctx, request.Msg, stream.Send)
}

// invokeSavedStream resolves the saved request and runs it through streamInvoke, which maps the
// target method's real streaming kind onto the one server-streaming frame protocol. Every
// message is composed up-front for a client-streaming or bidi target — the existing convention
// (D13), not a new one — so the saved body (or the per-run messages override) is the whole
// client side of the call.
//
// dry_run is rejected here rather than ignored: the streaming response has no frame that could
// carry a resolved request, and the unary form dry-runs a saved request of ANY streaming kind
// without dialing, so nothing is lost.
func (w Workspace) invokeSavedStream(ctx context.Context, msg *grpcviewv1.InvokeSavedRequest, send func(*grpcviewv1.InvokeStreamResponse) error) error {
	if msg.GetDryRun() {
		return connect.NewError(connect.CodeInvalidArgument,
			errors.New("dry_run is not supported by the streaming invoke: dry-run a saved request of any streaming kind through the unary one"))
	}
	run, err := w.resolveSavedRun(ctx, savedInvokeFrom(msg))
	if err != nil {
		return err
	}
	return w.streamInvoke(ctx, &grpcviewv1.InvokeStreamRequest{
		WorkspaceName:  run.spec.workspaceName,
		Path:           run.spec.path,
		ItemName:       run.spec.itemName,
		Service:        run.spec.service,
		Method:         run.spec.method,
		Messages:       run.messages,
		Target:         run.spec.target,
		MetadataScript: run.spec.metadataScript,
	}, send, run.spec.params, run.spec.recordHistory)
}

// savedInvokeFrom reads the wire request into the address + overrides resolveSavedRun takes,
// the single place record_history's default is applied: unset means TRUE (D7), so only an
// explicit false suppresses the history append.
func savedInvokeFrom(msg *grpcviewv1.InvokeSavedRequest) savedInvoke {
	recordHistory := true
	if msg.RecordHistory != nil {
		recordHistory = msg.GetRecordHistory()
	}
	return savedInvoke{
		workspaceName: msg.GetWorkspaceName(),
		parent:        msg.GetPath(),
		itemName:      msg.GetItemName(),
		params:        msg.GetParams().AsMap(),
		target:        msg.GetTarget(),
		messages:      msg.GetMessages(),
		recordHistory: recordHistory,
	}
}

// dryRun reports what the call WOULD have sent and sends nothing. It runs the shared pre-send
// sequence (resolvePreSend: bodies evaluated, metadata computed, middleware applied) and stops
// there — no dial, no reflection, so a dry run works with the target down and costs one
// evaluation. The target is resolved the same way the real call resolves it (the request's
// explicit target, else the source its service came from), which reads the collection but
// touches no network.
//
// Only the server can produce this: the bodies are TypeScript evaluated in QuickJS, and the
// middleware chain rewrites them afterwards.
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
