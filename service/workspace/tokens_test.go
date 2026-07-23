package workspace

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/structpb"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/scripting"
	"codeberg.org/ramilmsh/grpcview/service/store"
)

// newTestWorkspaceWithEngine is newTestWorkspace plus a real scripting engine, needed by
// the token-resolution tests (the engine compile is the expensive step, so a test builds
// one and reuses it across its subtests). The engine is torn down on cleanup.
func newTestWorkspaceWithEngine(t *testing.T) Workspace {
	t.Helper()
	eng, err := scripting.NewEngine(context.Background(), scriptingMaxPages)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	t.Cleanup(func() { _ = eng.Close(context.Background()) })
	return Workspace{
		store:  store.New(t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil))),
		engine: eng,
	}
}

// createGenerator saves a GENERATOR script through the store, the way the other workspace
// tests set up requests (create then patch source).
func createGenerator(t *testing.T, w Workspace, ctx context.Context, name, source string) {
	t.Helper()
	coll, err := w.store.Open(ctx, testWorkspace)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := coll.CreateScript(ctx, name, grpcviewv1.ScriptKind_SCRIPT_KIND_GENERATOR); err != nil {
		t.Fatalf("CreateScript %q: %v", name, err)
	}
	if err := coll.UpdateScript(ctx, name, store.ScriptPatch{Source: &source}); err != nil {
		t.Fatalf("UpdateScript %q: %v", name, err)
	}
}

// TestScanTokens covers the grammar: bare + dotted names, JSON-literal args (scalar and
// composite), non-grammar "{{…}}" left literal, and a token-free string yielding nothing.
func TestScanTokens(t *testing.T) {
	toks := scanTokens(`{"a": {{ uuid() }}, "b": {{ now("-24h") }}, "c": {{ auth.bearer }}}`)
	if len(toks) != 3 {
		t.Fatalf("want 3 tokens, got %d: %+v", len(toks), toks)
	}
	if toks[0].name != "uuid" || len(toks[0].args) != 0 {
		t.Errorf("token[0] = %+v, want uuid()", toks[0])
	}
	if toks[1].name != "now" || len(toks[1].args) != 1 || toks[1].args[0] != "-24h" {
		t.Errorf("token[1] = %+v, want now(\"-24h\")", toks[1])
	}
	if toks[2].name != "auth.bearer" || len(toks[2].args) != 0 {
		t.Errorf("token[2] = %+v, want auth.bearer", toks[2])
	}
	// Multiple JSON-literal args (number, string, object).
	multi := scanTokens(`{{ f(1, "x", {"k":true}) }}`)
	if len(multi) != 1 || multi[0].name != "f" || len(multi[0].args) != 3 {
		t.Fatalf("multi-arg token = %+v, want f(1,\"x\",{...})", multi)
	}
	// Non-grammar inner text is left literal (not a token).
	if got := scanTokens(`{{ not a token }}`); len(got) != 0 {
		t.Errorf("non-grammar {{…}} should be literal, got %+v", got)
	}
	// A token-free string yields no tokens.
	if got := scanTokens(`{"x": 1, "y": "a } b {"}`); len(got) != 0 {
		t.Errorf("token-free string yielded %+v", got)
	}
}

