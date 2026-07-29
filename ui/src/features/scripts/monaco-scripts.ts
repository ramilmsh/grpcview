// Monaco TypeScript setup for the Scripts scratchpad: teach the TS language
// service what the QuickJS engine gives a script (a buffered `console`, top-level
// await, "last expression is the value") and make the bare `import dayjs from
// "dayjs"` resolve to the real dayjs@1.11.21 types — so the editor offers real
// IntelliSense (autocomplete, hover, signature help, diagnostics) fully offline.
//
// Runs as a side effect on import (the monaco-nocturne.ts idiom): ScriptsView
// imports SCRATCH_PATH from here, which evaluates this module once and configures
// the shared `typescriptDefaults`. We reach Monaco through the same
// `monaco-editor` singleton monaco-nocturne.ts pointed @monaco-editor/react's
// loader at, so this configuration is what the mounted editor's worker sees.
//
// PREREQUISITE: the TS language worker must be bundled — see the `ts.worker`
// import in theme/monaco-nocturne.ts. Without it this config is inert.
import * as monaco from "monaco-editor";
// The vendored dayjs types as raw text (see vendor/dayjs.d.ts for how/why). `?raw`
// is a core Vite feature that inlines the file as a string and survives the
// singlefile release build — nothing is fetched at runtime.
import dayjsTypes from "./vendor/dayjs.d.ts?raw";

const ts = monaco.languages.typescript;

// The scratch buffer's model path. The scheme is deliberately `file://` (not the
// app's `grpcview://` model convention, e.g. Editor.tsx): NodeJs module
// resolution walks `node_modules` UP FROM THE IMPORTING FILE within the same URI
// scheme, so a `file://` script can reach the virtual `file:///node_modules/dayjs`
// libs below while a `grpcview://` one never could (cross-scheme). The `.ts`
// extension is what makes the worker treat the buffer as a TypeScript module.
export const SCRATCH_PATH = "file:///scripts/scratch.ts";

// Compiler options. Note monaco's PUBLIC enums lag the bundled TS: ScriptTarget
// names nothing between ES2020 and ESNext(99), and ModuleResolutionKind has only
// Classic/NodeJs — so we use ESNext + NodeJs and get the actual ES2022 library
// surface from `lib` below (the worker does bundle lib.es2022.d.ts).
ts.typescriptDefaults.setCompilerOptions({
  target: ts.ScriptTarget.ESNext,
  module: ts.ModuleKind.ESNext,
  // NodeJs resolution is what makes `import dayjs from "dayjs"` find the virtual
  // file:///node_modules/dayjs package registered below.
  moduleResolution: ts.ModuleResolutionKind.NodeJs,
  // Force: treat the scratch buffer as a module even before the user types an
  // import, so top-level `await` (which the engine allows) never false-errors as
  // "only allowed inside a module". `3` === ts.ModuleDetectionKind.Force, which
  // monaco's CompilerOptions type only exposes via its index signature.
  moduleDetection: 3,
  allowNonTsExtensions: true,
  // dayjs ships `export = dayjs` (CommonJS-style) but we import it as a default;
  // these two make `import dayjs from "dayjs"` synthesize that default instead of
  // erroring "Module has no default export".
  allowSyntheticDefaultImports: true,
  esModuleInterop: true,
  // A scratchpad, not a strict codebase — don't nag about implicit any etc.;
  // diagnostics stay ON (below) for genuine type errors and unresolved symbols.
  strict: false,
  noEmit: true,
  // ES2022 built-ins only. Deliberately NO "dom": the engine provides no
  // window/document/setTimeout/localStorage, and its `fetch` is a small SUBSET of
  // the WHATWG one — pulling in "dom" would type it as the full `window.fetch` and
  // lie about (blob/formData/streaming/Request/Headers.set/…). The two host globals
  // it does provide — `console` and that custom `fetch` — we declare ourselves below.
  lib: ["es2022"],
  // Don't type-check the vendored dayjs .d.ts itself; we only care about the user
  // buffer's diagnostics, and this keeps the worker fast.
  skipLibCheck: true,
});

