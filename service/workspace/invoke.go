package workspace

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/jhump/protoreflect/desc"
	"github.com/jhump/protoreflect/dynamic"
	"github.com/jhump/protoreflect/dynamic/grpcdynamic"
	"github.com/jhump/protoreflect/grpcreflect"

	"google.golang.org/protobuf/runtime/protoiface"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/scripting"
	"codeberg.org/ramilmsh/grpcview/service/store"
)

// historyLimit caps how many past runs are retained per request; older entries
// are dropped (and the drop logged) when a new run pushes the list over it.
const historyLimit = 50

// emptyTSBody is the canonical empty TypeScript request body. Every body is evaluated as a TS
// module on invoke now (resolveInvokeBody), so an invoke that carries no body — or a stream
// with no messages — defaults to this module (it returns an empty object) rather than bare
// "{}" JSON, which is not a module that returns an object.
const emptyTSBody = "export default () => ({})"

// MaxFolderMetadataDepth bounds the ancestor-folder-metadata chain foldAncestorMetadata walks
// (gv-features-plan.md Feature 1, D5's folder-chain cap): a path deeper than this is rejected as
// a Connect FailedPrecondition rather than paying for an unbounded number of fresh QuickJS
// instantiations. It is independent of gv.invoke's own recursion-depth cap (Feature 3).
const MaxFolderMetadataDepth = 16

// invokeSpec carries everything invokeUnary needs to run one unary RPC — the union of what its
// two callers build: the public Invoke RPC (below; params=nil, recordHistory=true) and
// gv.invoke's re-entry (scriptInvoker, gvinvoke.go; params=the caller's kwargs,
// recordHistory=false — gv-features-plan.md Feature 3 D6). Field names mirror InvokeRequest's;
// workspaceName/path/itemName/service/method/target address WHERE and WHAT to call and (via
// path+itemName) where to attach history/middleware; body/metadataScript/metadata are the
// (possibly unsaved) editor-shaped inputs resolveInvokeBody/resolveInvokeMetadata evaluate;
// params backs gv.request.params for this run's own body/metadata/middleware — and, via
// foldAncestorMetadata, its ancestor folders' too (D4) — and is nil outside a gv.invoke
// re-entry; recordHistory gates whether this run appends to its target's history.
type invokeSpec struct {
	workspaceName  string
	path           []string // parent-folder display-name path (NOT including itemName)
	itemName       string   // the saved request's display name; "" for an ad-hoc invoke
	service        string
	method         string
	target         *grpcviewv1.Server
	body           string // raw TS body source; "" defaults to emptyTSBody
	metadataScript string
	metadata       *structpb.Struct // fallback metadata Struct used when metadataScript is empty
	params         map[string]any   // gv.invoke(path, params)'s kwargs
	recordHistory  bool
}

// invokeUnary runs one unary RPC end to end — resolve target → evaluate body → evaluate
// metadata → run middleware → dial → send → decode — the block factored out of the public
// Invoke RPC (gv-features-plan.md Feature 3 §"Approach") so it is shared with gv.invoke's
// re-entry (scriptInvoker, gvinvoke.go). Both callers therefore reject a streaming target with
// the SAME check (it lives here), and a nested gv.invoke from EITHER caller's
// body/metadata/middleware evaluation re-enters through this exact function: ctx is augmented
// with this workspace's gv.invoke Invoker AT THE TOP, before any script runs, so every
// downstream RunRequestBody/RunMiddleware call (body, metadata, ancestor folder metadata,
// middleware) carries it.
//
// A gRPC-level failure of the *invoked* call is not a Go error here — it comes back as (out,
// nil) with the failure recorded in out.Status, so the public Invoke can render it in the
// response and gv.invoke can resolve its promise with ok:false (fetch-style, plan §"Return
// shape"). Only a failure grpcview itself can't get past — no target, unreachable schema, a
// streaming target, a body/metadata that won't evaluate — is a non-nil error, which the public
// Invoke surfaces as a Connect error and scriptInvoker turns into a rejected gv.invoke promise.
func (w Workspace) invokeUnary(ctx context.Context, spec invokeSpec) (*grpcviewv1.Request_Response, error) {
	ctx = scripting.WithInvoker(ctx, w.scriptInvoker(spec.workspaceName))

	conn, methodDesc, cleanup, err := w.resolveMethod(ctx, spec.target, spec.workspaceName, spec.service, spec.method)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	if methodDesc.IsClientStreaming() || methodDesc.IsServerStreaming() {
		return nil, connect.NewError(connect.CodeUnimplemented, fmt.Errorf("streaming methods are not supported yet: %s", methodDesc.GetFullyQualifiedName()))
	}

	body := strings.TrimSpace(spec.body)
	if body == "" {
		body = emptyTSBody
	}
	// Evaluate the TypeScript body → JSON: the body is always a
	// canonical TS module (the frontend migrates any legacy body before sending), so it is run
	// through the engine and its returned object becomes the JSON message. The same shared
	// pre-send step streamInvoke runs.
	evaluatedBodies, err := w.resolveInvokeBody(ctx, spec.workspaceName, []string{body}, spec.params)
	if err != nil {
		return nil, err
	}
	// Compute the outgoing metadata Struct: when metadata_script is set it is a TypeScript
	// module evaluated exactly like the TS body (generators composable as ambient globals);
	// otherwise the request's metadata Struct is used as-is (today's path). The same shared
	// pre-send step streamInvoke runs. spec.params backs gv.request.params for this eval (and,
	// when the script mentions inherit(), every ancestor folder script's eval too — D4).
	outgoingMD, err := w.resolveInvokeMetadata(ctx, spec.workspaceName, spec.path, spec.metadataScript, spec.metadata, spec.params)
	if err != nil {
		return nil, err
	}
	// Run the saved request's attached middleware chain (§S3) on the evaluated outgoing
	// request — middleware rewrites the body/metadata in order. The same shared pre-send step
	// streamInvoke runs; a no-op when nothing is attached, and a per-middleware failure is a
	// Connect error grpcview can't get past, like a bad body.
	resolvedBodies, resolvedMD, err := w.applyRequestMiddleware(ctx, spec.workspaceName, spec.path, spec.itemName, spec.service, spec.target, evaluatedBodies, outgoingMD, spec.params)
	if err != nil {
		return nil, err
	}
	body = resolvedBodies[0]

	reqMsg := dynamic.NewMessage(methodDesc.GetInputType())
	if err := reqMsg.UnmarshalJSON([]byte(body)); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid request body for %s: %w", methodDesc.GetInputType().GetFullyQualifiedName(), err))
	}

	reqMD := structToMetadata(resolvedMD)
	callCtx := metadata.NewOutgoingContext(ctx, reqMD)

	var header, trailer metadata.MD
	stub := grpcdynamic.NewStub(conn)

	start := time.Now()
	respMsg, invokeErr := stub.InvokeRpc(callCtx, methodDesc, reqMsg, grpc.Header(&header), grpc.Trailer(&trailer))
	latency := time.Since(start)

	out := &grpcviewv1.Request_Response{
		RequestMetadata:  metadataToStruct(reqMD),
		ResponseMetadata: metadataToStruct(mergeMD(header, trailer)),
		Latency:          durationpb.New(latency),
		Timestamp:        timestamppb.Now(),
	}

	if invokeErr != nil {
		st := status.Convert(invokeErr)
		out.Status = &grpcviewv1.Status{
			Code:    int32(st.Code()),
			Message: st.Message(),
			Details: st.Proto().GetDetails(),
		}
		if spec.recordHistory {
			w.recordHistory(ctx, spec.workspaceName, spec.path, spec.itemName, spec.service, spec.method, spec.body, out)
		}
		return out, nil
	}

	out.Status = &grpcviewv1.Status{Code: int32(codeOK)}
	dm, err := dynamic.AsDynamicMessage(respMsg)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("read response message: %w", err))
	}
	jsonBytes, err := dm.MarshalJSON()
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("marshal response to json: %w", err))
	}
	out.Response = jsonBytes

	if spec.recordHistory {
		w.recordHistory(ctx, spec.workspaceName, spec.path, spec.itemName, spec.service, spec.method, spec.body, out)
	}
	return out, nil
}

