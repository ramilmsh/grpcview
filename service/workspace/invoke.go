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

	"google.golang.org/protobuf/runtime/protoiface"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/scripting"
	"codeberg.org/ramilmsh/grpcview/service/store"
)

const historyLimit = 50

const emptyBody = store.EmptyBody

const MaxFolderMetadataDepth = 16

type invokeSpec struct {
	workspaceName  string
	path           []string
	itemName       string
	service        string
	method         string
	target         *grpcviewv1.Server
	body           string
	metadataScript string
	metadata       *structpb.Struct
	params         map[string]any
	recordHistory  bool

	// bodyFile and metadataFile are the saved request's body.ts/metadata.ts paths (relative
	// to the store root), set only when the spec was resolved from disk (resolveSavedRun).
	// The wire Invoke/InvokeStreaming path leaves them empty, since there the body came from
	// an editor buffer with no file behind it — error messages must stay unlabeled there.
	// bodyFile is further blanked by resolveSavedRun when the caller passed explicit
	// messages: the bytes being evaluated then came from the caller, not body.ts, and an
	// error must not name a file it never read. metadataFile has no such case — it is
	// always read from disk.
	bodyFile     string
	metadataFile string
}

func specFrom(in *grpcviewv1.InvokeSpec) invokeSpec {
	return invokeSpec{
		workspaceName:  in.GetCollection(),
		path:           in.GetPath(),
		itemName:       in.GetItemName(),
		service:        in.GetService(),
		method:         in.GetMethod(),
		target:         in.GetTarget(),
		metadataScript: in.GetMetadataScript(),
		metadata:       in.GetMetadata(),
	}
}

