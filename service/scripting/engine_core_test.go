package scripting

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

// newEngine builds an Engine for a test with the generous outer ceiling and tears it
// down on cleanup.
func newEngine(t *testing.T, opts ...EngineOption) *Engine {
	t.Helper()
	e, err := NewEngine(context.Background(), generousPages, opts...)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	t.Cleanup(func() { _ = e.Close(context.Background()) })
	return e
}

// TestStructuredOutput: the structured path returns the script's value as JSON — for
// scalars, objects, arrays, null, and a top-level awaited promise.
func TestStructuredOutput(t *testing.T) {
	e := newEngine(t)
	cases := []struct{ name, src, want string }{
		{"number", "6 * 7", "42"},
		{"string", `"hi"`, `"hi"`},
		{"object", `({a: 1, b: [2, 3], c: "x"})`, `{"a":1,"b":[2,3],"c":"x"}`},
		{"array", "[1, 2, 3].map(x => x * x)", "[1,4,9]"},
		{"null", "null", "null"},
		{"bool", "1 < 2", "true"},
		{"await", "await Promise.resolve({ok: true})", `{"ok":true}`},
		{"chained-await", "const a = await Promise.resolve(20); const b = await Promise.resolve(22); a + b", "42"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := e.RunScenario(context.Background(), c.src, Grant{}, Input{})
			if err != nil {
				t.Fatalf("run %q: %v", c.src, err)
			}
			if string(res.Value) != c.want {
				t.Fatalf("value = %s, want %s", res.Value, c.want)
			}
		})
	}
}

// TestUndefinedResult: a script whose value is undefined comes back as a nil Value,
// distinct from a JSON `null` return. (NB: a script that ends in a *statement* rather
// than an expression — e.g. `let x = 1;` — yields `{}` under QuickJS async eval, its
// representation of an empty completion; only a genuine undefined value maps to nil.)
func TestUndefinedResult(t *testing.T) {
	e := newEngine(t)

	for _, src := range []string{"undefined", "void 0", "(() => {})()"} {
		res, err := e.RunScenario(context.Background(), src, Grant{}, Input{})
		if err != nil {
			t.Fatalf("run %q: %v", src, err)
		}
		if res.Value != nil {
			t.Fatalf("undefined result for %q: Value = %s, want nil", src, res.Value)
		}
	}

	// A `null` return, by contrast, is the JSON literal null — not nil.
	res, err := e.RunScenario(context.Background(), "null", Grant{}, Input{})
	if err != nil {
		t.Fatalf("run null: %v", err)
	}
	if string(res.Value) != "null" {
		t.Fatalf("null result: Value = %q, want \"null\"", res.Value)
	}
}