// Invoke executes a single unary RPC against the target server and returns the
// result. A gRPC-level failure of the *invoked* call (e.g. the target returns
// NotFound) is not an error of this RPC: it is reported in the response's
// Status so the UI can render it. Only failures grpcview itself can't get past
// — no target, unreachable schema, a body that doesn't fit the request type —
// surface as Connect errors. All the actual work is invokeUnary; Invoke only builds its spec
// from the wire request (params=nil — gv.invoke is the only caller that ever sets it —
// recordHistory=true) and wraps the result.
func (w Workspace) Invoke(ctx context.Context, request *connect.Request[grpcviewv1.InvokeRequest]) (*connect.Response[grpcviewv1.InvokeResponse], error) {
	msg := request.Msg
	out, err := w.invokeUnary(ctx, invokeSpec{
		workspaceName:  msg.GetWorkspaceName(),
		path:           msg.GetPath(),
		itemName:       msg.GetItemName(),
		service:        msg.GetService(),
		method:         msg.GetMethod(),
		target:         msg.GetTarget(),
		body:           msg.GetBody(),
		metadataScript: msg.GetMetadataScript(),
		metadata:       msg.GetMetadata(),
		recordHistory:  true,
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&grpcviewv1.InvokeResponse{Response: out}), nil
}

// InvokeStreaming adapts the Connect server-streaming handler onto streamInvoke.
// It is a thin seam: all logic lives in streamInvoke, which writes frames through
// a plain send func so it can be unit-tested without a real connect.ServerStream.
func (w Workspace) InvokeStreaming(ctx context.Context, request *connect.Request[grpcviewv1.InvokeStreamRequest], stream *connect.ServerStream[grpcviewv1.InvokeStreamResponse]) error {
	return w.streamInvoke(ctx, request.Msg, stream.Send)
}

// streamInvoke executes an RPC of any kind against the target and streams the
// responses back through send. It maps the target method's real streaming kind
// (unary / server / client / bidi over full gRPC) onto the single
// server-streaming shape the browser transport allows: every client message is
// supplied up-front in msg.Messages and, for client-streaming and bidi targets,
// composed and sent before any response is read — there is no live interleave, a
// deliberate v1 limit of the browser transport (connect-web cannot stream a
// request body).
//
// Frame protocol: zero or more `message` frames carry response payloads as JSON
// as they arrive, then exactly one terminal `result` frame carries the final
// gRPC status, request/response metadata, latency and timestamp. Its `response`
// bytes stay empty — the payloads went out as message frames — but it is
// otherwise the same Request.Response shape unary Invoke returns.
//
// Error policy mirrors unary Invoke. Pre-flight failures grpcview itself can't
// get past (no target, unreachable schema, a request body that doesn't parse)
// return a Connect error and send nothing. A gRPC-status failure of the invoked
// call — even one that surfaces partway through a stream after some message
// frames were already sent — is NOT a Connect error: it is reported in the
// terminal frame's status and the handler returns nil. If send itself fails
// (client aborted / ctx cancelled) we stop and return that error without
// panicking; ctx cancellation propagates into the target call and surfaces as
// the terminal frame's (Canceled) status or a failed send.
func (w Workspace) streamInvoke(ctx context.Context, msg *grpcviewv1.InvokeStreamRequest, send func(*grpcviewv1.InvokeStreamResponse) error) error {
	conn, methodDesc, cleanup, err := w.resolveMethod(ctx, msg.GetTarget(), msg.GetWorkspaceName(), msg.GetService(), msg.GetMethod())
	if err != nil {
		return err
	}
	defer cleanup()

	// Build every client request message up-front. An empty list defaults to a
	// single "{}", mirroring unary Invoke's empty-body handling. A body that
	// doesn't fit the input type is a pre-flight failure: a Connect error with no
	// frames sent.
	bodies := msg.GetMessages()
	if len(bodies) == 0 {
		bodies = []string{emptyTSBody}
	}
	// Evaluate the TypeScript bodies → JSON: every message is a canonical TS module
	// (the frontend migrates before sending), so each is run through the engine and its
	// returned object becomes the JSON message. The same shared pre-send step unary Invoke
	// runs; a failure is a pre-flight Connect error that sends no frames.
	bodies, err = w.resolveInvokeBody(ctx, msg.GetWorkspaceName(), bodies, nil)
	if err != nil {
		return err
	}
	// Compute the outgoing metadata Struct (metadata_script evaluated like the TS body, else
	// the request's metadata Struct as-is), the same shared pre-send step unary Invoke runs.
	// A failure is a pre-flight Connect error that sends no frames. params is nil for now —
	// gv.invoke() (Feature 3) is the only planned caller that will ever pass non-nil params here.
	outgoingMD, err := w.resolveInvokeMetadata(ctx, msg.GetWorkspaceName(), msg.GetPath(), msg.GetMetadataScript(), msg.GetMetadata(), nil)
	if err != nil {
		return err
	}
	// Run the saved request's attached middleware chain (§S3) on the evaluated outgoing
	// request, the same shared pre-send step unary Invoke runs. A per-middleware failure is a
	// pre-flight Connect error that sends no frames.
	bodies, resolvedMD, err := w.applyRequestMiddleware(ctx, msg.GetWorkspaceName(), msg.GetPath(), msg.GetItemName(), msg.GetService(), msg.GetTarget(), bodies, outgoingMD, nil)
	if err != nil {
		return err
	}
	reqMsgs := make([]*dynamic.Message, len(bodies))
	for i, body := range bodies {
		body = strings.TrimSpace(body)
		if body == "" {
			body = "{}"
		}
		m := dynamic.NewMessage(methodDesc.GetInputType())
		if err := m.UnmarshalJSON([]byte(body)); err != nil {
			return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid request body [%d] for %s: %w", i, methodDesc.GetInputType().GetFullyQualifiedName(), err))
		}
		reqMsgs[i] = m
	}

	reqMD := structToMetadata(resolvedMD)
	callCtx := metadata.NewOutgoingContext(ctx, reqMD)
	stub := grpcdynamic.NewStub(conn)

	// sendMessage marshals a received response payload to JSON and emits it as a
	// message frame. A marshal failure is a grpcview-internal error (CodeInternal);
	// a send failure means the client aborted — both propagate out of streamInvoke.
	sendMessage := func(resp protoiface.MessageV1) error {
		dm, err := dynamic.AsDynamicMessage(resp)
		if err != nil {
			return connect.NewError(connect.CodeInternal, fmt.Errorf("read response message: %w", err))
		}
		jsonBytes, err := dm.MarshalJSON()
		if err != nil {
			return connect.NewError(connect.CodeInternal, fmt.Errorf("marshal response to json: %w", err))
		}
		return send(&grpcviewv1.InvokeStreamResponse{Event: &grpcviewv1.InvokeStreamResponse_Message{Message: jsonBytes}})
	}

	var (
		header, trailer metadata.MD
		invokeErr       error
	)
	client := methodDesc.IsClientStreaming()
	server := methodDesc.IsServerStreaming()
	start := time.Now()

	switch {
	case !client && !server:
		// Unary: a single request, a single response.
		resp, err := stub.InvokeRpc(callCtx, methodDesc, reqMsgs[0], grpc.Header(&header), grpc.Trailer(&trailer))
		invokeErr = err
		if err == nil {
			if serr := sendMessage(resp); serr != nil {
				return serr
			}
		}

	case !client && server:
		// Server-streaming: a single request, a stream of responses.
		ss, err := stub.InvokeRpcServerStream(callCtx, methodDesc, reqMsgs[0])
		invokeErr = err
		if err == nil {
			for {
				resp, rerr := ss.RecvMsg()
				if errors.Is(rerr, io.EOF) {
					break
				}
				if rerr != nil {
					invokeErr = rerr
					break
				}
				if serr := sendMessage(resp); serr != nil {
					return serr
				}
			}
			// Read metadata after the stream completes: Header blocks until the
			// server sends headers, Trailer is valid once RecvMsg has returned an
			// error (EOF included).
			header, _ = ss.Header()
			trailer = ss.Trailer()
		}

	case client && !server:
		// Client-streaming: every client message is sent up-front, then a single
		// response is received.
		cs, err := stub.InvokeRpcClientStream(callCtx, methodDesc)
		invokeErr = err
		if err == nil {
			for _, m := range reqMsgs {
				if invokeErr = cs.SendMsg(m); invokeErr != nil {
					break
				}
			}
			if invokeErr == nil {
				resp, rerr := cs.CloseAndReceive()
				invokeErr = rerr
				if rerr == nil {
					if serr := sendMessage(resp); serr != nil {
						return serr
					}
				}
			}
			header, _ = cs.Header()
			trailer = cs.Trailer()
		}

	default:
		// Bidi: every client message is composed and sent up-front, the send side
		// is closed, then the whole response stream is drained. There is no live
		// interleave of sends and receives — a deliberate v1 limit of the browser
		// transport — so the call is modeled as send-all-then-receive-all.
		bs, err := stub.InvokeRpcBidiStream(callCtx, methodDesc)
		invokeErr = err
		if err == nil {
			for _, m := range reqMsgs {
				if invokeErr = bs.SendMsg(m); invokeErr != nil {
					break
				}
			}
			if invokeErr == nil {
				invokeErr = bs.CloseSend()
			}
			if invokeErr == nil {
				for {
					resp, rerr := bs.RecvMsg()
					if errors.Is(rerr, io.EOF) {
						break
					}
					if rerr != nil {
						invokeErr = rerr
						break
					}
					if serr := sendMessage(resp); serr != nil {
						return serr
					}
				}
			}
			header, _ = bs.Header()
			trailer = bs.Trailer()
		}
	}

	// Terminal frame: the single completion point for both success and a
	// gRPC-status failure of the invoked call. The failure is reported in Status
	// here (NOT as a Connect error), mirroring unary Invoke — even when some
	// message frames already went out before it. Response bytes stay empty; the
	// payloads were the message frames.
	out := &grpcviewv1.Request_Response{
		RequestMetadata:  metadataToStruct(reqMD),
		ResponseMetadata: metadataToStruct(mergeMD(header, trailer)),
		Latency:          durationpb.New(time.Since(start)),
		Timestamp:        timestamppb.Now(),
	}
	if invokeErr != nil {
		st := status.Convert(invokeErr)
		out.Status = &grpcviewv1.Status{
			Code:    int32(st.Code()),
			Message: st.Message(),
			Details: st.Proto().GetDetails(),
		}
	} else {
		out.Status = &grpcviewv1.Status{Code: int32(codeOK)}
	}

	if err := send(&grpcviewv1.InvokeStreamResponse{Event: &grpcviewv1.InvokeStreamResponse_Result{Result: out}}); err != nil {
		return err
	}

	// Record only after the terminal frame reaches the client (a send failure is a
	// client abort — skipped, like the early returns above). Streaming history keeps
	// the terminal status/metadata/latency/timestamp; the streamed payloads are not
	// stored (out.Response is empty for streams) and only the first request message
	// is captured — a documented limitation, see recordHistory.
	var body string
	if bodies := msg.GetMessages(); len(bodies) > 0 {
		body = bodies[0]
	}
	w.recordHistory(ctx, msg.GetWorkspaceName(), msg.GetPath(), msg.GetItemName(), msg.GetService(), msg.GetMethod(), body, out)
	return nil
}

// resolveInvokeBody is the shared pre-send body-evaluation step for unary Invoke (one body)
// and streaming InvokeStreaming (many). Every body is a
// canonical TS/JS module (the frontend migrates any legacy body/JSON to this form before
// sending), so each is run through the engine — uncached, so Math.random()/now() vary per
// invoke, and fully sandboxed (empty Grant, no vars/secrets/env, exactly like the middleware
// runs) — and its returned JSON object REPLACES the body, which then flows through the existing
// middleware/UnmarshalJSON pipeline unchanged. A body may also COMPOSE the
// workspace's saved generators by calling them as ambient globals; the workspace generators are
// loaded once and each body folds in the ones it transitively reaches (see transitiveGenerators /
// engine.RunRequestBody). params backs gv.request.params for every body's eval (gv-features-plan.md
// Feature 3) — nil outside a gv.invoke re-entry (the public Invoke's own top-level body, and every
// streamInvoke call, always pass nil: streaming is not a gv.invoke target in v1). A throw,
// timeout, or a non-object return is a Connect FailedPrecondition, mirroring the middleware error
// policy (middlewareError).
func (w Workspace) resolveInvokeBody(ctx context.Context, workspaceName string, bodies []string, params map[string]any) ([]string, error) {
	if w.engine == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			errors.New("a TypeScript request body requires the scripting engine, which is not available"))
	}
	// Load the workspace's saved generators ONCE so a body can call them as ambient globals.
	// loadGenerators returns an empty map when the scripts collection does not exist, so a
	// workspace with no scripts still evaluates a plain (non-composing) body; a real store error
	// propagates (it is grpcview's own failure, not the user script's).
	allGens, err := w.loadGenerators(ctx, workspaceName)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(bodies))
	for i, body := range bodies {
		// The whole body is the generator source (its own `export default` fires the
		// entry-point convention with no positional args, per entry.go). transitiveGenerators
		// narrows allGens to just what this body reaches — the generators it calls plus, to a
		// fixpoint, the generators THOSE call — so composition folds in the body's whole
		// dependency subgraph while an unrelated generator's compile error can't break a body
		// that does not (transitively) call it; an empty subset makes RunRequestBody take the
		// plain generator path. Result.Value is the returned object as raw JSON — the
		// replacement body.
		res, err := w.engine.RunRequestBody(ctx, body, transitiveGenerators(body, allGens), scripting.Grant{}, scripting.Input{Params: params})
		if err != nil {
			return nil, bodyError(i, err.Error())
		}
		if !isJSONObject(res.Value) {
			return nil, bodyError(i, "expected the body to return an object")
		}
		out[i] = string(res.Value)
	}
	return out, nil
}

