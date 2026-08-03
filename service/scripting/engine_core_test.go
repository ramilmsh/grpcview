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

const generousPages = 512

func newRuntime(t *testing.T, maxPages uint32) *Runtime {
	t.Helper()
	rt, err := New(context.Background(), maxPages)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close(context.Background()) })
	return rt
}

func newEngine(t *testing.T, opts ...EngineOption) *Engine {
	t.Helper()
	e, err := NewEngine(context.Background(), generousPages, opts...)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	t.Cleanup(func() { _ = e.Close(context.Background()) })
	return e
}

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

	res, err := e.RunScenario(context.Background(), "null", Grant{}, Input{})
	if err != nil {
		t.Fatalf("run null: %v", err)
	}
	if string(res.Value) != "null" {
		t.Fatalf("null result: Value = %q, want \"null\"", res.Value)
	}
}

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

func TestSettledResultSurvivesFireAndForget(t *testing.T) {
	e := newEngine(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	start := time.Now()
	res, err := e.RunScenario(ctx, `Promise.resolve().then(() => { while (true) {} }); 42`, Grant{}, Input{})
	if err != nil {
		t.Fatalf("settled result lost to a fire-and-forget microtask: %v", err)
	}
	if string(res.Value) != "42" {
		t.Fatalf("got %s, want 42", res.Value)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("took %v — the detached spinning .then must never run", elapsed)
	}
}

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

func TestInputsAreFrozen(t *testing.T) {
	e := newEngine(t)
	in := Input{Vars: map[string]any{"a": float64(1)}}
	res, err := e.RunScenario(context.Background(), `vars.a = 999; vars.b = 2; vars`, Grant{}, in)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if string(res.Value) != `{"a":1}` {
		t.Fatalf("frozen vars mutated: %s, want {\"a\":1}", res.Value)
	}
}

func TestAsyncJobPump(t *testing.T) {
	e := newEngine(t)
	src := `let total = 0;
for (let i = 1; i <= 5; i++) { total += await Promise.resolve(i); }
await Promise.resolve().then(() => total * 2)`
	res, err := e.RunScenario(context.Background(), src, Grant{}, Input{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if string(res.Value) != "30" {
		t.Fatalf("value = %s, want 30", res.Value)
	}
}

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

func TestErrorLineInfo(t *testing.T) {
	e := newEngine(t)
	_, err := e.RunScenario(context.Background(), "\nthrow new Error('boom')", Grant{}, Input{})
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

	res, err := e.RunScenario(context.Background(), "1 + 1", Grant{}, Input{})
	if err != nil || string(res.Value) != "2" {
		t.Fatalf("run after throwing-toString: value=%s err=%v, want 2", res.Value, err)
	}
}

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

func TestGeneratorCacheHit(t *testing.T) {
	e := newEngine(t)
	src := `({generated: true})`
	grant := Grant{}
	in := Input{Vars: map[string]any{"seed": "abc"}}

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

	res, err = e.RunGenerator(context.Background(), src, grant, Input{Vars: map[string]any{"seed": "xyz"}})
	if err != nil {
		t.Fatalf("run (different input): %v", err)
	}
	if string(res.Value) != `{"generated":true}` {
		t.Fatalf("different input: value = %s, want the real script result", res.Value)
	}
}

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

func TestPoolInterruptDiscard(t *testing.T) {
	rt := newRuntime(t, generousPages)
	pool := NewPool(rt, Middleware.MemLimit)
	defer pool.Close(context.Background())

	if _, err := pool.Run(context.Background(), "1 + 1", Grant{}, Input{}); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if got := len(pool.idle); got != 1 {
		t.Fatalf("after a normal run, idle = %d, want 1", got)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := pool.Run(ctx, "while (true) {}", Grant{}, Input{}); !errors.Is(err, ErrInterrupted) {
		t.Fatalf("interrupt run: got %v, want ErrInterrupted", err)
	}
	if got := len(pool.idle); got != 0 {
		t.Fatalf("after an interrupt, idle = %d, want 0 (dead instance discarded)", got)
	}

	res, err := pool.Run(context.Background(), "2 + 3", Grant{}, Input{})
	if err != nil {
		t.Fatalf("post-interrupt run: %v", err)
	}
	if string(res.Value) != "5" {
		t.Fatalf("post-interrupt value = %s, want 5", res.Value)
	}
}

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

	if _, err := e.RunScenario(context.Background(), src, Grant{}, in); err == nil ||
		!strings.Contains(err.Error(), "capability not granted") {
		t.Fatalf("ungranted fs: got %v, want a Gate 1 denial", err)
	}
}

func BenchmarkMiddleware(b *testing.B) {
	const src = `({status: 200, ok: true})`
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()

	run := func(b *testing.B, opts ...EngineOption) {
		e, err := NewEngine(context.Background(), generousPages, opts...)
		if err != nil {
			b.Fatalf("NewEngine: %v", err)
		}
		defer e.Close(context.Background())
		if _, err := e.RunMiddleware(ctx, src, Grant{}, Input{}); err != nil {
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