// Push every model to the worker eagerly so diagnostics/IntelliSense are live
// without the model first having to be focused.
ts.typescriptDefaults.setEagerModelSync(true);

// Keep genuine diagnostics ON (the whole point is type-checking + IntelliSense).
// No spurious codes to suppress: `moduleDetection: Force` above removes the
// otherwise-common top-level-await-outside-module false error, and the trailing
// value-expression is a valid expression statement.
ts.typescriptDefaults.setDiagnosticsOptions({
  noSemanticValidation: false,
  noSyntaxValidation: false,
  noSuggestionDiagnostics: false,
});

// --- Virtual node_modules/dayjs so the bare `"dayjs"` import resolves ----------
// package.json first: NodeJs resolution reads it and follows "types" to the .d.ts.
// It mirrors the real vendored package (service/scripting/testdata/npm/dayjs) and
// adds the "types" field so resolution is explicit rather than index-fallback.
ts.typescriptDefaults.addExtraLib(
  JSON.stringify(
    { name: "dayjs", version: "1.11.21", main: "dayjs.min.js", types: "index.d.ts" },
    null,
    2
  ),
  "file:///node_modules/dayjs/package.json"
);
ts.typescriptDefaults.addExtraLib(dayjsTypes, "file:///node_modules/dayjs/index.d.ts");

// --- Ambient script-environment types ------------------------------------------
// A SCRIPT (no import/export) so these are GLOBAL in the scratch module. Describes
// what the engine gives every run: a buffered `console` and a browser-style `fetch`
// (network is on for ALL scripts — there is no capability grant). It intentionally
// omits request/vars/env inputs, which arrive when generators/middleware are wired
// up. `console`, `fetch`, and the fetch types are a CUSTOM shim — not the DOM's —
// so they mirror only what the engine actually implements; this is safe (no
// duplicate-identifier clash) because `lib: ["es2022"]` above excludes the DOM.
const ENV_DTS = `
/**
 * grpcview scripting environment — QuickJS compiled to WebAssembly.
 *
 * The scratch buffer is evaluated as an ES module: top-level \`await\` is allowed,
 * and the script's VALUE is its LAST top-level expression (there is no
 * \`return\` — the trailing expression is the result). Its host globals are a
 * buffered \`console\` and a browser-style \`fetch\` (both declared below); there
 * are no workspace inputs yet.
 */
interface Console {
  /** Buffered at level "debug". */
  debug(...data: unknown[]): void;
  /** Buffered at level "log". */
  log(...data: unknown[]): void;
  /** Alias of \`log\` — buffered at level "log". */
  info(...data: unknown[]): void;
  /** Buffered at level "warn". */
  warn(...data: unknown[]): void;
  /** Buffered at level "error". */
  error(...data: unknown[]): void;
}
declare const console: Console;

/** The case-insensitive header bag on a fetch \`Response\`. */
interface Headers {
  /** The header's comma-joined value, or null if absent. Name is case-insensitive. */
  get(name: string): string | null;
  has(name: string): boolean;
  forEach(callback: (value: string, name: string) => void): void;
}
type HeadersInit = Record<string, string> | [string, string][] | Headers;
/** Options for \`fetch\`. Only these fields are honored by the engine. */
interface RequestInit {
  method?: string;
  headers?: HeadersInit;
  /** Request body; a non-string is coerced with String(). */
  body?: string;
}
/** The subset of the WHATWG Response the engine reconstructs. */
interface Response {
  readonly ok: boolean;
  readonly status: number;
  readonly statusText: string;
  readonly url: string;
  readonly headers: Headers;
  text(): Promise<string>;
  json(): Promise<any>;
}
/**
 * Perform an HTTP request. Enabled for EVERY script — there is no capability
 * grant to network. The request is bounded by the run's wall-clock budget; a bad
 * URL or transport failure REJECTS the promise (fetch never throws synchronously).
 */
declare function fetch(input: string, init?: RequestInit): Promise<Response>;
`;
ts.typescriptDefaults.addExtraLib(ENV_DTS, "file:///grpcview-scripts-env.d.ts");

