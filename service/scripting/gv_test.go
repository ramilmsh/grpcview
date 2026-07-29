package scripting

// gv_test.go — Phase 0 (the shared `gv` foundation) and Feature 3 Phase 1 (the gv.invoke
// host bridge) from docs/design/gv-features-plan.md. Exercised through the production
// Engine.RunScenario/RunGenerator paths, the same way capabilities_test.go and
// engine_core_test.go exercise fs/fetch and the generator cache.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// TestGvFrozenEmpty: with an empty Input, gv (and every container it hangs off) comes
// back frozen, and gv.request.params / gv.metadata.inherit() are both {} — the documented
// graceful-degradation defaults for a run with no gv.invoke/inheritance context.
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

// TestGvFreezeBlocksMemberAddition: gv is frozen at the top level (and gv.metadata one
// level down) — adding a new member or reassigning an existing one is a silent no-op in
// sloppy mode, and gv.invoke stays exactly the function it was installed as.
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

// TestGvInvokeNoInvokerRejects: without an Invoker on ctx (the default — nothing calls
// WithInvoker in this test), gv.invoke's returned promise rejects with the fixed message,
// never a synchronous throw — matching fetch's "always hand back a promise" contract.
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

// TestGvRequestParamsAndInheritedMetadata: a populated Input.Params/InheritedMetadata
// surfaces exactly as gv.request.params / gv.metadata.inherit(), and both stay frozen.
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

// TestGvInvokeStubRoundTrip: end-to-end through the whole seam — JS gvInvokeShim ->
// __grpcview_invoke -> js_host_invoke (qjs_wasm.c) -> Go hostInvoke -> the ctx-carried
// Invoker -> back. await gv.invoke(path, params) resolves to exactly what the stub
// Invoker returns (proving the sync-host-call + Promise.resolve round trip), and the
// stub's captured request bytes prove the {path, params} envelope the shim sent.
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

// TestGvInvokeStubDefaultsParams: gv.invoke(path) with no second argument sends params:{}
// (the shim's `params == null ? {} : params` default), not null/undefined.
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
	// Decode and compare structurally rather than byte-for-byte: only the shape (path
	// carried through, params defaulted to {}) is the contract, not JSON key order.
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

// TestGvInvokeStubRejects: an Invoker error becomes a rejected gv.invoke promise carrying
// the error's message, not a Go error out of RunScenario and not a silently-lost failure.
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

// TestInvokeDepthContextSeam: WithInvokeDepth/invokeDepthFromContext round-trip the gv.invoke
// nesting counter, and an untouched context defaults to depth 0 (the top-level request,
// which has not yet recursed through gv.invoke at all). This is pure ctx-seam plumbing for
// the workspace-side Invoker to build the depth cap (D5: 8) on top of — this leaf package
// only carries the counter, the same way it carries Grant without deciding policy.
func TestInvokeDepthContextSeam(t *testing.T) {
	if got := invokeDepthFromContext(context.Background()); got != 0 {
		t.Fatalf("depth on a bare context = %d, want 0", got)
	}
	ctx := WithInvokeDepth(context.Background(), 3)
	if got := invokeDepthFromContext(ctx); got != 3 {
		t.Fatalf("depth after WithInvokeDepth(3) = %d, want 3", got)
	}
	// A second WithInvokeDepth further down the chain overrides, mirroring WithGrant.
	nested := WithInvokeDepth(ctx, 4)
	if got := invokeDepthFromContext(nested); got != 4 {
		t.Fatalf("depth after a second WithInvokeDepth(4) = %d, want 4", got)
	}
}

// TestConfigDigestIgnoresGvFields: Params/InheritedMetadata must NEVER perturb the
// generator cache key (docs/design/gv-features-plan.md "Cache-soundness invariant") — the
// RunGenerator path is cache-served by configDigest, so if these fields leaked into the
// digest a cache hit could serve one caller's params/inherited-metadata to another.
// configDigest only ever reads Vars/Secrets/Env/Args off Input; this pins that by
// construction rather than trusting it stays that way.
func TestConfigDigestIgnoresGvFields(t *testing.T) {
	base := Input{Vars: map[string]any{"a": 1}}
	withGv := base
	withGv.Params = map[string]any{"secret": "leak-if-digested"}
	withGv.InheritedMetadata = map[string][]string{"authorization": {"should-not-affect-digest"}}

	d1, err := configDigest(Generator.Name, "src", Grant{}, base)
	if err != nil {
		t.Fatalf("configDigest(base): %v", err)
	}
	d2, err := configDigest(Generator.Name, "src", Grant{}, withGv)
	if err != nil {
		t.Fatalf("configDigest(withGv): %v", err)
	}
	if d1 != d2 {
		t.Fatalf("configDigest changed when only Params/InheritedMetadata differed: %s vs %s "+
			"— these fields must never affect the generator cache key", d1, d2)
	}
}

// TestRunGeneratorCacheIgnoresParams: the cache-soundness invariant proven end-to-end
// (not just at the digest layer) — a generator that echoes gv.request.params run twice
// with DIFFERENT Params must return the identical (first, cached) value, because the
// cached RunGenerator path must always behave as if params were {}.
func TestRunGeneratorCacheIgnoresParams(t *testing.T) {
	e := newEngine(t)
	src := `gv.request.params`

	res1, err := e.RunGenerator(context.Background(), src, Grant{}, Input{Params: map[string]any{"v": float64(1)}})
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	res2, err := e.RunGenerator(context.Background(), src, Grant{}, Input{Params: map[string]any{"v": float64(2)}})
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if string(res1.Value) != string(res2.Value) {
		t.Fatalf("cache did not ignore Params: first=%s second=%s (want a cache hit serving the first value both times)",
			res1.Value, res2.Value)
	}
}
