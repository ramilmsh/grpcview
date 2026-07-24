package workspace

// middleware.go runs a request's attached MIDDLEWARE scripts on the invoke path, pre-send
// (scripting-ui-plan §S3 — the "use scripts (rewrite)" half). applyRequestMiddleware is the
// shared pre-send step for both unary Invoke and streaming InvokeStreaming, run AFTER token
// resolution: tokens resolve to values first, then middleware rewrites the resolved outgoing
// request. Each attached middleware runs via Engine.RunMiddleware with a ctx built from the
// current {body, metadata, target}; the returned ctx threads into the next middleware and
// finally into the gRPC call.
//
// ctx contract (mirrors the engine's middleware entry point — entry.go's middlewarePostlude,
// which builds ctx = { body, metadata, target } and yields the returned ctx as JSON):
//   - ctx.body — the request message as a parsed JSON value. Passed in as the current body
//     (spliced verbatim as raw JSON), returned as the rewritten body and re-serialized to
//     JSON for the call. REQUIRED in the returned ctx (a missing body is a malformed ctx).
//   - ctx.metadata — the outgoing metadata flattened to a { key: string } object. The
//     returned object REPLACES the outgoing metadata (added/changed keys take effect; a key
//     omitted from the returned object is dropped); scalar values are coerced to strings.
//     A repeated (list) header is flattened to its first value when middleware is attached
//     (a documented limitation — single-valued headers are the common case).
//   - ctx.target — the resolved call target (host:port), threaded through the chain so a
//     later middleware observes an earlier one's change. It is NOT applied to the dialed
//     connection this pass: the connection is dialed (and the schema reflected) before the
//     middleware step runs, so a live target rewrite would need a re-dial/re-reflect — left
//     as a follow-up rather than half-applied.
//
// Middleware run fully SANDBOXED: an empty Grant (no host capabilities), no vars/secrets/env,
// and no invoke() into other requests (deferred to S4). A middleware that throws/times out or
// returns a malformed ctx is a Connect FailedPrecondition naming the offending script, like
// the token errors.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/structpb"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/scripting"
	"codeberg.org/ramilmsh/grpcview/service/store"
)

// applyRequestMiddleware runs the target request's attached middleware chain over every body
// (unary passes one, streaming many) and the shared metadata, in order, and returns the
// rewritten bodies + metadata for the call. The chain is loaded from the SAVED request keyed
// by path/item_name; an ad-hoc invoke (empty item_name), a request that isn't stored, or one
// with no attached middleware is a no-op that returns its inputs unchanged. Failures are
// Connect FailedPrecondition naming the offending script.
func (w Workspace) applyRequestMiddleware(ctx context.Context, workspaceName string, path []string, itemName, service string, target *grpcviewv1.Server, bodies []string, md *structpb.Struct) ([]string, *structpb.Struct, error) {
	if itemName == "" {
		return bodies, md, nil // ad-hoc invoke: no saved request to attach middleware to
	}
	names, err := w.loadAttachedMiddleware(ctx, workspaceName, path, itemName)
	if err != nil {
		return nil, nil, err
	}
	if len(names) == 0 {
		return bodies, md, nil // no attached middleware: pass through unchanged
	}
	if w.engine == nil {
		return nil, nil, connect.NewError(connect.CodeFailedPrecondition,
			errors.New("middleware requires the scripting engine, which is not available"))
	}
	sources, err := w.loadMiddlewareSources(ctx, workspaceName)
	if err != nil {
		return nil, nil, err
	}
	// Resolve each attached name to a source up front so an unknown attachment fails before
	// any middleware runs (and before we touch the outgoing request).
	chain := make([]middlewareScript, len(names))
	for i, name := range names {
		src, ok := sources[name]
		if !ok {
			return nil, nil, connect.NewError(connect.CodeFailedPrecondition,
				fmt.Errorf("cannot run middleware %q: no middleware script by that name in this workspace", name))
		}
		chain[i] = middlewareScript{name: name, source: src}
	}

	// ctx.target is informational this pass (not applied to the dialed connection); resolve
	// it only now that middleware is actually attached. Pass service so the informational
	// target matches the one resolveMethod actually dialed (the request's service-aware
	// default). resolveMethod has already resolved + dialed this target, so this cannot fail
	// here in practice.
	targetStr := ""
	if resolved, terr := w.resolveTarget(ctx, target, workspaceName, service); terr == nil {
		targetStr = fmt.Sprintf("%s:%d", resolved.GetHost(), resolved.GetPort())
	}

	// mdMap and targetStr thread through the WHOLE sequence (every body, every middleware):
	// metadata is a single per-call value, so a later body's chain observes the metadata an
	// earlier body's chain produced (last write wins for the sent metadata).
	mdMap := mdToStringMap(md)
	out := make([]string, len(bodies))
	for i, body := range bodies {
		body = strings.TrimSpace(body)
		if body == "" {
			body = "{}"
		}
		// The body must be valid JSON to hand the middleware a parsed ctx.body; a body that
		// doesn't parse is the same InvalidArgument the request-message unmarshal would raise,
		// surfaced a step earlier.
		if !json.Valid([]byte(body)) {
			return nil, nil, connect.NewError(connect.CodeInvalidArgument,
				fmt.Errorf("invalid request body [%d]: not valid JSON, cannot run middleware", i))
		}
		cur := json.RawMessage(body)
		for _, mw := range chain {
			res, rerr := w.engine.RunMiddleware(ctx, mw.source, scripting.Grant{}, scripting.Input{
				Request: scripting.RequestInput{Body: cur, Metadata: mdMap, Target: targetStr},
			})
			if rerr != nil {
				return nil, nil, middlewareError(mw.name, rerr.Error())
			}
			nextBody, nextMD, nextTarget, perr := parseMiddlewareResult(mw.name, res.Value)
			if perr != nil {
				return nil, nil, perr
			}
			cur, mdMap, targetStr = nextBody, nextMD, nextTarget
		}
		out[i] = string(cur)
	}
	return out, stringMapToStruct(mdMap), nil
}

