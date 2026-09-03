package scripting

import (
	_ "embed"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/evanw/esbuild/pkg/api"
)

//go:embed codegen_entry.js
var codegenEntryJS string

// CodegenPolyfillJS is the TextEncoder/TextDecoder polyfill callers must Eval on its own,
// before the bundle — see codegen_polyfill.js for why it can't just be prepended.
//
//go:embed codegen_polyfill.js
var CodegenPolyfillJS string

// BuildCodegenBundle bundles codegen_entry.js (vendored under npm/@bufbuild) against the npm
// registry rooted at registryDir (see MaterializeNpmRegistry), via plain NodePaths resolution
// rather than bundler.go's registryResolverPlugin — @bufbuild/protobuf's subpath exports need
// real package.json "exports" resolution, which that plugin's literal path-join doesn't do.
// Result is a standalone IIFE that, once Eval'd, defines globalThis.__grpcview_codegen.
func BuildCodegenBundle(registryDir string) (string, error) {
	result := api.Build(api.BuildOptions{
		Stdin: &api.StdinOptions{
			Contents:   codegenEntryJS,
			Loader:     api.LoaderJS,
			Sourcefile: "codegen-entry.js",
			ResolveDir: registryDir,
		},
		Bundle:      true,
		Write:       false,
		Format:      api.FormatIIFE,
		Target:      esbuildTarget,
		Platform:    api.PlatformNeutral, // no DOM, no node fs/process shims
		TreeShaking: api.TreeShakingFalse,
		LogLevel:    api.LogLevelSilent,
		NodePaths:   []string{registryDir},
	})
	if len(result.Errors) > 0 {
		return "", bundleErrors(result.Errors)
	}
	var code strings.Builder
	for _, f := range result.OutputFiles {
		code.Write(f.Contents)
	}
	return code.String(), nil
}

// CodegenCallScript builds the per-job script invoking the bundle's entry point with a
// FileDescriptorSet. Hex-encoded: source text is the only channel into a running instance.
func CodegenCallScript(fileDescriptorSet []byte) string {
	return fmt.Sprintf("globalThis.__grpcview_codegen(%q)", hex.EncodeToString(fileDescriptorSet))
}
