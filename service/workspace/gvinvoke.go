package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/scripting"
	"google.golang.org/protobuf/types/known/structpb"
)

const maxInvokeDepth = 8

type gvInvokeDepthCtxKey struct{}

func withGvInvokeDepth(ctx context.Context, depth int) context.Context {
	return context.WithValue(ctx, gvInvokeDepthCtxKey{}, depth)
}

func gvInvokeDepthFromContext(ctx context.Context) int {
	d, _ := ctx.Value(gvInvokeDepthCtxKey{}).(int)
	return d
}

type invokeEnvelope struct {
	Path   string         `json:"path"`
	Params map[string]any `json:"params"`
}

type gvInvokeResult struct {
	OK              bool                `json:"ok"`
	Status          gvInvokeStatus      `json:"status"`
	Body            json.RawMessage     `json:"body"`
	Metadata        map[string][]string `json:"metadata"`
	RequestMetadata map[string][]string `json:"requestMetadata"`
	LatencyMs       float64             `json:"latencyMs"`
}

type gvInvokeStatus struct {
	Code    int32  `json:"code"`
	Message string `json:"message"`
}

func (w Workspace) scriptInvoker(collectionID string) scripting.Invoker {
	return func(ctx context.Context, req []byte) ([]byte, error) {
		var env invokeEnvelope
		if err := json.Unmarshal(req, &env); err != nil {
			return nil, fmt.Errorf("gv.invoke: malformed request envelope: %w", err)
		}

		depth := gvInvokeDepthFromContext(ctx)
		if depth >= maxInvokeDepth {
			return nil, fmt.Errorf("gv.invoke(%q): nesting depth %d exceeds the max of %d", env.Path, depth, maxInvokeDepth)
		}

		parent, name, err := splitInvokePath(env.Path)
		if err != nil {
			return nil, err
		}

		run, err := w.resolveSavedRun(ctx, savedInvoke{
			workspaceName: collectionID,
			parent:        parent,
			itemName:      name,
			params:        env.Params,
			recordHistory: false,
		})
		if err != nil {
			return nil, fmt.Errorf("gv.invoke(%q): %w", env.Path, err)
		}

		childCtx := withGvInvokeDepth(ctx, depth+1)
		out, err := w.invokeUnary(childCtx, run.spec)
		if err != nil {
			return nil, err
		}
		return marshalInvokeResult(out)
	}
}

func SplitInvokePath(path string) (parent []string, name string, err error) {
	return splitInvokePath(path)
}

func splitInvokePath(path string) (parent []string, name string, err error) {
	segments := strings.Split(path, "/")
	name = segments[len(segments)-1]
	if name == "" {
		return nil, "", fmt.Errorf("gv.invoke: empty path %q", path)
	}
	return segments[:len(segments)-1], name, nil
}

func marshalInvokeResult(out *grpcviewv1.Request_Response) ([]byte, error) {
	body := json.RawMessage(out.GetResponse())
	if len(body) == 0 {
		body = json.RawMessage("null")
	}
	result := gvInvokeResult{
		OK: out.GetStatus().GetCode() == codeOK,
		Status: gvInvokeStatus{
			Code:    out.GetStatus().GetCode(),
			Message: out.GetStatus().GetMessage(),
		},
		Body:            body,
		Metadata:        structToStringLists(out.GetResponseMetadata()),
		RequestMetadata: structToStringLists(out.GetRequestMetadata()),
		LatencyMs:       float64(out.GetLatency().AsDuration().Milliseconds()),
	}
	return json.Marshal(result)
}

func structToStringLists(s *structpb.Struct) map[string][]string {
	fields := s.GetFields()
	out := make(map[string][]string, len(fields))
	for k, v := range fields {
		out[k] = valueToStrings(v)
	}
	return out
}