func (w Workspace) invokeUnary(ctx context.Context, spec invokeSpec) (*grpcviewv1.Request_Response, error) {
	conn, methodDesc, cleanup, err := w.resolveMethod(ctx, spec.target, spec.workspaceName, spec.service, spec.method)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	if methodDesc.IsClientStreaming() || methodDesc.IsServerStreaming() {
		return nil, connect.NewError(connect.CodeUnimplemented, fmt.Errorf(
			"%s is a streaming method: run it with invoke_streaming / invoke_saved_streaming (MCP), "+
				"InvokeStreaming / InvokeSavedStreaming (RPC), or the grpcview invoke verb",
			methodDesc.GetFullyQualifiedName()))
	}

	body := strings.TrimSpace(spec.body)
	if body == "" {
		body = emptyBody
	}
	resolvedBodies, resolvedMD, err := w.resolvePreSend(ctx, spec, []string{body})
	if err != nil {
		return nil, err
	}
	body = resolvedBodies[0]

	reqMsg := dynamic.NewMessage(methodDesc.GetInputType())
	if err := reqMsg.UnmarshalJSON([]byte(body)); err != nil {
		return nil, invalidBodyError(spec.bodyFile, methodDesc.GetInputType().GetFullyQualifiedName(), err)
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

// The ONE seam every invoke path shares, so the invoke() Invoker is installed here and nowhere else:
// hanging it off a single caller left it missing from the streaming and dry-run paths.
func (w Workspace) resolvePreSend(ctx context.Context, spec invokeSpec, bodies []string) ([]string, *structpb.Struct, error) {
	ctx = scripting.WithInvoker(ctx, w.scriptInvoker(spec.workspaceName))

	evaluatedBodies, err := w.resolveInvokeBody(ctx, spec.workspaceName, bodies, spec.params, spec.bodyFile)
	if err != nil {
		return nil, nil, err
	}
	outgoingMD, err := w.resolveInvokeMetadata(ctx, spec.workspaceName, spec.path, spec.metadataScript, spec.metadata, spec.params, spec.metadataFile)
	if err != nil {
		return nil, nil, err
	}
	return w.applyRequestMiddleware(ctx, spec.workspaceName, spec.path, spec.itemName, spec.service, spec.target, evaluatedBodies, outgoingMD, spec.params)
}

func (w Workspace) Invoke(ctx context.Context, request *connect.Request[grpcviewv1.InvokeRequest]) (*connect.Response[grpcviewv1.InvokeResponse], error) {
	spec := specFrom(request.Msg.GetSpec())
	spec.body = request.Msg.GetBody()
	spec.recordHistory = true
	out, err := w.invokeUnary(ctx, spec)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&grpcviewv1.InvokeResponse{Response: out}), nil
}

func (w Workspace) InvokeStreaming(ctx context.Context, request *connect.Request[grpcviewv1.InvokeStreamRequest], stream *connect.ServerStream[grpcviewv1.InvokeStreamingResponse]) error {
	spec := specFrom(request.Msg.GetSpec())
	spec.recordHistory = true
	return w.streamInvoke(ctx, spec, request.Msg.GetMessages(), stream.Send)
}

func (w Workspace) streamInvoke(ctx context.Context, spec invokeSpec, messages []string, send func(*grpcviewv1.InvokeStreamingResponse) error) error {
	conn, methodDesc, cleanup, err := w.resolveMethod(ctx, spec.target, spec.workspaceName, spec.service, spec.method)
	if err != nil {
		return err
	}
	defer cleanup()

	bodies := messages
	if len(bodies) == 0 {
		bodies = []string{emptyBody}
	}
	bodies, resolvedMD, err := w.resolvePreSend(ctx, spec, bodies)
	if err != nil {
		return err
	}
	reqMsgs := make([]*dynamic.Message, len(bodies))
	for i, body := range bodies {
		body = strings.TrimSpace(body)
		if body == "" {
			body = emptyBody
		}
		m := dynamic.NewMessage(methodDesc.GetInputType())
		if err := m.UnmarshalJSON([]byte(body)); err != nil {
			return invalidBodyErrorAt(i, spec.bodyFile, methodDesc.GetInputType().GetFullyQualifiedName(), err)
		}
		reqMsgs[i] = m
	}

	reqMD := structToMetadata(resolvedMD)
	callCtx := metadata.NewOutgoingContext(ctx, reqMD)
	stub := grpcdynamic.NewStub(conn)

	sendMessage := func(resp protoiface.MessageV1) error {
		dm, err := dynamic.AsDynamicMessage(resp)
		if err != nil {
			return connect.NewError(connect.CodeInternal, fmt.Errorf("read response message: %w", err))
		}
		jsonBytes, err := dm.MarshalJSON()
		if err != nil {
			return connect.NewError(connect.CodeInternal, fmt.Errorf("marshal response to json: %w", err))
		}
		return send(&grpcviewv1.InvokeStreamingResponse{Event: &grpcviewv1.InvokeStreamingResponse_Message{Message: jsonBytes}})
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
		resp, err := stub.InvokeRpc(callCtx, methodDesc, reqMsgs[0], grpc.Header(&header), grpc.Trailer(&trailer))
		invokeErr = err
		if err == nil {
			if serr := sendMessage(resp); serr != nil {
				return serr
			}
		}

	case !client && server:
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
			// Header blocks until the server sends headers; Trailer is only valid once RecvMsg has returned an
			// error (EOF included).
			header, _ = ss.Header()
			trailer = ss.Trailer()
		}

	case client && !server:
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

	if err := send(&grpcviewv1.InvokeStreamingResponse{Event: &grpcviewv1.InvokeStreamingResponse_Result{Result: out}}); err != nil {
		return err
	}

	if spec.recordHistory {
		var body string
		if len(messages) > 0 {
			body = messages[0]
		}
		w.recordHistory(ctx, spec.workspaceName, spec.path, spec.itemName, spec.service, spec.method, body, out)
	}
	return nil
}

func (w Workspace) resolveInvokeBody(ctx context.Context, workspaceName string, bodies []string, params map[string]any, label string) ([]string, error) {
	if w.engine == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			errors.New("a TypeScript request body requires the scripting engine, which is not available"))
	}
	coll, err := w.store.Open(ctx, workspaceName)
	if err != nil {
		return nil, toConnectError(err)
	}
	out := make([]string, len(bodies))
	for i, body := range bodies {
		res, err := w.engine.RunRequestBody(ctx, wrapExpressionScript(body), scripting.Grant{}, scripting.Input{
			Params:         params,
			CollectionRoot: coll.Root(),
		})
		if err != nil {
			return nil, bodyError(i, label, err.Error())
		}
		if !isJSONObject(res.Value) {
			return nil, bodyError(i, label, "expected the body to return an object")
		}
		out[i] = string(res.Value)
	}
	return out, nil
}

// Request body, request metadata, folder metadata: only the UI stores the wrapper; MCP, the CLI
// and a hand-edited request.json all arrive bare.
func wrapExpressionScript(src string) string {
	if scripting.HasDefaultExport(src) {
		return src
	}
	return scripting.WrapExpression(src)
}

func isJSONObject(raw []byte) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) > 0 && raw[0] == '{'
}

