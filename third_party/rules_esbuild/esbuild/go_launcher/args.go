package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/evanw/esbuild/pkg/api"
)

// buildArgs is the JSON shape written by esbuild.bzl's write_args_file (the --esbuild_args= file)
// and, optionally, a caller-supplied --user_args= file merged over it -- the same two inputs
// esbuild/private/launcher.js's runOneBuild consumes. Field names match esbuild's JS API
// (BuildOptions in the "esbuild" npm package), which is camelCase and not a 1:1 match for the Go
// API's field names/types -- this struct is the seam where that translation happens.
//
// Not every JS-API field is modeled: this covers what esbuild.bzl's own _esbuild_impl emits
// (bundle, define, entryPoints, external, logLevel, logLimit, tsconfig, metafile, outfile/outdir,
// platform, preserveSymlinks, sourcesContent, target, sourcemap, minify, ignoreAnnotations,
// format, splitting) plus the other common hand-authored options a --user_args= file is likely to
// carry. An unmodeled field is silently dropped rather than rejected -- see main.go's rejectPlugins
// for the one field (`plugins`) that must never be silently dropped.
type buildArgs struct {
	Bundle            bool              `json:"bundle,omitempty"`
	Define            map[string]string `json:"define,omitempty"`
	EntryPoints       []string          `json:"entryPoints,omitempty"`
	External          []string          `json:"external,omitempty"`
	LogLevel          string            `json:"logLevel,omitempty"`
	LogLimit          int               `json:"logLimit,omitempty"`
	Tsconfig          string            `json:"tsconfig,omitempty"`
	TsconfigRaw       string            `json:"tsconfigRaw,omitempty"`
	Metafile          bool              `json:"metafile,omitempty"`
	Outfile           string            `json:"outfile,omitempty"`
	Outdir            string            `json:"outdir,omitempty"`
	Outbase           string            `json:"outbase,omitempty"`
	Platform          string            `json:"platform,omitempty"`
	PreserveSymlinks  bool              `json:"preserveSymlinks,omitempty"`
	SourcesContent    *bool             `json:"sourcesContent,omitempty"`
	Target            json.RawMessage   `json:"target,omitempty"`
	Sourcemap         json.RawMessage   `json:"sourcemap,omitempty"`
	SourceRoot        string            `json:"sourceRoot,omitempty"`
	Minify            bool              `json:"minify,omitempty"`
	MinifyWhitespace  bool              `json:"minifyWhitespace,omitempty"`
	MinifyIdentifiers bool              `json:"minifyIdentifiers,omitempty"`
	MinifySyntax      bool              `json:"minifySyntax,omitempty"`
	IgnoreAnnotations bool              `json:"ignoreAnnotations,omitempty"`
	Format            string            `json:"format,omitempty"`
	Splitting         bool              `json:"splitting,omitempty"`
	GlobalName        string            `json:"globalName,omitempty"`
	AbsWorkingDir     string            `json:"absWorkingDir,omitempty"`
	Charset           string            `json:"charset,omitempty"`
	LegalComments     string            `json:"legalComments,omitempty"`
	TreeShaking       *bool             `json:"treeShaking,omitempty"`
	KeepNames         bool              `json:"keepNames,omitempty"`
	Banner            map[string]string `json:"banner,omitempty"`
	Footer            map[string]string `json:"footer,omitempty"`
	Loader            map[string]string `json:"loader,omitempty"`
	OutExtension      map[string]string `json:"outExtension,omitempty"`
	Conditions        []string          `json:"conditions,omitempty"`
	MainFields        []string          `json:"mainFields,omitempty"`
	ResolveExtensions []string          `json:"resolveExtensions,omitempty"`
	Alias             map[string]string `json:"alias,omitempty"`
	Inject            []string          `json:"inject,omitempty"`
	NodePaths         []string          `json:"nodePaths,omitempty"`
	Packages          string            `json:"packages,omitempty"`
	JSX               string            `json:"jsx,omitempty"`
	JSXFactory        string            `json:"jsxFactory,omitempty"`
	JSXFragment       string            `json:"jsxFragment,omitempty"`
	JSXImportSource   string            `json:"jsxImportSource,omitempty"`
	JSXDev            bool              `json:"jsxDev,omitempty"`
	JSXSideEffects    bool              `json:"jsxSideEffects,omitempty"`
	Pure              []string          `json:"pure,omitempty"`
	DropLabels        []string          `json:"dropLabels,omitempty"`
	Drop              []string          `json:"drop,omitempty"`
	Write             *bool             `json:"write,omitempty"`
	AllowOverwrite    bool              `json:"allowOverwrite,omitempty"`
	EntryNames        string            `json:"entryNames,omitempty"`
	ChunkNames        string            `json:"chunkNames,omitempty"`
	AssetNames        string            `json:"assetNames,omitempty"`
	PublicPath        string            `json:"publicPath,omitempty"`
	MangleProps       string            `json:"mangleProps,omitempty"`
	ReserveProps      string            `json:"reserveProps,omitempty"`
	MangleQuoted      *bool             `json:"mangleQuoted,omitempty"`
}

