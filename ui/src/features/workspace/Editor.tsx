import { useEffect, useRef } from "react";
import { Editor as MonacoEditor, useMonaco, type OnMount } from "@monaco-editor/react";
import type * as Monaco from "monaco-editor";
import { BodyLanguage } from "@grpcview/v1/workspace_pb";
import { NOCTURNE_MONACO_THEME } from "@/theme/monaco-nocturne";
// Side-effect import: configure the GLOBAL TypeScript defaults (compiler options,
// eager model sync, diagnostics ON) the same way the Scripts editor does, so the TS
// body model below gets sane options + live error markers. We deliberately mount on
// our OWN file:// URI (TS_MODEL_URI), distinct from that module's SCRATCH_PATH, so
// the body editor never shares a model with the Scripts / binding editors. T1 is
// UNTYPED: we do not addExtraLib the request-message type here (that is T2).
import "@/features/scripts/monaco-scripts";
// Side-effect import (T2): the fixed virtual `@bufbuild/protobuf{,/wkt,/codegenv2}` d.ts
// stubs the generated proto `_pb.ts` files import from. Added once; the per-method generated
// files + RequestMessage alias are injected by the effect below.
import "./vendor/bufbuild-stubs";
import { scanTokens, type Token } from "./tokens";

// The request editor has two modes, selected per-request by body_language:
//   • JSON (default / UNSPECIFIED): one JSON model at JSON_MODEL_URI; the per-method
//     JSON schema is swapped on the shared jsonDefaults (matched to that URI) and the
//     buffer is reloaded when the active request changes — so two requests on the
//     same method still show their own draft. `{{ generator(args?) }}` tokens get an
//     accent-2 chip decoration (Monaco can't host React chips inline, so this is an
//     inline-className decoration recomputed on every edit) and a mouse-down on a
//     token opens the binding editor for the generator it names.
//   • TYPESCRIPT (ts-request-body-plan §T1): the body is a TS/JS generator whose
//     returned object becomes the message. It mounts language="typescript" on a
//     DISTINCT file:// URI (Node-style resolution; never collides with the JSON model
//     or the Scripts sandbox's SCRATCH_PATH). The editor is re-keyed on the mode so
//     exactly one model is live at a time and each toggle re-seeds from the current
//     body; JSON-only concerns (schema, token chips) are gated off in TS mode.
// Ported from the previous Editor.tsx (plan §7), on the bundled Monaco + Nocturne theme.
const JSON_MODEL_URI = "grpcview://request/body.json";
const TS_MODEL_URI = "file:///grpcview/request/body.ts";

// JS reserved words that are ILLEGAL as a `function <name>` declaration identifier. A
// generator so named (e.g. `default`) would emit `declare function default(...)`, a SYNTAX
// error that makes Monaco fail to parse the whole generators.d.ts and silently kills ambient
// autocomplete for EVERY generator — so we skip such names (they still run server-side if the
// backend accepts them), the same graceful degradation as dotted names. (Names colliding with
// lib globals like `Date` only produce a self-contained duplicate-identifier squiggle, so they
// are left alone.)
const GENERATOR_NAME_RESERVED = new Set([
  "break", "case", "catch", "class", "const", "continue", "debugger", "default", "delete",
  "do", "else", "enum", "export", "extends", "false", "finally", "for", "function", "if",
  "import", "in", "instanceof", "new", "null", "return", "super", "switch", "this", "throw",
  "true", "try", "typeof", "var", "void", "while", "with", "let", "static", "yield", "await",
  "implements", "interface", "package", "private", "protected", "public",
]);

interface EditorProps {
  schema?: object; // the current method's input JSON schema (resolved upstream)
  data: string;
  onChange: (value: string) => void;
  currentMethod: { service: string; method: string };
  currentKey: string; // request identity — reload the buffer when it changes
  // How the body is interpreted (JSON vs TYPESCRIPT); drives the editor language +
  // model URI. UNSPECIFIED behaves as JSON.
  bodyLanguage: BodyLanguage;
  // T2 typed-body inputs (all optional; only used in TS mode). descriptorSet is the
  // workspace-global merged FileDescriptorSet; the input triple identifies the active
  // method's request message so the body types against its generated `<Message>Json`.
  descriptorSet?: Uint8Array;
  inputPackage?: string;
  inputName?: string;
  inputFile?: string;
  // T3 composition: the workspace's saved GENERATOR names (used only in TS mode). Each
  // simple-identifier name is declared as an ambient global so the body can call it with
  // autocomplete + typing. Optional; defaults to [] so JSON-only callers need not pass it.
  generators?: string[];
  onErrorsChange?: (errors: number) => void;
  // Called when a `{{ … }}` token in the body is clicked, with the generator name.
  onTokenClick?: (generator: string) => void;
}

