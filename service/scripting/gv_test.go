package scripting

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestGrpcviewInvokeModuleForm(t *testing.T) {
	e := newEngine(t)
	src := `import { invoke } from "grpcview:invoke";
await invoke('a/b', {x:1})
  .then(() => "resolved")
  .catch(e => "caught:" + String(e && e.message || e))`
	res, err := e.RunScenario(context.Background(), src, Grant{}, Input{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := `"caught:invoke is not available in this context"`
	if string(res.Value) != want {
		t.Fatalf("value = %s, want %s", res.Value, want)
	}
}

func TestGrpcviewInvokeExpressionForm(t *testing.T) {
	e := newEngine(t)
	body := WrapExpression(`require("grpcview:invoke").invoke('a/b', {x:1})
  .then(() => "resolved")
  .catch(e => "caught:" + String(e && e.message || e))`)
	res, err := e.RunRequestBody(context.Background(), body, Grant{}, Input{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := `"caught:invoke is not available in this context"`
	if string(res.Value) != want {
		t.Fatalf("value = %s, want %s", res.Value, want)
	}
}

func TestGrpcviewInvokeStubRoundTrip(t *testing.T) {
	e := newEngine(t)

	var gotReq []byte
	stub := Invoker(func(_ context.Context, req []byte) ([]byte, error) {
		gotReq = append([]byte(nil), req...)
		return []byte(`{"ok":true,"status":{"code":0,"message":""},"body":{"echo":"stub"},` +
			`"metadata":{},"requestMetadata":{},"latencyMs":1}`), nil
	})
	ctx := WithInvoker(context.Background(), stub)

	src := `import { invoke } from "grpcview:invoke"; await invoke('a/b', {x:1})`
	res, err := e.RunScenario(ctx, src, Grant{}, Input{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	var got struct {
		OK   bool `json:"ok"`
		Body struct {
			Echo string `json:"echo"`
		} `json:"body"`
	}
	if err := json.Unmarshal(res.Value, &got); err != nil {
		t.Fatalf("decode resolved value %s: %v", res.Value, err)
	}
	if !got.OK || got.Body.Echo != "stub" {
		t.Fatalf("resolved value = %s, want the stub's InvokeResult verbatim", res.Value)
	}

	var envelope struct {
		Path   string         `json:"path"`
		Params map[string]any `json:"params"`
	}
	if err := json.Unmarshal(gotReq, &envelope); err != nil {
		t.Fatalf("decode request envelope %s: %v", gotReq, err)
	}
	if envelope.Path != "a/b" || envelope.Params["x"] != float64(1) {
		t.Fatalf("request envelope = %+v, want path=\"a/b\" params={\"x\":1}", envelope)
	}
}

func TestGrpcviewInvokeStubDefaultsParams(t *testing.T) {
	e := newEngine(t)
	var gotReq []byte
	stub := Invoker(func(_ context.Context, req []byte) ([]byte, error) {
		gotReq = append([]byte(nil), req...)
		return []byte(`{"ok":true}`), nil
	})
	ctx := WithInvoker(context.Background(), stub)

	_, err := e.RunScenario(ctx, `import { invoke } from "grpcview:invoke"; await invoke('solo')`, Grant{}, Input{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	var envelope struct {
		Path   string         `json:"path"`
		Params map[string]any `json:"params"`
	}
	if err := json.Unmarshal(gotReq, &envelope); err != nil {
		t.Fatalf("decode request envelope %s: %v", gotReq, err)
	}
	if envelope.Path != "solo" || len(envelope.Params) != 0 {
		t.Fatalf("request envelope = %+v, want path=\"solo\" params={}", envelope)
	}
}

func TestGrpcviewInvokeStubRejects(t *testing.T) {
	e := newEngine(t)
	stub := Invoker(func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, errors.New("boom: no such request")
	})
	ctx := WithInvoker(context.Background(), stub)

	src := `import { invoke } from "grpcview:invoke";
await invoke('missing/thing').then(() => "resolved").catch(e => "caught:" + String(e && e.message || e))`
	res, err := e.RunScenario(ctx, src, Grant{}, Input{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := `"caught:boom: no such request"`
	if string(res.Value) != want {
		t.Fatalf("value = %s, want %s", res.Value, want)
	}
}

func TestGrpcviewAssertModuleFormPasses(t *testing.T) {
	e := newEngine(t)
	src := `import { assert } from "grpcview:assert";
assert("a bool", true);
assert("a sync fn", function () { return 1 < 2; });
assert("truthiness, not === true", "non-empty");
const r = assert("returns undefined", true);
({ ok: true, returned: r === undefined })`
	res, err := e.RunScenario(context.Background(), src, Grant{}, Input{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := `{"ok":true,"returned":true}`
	if string(res.Value) != want {
		t.Fatalf("value = %s, want %s", res.Value, want)
	}
	if len(res.Logs) != 0 {
		t.Fatalf("logs = %+v, want none (silence is a pass)", res.Logs)
	}
}

func TestGrpcviewAssertExpressionFormPasses(t *testing.T) {
	e := newEngine(t)
	body := WrapExpression(`(require("grpcview:assert").assert("d", true), "passed")`)
	res, err := e.RunRequestBody(context.Background(), body, Grant{}, Input{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if string(res.Value) != `"passed"` {
		t.Fatalf("value = %s, want \"passed\"", res.Value)
	}
}

func TestGrpcviewAssertFailsSync(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{
			"falsy-bool", `import { assert } from "grpcview:assert"; assert("bool is false", false)`,
			"assertion failed: bool is false",
		},
		{
			"falsy-fn", `import { assert } from "grpcview:assert"; assert("fn is false", function () { return false; })`,
			"assertion failed: fn is false",
		},
		{
			"throwing-fn", `import { assert } from "grpcview:assert";
assert("fn blew up", function () { throw new Error("inner boom"); })`,
			"assertion failed: fn blew up: inner boom",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEngine(t)
			_, err := e.RunScenario(context.Background(), tc.src, Grant{}, Input{})
			var je *JSError
			if !errors.As(err, &je) {
				t.Fatalf("got %v, want *JSError", err)
			}
			if !strings.Contains(je.Message, tc.want) {
				t.Fatalf("message = %q, want it to contain %q", je.Message, tc.want)
			}
			if !strings.Contains(je.Message, "AssertionError") {
				t.Fatalf("message = %q, want it to name AssertionError", je.Message)
			}
		})
	}
}

func TestGrpcviewAssertReportsTheCallersLineModuleForm(t *testing.T) {
	e := newEngine(t)
	src := "import { assert } from \"grpcview:assert\";\n" +
		"const a = 1;\nconst b = 2;\nassert(\"a equals b\", a === b);\n"
	_, err := e.RunScenario(context.Background(), src, Grant{}, Input{})
	var je *JSError
	if !errors.As(err, &je) {
		t.Fatalf("got %v, want *JSError", err)
	}
	// Line 1 is the import, so the failing call is on line 4. The throw happens inside the
	// bundled grpcview:assert module, so an unfiltered stack would report a line inside that
	// module instead of the caller's own line.
	if je.Line != 4 {
		t.Fatalf("line = %d, want 4 (the failing assertion's own line, stack=%q)", je.Line, je.Stack)
	}
}

func TestGrpcviewAssertReportsTheCallersLineExpressionForm(t *testing.T) {
	e := newEngine(t)
	src := "(() => {\n" +
		"  const a = 1;\n" +
		"  const b = 2;\n" +
		"  require(\"grpcview:assert\").assert(\"a equals b\", a === b);\n" +
		"})()"
	body := WrapExpression(src)
	_, err := e.RunRequestBody(context.Background(), body, Grant{}, Input{})
	var je *JSError
	if !errors.As(err, &je) {
		t.Fatalf("got %v, want *JSError", err)
	}
	// WrapExpression opens no new line, so the IIFE's own first line stays line 1 of the
	// wrapped source; the failing call sits on line 4.
	if je.Line != 4 {
		t.Fatalf("line = %d, want 4 (the failing assertion's own line, stack=%q)", je.Line, je.Stack)
	}
}

func TestGrpcviewAssertSyncFailureIsNotAPromise(t *testing.T) {
	e := newEngine(t)
	src := `import { assert } from "grpcview:assert";
let threwSynchronously = false;
try { assert("sync throw", false); } catch (e) { threwSynchronously = e.name === "AssertionError"; }
({ threwSynchronously })`
	res, err := e.RunScenario(context.Background(), src, Grant{}, Input{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if string(res.Value) != `{"threwSynchronously":true}` {
		t.Fatalf("value = %s, want a synchronous throw (an unawaited rejection would be dropped)", res.Value)
	}
}

func TestGrpcviewAssertAsync(t *testing.T) {
	e := newEngine(t)
	res, err := e.RunScenario(context.Background(),
		`import { assert } from "grpcview:assert";
await assert("async true", async () => true); "done"`, Grant{}, Input{})
	if err != nil {
		t.Fatalf("async true run: %v", err)
	}
	if string(res.Value) != `"done"` {
		t.Fatalf("async true value = %s, want \"done\"", res.Value)
	}

	for _, tc := range []struct{ name, src, want string }{
		{
			"async-false", `import { assert } from "grpcview:assert"; await assert("async is false", async () => false)`,
			"assertion failed: async is false",
		},
		{
			"bare-promise-false", `import { assert } from "grpcview:assert";
await assert("promise is false", Promise.resolve(false))`,
			"assertion failed: promise is false",
		},
		{
			"async-rejects", `import { assert } from "grpcview:assert";
await assert("async blew up", async () => { throw new Error("async boom"); })`,
			"assertion failed: async blew up: async boom",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEngine(t)
			_, err := e.RunScenario(context.Background(), tc.src, Grant{}, Input{})
			var je *JSError
			if !errors.As(err, &je) {
				t.Fatalf("got %v, want *JSError", err)
			}
			if !strings.Contains(je.Message, tc.want) {
				t.Fatalf("message = %q, want it to contain %q", je.Message, tc.want)
			}
		})
	}
}

func TestGrpcviewAssertBadDescription(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"missing", `import { assert } from "grpcview:assert"; assert()`},
		{"non-string", `import { assert } from "grpcview:assert"; assert(42, true)`},
		{"empty", `import { assert } from "grpcview:assert"; assert("", true)`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEngine(t)
			_, err := e.RunScenario(context.Background(), tc.src, Grant{}, Input{})
			var je *JSError
			if !errors.As(err, &je) {
				t.Fatalf("got %v, want *JSError", err)
			}
			if !strings.Contains(je.Message, "TypeError") ||
				!strings.Contains(je.Message, "description must be a non-empty string") {
				t.Fatalf("message = %q, want a TypeError naming description", je.Message)
			}
		})
	}
}

func TestGrpcviewAssertWorksInMiddleware(t *testing.T) {
	e := newEngine(t)
	_, err := e.RunMiddleware(context.Background(),
		`export function handle(ctx) { require("grpcview:assert").assert("mw check", false); return ctx; }`,
		Grant{}, Input{})
	var je *JSError
	if !errors.As(err, &je) {
		t.Fatalf("got %v, want *JSError", err)
	}
	if !strings.Contains(je.Message, "assertion failed: mw check") {
		t.Fatalf("message = %q, want it to contain the assertion failure", je.Message)
	}
}

func TestGrpcviewMetadataInherit(t *testing.T) {
	e := newEngine(t)
	src := `import { inherit } from "grpcview:metadata";
({ inherited: inherit(), frozen: Object.isFrozen(inherit()) })`

	t.Run("no inheritance context", func(t *testing.T) {
		res, err := e.RunScenario(context.Background(), src, Grant{}, Input{})
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		want := `{"inherited":{},"frozen":true}`
		if string(res.Value) != want {
			t.Fatalf("value = %s, want %s", res.Value, want)
		}
	})

	t.Run("with inherited metadata", func(t *testing.T) {
		in := Input{InheritedMetadata: map[string][]string{"authorization": {"Bearer tkn"}}}
		res, err := e.RunScenario(context.Background(), src, Grant{}, in)
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		want := `{"inherited":{"authorization":["Bearer tkn"]},"frozen":true}`
		if string(res.Value) != want {
			t.Fatalf("value = %s, want %s", res.Value, want)
		}
	})
}

func TestGrpcviewMetadataInheritExpressionForm(t *testing.T) {
	e := newEngine(t)
	body := WrapExpression(`require("grpcview:metadata").inherit()`)

	res, err := e.RunRequestBody(context.Background(), body, Grant{}, Input{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if string(res.Value) != `{}` {
		t.Fatalf("value = %s, want {}", res.Value)
	}

	in := Input{InheritedMetadata: map[string][]string{"authorization": {"Bearer tkn"}}}
	res, err = e.RunRequestBody(context.Background(), body, Grant{}, in)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := `{"authorization":["Bearer tkn"]}`
	if string(res.Value) != want {
		t.Fatalf("value = %s, want %s", res.Value, want)
	}
}

func TestGrpcviewRequestParams(t *testing.T) {
	e := newEngine(t)
	src := `import { params } from "grpcview:request";
({ params, frozen: Object.isFrozen(params) })`

	t.Run("plain run", func(t *testing.T) {
		res, err := e.RunScenario(context.Background(), src, Grant{}, Input{})
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		want := `{"params":{},"frozen":true}`
		if string(res.Value) != want {
			t.Fatalf("value = %s, want %s", res.Value, want)
		}
	})

	t.Run("with params", func(t *testing.T) {
		in := Input{Params: map[string]any{"id": float64(7), "name": "bob"}}
		res, err := e.RunScenario(context.Background(), src, Grant{}, in)
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		want := `{"params":{"id":7,"name":"bob"},"frozen":true}`
		if string(res.Value) != want {
			t.Fatalf("value = %s, want %s", res.Value, want)
		}
	})
}

func TestGrpcviewRequestParamsExpressionForm(t *testing.T) {
	e := newEngine(t)
	body := WrapExpression(`require("grpcview:request").params`)

	res, err := e.RunRequestBody(context.Background(), body, Grant{}, Input{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if string(res.Value) != `{}` {
		t.Fatalf("value = %s, want {}", res.Value)
	}

	in := Input{Params: map[string]any{"id": float64(7), "name": "bob"}}
	res, err = e.RunRequestBody(context.Background(), body, Grant{}, in)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := `{"id":7,"name":"bob"}`
	if string(res.Value) != want {
		t.Fatalf("value = %s, want %s", res.Value, want)
	}
}

func TestGrpcviewUnknownModuleIsResolveError(t *testing.T) {
	e := newEngine(t)
	_, err := e.RunScenario(context.Background(),
		`import x from "grpcview:whatever"; x`, Grant{}, Input{})
	if err == nil {
		t.Fatal("importing an unknown grpcview: module: got nil error, want a resolve error")
	}
	if !strings.Contains(err.Error(), "grpcview:whatever") {
		t.Fatalf("error = %q, want it to name the unresolved specifier", err.Error())
	}
}
