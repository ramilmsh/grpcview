package scripting

// The entry-point calling convention: an IIFE bundle that captures the module's exports to
// entryGlobalName, plus a run-time postlude whose final expression is the awaited call. It
// fires only when the source declares the expected export.

import (
	"encoding/json"
	"fmt"
	"regexp"
)

// Best-effort scans: a false negative just falls back to last-expression eval.
var (
	exportDefaultRe = regexp.MustCompile(`\bexport\s+default\b`)
	exportHandleRe  = regexp.MustCompile(`\bexport\s+((async\s+)?function\s+handle\b|(const|let|var)\s+handle\b)|\bexport\s*\{[^}]*\bhandle\b`)
)

func hasDefaultExport(source string) bool { return exportDefaultRe.MatchString(source) }

// HasDefaultExport reports whether source is a module, i.e. whether the entry-point
// convention fires for it — exported so callers agree with compileGenerator.
func HasDefaultExport(source string) bool { return hasDefaultExport(source) }

func hasHandleOrDefaultExport(source string) bool {
	return hasDefaultExport(source) || exportHandleRe.MatchString(source)
}

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

// Calls `handle` (or the default export) with a ctx DETACHED from the frozen input (body
// deep-copied), so a handler can rewrite the request in place.
var middlewarePostlude = fmt.Sprintf(`await (async () => {
  const __ctx = { body: JSON.parse(JSON.stringify(globalThis.request.body ?? null)), metadata: Object.assign({}, globalThis.request.metadata), target: globalThis.request.target };
  const __fn = %[1]s.handle || %[1]s.default;
  if (typeof __fn !== "function") { throw new TypeError("middleware must export a handle() function or a default export"); }
  const __out = await __fn(__ctx);
  return (__out === undefined || __out === null) ? __ctx : __out;
})()`, entryGlobalName)

func (e *Engine) compileGenerator(source string, g Grant, args []any) (compiled, string, error) {
	if hasDefaultExport(source) {
		c, err := e.bundler.compileEntry(source, g)
		return c, generatorPostlude(args), err
	}
	c, err := e.bundler.compile(source, g)
	return c, "", err
}

func (e *Engine) compileMiddleware(source string, g Grant) (compiled, string, error) {
	if hasHandleOrDefaultExport(source) {
		c, err := e.bundler.compileEntry(source, g)
		return c, middlewarePostlude, err
	}
	c, err := e.bundler.compile(source, g)
	return c, "", err
}