// calledNames returns the set of identifiers that appear as a CALL SITE in src — the name
// immediately followed by optional whitespace and an opening paren (mkid(), dbl (7)) — EXCLUDING
// a property/method access (x.format(), whose name is preceded by "."). This is the shared
// call-site detection behind transitiveGenerators: recognizing calls this way (not bare
// identifiers) is what makes FAILURE ISOLATION hold, so an object key ({ id: 7 }), a local
// variable, or a method call that merely shares a generator's name does NOT count as a call.
// Recognition stays textual, so the only residual over-approximation is a literal name( inside a
// string or comment; a generator used without a direct call (passed as a bare callback, e.g.
// arr.map(mkid)) is likewise not detected — both are acceptable for v1.
func calledNames(src string) map[string]struct{} {
	called := make(map[string]struct{})
	for _, loc := range genCallRe.FindAllStringSubmatchIndex(src, -1) {
		start, end := loc[2], loc[3] // the captured identifier's byte bounds
		if start > 0 && src[start-1] == '.' {
			continue // obj.name(...) — a property/method call, not a generator call
		}
		called[src[start:end]] = struct{}{}
	}
	return called
}

// transitiveGenerators returns every generator in all TRANSITIVELY REACHABLE from the call sites
// in source: it seeds from the generators source calls, then follows each included generator's
// OWN call sites to a fixpoint, so a body that calls `outer` where `outer` itself calls `inner`
// folds in BOTH (the engine binds every generator in the returned set as an ambient global, so a
// generator calling another only resolves when the callee is present). Only names that exist in
// all count as calls; anything else (a builtin, a local function) is ignored. The worklist tracks
// added names, so a cycle (a -> b -> a) terminates.
//
// It preserves FAILURE ISOLATION at the transitive frontier: a generator NOT reachable from the
// body's (transitive) call sites is never folded in, so an unrelated broken generator can't break
// an unrelated body. The semantic shift from the old direct-only scan: a generator the body
// TRANSITIVELY depends on, if broken, now correctly surfaces its compile error (it must, since
// the body genuinely needs it) — where before the missing indirect dependency produced a runtime
// "not defined". Dotted-named generators are excluded downstream (composeGeneratorPrelude skips a
// non-identifier name) — a documented v1 gap; when the returned set is empty RunRequestBody takes
// the plain (uncached) generator path, so a body that calls nothing behaves exactly as before.
func transitiveGenerators(source string, all map[string]string) map[string]string {
	out := make(map[string]string)
	var worklist []string
	for name := range calledNames(source) {
		worklist = append(worklist, name)
	}
	for len(worklist) > 0 {
		name := worklist[len(worklist)-1]
		worklist = worklist[:len(worklist)-1]
		if _, done := out[name]; done {
			continue // already folded in (guards against cycles)
		}
		src, ok := all[name]
		if !ok {
			continue // not a workspace generator: not a composition call
		}
		out[name] = src
		for dep := range calledNames(src) {
			if _, done := out[dep]; !done {
				worklist = append(worklist, dep)
			}
		}
	}
	return out
}

