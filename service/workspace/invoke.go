package workspace

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/jhump/protoreflect/dynamic"
	"github.com/jhump/protoreflect/dynamic/grpcdynamic"
	"github.com/jhump/protoreflect/grpcreflect"

	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
)

// Invoke executes a single unary RPC against the target server and returns the
// result. A gRPC-level failure of the *invoked* call (e.g. the target returns
// NotFound) is not an error of this RPC: it is reported in the response's
// Status so the UI can render it. Only failures grpcview itself can't get past
// — no target, unreachable schema, a body that doesn't fit the request type —
// surface as Connect errors.
func (w Workspace) Invoke(ctx context.Context, request *connect.Request[grpcviewv1.InvokeRequest]) (*connect.Response[grpcviewv1.InvokeResponse], error) {
	msg := request.Msg

	target, err := w.resolveTarget(ctx, msg)
	if err != nil {
		return nil, err
	}

	conn, err := dial(target)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("connect to %s:%d: %w", target.GetHost(), target.GetPort(), err))
	}
	defer conn.Close()

	// Resolve the method descriptor by reflecting the target. Reflection sources
	// don't persist full descriptors, so the schema is fetched fresh per call;
	// if the server is unreachable we can't build the request at all.
	refClient := grpcreflect.NewClientAuto(ctx, conn)
	defer refClient.Reset()

	svcDesc, err := refClient.ResolveService(msg.GetService())
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("resolve service %q: %w", msg.GetService(), err))
	}
	methodDesc := svcDesc.FindMethodByName(msg.GetMethod())
	if methodDesc == nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("method %q not found in service %q", msg.GetMethod(), msg.GetService()))
	}
	if methodDesc.IsClientStreaming() || methodDesc.IsServerStreaming() {
		return nil, connect.NewError(connect.CodeUnimplemented, fmt.Errorf("streaming methods are not supported yet: %s", methodDesc.GetFullyQualifiedName()))
	}

	reqMsg := dynamic.NewMessage(methodDesc.GetInputType())
	body := strings.TrimSpace(msg.GetBody())
	if body == "" {
		body = "{}"
	}
	if err := reqMsg.UnmarshalJSON([]byte(body)); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid request body for %s: %w", methodDesc.GetInputType().GetFullyQualifiedName(), err))
	}

	reqMD := structToMetadata(msg.GetMetadata())
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

	return connect.NewResponse(&grpcviewv1.InvokeResponse{Response: out}), nil
}

// codeOK is the gRPC status code for a successful call (google.rpc.Code.OK).
const codeOK = 0

// resolveTarget returns the server to send the call to: the explicit target on
// the request if set, otherwise the workspace's first reflection source.
func (w Workspace) resolveTarget(ctx context.Context, msg *grpcviewv1.InvokeRequest) (*grpcviewv1.Server, error) {
	if t := msg.GetTarget(); t != nil {
		return t, nil
	}

	coll, err := w.store.Open(ctx, msg.GetWorkspaceName())
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
