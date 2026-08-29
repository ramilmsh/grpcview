// Command go_launcher is a Go-native replacement for esbuild/private/launcher.js: an
// aspect_rules_esbuild `launcher` that drives esbuild's Go API (github.com/evanw/esbuild/pkg/api)
// in-process instead of shelling out to esbuild's JS API, which itself drives the real Go esbuild
// binary as a child process over a stdin/stdout IPC protocol. See
// docs/design/planned/esbuild-go-launcher-experiment.md in the grpcview root for why.
//
// It accepts the same three flags as the JS launcher, in the same `--flag=value` form:
//
//	--esbuild_args=<path>   required; a JSON file of esbuild JS-API-shaped BuildOptions
//	--user_args=<path>      optional; a second JSON file shallow-merged over the first
//	--config_file=<path>    optional; unsupported here (see rejectConfigFile) -- always an error
//
// and, when ctx.attr.metafile is set, esbuild.bzl also appends:
//
//	--metafile=<path>       where to write the build's metafile JSON on success
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/evanw/esbuild/pkg/api"
)

func main() {
	// Capture the execroot (this action's true working directory, matching the JS launcher's
	// JS_BINARY__EXECROOT) before anything below changes cwd.
	execroot, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, fmt.Errorf("determining execroot: %w", err))
		os.Exit(1)
	}

	// esbuild.bzl's `_bin_relative_path` computes every path handed to this launcher --
	// --esbuild_args=/--user_args=/--config_file=/--metafile=, plus entryPoints/tsconfig/outdir
	// values inside the args JSON itself -- relative to BAZEL_BINDIR, not the execroot. The JS
	// launcher only works because aspect_rules_js's js_binary.sh.tpl wrapper cd's into
	// BAZEL_BINDIR before exec'ing node; esbuild.bzl's non-js_binary branch (this launcher's
	// invocation path) sets BAZEL_BINDIR as an env var instead of cd'ing for it, so mirror that
	// chdir here before reading any flag-referenced file.
	if bindir := os.Getenv("BAZEL_BINDIR"); bindir != "" {
		if err := os.Chdir(bindir); err != nil {
			fmt.Fprintln(os.Stderr, fmt.Errorf("chdir to BAZEL_BINDIR %q: %w", bindir, err))
			os.Exit(1)
		}
	}

	if err := run(os.Args[1:], execroot); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(argv []string, execroot string) error {
	esbuildArgsPath, ok := getFlag(argv, "--esbuild_args")
	if !ok {
		return fmt.Errorf("expected flag '--esbuild_args' passed to launcher, but not found")
	}

	merged, err := readArgsFile(esbuildArgsPath)
	if err != nil {
		return fmt.Errorf("reading esbuild flags param file: %w", err)
	}

	if userArgsPath, ok := getFlag(argv, "--user_args"); ok {
		userArgs, err := readArgsFile(userArgsPath)
		if err != nil {
			return fmt.Errorf("reading user args file: %w", err)
		}
		for k, v := range userArgs {
			merged[k] = v
		}
	}

	// The JS launcher loads --config_file= as an arbitrary ES module (`await import(...)`) and
	// merges its exported object in. A Go binary has no JS runtime to execute that with, so any
	// config file -- whether or not it declares plugins -- is out of reach here, not just the
	// plugins case. Surfacing one general error at this flag, rather than only rejecting a
	// `plugins` key we can never actually see inside it, is the honest version of "explicitly
	// reject a config file that declares plugins": we cannot tell the difference between a
	// plugin-free config and one that isn't without evaluating its JS.
	if configPath, ok := getFlag(argv, "--config_file"); ok {
		return fmt.Errorf(
			"go_launcher does not support esbuild config files (%s) -- it cannot execute the "+
				"arbitrary JS a config file may run, including any `plugins` it declares; "+
				"use launcher = default (JS) instead", configPath)
	}

	if err := rejectPlugins(merged); err != nil {
		return err
	}

	buildArgsJSON, err := json.Marshal(merged)
	if err != nil {
		return fmt.Errorf("re-marshaling merged esbuild args: %w", err)
	}
	var parsed buildArgs
	if err := json.Unmarshal(buildArgsJSON, &parsed); err != nil {
		return fmt.Errorf("parsing esbuild args: %w", err)
	}

	plugins, err := buildPlugins(execroot)
	if err != nil {
		return err
	}

	opts, err := parsed.toBuildOptions(plugins)
	if err != nil {
		return fmt.Errorf("mapping esbuild args: %w", err)
	}

	result := api.Build(opts)
	if len(result.Errors) > 0 {
		// The JS launcher's `esbuild.build(args)` promise rejects on a build error, and its catch
		// block unconditionally `console.error(e)`s that -- regardless of logLevel. The Go API
		// instead only ever prints through its internal, logLevel-gated logger, which stays
		// silent on LogLevelSilent even for a fatal error. Format and print explicitly here so a
		// failure is never swallowed by a quiet logLevel the way it would be if this only relied
		// on esbuild's own logging.
		for _, msg := range api.FormatMessages(result.Errors, api.FormatMessagesOptions{Kind: api.ErrorMessage}) {
			fmt.Fprintln(os.Stderr, msg)
		}
		return fmt.Errorf("esbuild build failed with %d error(s)", len(result.Errors))
	}

	if result.Metafile != "" {
		if metafilePath, ok := getFlag(argv, "--metafile"); ok {
			if err := os.WriteFile(metafilePath, []byte(result.Metafile), 0o644); err != nil {
				return fmt.Errorf("writing metafile: %w", err)
			}
		}
	}

	return nil
}

