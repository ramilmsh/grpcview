package scripting

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

var (
	exportDefaultRe = regexp.MustCompile(`\bexport\s+default\b`)
	exportHandleRe  = regexp.MustCompile(`\bexport\s+((async\s+)?function\s+handle\b|(const|let|var)\s+handle\b)|\bexport\s*\{[^}]*\bhandle\b`)
)

func hasDefaultExport(source string) bool { return exportDefaultRe.MatchString(source) }

func HasDefaultExport(source string) bool { return hasDefaultExport(source) }

// The ONE rule for every script position an author writes a value at: a module, or an expression
// that gets wrapped into one. The wrap opens no new line, so a bundler error still names the
// author's line.
func WrapExpression(source string) string {
	return "export default async () => (" + source + "\n)"
}

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

func (e *Engine) compileMiddleware(source string, g Grant, gens map[string]string) (compiled, string, error) {
	if len(gens) > 0 {
		return e.compileMiddlewareComposed(source, g, gens)
	}
	if hasHandleOrDefaultExport(source) {
		c, err := e.bundler.compileEntry(source, g)
		return c, middlewarePostlude, err
	}
	c, err := e.bundler.compile(source, g)
	return c, "", err
}

func (e *Engine) compileMiddlewareComposed(source string, g Grant, gens map[string]string) (compiled, string, error) {
	prelude := composeGeneratorPrelude(gens)
	composed := prelude + source

	var (
		c        compiled
		postlude string
		err      error
	)
	if hasHandleOrDefaultExport(source) {
		c, err = e.bundler.buildEntryBundleComposed(composed, g, gens)
		postlude = middlewarePostlude
	} else {
		c, err = e.bundler.buildBundleComposed(composed, g, gens)
	}
	if err != nil {
		return compiled{}, "", err
	}
	c.authorPreludeLines = strings.Count(prelude, "\n")
	return c, postlude, nil
}
