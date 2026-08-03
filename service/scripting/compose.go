package scripting

// Composition: expose the workspace's saved generators to a TypeScript request body as
// ambient globals, by importing each through the grpcview:gen/<name> specifier.

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// A name that is not a single JS identifier cannot be bound as a global and is skipped.
var simpleIdentRe = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)

// Emits an import + globalThis bind pair per qualifying generator, in sorted name order so
// the prelude — and thus the compiled blob's line offset — is deterministic.
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
			continue
		}
		fmt.Fprintf(&b, "import __gen$%d from %q;\n", i, generatorSpecPrefix+name)
		fmt.Fprintf(&b, "globalThis.%s = __gen$%d;\n", name, i)
		i++
	}
	return b.String()
}

// The entry-point decision keys off the BODY alone: the prelude carries no `export default`,
// so it cannot flip it.
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
