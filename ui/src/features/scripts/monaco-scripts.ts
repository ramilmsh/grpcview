// Configures the shared Monaco `typescriptDefaults` for the Scripts scratchpad as a
// side effect on import. Inert unless the TS worker is bundled — see theme/monaco-nocturne.ts.
import * as monaco from "monaco-editor";
import type * as Monaco from "monaco-editor";
// Vite's `?raw` suffix has no esbuild equivalent, so the vendored dayjs .d.ts
// is a plain exported string constant instead — see the file itself.
import dayjsTypes from "./vendor/dayjs-types";

const ts = monaco.languages.typescript;

// Must be `file://`, not the app's `grpcview://` convention: NodeJs resolution only
// walks node_modules within the importing file's own URI scheme.
export const SCRATCH_PATH = "file:///scripts/scratch.ts";

// monaco's public enums lag the bundled TS: no ES2022 target, no Node16 resolution.
//
// Exported so gv-types.ts's useWorkspaceModuleTypes can re-set `compilerOptions.paths` without
// dropping everything set here: setCompilerOptions REPLACES the whole object, so a caller that
// only wants to add `paths` still has to restate every option below.
export const baseCompilerOptions: Monaco.languages.typescript.CompilerOptions = {
  target: ts.ScriptTarget.ESNext,
  module: ts.ModuleKind.ESNext,
  moduleResolution: ts.ModuleResolutionKind.NodeJs,
  // 3 === ModuleDetectionKind.Force — makes top-level `await` legal with no imports.
  moduleDetection: 3,
  allowNonTsExtensions: true,
  // dayjs ships `export = dayjs` but is imported as a default.
  allowSyntheticDefaultImports: true,
  esModuleInterop: true,
  strict: false,
  noEmit: true,
  // No "dom": it would clash with the custom console/fetch declarations below.
  lib: ["es2022"],
  skipLibCheck: true,
};
ts.typescriptDefaults.setCompilerOptions(baseCompilerOptions);

ts.typescriptDefaults.setEagerModelSync(true);

ts.typescriptDefaults.setDiagnosticsOptions({
  noSemanticValidation: false,
  noSyntaxValidation: false,
  noSuggestionDiagnostics: false,
});

// The package.json is required: NodeJs resolution follows its "types" to the .d.ts.
ts.typescriptDefaults.addExtraLib(
  JSON.stringify(
    { name: "dayjs", version: "1.11.21", main: "dayjs.min.js", types: "index.d.ts" },
    null,
    2
  ),
  "file:///node_modules/dayjs/package.json"
);
ts.typescriptDefaults.addExtraLib(dayjsTypes, "file:///node_modules/dayjs/index.d.ts");

// Must stay a script (no import/export) so its declarations are global in the buffer.
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