// buildPlugins assembles the fixed plugin list for this launcher. Unlike the JS launcher and
// esbuild.bzl's `config` attribute, there is no way for a caller to append their own plugins here
// (see rejectPlugins) -- the only plugin this launcher ever installs is its own sandbox guard, and
// only when the rule asked for it via the same env var the JS launcher gates on.
func buildPlugins(execroot string) ([]api.Plugin, error) {
	if os.Getenv("ESBUILD_BAZEL_SANDBOX_PLUGIN") == "" {
		return nil, nil
	}
	// Analog of the JS plugin's BAZEL_BINDIR: esbuild.bzl sets this explicitly for a non-js_binary
	// launcher (see esbuild.bzl's `else` branch, `env["BAZEL_BINDIR"] = ctx.bin_dir.path`) since
	// only the js_binary code path gets it set for free by aspect_rules_js's own wrapper.
	bindir := os.Getenv("BAZEL_BINDIR")
	return []api.Plugin{bazelSandboxPlugin(execroot, bindir)}, nil
}

// rejectPlugins fails fast, with a clear error, rather than silently ignoring a `plugins` entry in
// the merged esbuild args JSON (--esbuild_args= merged with --user_args=) the way an unmodeled
// field elsewhere in buildArgs would be. A Go binary cannot execute the arbitrary JS a plugin is
// written in, so honoring the field is not an option; misinterpreting it as "we bundled without
// your plugin, silently" would be worse than refusing to build at all.
func rejectPlugins(merged map[string]json.RawMessage) error {
	if _, ok := merged["plugins"]; !ok {
		return nil
	}
	return fmt.Errorf(
		"go_launcher does not support esbuild config plugins; use launcher = default (JS) instead")
}

func readArgsFile(path string) (map[string]json.RawMessage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// getFlag finds a `--flag=value` argument, matching launcher.js's getFlag.
func getFlag(argv []string, flag string) (string, bool) {
	prefix := flag + "="
	for _, arg := range argv {
		if strings.HasPrefix(arg, prefix) {
			return strings.TrimPrefix(arg, prefix), true
		}
	}
	return "", false
}
