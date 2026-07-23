package workspace

// tokens.go resolves `{{ generator(args?) }}` tokens in a request's body and metadata by
// running the referenced GENERATOR scripts on the invoke path, pre-send (scripting-ui-plan
// §S2 — the "use scripts (values)" half). resolveInvokeTokens is the shared pre-send step
// for both unary Invoke and streaming InvokeStreaming: each reads its body/metadata, calls
// it, then sends the rewritten values.
//
// Grammar: `{{ name(args?) }}` where name is a generator's (possibly dotted) display name
// and the optional parenthesized args are comma-separated JSON literals passed as the
// generator's positional arguments (Input.Args) — e.g. `{{ uuid() }}`, `{{ now("-24h") }}`,
// `{{ auth.bearer }}`. Recognition is textual and best-effort (like the engine's entry.go
// export scan): a `{{ … }}` whose inner text does not parse as name(args?) — including args
// that are not valid JSON — is left as literal text.
//
// Substitution:
//   - Body — a token sits in JSON value position; it is replaced by the JSON encoding of the
//     generator's return value (Result.Value is already raw JSON: a string result splices as
//     a quoted JSON string, an object/number/bool as raw JSON), so a body of valid JSON with
//     tokens stays valid JSON. The body text is rewritten BEFORE it is parsed into the
//     request message. A body with no tokens is returned byte-identical.
//   - Metadata — a value that is EXACTLY one token is replaced by the generator's result
//     coerced to a string (a JSON-string result is unquoted; any other value uses its JSON
//     text). An embedded token in a longer metadata value is left as-is.
//
// Generators run UNCACHED (RunGeneratorUncached) so `uuid()`/`now()` vary per invoke, fully
// sandboxed (empty Grant, no vars/secrets/env, no invoke() into other requests — those are
// deferred to S4/S6). An unknown generator name, a generator that throws, or a result that
// cannot splice (undefined) is a Connect FailedPrecondition naming the offending token.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"strings"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/structpb"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/scripting"
	"codeberg.org/ramilmsh/grpcview/service/store"
)

// tok is one recognized `{{ … }}` token and its position in the scanned string.
type tok struct {
	raw   string // the full "{{…}}" substring, for error messages
	name  string // the (possibly dotted) generator display name
	args  []any  // parsed positional args (nil when the token has none)
	start int    // byte offset of the leading "{{"
	end   int    // byte offset just past the trailing "}}"
}

