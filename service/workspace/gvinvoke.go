package workspace

// gvinvoke.go — the workspace-side end of gv.invoke (gv-features-plan.md Feature 3 Phase 2,
// "Workspace re-entry, addressing, depth guard"). service/scripting is a leaf package that
// cannot import service/workspace (that would be an import cycle), so it only carries a
// scripting.Invoker closure on the context (scripting.WithInvoker); this file BUILDS that
// closure. scriptInvoker's returned func is what every gv.invoke call — from a request body, a
// metadata script, a folder's ancestor metadata script, or a middleware — re-enters through,
// on the SAME goroutine that is running the calling script (hostInvoke, scripting/invoke.go,
// calls it synchronously from inside the guest's suspended host call).

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/scripting"
	"google.golang.org/protobuf/types/known/structpb"
)

// maxInvokeDepth bounds gv.invoke's own nesting (gv-features-plan.md D5: "gv.invoke depth 8"):
// the 9th nested gv.invoke call in a chain rejects rather than recursing further. It is
// independent of MaxFolderMetadataDepth (Feature 1's ancestor-folder-chain cap in invoke.go) —
// a folder script that itself calls gv.invoke consumes THIS budget, not that one (see the
// plan's "Cross-feature interactions"). Deliberately a depth cap ALONE, with no visited/cycle
// set: a legitimate self-recursive request (one that re-invokes itself with a next-page token)
// must keep working, and only an unbounded chain is a problem.
const maxInvokeDepth = 8

// gvInvokeDepthCtxKey carries scriptInvoker's own running count of gv.invoke nesting on ctx.
// scripting.WithInvokeDepth's ctx key is unexported by design (its docstring: "depth POLICY is
// yours to enforce") and has no matching exported getter — the cap is entirely a
// workspace-side policy, so workspace tracks the count it actually enforces via this
// package-private key. withGvInvokeDepth always sets BOTH this key and, via
// scripting.WithInvokeDepth, scripting's own documented ctx seam, so the two stay in lockstep
// even though only this package ever reads its copy back.
type gvInvokeDepthCtxKey struct{}

// withGvInvokeDepth returns a context carrying depth as the current gv.invoke nesting count,
// for both this package's own enforcement (gvInvokeDepthFromContext) and scripting's ctx seam
// (scripting.WithInvokeDepth).
func withGvInvokeDepth(ctx context.Context, depth int) context.Context {
	ctx = scripting.WithInvokeDepth(ctx, depth)
	return context.WithValue(ctx, gvInvokeDepthCtxKey{}, depth)
}

// gvInvokeDepthFromContext returns the current gv.invoke nesting depth, defaulting to 0 when
// absent — the top-level request, which has not yet recursed through gv.invoke at all.
func gvInvokeDepthFromContext(ctx context.Context) int {
	d, _ := ctx.Value(gvInvokeDepthCtxKey{}).(int)
	return d
}

// invokeEnvelope is the {path, params} JSON request envelope the guest gvInvokeShim
// (scripting/marshal.go) marshals and hostInvoke (scripting/invoke.go) hands verbatim to the
// ctx-carried Invoker.
type invokeEnvelope struct {
	Path   string         `json:"path"`
	Params map[string]any `json:"params"`
}

// gvInvokeResult is the fetch-style POJO gv.invoke resolves with (gv-features-plan.md Feature 3
// §"Return shape"). Its JSON shape is a contract with the frontend's gv.d.ts InvokeResult type —
// field names/casing must not change independently of that type.
type gvInvokeResult struct {
	OK              bool                `json:"ok"`
	Status          gvInvokeStatus      `json:"status"`
	Body            json.RawMessage     `json:"body"`
	Metadata        map[string][]string `json:"metadata"`
	RequestMetadata map[string][]string `json:"requestMetadata"`
	LatencyMs       float64             `json:"latencyMs"`
}