func bodyError(index int, label, detail string) error {
	if label == "" {
		return connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("cannot evaluate TypeScript request body [%d]: %s", index, detail))
	}
	return connect.NewError(connect.CodeFailedPrecondition,
		fmt.Errorf("cannot evaluate TypeScript request body [%d] (%s): %s", index, label, detail))
}

func invalidBodyError(label, typeName string, err error) error {
	if label == "" {
		return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid request body for %s: %w", typeName, err))
	}
	return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid request body for %s (%s): %w", typeName, label, err))
}

func invalidBodyErrorAt(index int, label, typeName string, err error) error {
	if label == "" {
		return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid request body [%d] for %s: %w", index, typeName, err))
	}
	return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid request body [%d] for %s (%s): %w", index, typeName, label, err))
}

func (w Workspace) resolveInvokeMetadata(ctx context.Context, workspaceName string, path []string, metadataScript string, fallback *structpb.Struct, params map[string]any, label string) (*structpb.Struct, error) {
	if strings.TrimSpace(metadataScript) == "" {
		return w.inheritedMetadataOnly(ctx, workspaceName, path, fallback, params)
	}
	if w.engine == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			errors.New("a TypeScript metadata script requires the scripting engine, which is not available"))
	}
	coll, err := w.store.Open(ctx, workspaceName)
	if err != nil {
		return nil, toConnectError(err)
	}
	inherited, err := w.foldAncestorMetadata(ctx, workspaceName, path, params)
	if err != nil {
		return nil, err
	}
	res, err := w.engine.RunRequestBody(ctx, wrapExpressionScript(metadataScript), scripting.Grant{}, scripting.Input{
		Params:            params,
		InheritedMetadata: inherited,
		CollectionRoot:    coll.Root(),
	})
	if err != nil {
		return nil, metadataError(connect.CodeFailedPrecondition, label, err.Error())
	}
	if !isJSONObject(res.Value) {
		return nil, metadataError(connect.CodeFailedPrecondition, label, "expected the metadata to return an object")
	}
	lists, err := metadataListsFromJSON(res.Value, label)
	if err != nil {
		return nil, err
	}
	return structFromMetadataLists(lists), nil
}

// A request with no metadata script of its own inherits its folder chain: that is what the UI's
// default `{ ...require("grpcview:metadata").inherit() }` buffer does, and a store-read path
// (grpcview:invoke, the CLI) must not silently drop an ancestor's authorization header just
// because nothing was ever saved. Explicit fallback keys still win over inherited ones.
func (w Workspace) inheritedMetadataOnly(ctx context.Context, workspaceName string, path []string, fallback *structpb.Struct, params map[string]any) (*structpb.Struct, error) {
	if w.engine == nil || len(path) == 0 {
		return fallback, nil
	}
	inherited, err := w.foldAncestorMetadata(ctx, workspaceName, path, params)
	if err != nil {
		return nil, err
	}
	if len(inherited) == 0 {
		return fallback, nil
	}
	for key, values := range structToStringLists(fallback) {
		inherited[key] = values
	}
	return structFromMetadataLists(inherited), nil
}

// Folding is unconditional — no textual gate on whether the script mentions inherit(): a gate over
// source text is only ever a guess, and a sound check would need a build. foldAncestorMetadata
// already skips folders whose script is empty, so this is simpler and strictly more correct.
func (w Workspace) foldAncestorMetadata(ctx context.Context, workspaceName string, path []string, params map[string]any) (map[string][]string, error) {
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
		return map[string][]string{}, nil
	}
	if err != nil {
		return nil, toConnectError(err)
	}

	accum := map[string][]string{}
	for i, script := range scripts {
		script = strings.TrimSpace(script)
		if script == "" {
			continue
		}
		folderPath := path[:i+1]
		res, rerr := w.engine.RunRequestBody(ctx, wrapExpressionScript(script), scripting.Grant{}, scripting.Input{
			Params:            params,
			InheritedMetadata: accum,
			CollectionRoot:    coll.Root(),
		})
		if rerr != nil {
			return nil, wrapFolderError(folderPath, metadataError(connect.CodeFailedPrecondition, "", rerr.Error()))
		}
		if !isJSONObject(res.Value) {
			return nil, wrapFolderError(folderPath, metadataError(connect.CodeFailedPrecondition, "", "expected the folder metadata to return an object"))
		}
		lists, lerr := metadataListsFromJSON(res.Value, "")
		if lerr != nil {
			return nil, wrapFolderError(folderPath, lerr)
		}
		accum = lists
	}
	return accum, nil
}

