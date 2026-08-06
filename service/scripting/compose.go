package scripting

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var simpleIdentRe = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)

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