// TestResolveBodyTokens covers value-position body substitution: string vs object vs number
// results, args passing, multiple tokens, byte-identical passthrough, and the three error
// modes (unknown generator, throwing generator, undefined result that cannot splice).
func TestResolveBodyTokens(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	gens := map[string]string{
		"greeting": `export default () => "hi"`,
		"obj":      `export default () => ({ a: 1, b: [2, 3] })`,
		"addOne":   `export default (n) => n + 1`,
		"boom":     `export default () => { throw new Error("nope") }`,
		"nothing":  `export default () => undefined`,
	}
	cases := []struct {
		name, in, want string
	}{
		{"string result splices as quoted JSON", `{"m": {{ greeting() }}}`, `{"m": "hi"}`},
		{"object result splices as raw JSON", `{"o": {{ obj() }}}`, `{"o": {"a":1,"b":[2,3]}}`},
		{"args passed to generator", `{"n": {{ addOne(41) }}}`, `{"n": 42}`},
		{"multiple tokens", `{"a": {{ greeting() }}, "n": {{ addOne(1) }}}`, `{"a": "hi", "n": 2}`},
		{"no tokens passes through byte-identical", `{"a": 1, "b": "text with } braces {"}`, `{"a": 1, "b": "text with } braces {"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := w.resolveBodyTokens(ctx, gens, c.in)
			if err != nil {
				t.Fatalf("resolveBodyTokens: %v", err)
			}
			if got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}

	for _, c := range []struct{ name, in string }{
		{"unknown generator", `{"x": {{ missing() }}}`},
		{"throwing generator", `{"x": {{ boom() }}}`},
		{"undefined result cannot splice", `{"x": {{ nothing() }}}`},
	} {
		t.Run(c.name+" errors FailedPrecondition", func(t *testing.T) {
			if _, err := w.resolveBodyTokens(ctx, gens, c.in); connect.CodeOf(err) != connect.CodeFailedPrecondition {
				t.Fatalf("code = %v, want FailedPrecondition (err=%v)", connect.CodeOf(err), err)
			}
		})
	}
}

// TestResolveMetadataTokens covers whole-value metadata substitution: a string result is
// unquoted, a number is coerced to its text, a non-token value and an embedded (non-whole)
// token are untouched, the input Struct is not mutated, and an unknown generator errors.
func TestResolveMetadataTokens(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	gens := map[string]string{
		"bearer": `export default () => "Bearer xyz"`,
		"num":    `export default () => 7`,
	}
	md, err := structpb.NewStruct(map[string]any{
		"authorization": "{{ bearer() }}",
		"x-num":         "{{ num() }}",
		"x-plain":       "literal",
		"x-partial":     "prefix {{ bearer() }}", // embedded, not the whole value
	})
	if err != nil {
		t.Fatalf("NewStruct: %v", err)
	}
	out, err := w.resolveMetadataTokens(ctx, gens, md)
	if err != nil {
		t.Fatalf("resolveMetadataTokens: %v", err)
	}
	if got := out.GetFields()["authorization"].GetStringValue(); got != "Bearer xyz" {
		t.Errorf("authorization = %q, want Bearer xyz", got)
	}
	if got := out.GetFields()["x-num"].GetStringValue(); got != "7" {
		t.Errorf("x-num = %q, want 7 (number coerced to text)", got)
	}
	if got := out.GetFields()["x-plain"].GetStringValue(); got != "literal" {
		t.Errorf("x-plain = %q, want literal (unchanged)", got)
	}
	if got := out.GetFields()["x-partial"].GetStringValue(); got != "prefix {{ bearer() }}" {
		t.Errorf("x-partial = %q, want unchanged (embedded token not resolved)", got)
	}
	// The input Struct must not be mutated in place.
	if got := md.GetFields()["authorization"].GetStringValue(); got != "{{ bearer() }}" {
		t.Errorf("input metadata mutated: authorization = %q", got)
	}

	bad, _ := structpb.NewStruct(map[string]any{"x": "{{ nope() }}"})
	if _, err := w.resolveMetadataTokens(ctx, gens, bad); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("unknown metadata generator code = %v, want FailedPrecondition", connect.CodeOf(err))
	}
}

// TestInvokeResolvesTokens is the end-to-end unary path: a generator saved via the store is
// referenced from both the body (value position, with an arg) and a metadata value, and the
// echo server confirms the resolved values were actually sent.
func TestInvokeResolvesTokens(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	createGenerator(t, w, ctx, "greeting", `export default (who) => "hello " + who`)
	createGenerator(t, w, ctx, "bearer", `export default () => "Bearer tok"`)

	port := startEchoServer(t)
	md, _ := structpb.NewStruct(map[string]any{"authorization": "{{ bearer() }}"})
	resp, err := w.Invoke(ctx, connect.NewRequest(&grpcviewv1.InvokeRequest{
		WorkspaceName: testWorkspace,
		Service:       echoService,
		Method:        "Unary",
		Body:          `{"message": {{ greeting("world") }}}`,
		Metadata:      md,
		Target:        &grpcviewv1.Server{Host: "127.0.0.1", Port: int32(port)},
	}))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if code := resp.Msg.GetResponse().GetStatus().GetCode(); code != int32(codeOK) {
		t.Fatalf("status = %d (%s)", code, resp.Msg.GetResponse().GetStatus().GetMessage())
	}
	// Echo replies "echo: " + message; the body token resolved to "hello world".
	var payload struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(resp.Msg.GetResponse().GetResponse(), &payload); err != nil {
		t.Fatalf("unmarshal echo response: %v", err)
	}
	if payload.Message != "echo: hello world" {
		t.Fatalf("echoed message = %q, want %q", payload.Message, "echo: hello world")
	}
	// The metadata token resolved on the sent request (reflected in RequestMetadata).
	if got := resp.Msg.GetResponse().GetRequestMetadata().GetFields()["authorization"].GetStringValue(); got != "Bearer tok" {
		t.Fatalf("request metadata authorization = %q, want Bearer tok", got)
	}
}

// TestStreamInvokeResolvesTokens is the end-to-end streaming path: the same resolver runs
// pre-send for InvokeStreaming, so a body token resolves before the server-streaming call.
func TestStreamInvokeResolvesTokens(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	createGenerator(t, w, ctx, "greeting", `export default () => "streamed"`)

	port := startEchoServer(t)
	msg := echoStreamReq(port, "ServerStream", `{"message": {{ greeting() }}, "count": 2}`)
	frames, err := collectStream(ctx, w, msg)
	if err != nil {
		t.Fatalf("streamInvoke: %v", err)
	}
	msgs, result := splitFrames(t, frames)
	if code := result.GetStatus().GetCode(); code != int32(codeOK) {
		t.Fatalf("status = %d (%s)", code, result.GetStatus().GetMessage())
	}
	if len(msgs) != 2 {
		t.Fatalf("want 2 message frames (count:2), got %d", len(msgs))
	}
	for i, m := range msgs {
		var payload struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal(m, &payload); err != nil {
			t.Fatalf("frame %d not JSON: %v", i, err)
		}
		if !strings.Contains(payload.Message, "streamed") {
			t.Fatalf("frame %d message = %q, want it to contain the resolved 'streamed'", i, payload.Message)
		}
	}
}