// middlewareScript is one resolved attachment: its display name (for errors) and source.
type middlewareScript struct {
	name   string
	source string
}

// loadAttachedMiddleware reads the saved request's ordered attached-middleware names. A
// missing collection or a request that isn't stored (ad-hoc / just-deleted target, or a stale
// path) yields no middleware rather than an error, matching recordHistory's best-effort policy
// for the same path/item_name addressing.
func (w Workspace) loadAttachedMiddleware(ctx context.Context, workspaceName string, path []string, itemName string) ([]string, error) {
	coll, err := w.store.Open(ctx, workspaceName)
	if err != nil {
		return nil, err
	}
	names, err := coll.RequestMiddleware(ctx, path, itemName)
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrItemNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, toConnectError(err)
	}
	return names, nil
}

// loadMiddlewareSources reads the workspace's committed scripts and returns a map from a
// MIDDLEWARE script's display name to its source (mirroring tokens.go's loadGenerators). A
// collection that does not exist yet yields an empty map (an attached name then fails as
// "no middleware script", not as a missing workspace).
func (w Workspace) loadMiddlewareSources(ctx context.Context, workspaceName string) (map[string]string, error) {
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
	mws := make(map[string]string, len(scripts))
	for _, s := range scripts {
		if s.GetKind() == grpcviewv1.ScriptKind_SCRIPT_KIND_MIDDLEWARE {
			mws[s.GetName()] = s.GetSource()
		}
	}
	return mws, nil
}

// parseMiddlewareResult decodes a middleware run's Result.Value (the returned ctx as JSON:
// {body, metadata, target}) into the threaded values. A value that is empty, not the ctx
// shape, or missing its body is a malformed ctx → FailedPrecondition naming the script.
func parseMiddlewareResult(name string, value json.RawMessage) (json.RawMessage, map[string]string, string, error) {
	if len(value) == 0 {
		return nil, nil, "", middlewareError(name, "returned no value (a middleware must return its ctx)")
	}
	var raw struct {
		Body     json.RawMessage `json:"body"`
		Metadata map[string]any  `json:"metadata"`
		Target   string          `json:"target"`
	}
	if err := json.Unmarshal(value, &raw); err != nil {
		return nil, nil, "", middlewareError(name, "returned a malformed ctx: "+err.Error())
	}
	if len(raw.Body) == 0 {
		return nil, nil, "", middlewareError(name, "returned a ctx without a body")
	}
	md := make(map[string]string, len(raw.Metadata))
	for k, v := range raw.Metadata {
		s, ok := coerceMetadataValue(v)
		if !ok {
			return nil, nil, "", middlewareError(name, fmt.Sprintf("metadata value for %q is not a string, number, or boolean", k))
		}
		md[k] = s
	}
	return raw.Body, md, raw.Target, nil
}

// coerceMetadataValue renders a middleware-supplied metadata value as a header string: a
// string is used as-is, a number/boolean is stringified. An object/array/null value is not a
// valid header and reports ok=false (a malformed ctx).
func coerceMetadataValue(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, true
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64), true
	case bool:
		return strconv.FormatBool(x), true
	default:
		return "", false
	}
}

// mdToStringMap flattens the outgoing metadata Struct into the flat { key: string } object the
// middleware ctx exposes (RequestInput.Metadata is map[string]string). A repeated (list) value
// is flattened to its first element — middleware sees single-valued headers (see the file
// header). A nil/empty Struct yields an empty (non-nil) map so a script can index ctx.metadata.
func mdToStringMap(md *structpb.Struct) map[string]string {
	fields := md.GetFields()
	out := make(map[string]string, len(fields))
	for k, v := range fields {
		if vals := valueToStrings(v); len(vals) > 0 {
			out[k] = vals[0]
		}
	}
	return out
}

// stringMapToStruct rebuilds a metadata Struct (all single string values) from the flat map a
// middleware chain produced. An empty map yields nil — no metadata — so structToMetadata sends
// none, matching how metadata is absent when unset.
func stringMapToStruct(m map[string]string) *structpb.Struct {
	if len(m) == 0 {
		return nil
	}
	fields := make(map[string]*structpb.Value, len(m))
	for k, v := range m {
		fields[k] = structpb.NewStringValue(v)
	}
	return &structpb.Struct{Fields: fields}
}

// middlewareError renders a middleware failure as a Connect FailedPrecondition naming the
// offending script, matching Invoke's policy that a pre-send failure grpcview itself can't get
// past is a typed Connect error (mirrors invoke.go's bodyError).
func middlewareError(name, detail string) error {
	return connect.NewError(connect.CodeFailedPrecondition,
		fmt.Errorf("middleware %q failed: %s", name, detail))
}