export function Editor({
  schema,
  data,
  onChange,
  currentMethod,
  currentKey,
  bodyLanguage,
  descriptorSet,
  inputPackage,
  inputName,
  inputFile,
  generators = [],
  onErrorsChange,
  onTokenClick,
}: EditorProps) {
  const isTS = bodyLanguage === BodyLanguage.TYPESCRIPT;
  const editorRef = useRef<Monaco.editor.IStandaloneCodeEditor | null>(null);
  // Set while we reload the buffer programmatically, so the resulting
  // onDidChangeModelContent isn't reported as a user edit. @monaco-editor/react
  // only suppresses onChange for its own controlled `value` prop, not for an
  // external editor.setValue() — without this guard, a tab switch looks like a
  // keystroke and schedules a spurious save (which cancels the previous
  // request's pending debounced save).
  const suppressChange = useRef(false);
  const monaco = useMonaco();

  // The tokens currently decorated (offsets into the buffer) + the latest click
  // handler, read by the once-at-mount Monaco listeners via refs so they always
  // see fresh values.
  const tokensRef = useRef<Token[]>([]);
  const onTokenClickRef = useRef(onTokenClick);
  onTokenClickRef.current = onTokenClick;

  // Point the JSON validator at the current method's input schema, matched to
  // this editor's single model URI.
  useEffect(() => {
    if (!monaco || isTS) return; // TS mode: no JSON model is live, so this is a no-op — skip it.
    monaco.languages.json.jsonDefaults.setDiagnosticsOptions({
      validate: true,
      schemaValidation: "error",
      enableSchemaRequest: false,
      schemas: schema
        ? [
            {
              uri: `grpcview://schemas/${currentMethod.service}/${currentMethod.method}`,
              fileMatch: [JSON_MODEL_URI],
              schema,
            },
          ]
        : [],
    });
  }, [monaco, isTS, schema, currentMethod.service, currentMethod.method]);

  // Load the active request's draft when the request identity changes. Guarded so
  // it never clobbers the buffer mid-typing (onChange keeps `data` === buffer).
  useEffect(() => {
    const ed = editorRef.current;
    if (ed && ed.getValue() !== data) {
      suppressChange.current = true;
      ed.setValue(data);
      suppressChange.current = false;
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [currentKey]);

  // Report JSON/schema error count for the footer validity line.
  useEffect(() => {
    if (!monaco || !onErrorsChange) return;
    const sub = monaco.editor.onDidChangeMarkers(() => {
      const model = editorRef.current?.getModel();
      if (!model) return;
      const errors = monaco.editor
        .getModelMarkers({ resource: model.uri })
        .filter((m) => m.severity === monaco.MarkerSeverity.Error).length;
      onErrorsChange(errors);
    });
    return () => sub.dispose();
  }, [monaco, onErrorsChange]);

  // T2 — type the TS body against the input message's generated `<Message>Json` type.
  // Only in TS mode with a descriptor set + a known input file: dynamically import the
  // client-side generator (keeps protoc-gen-es + typescript off the main chunk), run it
  // over the workspace descriptor set (memoized by reference), then addExtraLib each
  // generated `_pb.ts` under file:///grpcview/request/gen/<protopath> (extensionless
  // relative imports in the generated files resolve to these) plus a RequestMessage alias
  // d.ts at a constant path. typescriptDefaults is GLOBAL with no per-URI fileMatch, but
  // only one body editor is live, so we track the libs in a ref and DISPOSE-BEFORE-ADD on
  // every re-run (method change / descriptor change) — same-path replace can throw
  // "Duplicate definition". Cleanup disposes all libs so no stale `declare global` survives.
  const typeLibs = useRef<Monaco.IDisposable[]>([]);
  useEffect(() => {
    // descriptorSet is best-effort on the backend (empty when a reflection source is
    // unreachable) — an empty set has no files to generate, so skip and let the body stay
    // untyped rather than aliasing RequestMessage to a missing import.
    if (!monaco || !isTS || !descriptorSet?.length || !inputFile) return;
    let cancelled = false;
    void (async () => {
      const { generateWorkspaceTypes, requestMessageAlias } = await import("./proto-types");
      const files = generateWorkspaceTypes(descriptorSet);
      if (cancelled) return;
      const tsDefaults = monaco.languages.typescript.typescriptDefaults;
      typeLibs.current.forEach((d) => d.dispose());
      typeLibs.current = [];
      for (const [path, content] of files) {
        typeLibs.current.push(
          tsDefaults.addExtraLib(content, `file:///grpcview/request/gen/${path}`)
        );
      }
      const alias = requestMessageAlias(files, inputPackage ?? "", inputName ?? "", inputFile);
      typeLibs.current.push(
        tsDefaults.addExtraLib(alias.dts, "file:///grpcview/request/request-message.d.ts")
      );
    })();
    return () => {
      cancelled = true;
      typeLibs.current.forEach((d) => d.dispose());
      typeLibs.current = [];
    };
  }, [monaco, isTS, descriptorSet, inputPackage, inputName, inputFile]);

  // T3 composition — ambient autocomplete for the workspace's saved GENERATOR names.
  // In TS body mode the backend injects each referenced generator as `globalThis.<name>`
  // and the body calls it directly (e.g. `mkid()`); we mirror that here so those names
  // autocomplete + type-check instead of erroring "Cannot find name". The emitted .d.ts
  // has NO import/export, so its `declare`s are AMBIENT GLOBALS visible even inside the
  // module body (same idiom as the Scripts ENV_DTS in monaco-scripts.ts). We emit ONLY
  // simple-identifier names (/^[A-Za-z_$][A-Za-z0-9_$]*$/, minus JS reserved words) — this
  // mirrors the backend's rule: a name with dots/other chars can't be made a bare global, so
  // it is skipped (see GENERATOR_NAME_RESERVED above for why reserved words are excluded too).
  // Registered at a CONSTANT path DISTINCT from the T2 libs; dispose-before-add (like T2,
  // since typescriptDefaults is global with no per-URI fileMatch) so the set updates
  // cleanly on change with no leak / no "Duplicate definition". Gated on TS mode so JSON
  // mode is untouched; an empty list just registers empty content (harmless no-op decls).
  const genLib = useRef<Monaco.IDisposable | null>(null);
  useEffect(() => {
    if (!monaco || !isTS) return;
    const tsDefaults = monaco.languages.typescript.typescriptDefaults;
    const content = generators
      .filter((name) => /^[A-Za-z_$][A-Za-z0-9_$]*$/.test(name) && !GENERATOR_NAME_RESERVED.has(name))
      .map((name) => `declare function ${name}(...args: any[]): any;`)
      .join("\n");
    genLib.current?.dispose();
    genLib.current = tsDefaults.addExtraLib(content, "file:///grpcview/request/generators.d.ts");
    return () => {
      genLib.current?.dispose();
      genLib.current = null;
    };
  }, [monaco, isTS, generators]);

  const onMount: OnMount = (editor, m) => {
    editorRef.current = editor;
    // ⌘S / Ctrl+S formats the document (plan §7).
    editor.addCommand(m.KeyMod.CtrlCmd | m.KeyCode.KeyS, () => {
      editor.getAction("editor.action.formatDocument")?.run();
    });

    // `{{ … }}` token chips + click-to-edit are JSON-mode concerns only (a TS body
    // calls generators directly — there are no tokens to scan). Skip them in TS mode.
    // onMount is captured at mount and the editor is re-keyed on the mode, so `isTS`
    // here always reflects the model that just mounted.
    if (isTS) return;

    // Decorate every `{{ … }}` token as an accent-2 chip, recomputed from the live
    // buffer on each edit (offsets → line/column ranges). The collection is owned by
    // this editor, so it is torn down with it.
    const decorations = editor.createDecorationsCollection([]);
    const recompute = () => {
      const model = editor.getModel();
      if (!model) return;
      const toks = scanTokens(model.getValue());
      tokensRef.current = toks;
      decorations.set(
        toks.map((t) => {
          const s = model.getPositionAt(t.start);
          const e = model.getPositionAt(t.end);
          return {
            range: new m.Range(s.lineNumber, s.column, e.lineNumber, e.column),
            options: {
              inlineClassName: "tok-gen-decoration",
              hoverMessage: {
                value: `Generator \`${t.name}\` — click to edit its binding`,
              },
              stickiness: m.editor.TrackedRangeStickiness.NeverGrowsWhenTypingAtEdges,
            },
          };
        })
      );
    };
    recompute();
    editor.onDidChangeModelContent(recompute);

    // A mouse-down inside a token span opens the binding editor for it (best-effort
    // click-to-open — the reliable path is the Metadata chips).
    editor.onMouseDown((e) => {
      const handler = onTokenClickRef.current;
      const pos = e.target.position;
      const model = editor.getModel();
      if (!handler || !pos || !model) return;
      const offset = model.getOffsetAt(pos);
      const hit = tokensRef.current.find((t) => offset >= t.start && offset < t.end);
      if (hit) handler(hit.name);
    });
  };

  return (
    <MonacoEditor
      // Re-key on the mode so a JSON⇄TS toggle fully remounts the editor: the old
      // model is disposed and a fresh one is created at the mode's URI, seeded from
      // the current body via defaultValue. This keeps the two modes' models from ever
      // coexisting and avoids stale content on toggle-back. JSON-only usage never
      // toggles, so it never remounts — today's behavior is preserved exactly.
      key={isTS ? "ts" : "json"}
      path={isTS ? TS_MODEL_URI : JSON_MODEL_URI}
      language={isTS ? "typescript" : "json"}
      theme={NOCTURNE_MONACO_THEME}
      defaultValue={data}
      onMount={onMount}
      onChange={(v: string | undefined) => {
        if (!suppressChange.current) onChange(v ?? "");
      }}
      options={{
        formatOnType: true,
        formatOnPaste: true,
        automaticLayout: true,
        minimap: { enabled: false },
        scrollBeyondLastLine: false,
        fontFamily: "var(--mono)",
        fontSize: 13,
        padding: { top: 10, bottom: 10 },
        tabSize: 2,
      }}
    />
  );
}