// genCallRe matches a call site: an identifier (captured) followed by optional whitespace and an
// opening paren. calledNames additionally rejects a match preceded by "." (a method
// call) by inspecting the byte before the identifier — the identifier is always the start of a
// maximal identifier run (the match is greedy and leftmost), so the preceding byte is never
// itself an identifier char and only "." needs excluding.
var genCallRe = regexp.MustCompile(`([A-Za-z_$][A-Za-z0-9_$]*)\s*\(`)

// isJSONObject reports whether raw is a JSON object ({…}) — the shape a request message must
// have. It only inspects the first non-space byte: the engine already guarantees raw is valid
// JSON (or empty for an undefined/non-serializable return), and the subsequent UnmarshalJSON
// validates it against the message type. An empty (undefined) return is not an object.
func isJSONObject(raw []byte) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) > 0 && raw[0] == '{'
}

// bodyError renders a TypeScript-body evaluation failure as a Connect FailedPrecondition
// naming the offending body by index (mirroring the streaming unmarshal's "[%d]"), matching
// Invoke's policy that a pre-send failure grpcview itself can't get past is a typed Connect
// error (mirrors middleware.go's middlewareError).
func bodyError(index int, detail string) error {
	return connect.NewError(connect.CodeFailedPrecondition,
		fmt.Errorf("cannot evaluate TypeScript request body [%d]: %s", index, detail))
}

