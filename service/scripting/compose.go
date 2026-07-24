package scripting

// compose.go — the COMPOSITION half of the TypeScript request-body feature: let a request
// body call the workspace's saved GENERATORS as ambient globals. A synthetic prelude imports
// each referenced generator through the grpcview:gen/<name> specifier (resolved and inlined by
// generatorResolverPlugin, bundler.go) and binds it onto globalThis under its display name, so
// the body can call it directly — a body `export default () => ({ id: uuid() })` runs the saved
// `uuid` generator. This is OPT-IN and additive: a body that references nothing takes the plain
// (uncached) generator path unchanged (RunRequestBody).

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// simpleIdentRe matches a generator display name that is a single JS identifier, so it can be
// bound as `globalThis.<name>`. A dotted name (e.g. `auth.bearer`) is not one identifier and is
// skipped — a documented v1 gap: a dotted-name generator cannot be bound as a composition
// global (only single-identifier names can).
var simpleIdentRe = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)

// composeGeneratorPrelude builds the synthetic import+bind prelude that exposes gens to a
// composing body. For each generator whose name is a simple identifier, in SORTED name order
// (so the prelude — and thus the compiled blob and its line offset — is deterministic), it
// emits two lines:
//
//	import __gen$<i> from "grpcview:gen/<name>";
//	globalThis.<name> = __gen$<i>;
//
// The __gen$<i> locals are indexed per EMITTED generator (a skipped non-identifier name does
// not consume an index) and are deliberately obscure so they cannot collide with a generator's
// own top-level bindings. Returns "" when nothing qualifies, in which case the caller compiles
// the body alone.
func composeGeneratorPrelude(gens map[string]string) string {
	names := make([]string, 0, len(gens))
	for name := range gens {
		names = append(names, name)
	}
	sort.Strings(names)

	var b strings.Builder
	i := 0
	for _, name := range names {
		if !simpleIdentRe.MatchString(name) {
			continue // dotted / non-identifier name: not bindable as a global (v1 gap)
		}
		fmt.Fprintf(&b, "import __gen$%d from %q;\n", i, generatorSpecPrefix+name)
		fmt.Fprintf(&b, "globalThis.%s = __gen$%d;\n", name, i)
		i++
	}
	return b.String()
}

// compileRequestBody compiles a composing TypeScript request body: the generator-exposing
// prelude (composeGeneratorPrelude) is prepended to the body and the whole is bundled with the
// generator resolver in force, so the prelude's grpcview:gen/* imports inline the saved
// generators. The entry-point convention still keys off the BODY alone — a body that declares
// `export default` compiles as an IIFE entry, called via generatorPostlude(args); one that does
// not takes the last-expression ESM form (empty postlude). Checking hasDefaultExport on the body
// (not the composed source) is deliberate: the prelude carries no `export default`, so it cannot
// flip that decision. The prepended prelude sits in the same author source ("script.ts") above
// the body, so c.authorPreludeLines records its length for remapJSError to subtract from a body
// error's mapped line. These builds bypass the compile cache (see buildBundleComposed).
func (e *Engine) compileRequestBody(body string, g Grant, args []any, gens map[string]string) (compiled, string, error) {
	prelude := composeGeneratorPrelude(gens)
	source := prelude + body
	preludeLines := strings.Count(prelude, "\n")

	var (
		c        compiled
		postlude string
		err      error
	)
	if hasDefaultExport(body) {
		c, err = e.bundler.buildEntryBundleComposed(source, g, gens)
		postlude = generatorPostlude(args)
	} else {
		c, err = e.bundler.buildBundleComposed(source, g, gens)
	}
	if err != nil {
		return compiled{}, "", err
	}
	c.authorPreludeLines = preludeLines
	return c, postlude, nil
}