// Module scope, never a React effect: a second addExtraLib at this URI throws.
//
// `gv` is de-globalized in full (script-imports/decisions.md §3): no grpcview-specific
// identifier is global any more. Each capability below is a "grpcview:" module import
// instead. GvRequestMap / GvPath / GvBody / InvokeResult stay ambient (types, not values) so
// the module declarations below can reference them without an import of their own.
//
// This only matters in MODULE position (a saved script, or a request body/metadata's hidden
// `export default` wrapper — see body-wrapper.ts): `import` there resolves fine. A request
// body or metadata typed directly in EXPRESSION position (what a user sees, inside that
// hidden wrapper) cannot use an `import` statement — it is a hard parse error there — and
// must use `require("grpcview:invoke")` etc. instead; see decisions.md §4.
const GV_DTS = `
/**
 * The saved requests \`invoke()\` (from "grpcview:invoke") can reach, path → response type.
 * Declared empty here and MERGED into by the generated \`gv-requests.d.ts\` (proto-types.ts
 * \`gvRequestMapDts\`), which \`gv-types.ts\` registers app-level per collection — every editor
 * that can import "grpcview:invoke" needs it, so no one editor owns it. With no map
 * registered \`keyof\` is \`never\`, every path takes the string branch, and \`body\` is \`any\` —
 * the untyped behavior, unchanged.
 *
 * Streaming saved requests are deliberately absent: \`invoke()\` rejects them, so there is no
 * response to type.
 */
interface GvRequestMap {}
/** Paths of the collection's invokable saved requests. */
type GvPath = keyof GvRequestMap;
/** The response type behind a path literal; \`any\` for a computed or unrecognized path. */
type GvBody<P> = P extends keyof GvRequestMap ? GvRequestMap[P]["response"] : any;
/** The decoded result of \`invoke()\` — a fetch-style POJO, never a proto Struct/Duration/Any. */
type InvokeResult<T = any> = {
  /** \`true\` iff \`status.code === 0\`. */
  ok: boolean;
  status: { code: number; message: string };
  /**
   * Decoded response JSON — the target method's \`<Message>Json\` type when the path is a known
   * literal, else \`any\`. \`null\` at runtime when \`ok\` is false; \`strictNullChecks\` is off here,
   * so that is documented rather than encoded in the type.
   */
  body: T;
  /** Merged response header + trailer metadata. */
  metadata: Record<string, string[]>;
  /** The metadata actually sent with the nested request. */
  requestMetadata: Record<string, string[]>;
  latencyMs: number;
};
/** The shape of a middleware's ctx argument: the body, the metadata, and the call target. */
type GvMiddlewareCtx = {
  body: Record<string, unknown>;
  metadata: Record<string, string>;
  target: string;
};
/**
 * A middleware's runtime contract: take ctx and return it (or a promise of it, or nothing to
 * leave it unchanged). Annotate a default export with \`satisfies GvMiddleware\` to get a red
 * squiggle for the wrong shape — without it the shape is only checked at runtime, on attach
 * and on run.
 */
type GvMiddleware = (ctx: GvMiddlewareCtx) => GvMiddlewareCtx | void | Promise<GvMiddlewareCtx | void>;

/**
 * What a specifier literal \`require()\`s to. The "grpcview:" capabilities are known statically;
 * the workspace's own \`@/…\` and \`#/…\` modules are MERGED in by the generated
 * \`gv-modules.d.ts\` (require-types.ts \`requireTypesDts\`), which \`gv-types.ts\` registers
 * app-level and recomputes per collection — the same arrangement as GvRequestMap. A specifier
 * absent from here still compiles; it just falls back to \`any\`.
 */
interface GvModules {
  "grpcview:invoke": typeof import("grpcview:invoke");
  "grpcview:assert": typeof import("grpcview:assert");
  "grpcview:metadata": typeof import("grpcview:metadata");
  "grpcview:request": typeof import("grpcview:request");
}
/**
 * The expression-position escape hatch. An \`import\` STATEMENT cannot stand where a request body
 * or a metadata object literal is written — that is a hard parse error, not a style choice — so
 * an expression form pulls a module in with \`require("#/scripts/ids").requestId()\` instead. The
 * default metadata buffer (\`metadata-wrapper.ts\`) is exactly this shape, so without this
 * declaration every new request's metadata tab opens red.
 *
 * The specifier must be a string literal: a computed one is rejected before the bundle, because
 * esbuild neither errors nor warns on it and emits code that fails at run time. That is also what
 * makes the typed branch safe — a literal argument infers \`S\` as its own literal type, so a known
 * specifier gets the module's real exports while a computed one widens to \`string\`, misses
 * \`keyof GvModules\`, and stays \`any\` rather than going red.
 */
declare function require<S extends string>(
  specifier: S
): S extends keyof GvModules ? GvModules[S] : any;

declare module "grpcview:invoke" {
  /**
   * Invoke a saved request by its slash-separated display-name path (e.g.
   * "UserService/GetUser"), optionally passing kwargs the target reads via \`params\` from
   * "grpcview:request" (Feature 3). A gRPC-status failure still RESOLVES (\`ok: false\`,
   * fetch-style); invoke() only REJECTS for an unknown path, a streaming target, a
   * body/metadata that won't evaluate, or the invoke-depth cap.
   *
   * A literal path from \`GvRequestMap\` completes inside the quotes and types \`body\` as that
   * method's response message. \`(string & {})\` keeps a computed path — one built from a
   * variable or a template literal — compiling; it just falls back to \`body: any\`.
   */
  export function invoke<P extends GvPath | (string & {})>(
    path: P,
    params?: Record<string, unknown>
  ): Promise<InvokeResult<GvBody<P>>>;
}
declare module "grpcview:assert" {
  /**
   * Assert a condition, THROWING on failure — an \`AssertionError\` whose message is
   * \`assertion failed: <description>\`, with the underlying error's text appended when the
   * predicate itself throws or its promise rejects. Truthiness decides, not \`=== true\`, so a
   * non-empty string or a found object passes. Nothing is logged on success: silence is a pass.
   *
   * A boolean or sync predicate throws SYNCHRONOUSLY and returns \`void\`. Only a thenable
   * condition — an \`async\` predicate or a bare promise — returns a promise, and that one MUST be
   * awaited: an unawaited rejection is dropped and the assertion reads as passing.
   */
  export function assert(description: string, condition: boolean | (() => boolean)): void;
  export function assert(
    description: string,
    condition: (() => PromiseLike<boolean>) | PromiseLike<boolean>
  ): Promise<void>;
}
declare module "grpcview:metadata" {
  /**
   * This node's already-evaluated, merged ancestor-folder metadata (root through immediate
   * parent) — precomputed, not a re-entrant call. \`{}\` where there is no inheritance context.
   */
  export function inherit(): { [key: string]: string[] };
}
declare module "grpcview:request" {
  /**
   * Kwargs passed by an \`invoke()\` caller; \`{}\` on a top-level user invoke (Feature 3).
   * Values are \`any\`, not \`unknown\`: their real shape is whatever the caller passed, and a
   * body that assigns one straight into a typed request field (\`refresh: params.refresh\`)
   * must not have to cast to get past the checker.
   */
  export const params: Readonly<Record<string, any>>;
}
`;
ts.typescriptDefaults.addExtraLib(GV_DTS, "file:///grpcview/gv.d.ts");