// loadGenerators reads the workspace's committed scripts and returns a map from a generator's
// display name to its source, for a TS body/metadata module to compose as ambient globals
// (resolveInvokeBody / resolveInvokeMetadata). A collection that does not exist yet yields an
// empty map, so a body/metadata module that composes no generator still evaluates.
func (w Workspace) loadGenerators(ctx context.Context, workspaceName string) (map[string]string, error) {
	coll, err := w.store.Open(ctx, workspaceName)
	if err != nil {
		return nil, err
	}
	scripts, err := coll.Scripts(ctx)
	if errors.Is(err, store.ErrNotFound) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, toConnectError(err)
	}
	gens := make(map[string]string, len(scripts))
	for _, s := range scripts {
		if s.GetKind() == grpcviewv1.ScriptKind_SCRIPT_KIND_GENERATOR {
			gens[s.GetName()] = s.GetSource()
		}
	}
	return gens, nil
}

// resolveInvokeMetadata computes the outgoing metadata Struct for an invoke. When
// metadataScript is non-empty it is a TypeScript module (the metadata-wrapper's
// `export default (): Metadata => ({ ... })`) evaluated exactly like the TS request body
// — through the SAME engine path (RunRequestBody), uncached and fully sandboxed, with the
// workspace's saved generators composable as ambient globals — so a user can write
// `{ authorization: ["Bearer " + apiToken()], "x-request-id": [uuid()] }`. Its returned
// {[key]: string[]} object becomes a google.protobuf.Struct of {[key]: ListValue<string>},
// which then flows through structToMetadata (expanding string[] to repeated headers) unchanged.
//
// path is the invoked node's PARENT-folder display-name path (msg.GetPath(), NOT including its
// own item name) — gv-features-plan.md Feature 1's ancestor-folder-metadata seam. When
// metadataScript textually mentions an inherit(...) call (mentionsInherit), foldAncestorMetadata
// walks path's ancestor folder scripts and the result becomes gv.metadata.inherit()'s data for
// THIS script's own eval; otherwise the fold is skipped entirely and inherit() just returns {}
// (the efficiency gate — the fold cost is O(depth) fresh QuickJS instantiations). params backs
// gv.request.params for both the fold and this eval; every caller passes nil today — Feature 1
// only plumbs the parameter, a later gv.invoke() wiring (Feature 3) fills it in (see the plan's
// "Cross-feature interactions": "cleanly absorb Feature 1's already-present
// resolveInvokeMetadata(path, params) fold").
//
// When metadataScript is empty this is a pure no-op: the fallback Struct (the request's
// metadata field, i.e. today's path) is returned verbatim, so a request carrying a plain
// Struct behaves exactly as before. A throw/timeout or a non-object return is a Connect
// FailedPrecondition (mirroring the body's bodyError); a value that is not a string or
// string[] is a Connect InvalidArgument (mirroring the body's UnmarshalJSON type mismatch).
func (w Workspace) resolveInvokeMetadata(ctx context.Context, workspaceName string, path []string, metadataScript string, fallback *structpb.Struct, params map[string]any) (*structpb.Struct, error) {
	if strings.TrimSpace(metadataScript) == "" {
		return fallback, nil // no script: use the Struct metadata as-is (today's path)
	}
	if w.engine == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			errors.New("a TypeScript metadata script requires the scripting engine, which is not available"))
	}
	// Load the workspace's saved generators ONCE so the metadata module can call them as
	// ambient globals, exactly like a composing body; transitiveGenerators narrows the set to
	// what the module reaches — the generators it calls plus, to a fixpoint, the generators
	// those call (failure isolation at the transitive frontier) — and an empty subset takes the
	// plain path. The same allGens set is reused below for the ancestor-folder fold, so the
	// scripts collection is only read once per invoke.
	allGens, err := w.loadGenerators(ctx, workspaceName)
	if err != nil {
		return nil, err
	}
	// The fold runs ONLY when this script could actually observe its result — otherwise
	// InheritedMetadata stays nil and gv.metadata.inherit() just returns {} (buildGvPrelude /
	// orEmptyMetadata), so a request/folder that never inherits never pays for it.
	var inherited map[string][]string
	if mentionsInherit(metadataScript) {
		inherited, err = w.foldAncestorMetadata(ctx, workspaceName, path, params, allGens)
		if err != nil {
			return nil, err
		}
	}
	res, err := w.engine.RunRequestBody(ctx, metadataScript, transitiveGenerators(metadataScript, allGens), scripting.Grant{}, scripting.Input{
		Params:            params,
		InheritedMetadata: inherited,
	})
	if err != nil {
		return nil, metadataError(connect.CodeFailedPrecondition, err.Error())
	}
	if !isJSONObject(res.Value) {
		return nil, metadataError(connect.CodeFailedPrecondition, "expected the metadata to return an object")
	}
	lists, err := metadataListsFromJSON(res.Value)
	if err != nil {
		return nil, err
	}
	return structFromMetadataLists(lists), nil
}

