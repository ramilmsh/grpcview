package scripting

// entry.go — the ENTRY-POINT calling convention (scripting-ui-plan §2.5).
//
// A saved GENERATOR runs its `export default (…args) => value`; a saved MIDDLEWARE runs
// its `handle(ctx)` (or default export) with a ctx built from the request Input, and its
// returned ctx is the value. The convention is implemented as: compile the module as an
// IIFE that captures its exports to entryGlobalName (bundler.compileEntry), then append a
// run-time POSTLUDE whose final expression is the awaited call — reusing runCompiled's
// last-expression + async-pump + JSON-result machinery unchanged.
//
// Backward compatibility: the convention fires ONLY when the source actually declares the
// expected export. A script without it (e.g. the last-expression scratchpad forms the
// engine tests and the ad-hoc RunScript path use) takes the existing compile path, so those
// paths — and their tests — are unaffected.

import (
	"encoding/json"
	"fmt"
	"regexp"
)

// exportDefaultRe / exportHandleRe detect the authored entry points. Like the bundler's
// importStmtRe this is a best-effort source scan (it can be fooled by the keywords in a
// comment/string); a false negative merely falls back to last-expression eval, and a
// false positive fails cleanly at bundle time if no such export exists.
var (
	exportDefaultRe = regexp.MustCompile(`\bexport\s+default\b`)
	exportHandleRe  = regexp.MustCompile(`\bexport\s+((async\s+)?function\s+handle\b|(const|let|var)\s+handle\b)|\bexport\s*\{[^}]*\bhandle\b`)
)

func hasDefaultExport(source string) bool { return exportDefaultRe.MatchString(source) }

func hasHandleOrDefaultExport(source string) bool {
	return hasDefaultExport(source) || exportHandleRe.MatchString(source)
}

// generatorPostlude is the call site for a generator: await the default export applied to
// the token's positional args (marshalled to a JSON array literal, which is a valid JS
// array literal). No args => a plain call.
func generatorPostlude(args []any) string {
	if len(args) == 0 {
		return fmt.Sprintf("await Promise.resolve(%s.default())", entryGlobalName)
	}
	argsJSON, err := json.Marshal(args)
	if err != nil {
		return fmt.Sprintf("await Promise.resolve(%s.default())", entryGlobalName)
	}
	return fmt.Sprintf("await Promise.resolve(%s.default(...%s))", entryGlobalName, argsJSON)
}

// middlewarePostlude is the call site for a middleware: build a MUTABLE ctx from the frozen
// request Input, call `handle` (or the default export), await it, and yield the returned
// ctx — falling back to the passed ctx when the handler mutates in place and returns
// nothing. Unlike a generator (whose request is a read-only input), a middleware's job is to
// rewrite the request, so ctx is detached from the frozen input: metadata is shallow-copied
// and body is DEEP-copied (a JSON round-trip), so `ctx.body.field = …` / `ctx.metadata[k] = v`
// mutate the ctx, never the frozen input. body defaults to null when the request has none.
var middlewarePostlude = fmt.Sprintf(`await (async () => {
  const __ctx = { body: JSON.parse(JSON.stringify(globalThis.request.body ?? null)), metadata: Object.assign({}, globalThis.request.metadata), target: globalThis.request.target };
  const __fn = %[1]s.handle || %[1]s.default;
  if (typeof __fn !== "function") { throw new TypeError("middleware must export a handle() function or a default export"); }
  const __out = await __fn(__ctx);
  return (__out === undefined || __out === null) ? __ctx : __out;
})()`, entryGlobalName)

// compileGenerator returns the compiled blob and the run-time postlude for a generator run.
// When the source declares `export default` it uses the entry-point convention (IIFE +
// call site); otherwise it falls back to the last-expression form (empty postlude).
func (e *Engine) compileGenerator(source string, g Grant, args []any) (compiled, string, error) {
	if hasDefaultExport(source) {
		c, err := e.bundler.compileEntry(source, g)
		return c, generatorPostlude(args), err
	}
	c, err := e.bundler.compile(source, g)
	return c, "", err
}

// compileMiddleware is compileGenerator's analogue for middleware: the convention fires on
// a `handle` or default export, otherwise last-expression eval.
func (e *Engine) compileMiddleware(source string, g Grant) (compiled, string, error) {
	if hasHandleOrDefaultExport(source) {
		c, err := e.bundler.compileEntry(source, g)
		return c, middlewarePostlude, err
	}
	c, err := e.bundler.compile(source, g)
	return c, "", err
}
