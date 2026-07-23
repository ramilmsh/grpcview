package workspace

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
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

// Invoke executes a single unary RPC against the target server and returns the
// result. A gRPC-level failure of the *invoked* call (e.g. the target returns
// NotFound) is not an error of this RPC: it is reported in the response's
// Status so the UI can render it. Only failures grpcview itself can't get past
// — no target, unreachable schema, a body that doesn't fit the request type —
// surface as Connect errors.
func (w Workspace) Invoke(ctx context.Context, request *connect.Request[grpcviewv1.InvokeRequest]) (*connect.Response[grpcviewv1.InvokeResponse], error) {
	msg := request.Msg

	conn, methodDesc, cleanup, err := w.resolveMethod(ctx, msg.GetTarget(), msg.GetWorkspaceName(), msg.GetService(), msg.GetMethod())
	if err != nil {
		return nil, err
	}
	defer cleanup()

	if methodDesc.IsClientStreaming() || methodDesc.IsServerStreaming() {
		return nil, connect.NewError(connect.CodeUnimplemented, fmt.Errorf("streaming methods are not supported yet: %s", methodDesc.GetFullyQualifiedName()))
	}

	body := strings.TrimSpace(msg.GetBody())
	if body == "" {
		body = "{}"
	}
	// Evaluate a TypeScript body → JSON first (§T1), the shared pre-send step streamInvoke
	// also runs BEFORE token resolution: when body_language is TYPESCRIPT the body is a
	// generator whose returned object becomes the JSON message; for JSON (the default) this
	// is a byte-identical no-op, so today's JSON path + {{ }} tokens are untouched.
	evaluatedBodies, err := w.resolveInvokeBody(ctx, msg.GetWorkspaceName(), []string{body}, msg.GetBodyLanguage())
	if err != nil {
		return nil, err
	}
	// Resolve {{ generator() }} tokens in the body + metadata before the call (§S2), the
	// shared pre-send step streamInvoke also runs. A token-free request is untouched; a
	// resolution failure is a Connect error grpcview can't get past, like a bad body.
	resolvedBodies, resolvedMD, err := w.resolveInvokeTokens(ctx, msg.GetWorkspaceName(), evaluatedBodies, msg.GetMetadata())
	if err != nil {
		return nil, err
	}
	// Run the saved request's attached middleware chain (§S3) on the resolved outgoing
	// request — tokens resolve to values first, then middleware rewrites the body/metadata,
	// in order. The same shared pre-send step streamInvoke runs; a no-op when nothing is
	// attached, and a per-middleware failure is a Connect error like the token errors.
	resolvedBodies, resolvedMD, err = w.applyRequestMiddleware(ctx, msg.GetWorkspaceName(), msg.GetPath(), msg.GetItemName(), msg.GetTarget(), resolvedBodies, resolvedMD)
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
		w.recordHistory(ctx, msg.GetWorkspaceName(), msg.GetPath(), msg.GetItemName(), msg.GetService(), msg.GetMethod(), msg.GetBody(), out)
		return connect.NewResponse(&grpcviewv1.InvokeResponse{Response: out}), nil
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

	w.recordHistory(ctx, msg.GetWorkspaceName(), msg.GetPath(), msg.GetItemName(), msg.GetService(), msg.GetMethod(), msg.GetBody(), out)
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
		bodies = []string{"{}"}
	}
	// Evaluate a TypeScript body → JSON before token resolution (§T1), the same shared
	// pre-send step unary Invoke runs; a byte-identical no-op for JSON bodies. A failure is
	// a pre-flight Connect error that sends no frames, like the other pre-flight errors here.
	bodies, err = w.resolveInvokeBody(ctx, msg.GetWorkspaceName(), bodies, msg.GetBodyLanguage())
	if err != nil {
		return err
	}
	// Resolve {{ generator() }} tokens across every request body + the metadata before the
	// call (§S2), the same pre-send step unary Invoke runs. A resolution failure is a
	// pre-flight Connect error that sends no frames, like the other pre-flight errors here.
	bodies, resolvedMD, err := w.resolveInvokeTokens(ctx, msg.GetWorkspaceName(), bodies, msg.GetMetadata())
	if err != nil {
		return err
	}
	// Run the saved request's attached middleware chain (§S3) on the resolved outgoing
	// request, the same shared pre-send step unary Invoke runs (after token resolution). A
	// per-middleware failure is a pre-flight Connect error that sends no frames.
	bodies, resolvedMD, err = w.applyRequestMiddleware(ctx, msg.GetWorkspaceName(), msg.GetPath(), msg.GetItemName(), msg.GetTarget(), bodies, resolvedMD)
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