// inheritCallRe matches an `inherit(` call site — mentionsInherit's efficiency gate. It is
// intentionally loose (it does not require the `gv.metadata.` receiver): a false positive (e.g.
// a local function that happens to be named inherit) only costs one unnecessary — but still
// correct — fold, whereas a false negative would silently skip real inheritance, which the gate
// must never risk.
var inheritCallRe = regexp.MustCompile(`\binherit\s*\(`)

// mentionsInherit reports whether src textually references an inherit(...) call — the
// efficiency gate guarding foldAncestorMetadata (gv-features-plan.md Feature 1's "Efficiency
// gate"): the fold costs O(depth) fresh QuickJS instantiations, so resolveInvokeMetadata runs it
// only when the script could actually observe gv.metadata.inherit()'s result.
func mentionsInherit(src string) bool {
	return inheritCallRe.MatchString(src)
}

// foldAncestorMetadata computes gv.metadata.inherit()'s data for a node whose parent-folder
// display-name path is path: an ITERATIVE GO FOLD (no JS recursion, no async, no re-entrancy)
// over path's ancestor folder metadata scripts, root -> immediate-parent
// (store.FolderMetadataChain). Each non-empty script is evaluated through the UNCACHED
// RunRequestBody path — NEVER the cached RunGenerator path, per the cache-soundness invariant in
// gv-features-plan.md — with the running accumulator injected as that eval's
// Input.InheritedMetadata and params as its Input.Params; the accumulator is then REPLACED with
// the folder's evaluated result.
//
// Semantics are D2 — spread-driven replace: transitivity is userland
// `{ ...gv.metadata.inherit(), ... }`, so an EMPTY folder script is a transparent passthrough
// (accumulator unchanged) while a NON-EMPTY script that omits the spread is a deliberate barrier
// that whole-replaces (drops ancestor keys it does not re-emit); a redefined key whole-replaces
// its array, like any JS spread.
//
// A path deeper than MaxFolderMetadataDepth is rejected up front (bounds a pathological tree
// before paying for any of the O(depth) QuickJS instantiations — a Connect error, not a hang). A
// stale path segment (a folder renamed/deleted out from under an open tab) degrades to "no
// inheritance" — an empty accumulator, nil error — mirroring how applyRequestMiddleware /
// loadAttachedMiddleware tolerate a missing target request, because FolderMetadataChain itself
// PROPAGATES ErrItemNotFound/ErrNotAFolder rather than swallowing them (store/fs.go). Any other
// per-folder failure (a script that throws, or returns a non-object / wrongly shaped value) is
// wrapped to name the offending folder's path.
func (w Workspace) foldAncestorMetadata(ctx context.Context, workspaceName string, path []string, params map[string]any, allGens map[string]string) (map[string][]string, error) {
	if len(path) > MaxFolderMetadataDepth {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("folder metadata chain depth %d exceeds the max of %d", len(path), MaxFolderMetadataDepth))
	}
	coll, err := w.store.Open(ctx, workspaceName)
	if err != nil {
		return nil, err
	}
	scripts, err := coll.FolderMetadataChain(ctx, path)
	if errors.Is(err, store.ErrItemNotFound) || errors.Is(err, store.ErrNotAFolder) {
		return map[string][]string{}, nil // stale/renamed folder path: no inheritance, not a failure
	}
	if err != nil {
		return nil, toConnectError(err)
	}

	accum := map[string][]string{}
	for i, script := range scripts {
		script = strings.TrimSpace(script)
		if script == "" {
			continue // empty folder script: transparent passthrough (D2) — accumulator unchanged
		}
		folderPath := path[:i+1]
		res, rerr := w.engine.RunRequestBody(ctx, script, transitiveGenerators(script, allGens), scripting.Grant{}, scripting.Input{
			Params:            params,
			InheritedMetadata: accum,
		})
		if rerr != nil {
			return nil, wrapFolderError(folderPath, metadataError(connect.CodeFailedPrecondition, rerr.Error()))
		}
		if !isJSONObject(res.Value) {
			return nil, wrapFolderError(folderPath, metadataError(connect.CodeFailedPrecondition, "expected the folder metadata to return an object"))
		}
		lists, lerr := metadataListsFromJSON(res.Value)
		if lerr != nil {
			return nil, wrapFolderError(folderPath, lerr)
		}
		accum = lists // D2: whole-replace, never merge — transitivity is the script's own spread
	}
	return accum, nil
}

// wrapFolderError re-renders a per-folder metadata evaluation/shape error (already a
// *connect.Error produced by metadataError/metadataListsFromJSON, so connect.CodeOf(err) is
// preserved) to additionally name the offending ancestor folder's display-name path — the "wrap
// any per-folder error so it names the offending folder path" requirement (gv-features-plan.md
// Feature 1 Phase 3).
func wrapFolderError(folderPath []string, err error) error {
	return connect.NewError(connect.CodeOf(err),
		fmt.Errorf("folder %q metadata: %w", strings.Join(folderPath, "/"), err))
}

// metadataListsFromJSON converts a metadata module's evaluated JSON object (a request's own
// script, or one ancestor folder's script) into the map[string][]string form — the shape
// gv.metadata.inherit()'s accumulator uses (foldAncestorMetadata) and structFromMetadataLists
// renders into the final Struct, so the fold and the outgoing request share this one normalizer.
// Each value must be a JSON string (a single-element list) or a JSON array of strings (a list);
// any other shape is a clear InvalidArgument naming the offending key (metadataValueList).
func metadataListsFromJSON(raw []byte) (map[string][]string, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, metadataError(connect.CodeFailedPrecondition, "metadata is not a JSON object: "+err.Error())
	}
	lists := make(map[string][]string, len(obj))
	for key, rawVal := range obj {
		values, err := metadataValueList(key, rawVal)
		if err != nil {
			return nil, err
		}
		lists[key] = values
	}
	return lists, nil
}

