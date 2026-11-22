package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/structpb"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/scripting"
	"codeberg.org/ramilmsh/grpcview/service/store"
	"codeberg.org/ramilmsh/grpcview/service/wsroot"
)

func (w Workspace) applyRequestMiddleware(ctx context.Context, workspaceName string, path []string, itemName, service string, target *grpcviewv1.Server, bodies []string, md *structpb.Struct, params map[string]any) ([]string, *structpb.Struct, error) {
	if itemName == "" {
		return bodies, md, nil
	}
	specifiers, err := w.loadAttachedMiddleware(ctx, workspaceName, path, itemName)
	if err != nil {
		return nil, nil, err
	}
	if len(specifiers) == 0 {
		return bodies, md, nil
	}
	if w.engine == nil {
		return nil, nil, connect.NewError(connect.CodeFailedPrecondition,
			errors.New("middleware requires the scripting engine, which is not available"))
	}
	coll, err := w.store.Open(ctx, workspaceName)
	if err != nil {
		return nil, nil, toConnectError(err)
	}
	collRoot := coll.Root()

	chain := make([]middlewareScript, len(specifiers))
	for i, specifier := range specifiers {
		src, rerr := loadMiddlewareSource(specifier, w.store.Root(), collRoot)
		if rerr != nil {
			return nil, nil, rerr
		}
		chain[i] = middlewareScript{name: specifier, source: src}
	}

	targetStr := ""
	if resolved, terr := w.resolveTarget(ctx, target, workspaceName, service); terr == nil {
		targetStr = resolved.GetAddress()
	}

	mdMap := mdToStringMap(md)
	out := make([]string, len(bodies))
	for i, body := range bodies {
		body = strings.TrimSpace(body)
		if body == "" {
			body = "{}"
		}
		if !json.Valid([]byte(body)) {
			return nil, nil, connect.NewError(connect.CodeInvalidArgument,
				fmt.Errorf("invalid request body [%d]: not valid JSON, cannot run middleware", i))
		}
		cur := json.RawMessage(body)
		for _, mw := range chain {
			res, rerr := w.engine.RunMiddleware(ctx, mw.source, scripting.Grant{}, scripting.Input{
				Request:        scripting.RequestInput{Body: cur, Metadata: mdMap, Target: targetStr},
				Params:         params,
				CollectionRoot: collRoot,
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

// name is the specifier as the author wrote it (`#/...` or `@/...`), which is what
// middlewareError reports — a better error subject than a display name ever was.
type middlewareScript struct {
	name   string
	source string
}

func (w Workspace) loadAttachedMiddleware(ctx context.Context, workspaceName string, path []string, itemName string) ([]string, error) {
	coll, err := w.store.Open(ctx, workspaceName)
	if err != nil {
		return nil, err
	}
	specifiers, err := coll.RequestMiddleware(ctx, path, itemName)
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrItemNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, toConnectError(err)
	}
	return specifiers, nil
}

// resolveMiddlewareSpecifier mirrors decisions.md §2's two sigils: `@/` resolves against the
// workspace root, `#/` against the collection root, each confined to its root (wsroot.Confine).
func resolveMiddlewareSpecifier(specifier, workspaceRoot, collectionRoot string) (string, error) {
	var root, which string
	switch {
	case strings.HasPrefix(specifier, "@/"):
		root, which = workspaceRoot, "workspace"
	case strings.HasPrefix(specifier, "#/"):
		root, which = collectionRoot, "collection"
	default:
		return "", specifierError(specifier, `must start with "@/" or "#/"`)
	}
	if root == "" {
		return "", specifierError(specifier, "the "+which+" root is not configured")
	}
	real, err := wsroot.Confine(root, filepath.Join(root, filepath.FromSlash(specifier[2:])))
	if errors.Is(err, wsroot.ErrEscapes) {
		return "", specifierError(specifier, "resolves outside the "+which)
	}
	if err != nil {
		return "", specifierError(specifier, err.Error())
	}
	return real, nil
}

func loadMiddlewareSource(specifier, workspaceRoot, collectionRoot string) (string, error) {
	abs, err := resolveMiddlewareSpecifier(specifier, workspaceRoot, collectionRoot)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", specifierError(specifier, "no such file")
	}
	return string(data), nil
}

func specifierError(specifier, detail string) error {
	return connect.NewError(connect.CodeFailedPrecondition,
		fmt.Errorf("cannot resolve middleware %q: %s", specifier, detail))
}

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

func middlewareError(name, detail string) error {
	return connect.NewError(connect.CodeFailedPrecondition,
		fmt.Errorf("middleware %q failed: %s", name, detail))
}