// toBuildOptions maps buildArgs onto api.BuildOptions. plugins is supplied by the caller (main.go
// installs the sandbox-guard plugin here; args never carries a `plugins` field of its own -- see
// rejectPlugins).
func (a buildArgs) toBuildOptions(plugins []api.Plugin) (api.BuildOptions, error) {
	opts := api.BuildOptions{
		Bundle:            a.Bundle,
		Define:            a.Define,
		EntryPoints:       a.EntryPoints,
		External:          a.External,
		LogLimit:          a.LogLimit,
		Tsconfig:          a.Tsconfig,
		TsconfigRaw:       a.TsconfigRaw,
		Metafile:          a.Metafile,
		Outfile:           a.Outfile,
		Outdir:            a.Outdir,
		Outbase:           a.Outbase,
		PreserveSymlinks:  a.PreserveSymlinks,
		SourceRoot:        a.SourceRoot,
		MinifyWhitespace:  a.Minify || a.MinifyWhitespace,
		MinifyIdentifiers: a.Minify || a.MinifyIdentifiers,
		MinifySyntax:      a.Minify || a.MinifySyntax,
		IgnoreAnnotations: a.IgnoreAnnotations,
		Splitting:         a.Splitting,
		GlobalName:        a.GlobalName,
		AbsWorkingDir:     a.AbsWorkingDir,
		KeepNames:         a.KeepNames,
		Banner:            a.Banner,
		Footer:            a.Footer,
		OutExtension:      a.OutExtension,
		Conditions:        a.Conditions,
		MainFields:        a.MainFields,
		ResolveExtensions: a.ResolveExtensions,
		Alias:             a.Alias,
		Inject:            a.Inject,
		NodePaths:         a.NodePaths,
		JSXFactory:        a.JSXFactory,
		JSXFragment:       a.JSXFragment,
		JSXImportSource:   a.JSXImportSource,
		JSXDev:            a.JSXDev,
		JSXSideEffects:    a.JSXSideEffects,
		Pure:              a.Pure,
		DropLabels:        a.DropLabels,
		AllowOverwrite:    a.AllowOverwrite,
		EntryNames:        a.EntryNames,
		ChunkNames:        a.ChunkNames,
		AssetNames:        a.AssetNames,
		PublicPath:        a.PublicPath,
		MangleProps:       a.MangleProps,
		ReserveProps:      a.ReserveProps,
		Plugins:           plugins,
		// Bazel needs the actual files on disk: the JS API defaults `write` to true when
		// outfile/outdir is set, so match that unless the args explicitly say otherwise.
		Write: true,
	}
	if a.Write != nil {
		opts.Write = *a.Write
	}
	if a.SourcesContent != nil {
		if *a.SourcesContent {
			opts.SourcesContent = api.SourcesContentInclude
		} else {
			opts.SourcesContent = api.SourcesContentExclude
		}
	}
	if a.TreeShaking != nil {
		if *a.TreeShaking {
			opts.TreeShaking = api.TreeShakingTrue
		} else {
			opts.TreeShaking = api.TreeShakingFalse
		}
	}
	if a.MangleQuoted != nil {
		if *a.MangleQuoted {
			opts.MangleQuoted = api.MangleQuotedTrue
		} else {
			opts.MangleQuoted = api.MangleQuotedFalse
		}
	}

	if len(a.Loader) > 0 {
		loaders := make(map[string]api.Loader, len(a.Loader))
		for ext, name := range a.Loader {
			l, err := parseLoader(name)
			if err != nil {
				return api.BuildOptions{}, fmt.Errorf("loader[%q]: %w", ext, err)
			}
			loaders[ext] = l
		}
		opts.Loader = loaders
	}

	var err error
	if opts.Platform, err = parsePlatform(a.Platform); err != nil {
		return api.BuildOptions{}, err
	}
	if opts.Format, err = parseFormat(a.Format); err != nil {
		return api.BuildOptions{}, err
	}
	if opts.LogLevel, err = parseLogLevel(a.LogLevel); err != nil {
		return api.BuildOptions{}, err
	}
	if opts.Charset, err = parseCharset(a.Charset); err != nil {
		return api.BuildOptions{}, err
	}
	if opts.LegalComments, err = parseLegalComments(a.LegalComments); err != nil {
		return api.BuildOptions{}, err
	}
	if opts.JSX, err = parseJSX(a.JSX); err != nil {
		return api.BuildOptions{}, err
	}
	if opts.Packages, err = parsePackages(a.Packages); err != nil {
		return api.BuildOptions{}, err
	}
	if opts.Drop, err = parseDrop(a.Drop); err != nil {
		return api.BuildOptions{}, err
	}
	if opts.Sourcemap, err = parseSourcemap(a.Sourcemap); err != nil {
		return api.BuildOptions{}, err
	}
	if opts.Target, opts.Engines, err = parseTargets(a.Target); err != nil {
		return api.BuildOptions{}, err
	}

	return opts, nil
}