// structFromMetadataLists renders the map[string][]string form (metadataListsFromJSON) as the
// google.protobuf.Struct of ListValue<string> that structToMetadata expands into repeated
// headers — the shape the final resolved outgoing metadata needs. A purely mechanical rebuild:
// it does not itself apply any "-bin" base64 handling (encodeMetadataValue does that later, at
// the point structToMetadata builds the real gRPC metadata.MD), so it must never be asked to
// duplicate that behavior.
func structFromMetadataLists(lists map[string][]string) *structpb.Struct {
	fields := make(map[string]*structpb.Value, len(lists))
	for key, values := range lists {
		vals := make([]*structpb.Value, len(values))
		for i, v := range values {
			vals[i] = structpb.NewStringValue(v)
		}
		fields[key] = structpb.NewListValue(&structpb.ListValue{Values: vals})
	}
	return &structpb.Struct{Fields: fields}
}

// metadataValueList renders one metadata value as the list of strings it stands for: a JSON
// string is a single-element list; a JSON array must hold only strings and becomes a list of
// them. Numbers/booleans/objects/null (or a non-string array element) are rejected — gRPC
// metadata is string-valued and the editor types the module against
// `{ [key: string]: string[] }`, so a runtime violation is a clear InvalidArgument.
func metadataValueList(key string, raw json.RawMessage) ([]string, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return []string{s}, nil
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil, metadataError(connect.CodeInvalidArgument,
			fmt.Sprintf("metadata value for %q must be a string or string[]", key))
	}
	out := make([]string, len(arr))
	for i, el := range arr {
		var es string
		if err := json.Unmarshal(el, &es); err != nil {
			return nil, metadataError(connect.CodeInvalidArgument,
				fmt.Sprintf("metadata value for %q must be a string or string[]; element %d is not a string", key, i))
		}
		out[i] = es
	}
	return out, nil
}

// metadataError renders a metadata-evaluation failure (of a request's own script, or — wrapped
// further by wrapFolderError — one ancestor folder's script) as a Connect error, mirroring the
// body's error policy: an eval failure grpcview can't get past (throw/timeout/non-object
// return) is FailedPrecondition like bodyError, while a value whose shape is not
// string|string[] is InvalidArgument like the body's UnmarshalJSON type mismatch.
func metadataError(code connect.Code, detail string) error {
	return connect.NewError(code, fmt.Errorf("cannot evaluate the request metadata: %s", detail))
}

// recordHistory persists one completed invoke to the target request's run
// history. It is best-effort by design: an ad-hoc invoke with no stored target
// request (empty item_name) records nothing, and any persistence failure is
// logged, never returned — history is local, regenerable state and must never
// fail the RPC. The History.Response mirrors the invoke's out almost 1:1; only
// Status.details (google.protobuf.Any) is dropped, since its target-defined types
// aren't in grpcview's registry and could break protojson round-tripping — the
// code + message are what the UI's status chip needs. For a streaming invoke, out
// carries the terminal status/metadata/latency/timestamp but no streamed payloads
// (out.Response is empty), and body holds only the first request message.
func (w Workspace) recordHistory(ctx context.Context, workspaceName string, path []string, itemName, service, method, body string, out *grpcviewv1.Request_Response) {
	if itemName == "" {
		return // ad-hoc invoke: no stored request to attach history to
	}
	st := out.GetStatus()
	entry := &grpcviewv1.History{
		Request: &grpcviewv1.History_Request{
			Service:  service,
			Method:   method,
			Body:     []byte(body),
			Metadata: out.GetRequestMetadata(),
		},
		Response: &grpcviewv1.History_Response{
			Status:    &grpcviewv1.Status{Code: st.GetCode(), Message: st.GetMessage()},
			Response:  out.GetResponse(),
			Metadata:  out.GetResponseMetadata(),
			Latency:   out.GetLatency(),
			Timestamp: out.GetTimestamp(),
		},
	}
	coll, err := w.store.Open(ctx, workspaceName)
	if err != nil {
		slog.Warn("history: open collection", "workspace", workspaceName, "err", err)
		return
	}
	if err := coll.AppendHistory(ctx, path, itemName, entry, historyLimit); err != nil {
		if errors.Is(err, store.ErrItemNotFound) {
			return // request not stored (deleted mid-call): nothing to attach to
		}
		slog.Warn("history: append", "workspace", workspaceName, "item", itemName, "err", err)
	}
}

// codeOK is the gRPC status code for a successful call (google.rpc.Code.OK).
const codeOK = 0

// resolveMethod resolves the target server, dials it, reflects its schema, and
// locates the requested method descriptor. It returns the open connection, the
// method descriptor, and a cleanup func the caller must invoke when done (it
// resets the reflection client and closes the connection). Shared by unary
// Invoke and streamInvoke. Every failure here is a pre-flight failure grpcview
// itself can't get past, so they surface as Connect errors, with the same codes
// unary Invoke has always used.
func (w Workspace) resolveMethod(ctx context.Context, target *grpcviewv1.Server, workspaceName, service, method string) (*grpc.ClientConn, *desc.MethodDescriptor, func(), error) {
	resolved, err := w.resolveTarget(ctx, target, workspaceName, service)
	if err != nil {
		return nil, nil, nil, err
	}

	conn, err := dial(resolved)
	if err != nil {
		return nil, nil, nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("connect to %s: %w", resolved.GetAddress(), err))
	}

	// Resolve the method descriptor by reflecting the target. Reflection sources
	// don't persist full descriptors, so the schema is fetched fresh per call; if
	// the server is unreachable we can't build the request at all.
	refClient := grpcreflect.NewClientAuto(ctx, conn)
	cleanup := func() {
		refClient.Reset()
		_ = conn.Close()
	}

	svcDesc, err := refClient.ResolveService(service)
	if err != nil {
		cleanup()
		return nil, nil, nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("resolve service %q: %w", service, err))
	}
	methodDesc := svcDesc.FindMethodByName(method)
	if methodDesc == nil {
		cleanup()
		return nil, nil, nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("method %q not found in service %q", method, service))
	}

	return conn, methodDesc, cleanup, nil
}