// gvInvokeStatus is InvokeResult.status: a plain {code, message}, never the full google.rpc
// Status (whose `details` is google.protobuf.Any and does not belong in a script-facing POJO).
type gvInvokeStatus struct {
	Code    int32  `json:"code"`
	Message string `json:"message"`
}

// scriptInvoker returns the scripting.Invoker that every gv.invoke call made by a script
// running against workspaceName's engine re-enters through (threaded onto ctx by invokeUnary
// via scripting.WithInvoker). It parses the {path, params} envelope, enforces the depth cap,
// splits path into a display-name parent path + item name, resolves the named saved request
// through the shared resolveSavedRun (invoke_saved.go, which the InvokeSaved RPC also goes
// through), and calls the SAME invokeUnary the public Invoke RPC uses — with
// recordHistory=false (D6: a script fan-out must not spam N requests' histories) and the
// caller's params threaded through so the target's own body/metadata/middleware (and, per D4,
// its ancestor folders' metadata scripts) see them as gv.request.params.
//
// An error return here becomes a REJECTED gv.invoke promise (hostInvoke tagThrows err.Error());
// per the plan's "Resolve vs. reject", that happens only for an unknown path / a path naming a
// folder, a streaming target (invokeUnary's own guard), a body/metadata that won't evaluate, or
// the depth cap — a gRPC-status failure of the invoked call is NOT one of these: invokeUnary
// returns it as (out, nil) with the failure in out.Status, which marshalInvokeResult renders as
// an ok:false RESOLUTION (fetch-style), never a rejection.
func (w Workspace) scriptInvoker(workspaceName string) scripting.Invoker {
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

		// The same resolve + invokeSpec build the InvokeSaved RPC runs (invoke_saved.go): one
		// helper, so a script's re-entry and an addressed RPC run cannot resolve a saved request
		// differently. recordHistory=false is D6 — a script fan-out must not spam N histories.
		run, err := w.resolveSavedRun(ctx, savedInvoke{
			workspaceName: workspaceName,
			parent:        parent,
			itemName:      name,
			params:        env.Params,
			recordHistory: false, // D6
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

// splitInvokePath splits a gv.invoke path on "/" into the target's parent-folder display-name
// path and its own item name (gv-features-plan.md Feature 3 §"Addressing"): the last segment is
// the item name, the leading ones are the parent path — matching the frontend's
// itemKey/keyOf slash paths (e.g. "UserService/GetUser"). A display name containing a literal
// "/" is unreachable this way, an accepted v1 gap. An empty path, or one whose final segment is
// empty (e.g. a trailing slash), is rejected up front rather than resolving to a confusing
// "not found".
func splitInvokePath(path string) (parent []string, name string, err error) {
	segments := strings.Split(path, "/")
	name = segments[len(segments)-1]
	if name == "" {
		return nil, "", fmt.Errorf("gv.invoke: empty path %q", path)
	}
	return segments[:len(segments)-1], name, nil
}

// marshalInvokeResult renders a completed invokeUnary result as gv.invoke's JSON return shape
// (gv-features-plan.md Feature 3 §"Return shape"): ok mirrors a gRPC-status success
// (status.code == 0); body is the decoded response JSON, or the JSON literal null on failure
// (out.Response is empty in that case — a script should not need to special-case "" vs. null);
// metadata/requestMetadata flatten their Struct form to {[key]: string[]} via the same
// valueToStrings invoke.go's own Struct rendering already uses, so a multi-valued header is a
// real array here too (unlike metadataToStruct's single-value-collapses-to-scalar convenience,
// which a script should not need to special-case either).
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

// structToStringLists flattens a metadata Struct (as produced by metadataToStruct: a single
// value is a bare string, multiple values a ListValue) to the {[key]: string[]} shape
// InvokeResult's metadata/requestMetadata carry, reusing valueToStrings (invoke.go) — the same
// flattening this package's own Struct handling already relies on.
func structToStringLists(s *structpb.Struct) map[string][]string {
	fields := s.GetFields()
	out := make(map[string][]string, len(fields))
	for k, v := range fields {
		out[k] = valueToStrings(v)
	}
	return out
}
