package scripting

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// generousPages is a high per-instance memory ceiling used by tests that are not
// exercising the outer cap (32 MiB). QuickJS boots + evals trivial scripts well
// under this. The module's own initial memory is 32 pages (2 MiB); it grows on
// demand up to whichever ceiling New() is given.
const generousPages = 512 // 512 * 64 KiB = 32 MiB

// pagesMiB converts a linear-memory page count to whole MiB for log lines.
func pagesMiB(p uint32) uint32 { return p * WasmPageSize / (1 << 20) }

func newRuntime(t *testing.T, maxPages uint32) *Runtime {
	t.Helper()
	rt, err := New(context.Background(), maxPages)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close(context.Background()) })
	return rt
}

// TestEvalArithmetic: the module loads and evaluates 1+1.
func TestEvalArithmetic(t *testing.T) {
	rt := newRuntime(t, generousPages)
	got, err := rt.EvalIsolated(context.Background(), "1 + 1", 0)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	if got != "2" {
		t.Fatalf("1 + 1 = %q, want %q", got, "2")
	}
}

// TestRoundTripString: a string goes in as source and comes back across linear memory,
// including a JSON round-trip and non-ASCII (UTF-8) to prove the marshalling boundary.
func TestRoundTripString(t *testing.T) {
	rt := newRuntime(t, generousPages)
	cases := []struct{ src, want string }{
		{`"hello " + "world"`, "hello world"},
		{`JSON.stringify({a: 1, b: [2, 3], c: "x"})`, `{"a":1,"b":[2,3],"c":"x"}`},
		{`"héllo — 世界 🌍"`, "héllo — 世界 🌍"},
		{`[1,2,3].map(x => x * x).join(",")`, "1,4,9"},
	}
	inst, err := rt.Instantiate(context.Background())
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	defer inst.Close(context.Background())
	for _, c := range cases {
		got, err := inst.Eval(context.Background(), c.src, 0)
		if err != nil {
			t.Fatalf("eval %q: %v", c.src, err)
		}
		if got != c.want {
			t.Fatalf("eval %q = %q, want %q", c.src, got, c.want)
		}
	}
}

// TestInnerMemoryLimit: JS_SetMemoryLimit rejects a huge allocation and the script
// gets a catchable OOM, while the host process is unharmed. This is the bound goja
// cannot provide.
func TestInnerMemoryLimit(t *testing.T) {
	rt := newRuntime(t, generousPages) // outer ceiling generous; inner limit does the work
	const innerLimit = 4 << 20         // 4 MiB QuickJS heap cap (boots the context, rejects the big alloc)

	// A single allocation far larger than the cap.
	_, err := rt.EvalIsolated(context.Background(), `"x".repeat(50 * 1000 * 1000)`, innerLimit)
	var jsErr *JSError
	if !errors.As(err, &jsErr) {
		t.Fatalf("huge alloc under inner limit: got err %v (want *JSError out-of-memory)", err)
	}
	if !strings.Contains(strings.ToLower(jsErr.Message), "out of memory") {
		t.Fatalf("inner-limit error = %q, want it to mention out of memory", jsErr.Message)
	}

	// Sanity: a modest allocation under the same cap still succeeds.
	got, err := rt.EvalIsolated(context.Background(), `"x".repeat(1000).length`, innerLimit)
	if err != nil {
		t.Fatalf("small alloc under inner limit: %v", err)
	}
	if got != "1000" {
		t.Fatalf("small alloc length = %q, want 1000", got)
	}
}

// TestMemLimitAboveWasm32AddressSpace: a memLimitBytes at or above 4 GiB (the whole
// wasm32 address space) must be treated as effectively unbounded, not silently narrowed.
// size_t is 32-bit in the guest, so before the shim clamped, uint64 4 GiB (0x1_0000_0000)
// cast to size_t became 0, and JS_SetMemoryLimit(rt, 0) rejects every allocation (0 is not
// a "disabled" sentinel) — JS_NewContext then failed and the eval returned a spurious
// "cannot create context" instead of running the script.
func TestMemLimitAboveWasm32AddressSpace(t *testing.T) {
	rt := newRuntime(t, generousPages)
	const fourGiB = uint64(4) << 30 // 0x1_0000_0000: nonzero, but low 32 bits are all zero
	got, err := rt.EvalIsolated(context.Background(), "6 * 7", fourGiB)
	if err != nil {
		t.Fatalf("eval under a >=4 GiB inner limit: got err %v, want the limit clamped to effectively-unbounded", err)
	}
	if got != "42" {
		t.Fatalf("6 * 7 under a >=4 GiB inner limit = %q, want %q", got, "42")
	}
}