// resolveTarget returns the server to send the call to: the explicit target if
// set, else the reflection source the request's service was resolved from, else
// the workspace's first reflection source. Both invoke request types expose
// GetTarget/GetWorkspaceName/GetService, so this takes those values rather than a
// concrete request type.
//
// The service-aware default (Service.source, attributed per source in
// convertService) is what makes a request against a method from the 2nd+ reflection
// source dial THAT source instead of always the first: an unset request.target means
// "follow the service's origin live", so this default holds for both new and
// pre-existing mis-defaulted requests without persisting anything on the request. A
// service with no attributed source (a descriptor-set upload, or a services cache
// written before Service.source existed), an unrecognized service, and an ad-hoc
// invoke with no service all fall back to the first reflection source — the exact
// pre-attribution behavior.
func (w Workspace) resolveTarget(ctx context.Context, target *grpcviewv1.Server, workspaceName, service string) (*grpcviewv1.Server, error) {
	if target != nil {
		return target, nil
	}

	coll, err := w.store.Open(ctx, workspaceName)
	if err != nil {
		return nil, err
	}

	// Prefer the source the request's service was resolved from. Match on the same
	// `${package}.${name}` identity the UI stores on the request and passes to invoke.
	services, err := coll.Services(ctx)
	if err != nil {
		return nil, toConnectError(err)
	}
	for _, svc := range services {
		if fmt.Sprintf("%s.%s", svc.GetPackage(), svc.GetName()) == service {
			if src := svc.GetSource(); src != nil {
				return src, nil
			}
			break // service found but unattributed: fall through to the first source
		}
	}

	sources, err := coll.Sources(ctx)
	if err != nil {
		return nil, toConnectError(err)
	}
	for _, src := range sources {
		if refl := src.GetReflection(); refl != nil {
			return refl, nil
		}
	}
	return nil, connect.NewError(connect.CodeFailedPrecondition,
		fmt.Errorf("no target server: add a reflection source or specify a target"))
}

// dial opens a lazy client connection to the target. The target's address is a
// host:port string passed straight to grpc.NewClient (which accepts exactly that
// form). TLS is enabled when the server carries a (possibly empty) TLS block,
// using the system roots.
func dial(target *grpcviewv1.Server) (*grpc.ClientConn, error) {
	creds := insecure.NewCredentials()
	if target.GetTls() != nil {
		creds = credentials.NewTLS(&tls.Config{})
	}
	return grpc.NewClient(target.GetAddress(), grpc.WithTransportCredentials(creds))
}

// mergeMD combines response header and trailer metadata into one set. Header
// keys win over trailer keys on collision (headers arrive first).
func mergeMD(header, trailer metadata.MD) metadata.MD {
	if len(header) == 0 {
		return trailer
	}
	if len(trailer) == 0 {
		return header
	}
	merged := header.Copy()
	for k, v := range trailer {
		if _, ok := merged[k]; !ok {
			merged[k] = v
		}
	}
	return merged
}

// structToMetadata converts request metadata (a google.protobuf.Struct of
// string or list-of-string values) into gRPC metadata. Binary keys ("-bin")
// carry base64 in the Struct and are decoded to raw bytes, which is what
// grpc-go expects to send.
func structToMetadata(s *structpb.Struct) metadata.MD {
	md := metadata.MD{}
	for key, value := range s.GetFields() {
		key = strings.ToLower(key)
		for _, raw := range valueToStrings(value) {
			md.Append(key, encodeMetadataValue(key, raw))
		}
	}
	return md
}

// metadataToStruct converts gRPC metadata into a google.protobuf.Struct: a
// single value becomes a string, multiple values a list of strings. Binary
// ("-bin") values are base64-encoded so the Struct stays valid UTF-8.
func metadataToStruct(md metadata.MD) *structpb.Struct {
	if len(md) == 0 {
		return nil
	}
	fields := make(map[string]*structpb.Value, len(md))
	for key, values := range md {
		encoded := make([]*structpb.Value, len(values))
		for i, v := range values {
			encoded[i] = structpb.NewStringValue(decodeMetadataValue(key, v))
		}
		if len(encoded) == 1 {
			fields[key] = encoded[0]
		} else {
			fields[key] = structpb.NewListValue(&structpb.ListValue{Values: encoded})
		}
	}
	return &structpb.Struct{Fields: fields}
}

// valueToStrings flattens a Struct value into the metadata value(s) it stands
// for. Lists expand to multiple values; scalars stringify.
func valueToStrings(v *structpb.Value) []string {
	if lv, ok := v.GetKind().(*structpb.Value_ListValue); ok {
		out := make([]string, 0, len(lv.ListValue.GetValues()))
		for _, item := range lv.ListValue.GetValues() {
			out = append(out, scalarToString(item))
		}
		return out
	}
	return []string{scalarToString(v)}
}

func scalarToString(v *structpb.Value) string {
	switch k := v.GetKind().(type) {
	case *structpb.Value_StringValue:
		return k.StringValue
	case *structpb.Value_NumberValue:
		return strconv.FormatFloat(k.NumberValue, 'f', -1, 64)
	case *structpb.Value_BoolValue:
		return strconv.FormatBool(k.BoolValue)
	default:
		return ""
	}
}

// encodeMetadataValue prepares a value typed by the user for the wire: "-bin"
// keys are decoded from base64 to the raw bytes grpc-go transmits. A value that
// isn't valid base64 is passed through unchanged.
func encodeMetadataValue(key, value string) string {
	if !strings.HasSuffix(key, "-bin") {
		return value
	}
	if raw, err := base64.StdEncoding.DecodeString(value); err == nil {
		return string(raw)
	}
	return value
}

// decodeMetadataValue renders a received value for display: "-bin" keys hold
// raw bytes on the wire, base64-encoded here so the Struct stays UTF-8 safe.
func decodeMetadataValue(key, value string) string {
	if strings.HasSuffix(key, "-bin") {
		return base64.StdEncoding.EncodeToString([]byte(value))
	}
	return value
}