// TestConsoleCaptured: console.* output is buffered host-side and returned alongside
// the value, in order, with levels and non-string args JSON-formatted.
func TestConsoleCaptured(t *testing.T) {
	e := newEngine(t)
	src := `console.log("hello", 42);
console.info("info-as-log");
console.warn("careful");
console.error({code: 1});
1`
	res, err := e.RunScenario(context.Background(), src, Grant{}, Input{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := []LogLine{
		{Level: "log", Message: "hello 42"},
		{Level: "log", Message: "info-as-log"},
		{Level: "warn", Message: "careful"},
		{Level: "error", Message: `{"code":1}`},
	}
	if !reflect.DeepEqual(res.Logs, want) {
		t.Fatalf("logs = %+v, want %+v", res.Logs, want)
	}
}

// TestConsoleFormatting: the console formatter renders non-string args honestly —
// NaN/Infinity/undefined/functions must not collapse to "null"/"" (the JSON.stringify
// trap), objects/arrays are JSON, null is "null".
func TestConsoleFormatting(t *testing.T) {
	e := newEngine(t)
	res, err := e.RunScenario(context.Background(),
		`console.log(NaN, Infinity, undefined, null, true, function(){}, {a:1}, [1,2]); 1`,
		Grant{}, Input{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := `NaN Infinity undefined null true [Function] {"a":1} [1,2]`
	if len(res.Logs) != 1 || res.Logs[0].Message != want {
		t.Fatalf("console message = %q, want %q", res.Logs, want)
	}
}

// TestSettledResultSurvivesFireAndForget: a script that settles to a value but leaves a
// hostile fire-and-forget microtask behind must still return that value — the engine
// must not run the detached microtask and lose the result to a spurious interrupt.
func TestSettledResultSurvivesFireAndForget(t *testing.T) {
	rt := newRuntime(t, generousPages)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	start := time.Now()
	got, err := rt.EvalIsolated(ctx, `Promise.resolve().then(() => { while (true) {} }); 42`, 0)
	if err != nil {
		t.Fatalf("settled result lost to a fire-and-forget microtask: %v", err)
	}
	if got != "42" {
		t.Fatalf("got %q, want 42", got)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("took %v — the detached spinning .then must never run", elapsed)
	}
}

// TestInputInjection: the frozen inert globals (request/vars/secrets/env) are readable
// by the script exactly as supplied.
func TestInputInjection(t *testing.T) {
	e := newEngine(t)
	in := Input{
		Request: RequestInput{
			Body:     map[string]any{"name": "world"},
			Metadata: map[string]string{"authorization": "Bearer t"},
			Target:   "localhost:10000",
		},
		Vars:    map[string]any{"greeting": "hi"},
		Secrets: map[string]any{"token": "s3cr3t"},
		Env:     map[string]any{"stage": "test"},
	}
	src := `[vars.greeting, request.body.name, request.target,
request.metadata.authorization, secrets.token, env.stage].join("|")`
	res, err := e.RunScenario(context.Background(), src, Grant{}, in)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := `"hi|world|localhost:10000|Bearer t|s3cr3t|test"`
	if string(res.Value) != want {
		t.Fatalf("value = %s, want %s", res.Value, want)
	}
}

// TestInputsAreFrozen: a script cannot mutate its inputs — the injected objects are
// deep-frozen, so writes are dropped (non-strict) and the value is unchanged.
func TestInputsAreFrozen(t *testing.T) {
	e := newEngine(t)
	in := Input{Vars: map[string]any{"a": float64(1)}}
	// Attempt to add and overwrite keys, then return vars.
	res, err := e.RunScenario(context.Background(), `vars.a = 999; vars.b = 2; vars`, Grant{}, in)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if string(res.Value) != `{"a":1}` {
		t.Fatalf("frozen vars mutated: %s, want {\"a\":1}", res.Value)
	}
}

// TestAsyncJobPump: a chain of awaited microtasks settles under the host-driven pump.
func TestAsyncJobPump(t *testing.T) {
	e := newEngine(t)
	src := `let total = 0;
for (let i = 1; i <= 5; i++) { total += await Promise.resolve(i); }
await Promise.resolve().then(() => total * 2)`
	res, err := e.RunScenario(context.Background(), src, Grant{}, Input{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if string(res.Value) != "30" { // (1+2+3+4+5)=15, *2=30
		t.Fatalf("value = %s, want 30", res.Value)
	}
}

// TestUnsettledPromise: a top-level promise that can never settle (no host work will
// advance it) is reported promptly as ErrUnsettled rather than burning the deadline.
func TestUnsettledPromise(t *testing.T) {
	e := newEngine(t)
	start := time.Now()
	_, err := e.RunScenario(context.Background(), "await new Promise(() => {})", Grant{}, Input{})
	if !errors.Is(err, ErrUnsettled) {
		t.Fatalf("never-settling promise: got %v, want ErrUnsettled", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("ErrUnsettled took %v; expected it to be prompt (not deadline-bound)", elapsed)
	}
}

// TestErrorLineInfo: a thrown Error surfaces as *JSError carrying the message and a
// parsed source line. Uses the legacy (no-prelude) path so the line is the author's.
func TestErrorLineInfo(t *testing.T) {
	rt := newRuntime(t, generousPages)
	// throw is on line 2 (after one leading newline).
	_, err := rt.EvalIsolated(context.Background(), "\nthrow new Error('boom')", 0)
	var je *JSError
	if !errors.As(err, &je) {
		t.Fatalf("thrown error: got %v, want *JSError", err)
	}
	t.Logf("JSError: message=%q line=%d stack=%q", je.Message, je.Line, je.Stack)
	if !strings.Contains(je.Message, "boom") {
		t.Fatalf("message = %q, want it to contain boom", je.Message)
	}
	if je.Line != 2 {
		t.Fatalf("line = %d, want 2 (stack=%q)", je.Line, je.Stack)
	}
}

// TestErrorSurfacesLogs: console output emitted before a throw is still returned, so a
// failing script's logs are not lost.
func TestErrorSurfacesLogs(t *testing.T) {
	e := newEngine(t)
	res, err := e.RunScenario(context.Background(),
		`console.log("before the throw"); throw new Error("boom")`, Grant{}, Input{})
	var je *JSError
	if !errors.As(err, &je) {
		t.Fatalf("got %v, want *JSError", err)
	}
	if len(res.Logs) != 1 || res.Logs[0].Message != "before the throw" {
		t.Fatalf("logs = %+v, want the pre-throw log preserved", res.Logs)
	}
}

// TestThrowWithFailingToString: a rejection reason whose toString() itself throws must
// still produce a non-empty *JSError message (not the empty string) and must not leave a
// pending exception that breaks a later run in the same runtime.
func TestThrowWithFailingToString(t *testing.T) {
	e := newEngine(t)
	_, err := e.RunScenario(context.Background(),
		`throw { toString() { throw new Error("nested") } }`, Grant{}, Input{})
	var je *JSError
	if !errors.As(err, &je) {
		t.Fatalf("got %v, want *JSError", err)
	}
	if je.Message == "" {
		t.Fatal("expected a non-empty fallback message when toString() throws")
	}
	t.Logf("fallback message: %q", je.Message)

	// The runtime must stay healthy for a subsequent run (no lingering pending exception).
	res, err := e.RunScenario(context.Background(), "1 + 1", Grant{}, Input{})
	if err != nil || string(res.Value) != "2" {
		t.Fatalf("run after throwing-toString: value=%s err=%v, want 2", res.Value, err)
	}
}

// TestProfilesBounded: every profile enforces the memory ceiling (a huge allocation is
// a catchable OOM) and the wall-clock deadline (a tight loop is interrupted). This is
// the milestone's exit criterion — all three run bounded with clean error propagation.
func TestProfilesBounded(t *testing.T) {
	e := newEngine(t)
	profiles := []struct {
		name string
		run  func(context.Context, string, Grant, Input) (Result, error)
	}{
		{"generator", e.RunGenerator},
		{"middleware", e.RunMiddleware},
		{"scenario", e.RunScenario},
	}
	for _, p := range profiles {
		t.Run(p.name+"/memory", func(t *testing.T) {
			_, err := p.run(context.Background(), `"x".repeat(50 * 1000 * 1000)`, Grant{}, Input{})
			var je *JSError
			if !errors.As(err, &je) {
				t.Fatalf("huge allocation: got %v, want a *JSError OOM", err)
			}
			if !strings.Contains(strings.ToLower(je.Message), "out of memory") {
				t.Fatalf("OOM message = %q, want it to mention out of memory", je.Message)
			}
		})
		t.Run(p.name+"/deadline", func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
			defer cancel()
			start := time.Now()
			_, err := p.run(ctx, "while (true) {}", Grant{}, Input{})
			if !errors.Is(err, ErrInterrupted) {
				t.Fatalf("infinite loop: got %v, want ErrInterrupted", err)
			}
			if elapsed := time.Since(start); elapsed > 5*time.Second {
				t.Fatalf("interrupt took %v, expected it to be prompt", elapsed)
			}
		})
	}
}

// TestGeneratorCacheHit: a generator's result is served from the config-digest cache on
// a repeat call. Proven by pre-seeding the cache with a sentinel — a re-execution would
// return the script's real value, not the sentinel.
func TestGeneratorCacheHit(t *testing.T) {
	e := newEngine(t)
	src := `({generated: true})`
	grant := Grant{}
	in := Input{Vars: map[string]any{"seed": "abc"}}

	// Pre-seed the exact key this call will compute.
	key, err := configDigest(Generator.Name, src, grant, in)
	if err != nil {
		t.Fatalf("configDigest: %v", err)
	}
	e.genCache.Store(key, Result{Value: json.RawMessage(`"FROM-CACHE"`)})

	res, err := e.RunGenerator(context.Background(), src, grant, in)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if string(res.Value) != `"FROM-CACHE"` {
		t.Fatalf("cache miss: value = %s, want the pre-seeded sentinel (cache not consulted)", res.Value)
	}

	// A different input must NOT hit that entry — it runs the script for real.
	res, err = e.RunGenerator(context.Background(), src, grant, Input{Vars: map[string]any{"seed": "xyz"}})
	if err != nil {
		t.Fatalf("run (different input): %v", err)
	}
	if string(res.Value) != `{"generated":true}` {
		t.Fatalf("different input: value = %s, want the real script result", res.Value)
	}
}

// TestGeneratorCachePopulates: a first real run populates the cache, and a second
// identical call returns byte-identical output.
func TestGeneratorCachePopulates(t *testing.T) {
	e := newEngine(t)
	src := `({v: 1 + 1})`
	res1, err := e.RunGenerator(context.Background(), src, Grant{}, Input{})
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	key, err := configDigest(Generator.Name, src, Grant{}, Input{})
	if err != nil {
		t.Fatalf("configDigest: %v", err)
	}
	if _, ok := e.genCache.Load(key); !ok {
		t.Fatal("cache not populated after first run")
	}
	res2, err := e.RunGenerator(context.Background(), src, Grant{}, Input{})
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if string(res1.Value) != string(res2.Value) || string(res2.Value) != `{"v":2}` {
		t.Fatalf("cached result mismatch: %s vs %s", res1.Value, res2.Value)
	}
}

// TestPoolInterruptDiscard: an interrupted (dead) instance is discarded by the pool,
// never pooled for reuse, and the pool self-heals on the next run.
func TestPoolInterruptDiscard(t *testing.T) {
	rt := newRuntime(t, generousPages)
	pool := NewPool(rt, Middleware.MemLimit)
	defer pool.Close(context.Background())

	// A normal run returns its instance to the idle set.
	if _, err := pool.Run(context.Background(), "1 + 1", Grant{}, Input{}); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if got := len(pool.idle); got != 1 {
		t.Fatalf("after a normal run, idle = %d, want 1", got)
	}

	// An interrupted run kills its instance; it must be discarded, not pooled.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := pool.Run(ctx, "while (true) {}", Grant{}, Input{}); !errors.Is(err, ErrInterrupted) {
		t.Fatalf("interrupt run: got %v, want ErrInterrupted", err)
	}
	if got := len(pool.idle); got != 0 {
		t.Fatalf("after an interrupt, idle = %d, want 0 (dead instance discarded)", got)
	}

	// The pool self-heals: a fresh instance is created and the run succeeds.
	res, err := pool.Run(context.Background(), "2 + 3", Grant{}, Input{})
	if err != nil {
		t.Fatalf("post-interrupt run: %v", err)
	}
	if string(res.Value) != "5" {
		t.Fatalf("post-interrupt value = %s, want 5", res.Value)
	}
}

// TestCapabilityGrantStructured: the structured path enforces the same two capability
// gates as RunScript — a granted, in-scope fs read succeeds and reaches the script.
func TestCapabilityGrantStructured(t *testing.T) {
	e := newEngine(t)
	dir := t.TempDir()
	file := dir + "/token.txt"
	writeFile(t, file, "grpc-token")

	grant := Grant{FS: &FSGrant{AllowedPaths: []string{dir}}}
	src := `import fs from "node:fs"; ({token: fs.readFileSync(request.body.path)})`
	in := Input{Request: RequestInput{Body: map[string]any{"path": file}}}

	res, err := e.RunScenario(context.Background(), src, grant, in)
	if err != nil {
		t.Fatalf("granted fs read: %v", err)
	}
	if string(res.Value) != `{"token":"grpc-token"}` {
		t.Fatalf("value = %s, want the file contents", res.Value)
	}

	// Ungranted: Gate 1 refuses to resolve the import at bundle time.
	if _, err := e.RunScenario(context.Background(), src, Grant{}, in); err == nil ||
		!strings.Contains(err.Error(), "capability not granted") {
		t.Fatalf("ungranted fs: got %v, want a Gate 1 denial", err)
	}
}

// BenchmarkMiddleware records the milestone's key open number: per-invoke middleware
// latency for the warm pool with a fresh context per run (full isolation) versus a
// long-lived reused context (the ~800 µs QuickJS bootstrap paid once). The script is
// re-eval-safe (no top-level declarations) so the long-lived context can reuse it.
func BenchmarkMiddleware(b *testing.B) {
	const src = `({status: 200, ok: true})`
	// A far-future deadline so RunMiddleware doesn't allocate a per-call timeout timer;
	// we want to measure execution, not context plumbing.
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()

	run := func(b *testing.B, opts ...EngineOption) {
		e, err := NewEngine(context.Background(), generousPages, opts...)
		if err != nil {
			b.Fatalf("NewEngine: %v", err)
		}
		defer e.Close(context.Background())
		if _, err := e.RunMiddleware(ctx, src, Grant{}, Input{}); err != nil { // prime the pool
			b.Fatalf("prime: %v", err)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, err := e.RunMiddleware(ctx, src, Grant{}, Input{}); err != nil {
				b.Fatalf("run: %v", err)
			}
		}
	}

	b.Run("warm-pool-fresh-context", func(b *testing.B) { run(b) })
	b.Run("warm-pool-long-lived-context", func(b *testing.B) { run(b, WithLongLivedMiddleware()) })
}