// resolveInvokeBody is the shared pre-send body-evaluation step (ts-request-body-plan §T1 +
// T3) for unary Invoke (one body) and streaming InvokeStreaming (many), run BEFORE token
// resolution at both sites. When lang is TYPESCRIPT each body is a TS/JS generator: it is
// run through the engine — uncached, so Math.random()/now() vary per invoke, and fully
// sandboxed (empty Grant, no vars/secrets/env, exactly like the token/middleware runs) — and
// its returned JSON object REPLACES the body, which then flows through the existing
// token/middleware/UnmarshalJSON pipeline unchanged. As of T3 (pillar C, opt-in) a body may
// also COMPOSE the workspace's saved generators by calling them as ambient globals; the
// workspace generators are loaded once and each body folds in only the ones it references (see
// referencedGenerators / engine.RunRequestBody). For JSON or UNSPECIFIED (the default, so a
// request saved before body_language existed) it is a pure no-op: the bodies pass through
// byte-identical, so today's JSON path and {{ }} tokens are completely untouched. A throw,
// timeout, or a non-object return is a Connect FailedPrecondition, mirroring the token/
// middleware error policy (tokenError/middlewareError).
func (w Workspace) resolveInvokeBody(ctx context.Context, workspaceName string, bodies []string, lang grpcviewv1.BodyLanguage) ([]string, error) {
	if lang != grpcviewv1.BodyLanguage_BODY_LANGUAGE_TYPESCRIPT {
		return bodies, nil // JSON / UNSPECIFIED: no-op, today's path runs verbatim
	}
	if w.engine == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			errors.New("a TypeScript request body requires the scripting engine, which is not available"))
	}
	// Load the workspace's saved generators ONCE so a body can call them as ambient globals
	// (pillar C / T3). loadGenerators returns an empty map when the scripts collection does not
	// exist, so a workspace with no scripts still evaluates a plain (non-composing) body; a real
	// store error propagates (it is grpcview's own failure, like the token path's).
	allGens, err := w.loadGenerators(ctx, workspaceName)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(bodies))
	for i, body := range bodies {
		// The whole body is the generator source (its own `export default` fires the
		// entry-point convention with no positional args, per entry.go). referencedGenerators
		// narrows allGens to just what this body names, so composition folds in only those and
		// an unrelated generator's compile error can't break a body that does not call it; an
		// empty subset makes RunRequestBody take the plain §T1 generator path. Result.Value is
		// the returned object as raw JSON — the replacement body.
		res, err := w.engine.RunRequestBody(ctx, body, referencedGenerators(body, allGens), scripting.Grant{}, scripting.Input{})
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

// referencedGenerators returns the subset of all whose name is CALLED in body. A generator is
// "called" when its name appears as a call site — the name immediately followed by optional
// whitespace and an opening paren (mkid(), dbl (7)) — and is NOT a property/method access
// (x.format()). This mirrors how generators were used as tokens ({{ name(args) }} is always a
// call) and is what makes FAILURE ISOLATION hold: an object key ({ id: 7 }), a local variable,
// or a method call that merely shares a generator's name does NOT pull that generator into the
// composed bundle, so an unrelated (possibly broken) generator cannot break a body that never
// calls it. It also bounds each per-invoke bundle to the generators the body actually calls;
// when the subset is empty RunRequestBody takes the plain (uncached) §T1 path, so a TS body that
// calls nothing behaves exactly as before. Dotted-named generators are excluded (a dotted name is
// never one identifier) — a documented v1 gap. Recognition stays textual (like the token scan),
// so the only residual over-approximation is a literal name( inside a string or comment; a
// generator used without a direct call (passed as a bare callback, e.g. arr.map(mkid)) is
// likewise not detected — both are acceptable for v1 (parity with the token model, which only
// ever called generators).
func referencedGenerators(body string, all map[string]string) map[string]string {
	called := make(map[string]struct{})
	for _, loc := range genCallRe.FindAllStringSubmatchIndex(body, -1) {
		start, end := loc[2], loc[3] // the captured identifier's byte bounds
		if start > 0 && body[start-1] == '.' {
			continue // obj.name(...) — a property/method call, not a generator call
		}
		called[body[start:end]] = struct{}{}
	}
	out := make(map[string]string)
	for name, source := range all {
		if _, ok := called[name]; ok {
			out[name] = source
		}
	}
	return out
}

// genCallRe matches a call site: an identifier (captured) followed by optional whitespace and an
// opening paren. referencedGenerators additionally rejects a match preceded by "." (a method
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
// error (mirrors tokens.go's tokenError and middleware.go's middlewareError).
func bodyError(index int, detail string) error {
	return connect.NewError(connect.CodeFailedPrecondition,
		fmt.Errorf("cannot evaluate TypeScript request body [%d]: %s", index, detail))
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
	resolved, err := w.resolveTarget(ctx, target, workspaceName)
	if err != nil {
		return nil, nil, nil, err
	}

	conn, err := dial(resolved)
	if err != nil {
		return nil, nil, nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("connect to %s:%d: %w", resolved.GetHost(), resolved.GetPort(), err))
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
// set, otherwise the named workspace's first reflection source. Both invoke
// request types expose GetTarget/GetWorkspaceName, so this takes those two
// values rather than a concrete request type.
func (w Workspace) resolveTarget(ctx context.Context, target *grpcviewv1.Server, workspaceName string) (*grpcviewv1.Server, error) {
	if target != nil {
		return target, nil
	}

	coll, err := w.store.Open(ctx, workspaceName)
	if err != nil {
		return nil, err
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

// dial opens a lazy client connection to the target. TLS is enabled when the
// server carries a (possibly empty) TLS block, using the system roots.
func dial(target *grpcviewv1.Server) (*grpc.ClientConn, error) {
	creds := insecure.NewCredentials()
	if target.GetTls() != nil {
		creds = credentials.NewTLS(&tls.Config{})
	}
	addr := fmt.Sprintf("%s:%d", target.GetHost(), target.GetPort())
	return grpc.NewClient(addr, grpc.WithTransportCredentials(creds))
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
