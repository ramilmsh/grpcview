// Configures the shared Monaco `typescriptDefaults` for the Scripts scratchpad as a
// side effect on import. Inert unless the TS worker is bundled — see theme/monaco-nocturne.ts.
import * as monaco from "monaco-editor";
import dayjsTypes from "./vendor/dayjs.d.ts?raw";

const ts = monaco.languages.typescript;

// Must be `file://`, not the app's `grpcview://` convention: NodeJs resolution only
// walks node_modules within the importing file's own URI scheme.
export const SCRATCH_PATH = "file:///scripts/scratch.ts";

// monaco's public enums lag the bundled TS: no ES2022 target, no Node16 resolution.
ts.typescriptDefaults.setCompilerOptions({
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
});

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