// --- The shared `gv` scripting global (gv-features-plan.md §"Phase 0 — shared gv foundation") ---
// A SCRIPT (no import/export), exactly like ENV_DTS above, so `gv`/`InvokeResult` are GLOBAL —
// ambient across every editor that (transitively) imports this module: the request body editor
// (Editor.tsx), the request AND folder metadata editor (MetadataEditor.tsx, both the request tab
// and the new folder-metadata dialog), and the Scripts scratchpad. The backend installs `gv`
// unconditionally in every run (service/scripting/marshal.go buildGvPrelude) and freezes it once,
// so this declares the FULL merged surface across all three features — feature 1's
// `metadata.inherit()`, feature 3's `request.params` + `invoke()` + `InvokeResult` — even though
// this frontend pass only lights up feature 1's editor affordances. Members degrade gracefully
// rather than being absent (inherit() -> {}, params -> {} with no invoke context), so one static
// declaration is correct in every context; nothing here needs to vary per editor.
//
// Registered EXACTLY ONCE, at MODULE-EVALUATION time (this top-level call, not inside a React
// effect) at the single constant URI below. This file is a plain ES module singleton — every
// importer (Editor.tsx, MetadataEditor.tsx, ScriptsView.tsx) triggers the SAME one-time evaluation
// of this top level, never a re-run — so, unlike the per-request/per-generator libs above (which
// dispose-and-re-add on change via a component effect), `gv.d.ts` is static and gets no dispose
// path at all. Do not move this registration into a component/effect and do not add a second
// `addExtraLib` at this URI anywhere else: typescriptDefaults throws "Duplicate definition" on a
// second registration at the same path, and there is nothing here that ever needs to change.
const GV_DTS = `
/**
 * grpcview's shared scripting global (gv-features-plan.md). Installed in every script run —
 * request body, request metadata, folder metadata, middleware, scenario, and inline-composed
 * generators — so its members are always present; they degrade gracefully where there is no
 * relevant context rather than being absent.
 */
declare const gv: {
  /** Folder-metadata inheritance (Feature 1). */
  metadata: {
    /**
     * This node's already-evaluated, merged ancestor-folder metadata (root through immediate
     * parent) — precomputed, not a re-entrant call. \`{}\` where there is no inheritance context.
     */
    inherit(): { [key: string]: string[] };
  };
  /** Kwargs passed by a \`gv.invoke()\` caller; \`{}\` on a top-level user invoke (Feature 3). */
  request: { params: Readonly<Record<string, unknown>> };
  /**
   * Invoke a saved request by its slash-separated display-name path (e.g.
   * "UserService/GetUser"), optionally passing kwargs the target reads as \`gv.request.params\`
   * (Feature 3). A gRPC-status failure still RESOLVES (\`ok: false\`, fetch-style); invoke() only
   * REJECTS for an unknown path, a streaming target, a body/metadata that won't evaluate, or the
   * invoke-depth cap.
   */
  invoke(path: string, params?: Record<string, unknown>): Promise<InvokeResult>;
};
/** The decoded result of \`gv.invoke()\` — a fetch-style POJO, never a proto Struct/Duration/Any. */
type InvokeResult = {
  /** \`true\` iff \`status.code === 0\`. */
  ok: boolean;
  status: { code: number; message: string };
  /** Decoded response JSON, or \`null\` on failure. */
  body: unknown | null;
  /** Merged response header + trailer metadata. */
  metadata: Record<string, string[]>;
  /** The metadata actually sent with the nested request. */
  requestMetadata: Record<string, string[]>;
  latencyMs: number;
};
`;
ts.typescriptDefaults.addExtraLib(GV_DTS, "file:///grpcview/gv.d.ts");
