package scripting

import (
	"encoding/json"
	"fmt"
	"regexp"
)

var (
	exportDefaultRe = regexp.MustCompile(`\bexport\s+default\b`)
	exportHandleRe  = regexp.MustCompile(`\bexport\s+((async\s+)?function\s+handle\b|(const|let|var)\s+handle\b)|\bexport\s*\{[^}]*\bhandle\b`)
)

// A false positive here (comment or string text that merely mentions the syntax) makes
// WrapExpression's caller skip wrapping, so a plain expression parses as a block statement and
// fails with an error that names no useful line — see the bundle-failure example rejectComputedImports
// documents for the same masking approach. A false negative just wraps a real module, which fails
// loudly and immediately. So: bias toward not claiming `export default`. maskLiterals only ever
// removes characters (blanks comment/string interiors), never adds them, so a masked match implies
// a raw match; the raw regex is therefore a safe, cheap pre-filter that skips masking entirely for
// the common case of source with no mention of the syntax at all.
func hasDefaultExport(source string) bool {
	return exportDefaultRe.MatchString(source) && exportDefaultRe.MatchString(maskLiterals(source))
}

func HasDefaultExport(source string) bool { return hasDefaultExport(source) }

// The ONE rule for every script position an author writes a value at: a module, or an expression
// that gets wrapped into one. The wrap opens no new line, so a bundler error still names the
// author's line.
func WrapExpression(source string) string {
	return "export default async () => (" + source + "\n)"
}

func hasHandleOrDefaultExport(source string) bool {
	if !exportDefaultRe.MatchString(source) && !exportHandleRe.MatchString(source) {
		return false
	}
	masked := maskLiterals(source)
	return exportDefaultRe.MatchString(masked) || exportHandleRe.MatchString(masked)
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

var middlewarePostlude = fmt.Sprintf(`await (async () => {
  const __ctx = { body: JSON.parse(JSON.stringify(globalThis.__grpcview_request.body ?? null)), metadata: Object.assign({}, globalThis.__grpcview_request.metadata), target: globalThis.__grpcview_request.target };
  const __fn = %[1]s.handle || %[1]s.default;
  if (typeof __fn !== "function") { throw new TypeError("middleware must export a handle() function or a default export"); }
  const __out = await __fn(__ctx);
  return (__out === undefined || __out === null) ? __ctx : __out;
})()`, entryGlobalName)

func (e *Engine) compileGenerator(source string, g Grant, args []any, collRoot string) (compiled, string, error) {
	if hasDefaultExport(source) {
		c, err := e.bundler.compileEntry(source, g, collRoot)
		return c, generatorPostlude(args), err
	}
	c, err := e.bundler.compile(source, g, collRoot)
	return c, "", err
}

func (e *Engine) compileMiddleware(source string, g Grant, collRoot string) (compiled, string, error) {
	if hasHandleOrDefaultExport(source) {
		c, err := e.bundler.compileEntry(source, g, collRoot)
		return c, middlewarePostlude, err
	}
	c, err := e.bundler.compile(source, g, collRoot)
	return c, "", err
}