func wrapFolderError(folderPath []string, err error) error {
	return connect.NewError(connect.CodeOf(err),
		fmt.Errorf("folder %q metadata: %w", strings.Join(folderPath, "/"), err))
}

func metadataListsFromJSON(raw []byte, label string) (map[string][]string, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, metadataError(connect.CodeFailedPrecondition, label, "metadata is not a JSON object: "+err.Error())
	}
	lists := make(map[string][]string, len(obj))
	for key, rawVal := range obj {
		values, err := metadataValueList(key, rawVal, label)
		if err != nil {
			return nil, err
		}
		lists[key] = values
	}
	return lists, nil
}

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

func metadataValueList(key string, raw json.RawMessage, label string) ([]string, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return []string{s}, nil
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil, metadataError(connect.CodeInvalidArgument, label,
			fmt.Sprintf("metadata value for %q must be a string or string[]", key))
	}
	out := make([]string, len(arr))
	for i, el := range arr {
		var es string
		if err := json.Unmarshal(el, &es); err != nil {
			return nil, metadataError(connect.CodeInvalidArgument, label,
				fmt.Sprintf("metadata value for %q must be a string or string[]; element %d is not a string", key, i))
		}
		out[i] = es
	}
	return out, nil
}

func metadataError(code connect.Code, label, detail string) error {
	if label == "" {
		return connect.NewError(code, fmt.Errorf("cannot evaluate the request metadata: %s", detail))
	}
	return connect.NewError(code, fmt.Errorf("cannot evaluate the request metadata (%s): %s", label, detail))
}

func (w Workspace) recordHistory(ctx context.Context, workspaceName string, path []string, itemName, service, method, body string, out *grpcviewv1.Request_Response) {
	if itemName == "" {
		return
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
			// details is dropped: its target-defined Any types are not in grpcview's registry and would break
			// protojson round-tripping.
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
			return
		}
		slog.Warn("history: append", "workspace", workspaceName, "item", itemName, "err", err)
	}
}

const codeOK = 0

// The descriptor comes from the workspace's merged definitions, NOT from reflection on the target: the
// sources already resolved and cached it, and re-resolving here would both duplicate that work and
// confine invoke to targets that serve reflection — which the deployment you actually call often does
// not.
func (w Workspace) resolveMethod(ctx context.Context, target *grpcviewv1.Server, workspaceName, service, method string) (*grpc.ClientConn, *desc.MethodDescriptor, func(), error) {
	defs, err := w.definitions(ctx, workspaceName)
	if err != nil {
		return nil, nil, nil, err
	}
	methodDesc, err := defs.method(workspaceName, service, method)
	if err != nil {
		return nil, nil, nil, err
	}

	resolved, err := w.resolveTarget(ctx, target, workspaceName, service)
	if err != nil {
		return nil, nil, nil, err
	}
	conn, err := dial(resolved)
	if err != nil {
		return nil, nil, nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("connect to %s: %w", resolved.GetAddress(), err))
	}

	return conn, methodDesc, func() { _ = conn.Close() }, nil
}

func (w Workspace) resolveTarget(ctx context.Context, target *grpcviewv1.Server, workspaceName, service string) (*grpcviewv1.Server, error) {
	if target != nil {
		return target, nil
	}

	coll, err := w.store.Open(ctx, workspaceName)
	if err != nil {
		return nil, toConnectError(err)
	}

	defs, err := w.definitionsOf(ctx, coll)
	if err != nil {
		return nil, err
	}
	for _, svc := range defs.serviceList {
		if fmt.Sprintf("%s.%s", svc.GetPackage(), svc.GetName()) == service {
			if src := svc.GetSource(); src != nil {
				return src, nil
			}
			break
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

func dial(target *grpcviewv1.Server) (*grpc.ClientConn, error) {
	creds := insecure.NewCredentials()
	if target.GetTls() != nil {
		creds = credentials.NewTLS(&tls.Config{})
	}
	return grpc.NewClient(target.GetAddress(), grpc.WithTransportCredentials(creds))
}

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

// grpc-go base64s "-bin" keys itself, so the base64 the Struct carries is decoded back to raw bytes.
func encodeMetadataValue(key, value string) string {
	if !strings.HasSuffix(key, "-bin") {
		return value
	}
	if raw, err := base64.StdEncoding.DecodeString(value); err == nil {
		return string(raw)
	}
	return value
}

func decodeMetadataValue(key, value string) string {
	if strings.HasSuffix(key, "-bin") {
		return base64.StdEncoding.EncodeToString([]byte(value))
	}
	return value
}