// tokenGrammarRe matches a token's trimmed inner text: a dotted identifier optionally
// followed by a parenthesized argument list. The args group keeps its surrounding parens.
var tokenGrammarRe = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*)\s*(\([\s\S]*\))?$`)

// scanTokens returns every recognized token in s, in order. It is textual and never errors:
// a "{{" without a balanced "}}", or a "{{ … }}" whose inner text is not name(args?)
// (including args that are not valid JSON), is skipped as literal text.
func scanTokens(s string) []tok {
	var toks []tok
	for i := 0; i+1 < len(s); {
		if s[i] == '{' && s[i+1] == '{' {
			if ci := tokenClose(s, i+2); ci >= 0 {
				if t, ok := parseTokenBody(s[i+2 : ci]); ok {
					t.raw = s[i : ci+2]
					t.start = i
					t.end = ci + 2
					toks = append(toks, t)
					i = ci + 2
					continue
				}
			}
		}
		i++
	}
	return toks
}

// tokenClose returns the index of the "}" that opens the "}}" closing a token whose inner
// text starts at from, or -1 if there is none. A "}}" inside a JSON string literal in the
// args (e.g. `{{ f("}}") }}`) does not close the token.
func tokenClose(s string, from int) int {
	inString, escaped := false, false
	for i := from; i < len(s); i++ {
		c := s[i]
		switch {
		case escaped:
			escaped = false
		case inString && c == '\\':
			escaped = true
		case c == '"':
			inString = !inString
		case !inString && c == '}' && i+1 < len(s) && s[i+1] == '}':
			return i
		}
	}
	return -1
}

// parseTokenBody parses a token's inner text (between "{{" and "}}") into a tok's name+args.
// It returns ok=false when the text is not name(args?) or the args are not valid JSON, so
// scanTokens can leave the surrounding "{{ … }}" as literal text.
func parseTokenBody(inner string) (tok, bool) {
	m := tokenGrammarRe.FindStringSubmatch(strings.TrimSpace(inner))
	if m == nil {
		return tok{}, false
	}
	t := tok{name: m[1]}
	if argsPart := m[2]; argsPart != "" {
		if body := strings.TrimSpace(argsPart[1 : len(argsPart)-1]); body != "" {
			// Comma-separated JSON values are the elements of a JSON array literal.
			if err := json.Unmarshal([]byte("["+body+"]"), &t.args); err != nil {
				return tok{}, false
			}
		}
	}
	return t, true
}

// resolveInvokeTokens is the shared pre-send token-resolution step for unary Invoke (one
// body) and InvokeStreaming (many). It rewrites the `{{ … }}` tokens in every body and in
// metadata values that are exactly a token, returning the rewritten bodies and metadata.
// The workspace's generators are loaded at most once, and only when a token marker is
// actually present — an invoke with no tokens does no script load and returns its bodies +
// metadata unchanged (the metadata Struct is never mutated in place). Failures are Connect
// FailedPrecondition naming the offending token.
func (w Workspace) resolveInvokeTokens(ctx context.Context, workspaceName string, bodies []string, md *structpb.Struct) ([]string, *structpb.Struct, error) {
	if !hasTokenMarker(bodies, md) {
		return bodies, md, nil
	}
	if w.engine == nil {
		return nil, nil, connect.NewError(connect.CodeFailedPrecondition,
			errors.New("token resolution requires the scripting engine, which is not available"))
	}
	gens, err := w.loadGenerators(ctx, workspaceName)
	if err != nil {
		return nil, nil, err
	}
	out := make([]string, len(bodies))
	for i, body := range bodies {
		if out[i], err = w.resolveBodyTokens(ctx, gens, body); err != nil {
			return nil, nil, err
		}
	}
	mdOut, err := w.resolveMetadataTokens(ctx, gens, md)
	if err != nil {
		return nil, nil, err
	}
	return out, mdOut, nil
}

// loadGenerators reads the workspace's committed scripts and returns a map from a
// generator's display name to its source. A collection that does not exist yet yields an
// empty map (a token then fails as "no generator", not as a missing workspace).
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

// resolveBodyTokens replaces every token in body with the JSON encoding of its generator's
// return value. A body with no tokens is returned byte-identical.
func (w Workspace) resolveBodyTokens(ctx context.Context, gens map[string]string, body string) (string, error) {
	toks := scanTokens(body)
	if len(toks) == 0 {
		return body, nil
	}
	var b strings.Builder
	last := 0
	for _, t := range toks {
		res, err := w.runToken(ctx, gens, t)
		if err != nil {
			return "", err
		}
		if len(res.Value) == 0 {
			return "", tokenError(t, "generator returned no value")
		}
		b.WriteString(body[last:t.start])
		b.Write(res.Value) // raw JSON: string→quoted, object/number/bool→raw
		last = t.end
	}
	b.WriteString(body[last:])
	return b.String(), nil
}

// resolveMetadataTokens returns md with any value that is EXACTLY one token replaced by its
// generator's result coerced to a string. md is not mutated: a new Struct is built only if
// some value changes, otherwise md is returned as-is.
func (w Workspace) resolveMetadataTokens(ctx context.Context, gens map[string]string, md *structpb.Struct) (*structpb.Struct, error) {
	if len(md.GetFields()) == 0 {
		return md, nil
	}
	var out *structpb.Struct
	for key, val := range md.GetFields() {
		nv, changed, err := w.resolveMetadataValue(ctx, gens, val)
		if err != nil {
			return nil, err
		}
		if changed {
			if out == nil {
				out = &structpb.Struct{Fields: maps.Clone(md.GetFields())}
			}
			out.Fields[key] = nv
		}
	}
	if out == nil {
		return md, nil
	}
	return out, nil
}

// resolveMetadataValue resolves a single metadata value: a string that is exactly a token,
// or — for a repeated (list) value — each such string element. Any other value is unchanged.
func (w Workspace) resolveMetadataValue(ctx context.Context, gens map[string]string, v *structpb.Value) (*structpb.Value, bool, error) {
	switch k := v.GetKind().(type) {
	case *structpb.Value_StringValue:
		s, changed, err := w.resolveMetadataString(ctx, gens, k.StringValue)
		if err != nil || !changed {
			return v, false, err
		}
		return structpb.NewStringValue(s), true, nil
	case *structpb.Value_ListValue:
		items := k.ListValue.GetValues()
		resolved := make([]*structpb.Value, len(items))
		anyChanged := false
		for i, item := range items {
			nv, changed, err := w.resolveMetadataValue(ctx, gens, item)
			if err != nil {
				return nil, false, err
			}
			resolved[i] = nv
			anyChanged = anyChanged || changed
		}
		if !anyChanged {
			return v, false, nil
		}
		return structpb.NewListValue(&structpb.ListValue{Values: resolved}), true, nil
	default:
		return v, false, nil
	}
}

// resolveMetadataString resolves s when it is exactly one token, returning the generator
// result coerced to a string; otherwise s is returned unchanged (changed=false).
func (w Workspace) resolveMetadataString(ctx context.Context, gens map[string]string, s string) (string, bool, error) {
	t, ok := wholeToken(s)
	if !ok {
		return s, false, nil
	}
	res, err := w.runToken(ctx, gens, t)
	if err != nil {
		return "", false, err
	}
	coerced, err := coerceResultString(t, res.Value)
	if err != nil {
		return "", false, err
	}
	return coerced, true, nil
}

// runToken runs the generator a token references, uncached and fully sandboxed (empty Grant,
// no vars/secrets/env), passing the token's parsed args as the generator's positional
// arguments. An unknown name or a run failure (throw/timeout) is a FailedPrecondition
// naming the token.
func (w Workspace) runToken(ctx context.Context, gens map[string]string, t tok) (scripting.Result, error) {
	source, ok := gens[t.name]
	if !ok {
		return scripting.Result{}, tokenError(t, fmt.Sprintf("no generator named %q in this workspace", t.name))
	}
	res, err := w.engine.RunGeneratorUncached(ctx, source, scripting.Grant{}, scripting.Input{Args: t.args})
	if err != nil {
		return scripting.Result{}, tokenError(t, err.Error())
	}
	return res, nil
}

// wholeToken reports whether s (ignoring surrounding whitespace) is exactly one token.
func wholeToken(s string) (tok, bool) {
	trimmed := strings.TrimSpace(s)
	if toks := scanTokens(trimmed); len(toks) == 1 && toks[0].start == 0 && toks[0].end == len(trimmed) {
		return toks[0], true
	}
	return tok{}, false
}

// coerceResultString renders a generator's JSON result value as a metadata string: a JSON
// string is unquoted to its text, any other JSON value uses its raw text. An empty
// (undefined) result cannot be coerced and is an error.
func coerceResultString(t tok, value json.RawMessage) (string, error) {
	if len(value) == 0 {
		return "", tokenError(t, "generator returned no value")
	}
	var s string
	if err := json.Unmarshal(value, &s); err == nil {
		return s, nil
	}
	return string(value), nil
}

// tokenError renders a token-resolution failure as a Connect FailedPrecondition naming the
// offending token, matching Invoke's policy that a pre-send failure grpcview itself can't
// get past is a typed Connect error (scripting-ui-plan §S2).
func tokenError(t tok, detail string) error {
	return connect.NewError(connect.CodeFailedPrecondition,
		fmt.Errorf("cannot resolve token %s: %s", t.raw, detail))
}

// hasTokenMarker is a cheap pre-check: it reports whether any body or metadata value even
// contains a "{{", so an invoke with no tokens skips the script load entirely (and its
// bodies pass through byte-identical). A false positive (a literal "{{" that is not a token)
// merely triggers the load; scanTokens then finds nothing and the text is unchanged.
func hasTokenMarker(bodies []string, md *structpb.Struct) bool {
	for _, b := range bodies {
		if strings.Contains(b, "{{") {
			return true
		}
	}
	for _, v := range md.GetFields() {
		if valueHasTokenMarker(v) {
			return true
		}
	}
	return false
}

func valueHasTokenMarker(v *structpb.Value) bool {
	switch k := v.GetKind().(type) {
	case *structpb.Value_StringValue:
		return strings.Contains(k.StringValue, "{{")
	case *structpb.Value_ListValue:
		for _, e := range k.ListValue.GetValues() {
			if valueHasTokenMarker(e) {
				return true
			}
		}
	}
	return false
}