func parsePlatform(s string) (api.Platform, error) {
	switch s {
	case "", "browser":
		return api.PlatformBrowser, nil
	case "node":
		return api.PlatformNode, nil
	case "neutral":
		return api.PlatformNeutral, nil
	default:
		return 0, fmt.Errorf("platform: unrecognized value %q", s)
	}
}

func parseFormat(s string) (api.Format, error) {
	switch s {
	case "":
		return api.FormatDefault, nil
	case "iife":
		return api.FormatIIFE, nil
	case "cjs":
		return api.FormatCommonJS, nil
	case "esm":
		return api.FormatESModule, nil
	default:
		return 0, fmt.Errorf("format: unrecognized value %q", s)
	}
}

func parseLogLevel(s string) (api.LogLevel, error) {
	switch s {
	case "":
		return api.LogLevelInfo, nil
	case "silent":
		return api.LogLevelSilent, nil
	case "verbose":
		return api.LogLevelVerbose, nil
	case "debug":
		return api.LogLevelDebug, nil
	case "info":
		return api.LogLevelInfo, nil
	case "warning":
		return api.LogLevelWarning, nil
	case "error":
		return api.LogLevelError, nil
	default:
		return 0, fmt.Errorf("logLevel: unrecognized value %q", s)
	}
}

func parseCharset(s string) (api.Charset, error) {
	switch s {
	case "":
		return api.CharsetDefault, nil
	case "ascii":
		return api.CharsetASCII, nil
	case "utf8":
		return api.CharsetUTF8, nil
	default:
		return 0, fmt.Errorf("charset: unrecognized value %q", s)
	}
}

func parseLegalComments(s string) (api.LegalComments, error) {
	switch s {
	case "":
		return api.LegalCommentsDefault, nil
	case "none":
		return api.LegalCommentsNone, nil
	case "inline":
		return api.LegalCommentsInline, nil
	case "eof":
		return api.LegalCommentsEndOfFile, nil
	case "linked":
		return api.LegalCommentsLinked, nil
	case "external":
		return api.LegalCommentsExternal, nil
	default:
		return 0, fmt.Errorf("legalComments: unrecognized value %q", s)
	}
}

func parseJSX(s string) (api.JSX, error) {
	switch s {
	case "":
		return api.JSXTransform, nil
	case "transform":
		return api.JSXTransform, nil
	case "preserve":
		return api.JSXPreserve, nil
	case "automatic":
		return api.JSXAutomatic, nil
	default:
		return 0, fmt.Errorf("jsx: unrecognized value %q", s)
	}
}

func parsePackages(s string) (api.Packages, error) {
	switch s {
	case "":
		return api.PackagesDefault, nil
	case "bundle":
		return api.PackagesBundle, nil
	case "external":
		return api.PackagesExternal, nil
	default:
		return 0, fmt.Errorf("packages: unrecognized value %q", s)
	}
}

func parseDrop(entries []string) (api.Drop, error) {
	var drop api.Drop
	for _, e := range entries {
		switch e {
		case "console":
			drop |= api.DropConsole
		case "debugger":
			drop |= api.DropDebugger
		default:
			return 0, fmt.Errorf("drop: unrecognized value %q", e)
		}
	}
	return drop, nil
}

func parseLoader(s string) (api.Loader, error) {
	switch s {
	case "base64":
		return api.LoaderBase64, nil
	case "binary":
		return api.LoaderBinary, nil
	case "copy":
		return api.LoaderCopy, nil
	case "css":
		return api.LoaderCSS, nil
	case "dataurl":
		return api.LoaderDataURL, nil
	case "default":
		return api.LoaderDefault, nil
	case "empty":
		return api.LoaderEmpty, nil
	case "file":
		return api.LoaderFile, nil
	case "global-css":
		return api.LoaderGlobalCSS, nil
	case "js":
		return api.LoaderJS, nil
	case "json":
		return api.LoaderJSON, nil
	case "jsx":
		return api.LoaderJSX, nil
	case "local-css":
		return api.LoaderLocalCSS, nil
	case "text":
		return api.LoaderText, nil
	case "ts":
		return api.LoaderTS, nil
	case "tsx":
		return api.LoaderTSX, nil
	default:
		return 0, fmt.Errorf("unrecognized loader %q", s)
	}
}

