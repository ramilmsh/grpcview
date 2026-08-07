package scripting

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestGvFrozenEmpty(t *testing.T) {
	e := newEngine(t)
	src := `({
  gvFrozen: Object.isFrozen(gv),
  requestFrozen: Object.isFrozen(gv.request),
  paramsFrozen: Object.isFrozen(gv.request.params),
  params: gv.request.params,
  metadataFrozen: Object.isFrozen(gv.metadata),
  inherited: gv.metadata.inherit(),
  inheritedFrozen: Object.isFrozen(gv.metadata.inherit()),
})`
	res, err := e.RunScenario(context.Background(), src, Grant{}, Input{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := `{"gvFrozen":true,"requestFrozen":true,"paramsFrozen":true,"params":{},` +
		`"metadataFrozen":true,"inherited":{},"inheritedFrozen":true}`
	if string(res.Value) != want {
		t.Fatalf("value = %s, want %s", res.Value, want)
	}
}

func TestGvFreezeBlocksMemberAddition(t *testing.T) {
	e := newEngine(t)
	src := `gv.newMember = "should not stick";
gv.invoke = null;
gv.metadata.inherit = null;
({
  hasNewMember: Object.prototype.hasOwnProperty.call(gv, "newMember"),
  invokeStillFunction: typeof gv.invoke === "function",
  inheritStillFunction: typeof gv.metadata.inherit === "function",
})`
	res, err := e.RunScenario(context.Background(), src, Grant{}, Input{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := `{"hasNewMember":false,"invokeStillFunction":true,"inheritStillFunction":true}`
	if string(res.Value) != want {
		t.Fatalf("value = %s, want %s", res.Value, want)
	}
}

func TestGvInvokeNoInvokerRejects(t *testing.T) {
	e := newEngine(t)
	src := `await gv.invoke('a/b', {x:1})
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

func TestGvRequestParamsAndInheritedMetadata(t *testing.T) {
	e := newEngine(t)
	in := Input{
		Params:            map[string]any{"id": float64(7), "name": "bob"},
		InheritedMetadata: map[string][]string{"authorization": {"Bearer tkn"}},
	}
	src := `({
  params: gv.request.params,
  paramsFrozen: Object.isFrozen(gv.request.params),
  inherited: gv.metadata.inherit(),
  inheritedFrozen: Object.isFrozen(gv.metadata.inherit()),
})`
	res, err := e.RunScenario(context.Background(), src, Grant{}, in)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := `{"params":{"id":7,"name":"bob"},"paramsFrozen":true,` +
		`"inherited":{"authorization":["Bearer tkn"]},"inheritedFrozen":true}`
	if string(res.Value) != want {
		t.Fatalf("value = %s, want %s", res.Value, want)
	}
}

func TestGvInvokeStubRoundTrip(t *testing.T) {
	e := newEngine(t)

	var gotReq []byte
	stub := Invoker(func(_ context.Context, req []byte) ([]byte, error) {
		gotReq = append([]byte(nil), req...)
		return []byte(`{"ok":true,"status":{"code":0,"message":""},"body":{"echo":"stub"},` +
			`"metadata":{},"requestMetadata":{},"latencyMs":1}`), nil
	})
	ctx := WithInvoker(context.Background(), stub)

	res, err := e.RunScenario(ctx, `await gv.invoke('a/b', {x:1})`, Grant{}, Input{})
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

func TestGvInvokeStubDefaultsParams(t *testing.T) {
	e := newEngine(t)
	var gotReq []byte
	stub := Invoker(func(_ context.Context, req []byte) ([]byte, error) {
		gotReq = append([]byte(nil), req...)
		return []byte(`{"ok":true}`), nil
	})
	ctx := WithInvoker(context.Background(), stub)

	_, err := e.RunScenario(ctx, `await gv.invoke('solo')`, Grant{}, Input{})
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

func TestGvAssertPasses(t *testing.T) {
	e := newEngine(t)
	src := `gv.assert("a bool", true);
gv.assert("a sync fn", function () { return 1 < 2; });
gv.assert("truthiness, not === true", "non-empty");
const r = gv.assert("returns undefined", true);
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

func TestGvAssertFailsSync(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"falsy-bool", `gv.assert("bool is false", false)`, "assertion failed: bool is false"},
		{"falsy-fn", `gv.assert("fn is false", function () { return false; })`, "assertion failed: fn is false"},
		{"throwing-fn", `gv.assert("fn blew up", function () { throw new Error("inner boom"); })`,
			"assertion failed: fn blew up: inner boom"},
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

func TestGvAssertReportsTheCallersLine(t *testing.T) {
	e := newEngine(t)
	src := "const a = 1;\nconst b = 2;\ngv.assert(\"a equals b\", a === b);\n"
	_, err := e.RunScenario(context.Background(), src, Grant{}, Input{})
	var je *JSError
	if !errors.As(err, &je) {
		t.Fatalf("got %v, want *JSError", err)
	}
	// The throw happens inside the prelude shim, so an unfiltered stack would report a prelude line.
	if je.Line != 3 {
		t.Fatalf("line = %d, want 3 (the failing assertion's own line)", je.Line)
	}
}

func TestGvAssertSyncFailureIsNotAPromise(t *testing.T) {
	e := newEngine(t)
	src := `let threwSynchronously = false;
try { gv.assert("sync throw", false); } catch (e) { threwSynchronously = e.name === "AssertionError"; }
({ threwSynchronously })`
	res, err := e.RunScenario(context.Background(), src, Grant{}, Input{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if string(res.Value) != `{"threwSynchronously":true}` {
		t.Fatalf("value = %s, want a synchronous throw (an unawaited rejection would be dropped)", res.Value)
	}
}

func TestGvAssertAsync(t *testing.T) {
	e := newEngine(t)
	res, err := e.RunScenario(context.Background(),
		`await gv.assert("async true", async () => true); "done"`, Grant{}, Input{})
	if err != nil {
		t.Fatalf("async true run: %v", err)
	}
	if string(res.Value) != `"done"` {
		t.Fatalf("async true value = %s, want \"done\"", res.Value)
	}

	for _, tc := range []struct{ name, src, want string }{
		{"async-false", `await gv.assert("async is false", async () => false)`,
			"assertion failed: async is false"},
		{"bare-promise-false", `await gv.assert("promise is false", Promise.resolve(false))`,
			"assertion failed: promise is false"},
		{"async-rejects", `await gv.assert("async blew up", async () => { throw new Error("async boom"); })`,
			"assertion failed: async blew up: async boom"},
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

func TestGvAssertBadDescription(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"missing", `gv.assert()`},
		{"non-string", `gv.assert(42, true)`},
		{"empty", `gv.assert("", true)`},
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

// gv.assert rides buildGvPrelude, which every profile's runCompiled calls — not a body-only path.
func TestGvAssertInEveryProfile(t *testing.T) {
	e := newEngine(t)
	probe := `({ present: typeof gv.assert === "function", frozenOut: (gv.assert = null, typeof gv.assert === "function") })`
	want := `{"present":true,"frozenOut":true}`

	t.Run("generator", func(t *testing.T) {
		res, err := e.RunRequestBody(context.Background(),
			"export default function () { return "+probe+"; }", nil, Grant{}, Input{})
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		if string(res.Value) != want {
			t.Fatalf("value = %s, want %s", res.Value, want)
		}
	})

	t.Run("middleware", func(t *testing.T) {
		res, err := e.RunMiddleware(context.Background(),
			"export function handle(ctx) { ctx.metadata.probe = JSON.stringify("+probe+"); return ctx; }",
			nil, Grant{}, Input{})
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		var out struct {
			Metadata struct {
				Probe string `json:"probe"`
			} `json:"metadata"`
		}
		if err := json.Unmarshal(res.Value, &out); err != nil {
			t.Fatalf("decode %s: %v", res.Value, err)
		}
		if out.Metadata.Probe != want {
			t.Fatalf("probe = %s, want %s", out.Metadata.Probe, want)
		}
	})

	t.Run("middleware-assertion-fails-the-run", func(t *testing.T) {
		_, err := e.RunMiddleware(context.Background(),
			`export function handle(ctx) { gv.assert("mw check", false); return ctx; }`, nil, Grant{}, Input{})
		var je *JSError
		if !errors.As(err, &je) {
			t.Fatalf("got %v, want *JSError", err)
		}
		if !strings.Contains(je.Message, "assertion failed: mw check") {
			t.Fatalf("message = %q, want it to contain the assertion failure", je.Message)
		}
	})
}

func TestGvInvokeStubRejects(t *testing.T) {
	e := newEngine(t)
	stub := Invoker(func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, errors.New("boom: no such request")
	})
	ctx := WithInvoker(context.Background(), stub)

	res, err := e.RunScenario(ctx,
		`await gv.invoke('missing/thing').then(() => "resolved").catch(e => "caught:" + String(e && e.message || e))`,
		Grant{}, Input{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := `"caught:boom: no such request"`
	if string(res.Value) != want {
		t.Fatalf("value = %s, want %s", res.Value, want)
	}
}