// TestOuterMemoryCeiling: with JS_SetMemoryLimit DISABLED (0), a huge allocation still
// cannot succeed because wazero refuses to grow linear memory past the page ceiling —
// the backstop that holds even if QuickJS's own accounting were bypassed. A normal
// script under the same low ceiling still runs.
func TestOuterMemoryCeiling(t *testing.T) {
	const lowPages = 128 // 8 MiB hard ceiling, enforced by wazero (comfortably boots QuickJS)
	rt := newRuntime(t, lowPages)

	// Report the real boot footprint so it's clear the ceiling sits above it.
	if inst, err := rt.Instantiate(context.Background()); err == nil {
		if _, err := inst.Eval(context.Background(), "1+1", 0); err == nil {
			pages := inst.MemoryPages()
			t.Logf("boot+trivial-eval footprint: %d pages (%d MiB); ceiling: %d pages (%d MiB)",
				pages, pagesMiB(pages), lowPages, pagesMiB(lowPages))
		}
		_ = inst.Close(context.Background())
	}

	// Inner limit DISABLED: only wazero's page ceiling stands between the script and
	// a 20 MiB allocation, and it holds.
	_, err := rt.EvalIsolated(context.Background(), `"x".repeat(20 * 1000 * 1000)`, 0)
	if err == nil {
		t.Fatal("expected allocation beyond wazero page ceiling to fail, got nil")
	}

	got, err := rt.EvalIsolated(context.Background(), "40 + 2", 0)
	if err != nil {
		t.Fatalf("trivial eval under low ceiling: %v", err)
	}
	if got != "42" {
		t.Fatalf("got %q, want 42", got)
	}
}

// TestWallClockInterrupt: an infinite loop is killed by the host via context deadline
// (wazero WithCloseOnContextDone), promptly, and reported as ErrInterrupted. This is
// the preemption goja's Interrupt() cannot guarantee against a tight loop.
func TestWallClockInterrupt(t *testing.T) {
	rt := newRuntime(t, generousPages)

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := rt.EvalIsolated(ctx, "while (true) {}", 0)
	elapsed := time.Since(start)

	if !errors.Is(err, ErrInterrupted) {
		t.Fatalf("infinite loop: got err %v, want ErrInterrupted", err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("interrupt took %v, expected it to be prompt", elapsed)
	}
	t.Logf("infinite loop interrupted after %v", elapsed)

	// The runtime is still usable for a fresh isolated eval afterwards.
	got, err := rt.EvalIsolated(context.Background(), "7 * 6", 0)
	if err != nil {
		t.Fatalf("post-interrupt eval: %v", err)
	}
	if got != "42" {
		t.Fatalf("post-interrupt eval = %q, want 42", got)
	}
}

// TestDeepRecursionContained: unbounded JS recursion must be CONTAINED — the host
// survives and a fresh eval still works — even though CONFIG_STACK_CHECK is compiled out
// under wasi (so QuickJS itself raises no RangeError). This pins the design doc's safety
// claim that guest stack exhaustion traps cleanly rather than silently corrupting guest
// memory. The wall-clock deadline is only a backstop so a hypothetical non-trapping
// runaway cannot hang the suite; a clean trap should return well before it.
func TestDeepRecursionContained(t *testing.T) {
	rt := newRuntime(t, generousPages)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	got, err := rt.EvalIsolated(ctx, "function f(){ return f() } f()", 0)
	if err == nil {
		t.Fatalf("unbounded recursion returned %q with no error; expected a contained failure", got)
	}
	t.Logf("unbounded recursion contained as: %v", err)

	// The host and runtime must remain healthy for a subsequent fresh eval.
	got2, err2 := rt.EvalIsolated(context.Background(), "6 * 7", 0)
	if err2 != nil {
		t.Fatalf("fresh eval after runaway recursion failed: %v", err2)
	}
	if got2 != "42" {
		t.Fatalf("fresh eval after runaway recursion = %q, want 42", got2)
	}
}

// TestNumbers reports the spike's headline metrics: module size, cold compile, warm
// eval latency, and per-run isolated cost. Run with `-v` to see them.
func TestNumbers(t *testing.T) {
	ctx := context.Background()

	t.Logf("embedded quickjs.wasm size: %d bytes (%.0f KiB)", len(quickjsWasm), float64(len(quickjsWasm))/1024)

	coldStart := time.Now()
	rt, err := New(ctx, generousPages)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer rt.Close(ctx)
	t.Logf("cold: compile module: %v", time.Since(coldStart))

	instStart := time.Now()
	inst, err := rt.Instantiate(ctx)
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	defer inst.Close(ctx)
	t.Logf("cold: first instantiate: %v", time.Since(instStart))

	// Warm eval latency on a pooled instance.
	const warmN = 200
	if _, err := inst.Eval(ctx, "1+1", 0); err != nil { // prime
		t.Fatalf("warm prime: %v", err)
	}
	warmStart := time.Now()
	for i := 0; i < warmN; i++ {
		if _, err := inst.Eval(ctx, "1+1", 0); err != nil {
			t.Fatalf("warm eval: %v", err)
		}
	}
	t.Logf("warm: eval '1+1' on pooled instance: %v/op (n=%d)", time.Since(warmStart)/warmN, warmN)
	warmPages := inst.MemoryPages()
	t.Logf("instance footprint after %d warm evals: %d pages (%d MiB)", warmN, warmPages, pagesMiB(warmPages))

	// Full isolated run cost (instantiate + eval + teardown), the per-script-run price.
	const isoN = 50
	isoStart := time.Now()
	for i := 0; i < isoN; i++ {
		if _, err := rt.EvalIsolated(ctx, "1+1", 0); err != nil {
			t.Fatalf("isolated eval: %v", err)
		}
	}
	t.Logf("per-run: instantiate+eval+teardown (fresh instance): %v/op (n=%d)", time.Since(isoStart)/isoN, isoN)
	t.Logf("outer memory ceiling enforced: %d pages = %d MiB", generousPages, pagesMiB(generousPages))
}