// parseSourcemap accepts either JSON form the JS API allows: a boolean, or one of "linked" /
// "external" / "inline" / "both". esbuild.bzl only ever emits the string form, but a hand-authored
// --user_args= file may use the boolean shorthand.
func parseSourcemap(raw json.RawMessage) (api.SourceMap, error) {
	if len(raw) == 0 {
		return api.SourceMapNone, nil
	}
	var b bool
	if err := json.Unmarshal(raw, &b); err == nil {
		if b {
			return api.SourceMapLinked, nil
		}
		return api.SourceMapNone, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return 0, fmt.Errorf("sourcemap: expected a boolean or string, got %s", raw)
	}
	switch s {
	case "linked":
		return api.SourceMapLinked, nil
	case "inline":
		return api.SourceMapInline, nil
	case "external":
		return api.SourceMapExternal, nil
	case "both":
		return api.SourceMapInlineAndExternal, nil
	default:
		return 0, fmt.Errorf("sourcemap: unrecognized value %q", s)
	}
}

// esTargets maps the ECMAScript-version half of the JS API's `target` strings onto Go's single
// Target enum. engineTargetRE + engineNames handle the other half: runtime/browser engine
// versions, which the Go API keeps in a separate Engines slice instead.
//
// This split is "the fiddly part" flagged by the design doc: esbuild's JS API takes `target` as
// one flat string array (e.g. ["es2020", "chrome58", "node18"]) and the Go API's BuildOptions
// requires the caller to have already sorted that array into Target (at most one ES version) plus
// Engines (any number of runtime engines).
var esTargets = map[string]api.Target{
	"es5":     api.ES5,
	"es6":     api.ES2015,
	"esnext":  api.ESNext,
	"es2015":  api.ES2015,
	"es2016":  api.ES2016,
	"es2017":  api.ES2017,
	"es2018":  api.ES2018,
	"es2019":  api.ES2019,
	"es2020":  api.ES2020,
	"es2021":  api.ES2021,
	"es2022":  api.ES2022,
	"es2023":  api.ES2023,
	"es2024":  api.ES2024,
	"es2025":  api.ES2025,
}

var engineNames = map[string]api.EngineName{
	"chrome":  api.EngineChrome,
	"deno":    api.EngineDeno,
	"edge":    api.EngineEdge,
	"firefox": api.EngineFirefox,
	"hermes":  api.EngineHermes,
	"ie":      api.EngineIE,
	"ios":     api.EngineIOS,
	"node":    api.EngineNode,
	"opera":   api.EngineOpera,
	"rhino":   api.EngineRhino,
	"safari":  api.EngineSafari,
}

var engineTargetRE = regexp.MustCompile(`^(chrome|deno|edge|firefox|hermes|ie|ios|node|opera|rhino|safari)([0-9]+(?:\.[0-9]+)*)$`)

// parseTargets unmarshals the JSON `target` field (either a bare string, e.g. "es2020", or an
// array of strings, matching whichever shape the caller used -- esbuild.bzl's own string_list
// attr always encodes as an array) and splits it into a Target and an Engines slice.
func parseTargets(raw json.RawMessage) (api.Target, []api.Engine, error) {
	if len(raw) == 0 {
		return api.DefaultTarget, nil, nil
	}

	var entries []string
	if err := json.Unmarshal(raw, &entries); err != nil {
		var single string
		if err := json.Unmarshal(raw, &single); err != nil {
			return 0, nil, fmt.Errorf("target: expected a string or array of strings, got %s", raw)
		}
		entries = strings.Split(single, ",")
	}

	target := api.DefaultTarget
	var engines []api.Engine
	for _, rawEntry := range entries {
		s := strings.ToLower(strings.TrimSpace(rawEntry))
		if s == "" {
			continue
		}
		if t, ok := esTargets[s]; ok {
			// Matches esbuild's own canonical parser (pkg/cli/cli_impl.go's parseTargets): on
			// multiple ES-version entries, the last one wins rather than erroring.
			target = t
			continue
		}
		if m := engineTargetRE.FindStringSubmatch(s); m != nil {
			engines = append(engines, api.Engine{Name: engineNames[m[1]], Version: m[2]})
			continue
		}
		return 0, nil, fmt.Errorf(
			"target: unrecognized entry %q (expected an ECMAScript version like \"es2020\"/\"esnext\", or an engine like \"chrome58\")",
			rawEntry)
	}
	return target, engines, nil
}
