import { useEffect, useRef } from "react";
import { Editor as MonacoEditor, useMonaco, type OnMount } from "@monaco-editor/react";
import type * as Monaco from "monaco-editor";
import { NOCTURNE_MONACO_THEME } from "@/theme/monaco-nocturne";
// Side-effect import: configure the GLOBAL TypeScript defaults (compiler options,
// eager model sync, diagnostics ON) the same way the Scripts editor does, so the TS
// body model below gets sane options + live error markers. We deliberately mount on
// our OWN file:// URI (TS_MODEL_URI), distinct from that module's SCRATCH_PATH, so
// the body editor never shares a model with the Scripts / binding editors.
import "@/features/scripts/monaco-scripts";
// Side-effect import (T2): the fixed virtual `@bufbuild/protobuf{,/wkt,/codegenv2}` d.ts
// stubs the generated proto `_pb.ts` files import from. Added once; the per-method generated
// files + RequestMessage alias are injected by the effect below.
import "./vendor/bufbuild-stubs";
// T4 hidden-wrapper: the canonical `export default (): RequestMessage => (\n<body>\n)` module
// shape + helpers. We HIDE the prefix/suffix lines so the user edits a bare object.
import { PREFIX_LINES, SUFFIX_LINES, isCanonical } from "./body-wrapper";
// §P5 typed generator signatures: the shared GeneratorDef shape (name + source) + the helper that
// registers each generator's source as an isolated module and declares it as an ambient global
// with its INFERRED signature. Centralizes the name-safety / failure-isolation / body-vs-metadata
// URI invariants so the body + metadata editors can't diverge on them.
import { registerGeneratorLibs, type GeneratorDef } from "./generator-libs";

// The request body is ALWAYS authored as TypeScript (ts-request-body-plan §T1–§T4 + the all-JS
// phase): the body is a TS/JS generator whose returned object becomes the message. It mounts
// language="typescript" on a DISTINCT file:// URI (Node-style resolution; never collides with the
// Scripts sandbox's SCRATCH_PATH). The model text is the canonical hidden-wrapper module
// (body-wrapper.ts); we HIDE the wrapper's prefix/suffix lines via setHiddenAreas so the user sees
// only a bare object. The body types against the input message's generated `<Message>Json` (T2)
// and can call the workspace's saved generators as ambient globals (T3). Every persisted body is
// migrated to this canonical shape on load (migrateBodyToTs in RequestWorkspace), so the editor
// only ever hosts a canonical module — there is no JSON authoring mode anymore.
// Ported from the previous Editor.tsx (plan §7), on the bundled Monaco + Nocturne theme.
const TS_MODEL_URI = "file:///grpcview/request/body.ts";

// --- Hidden-wrapper geometry (ts-request-body-plan §T4) ---------------------------------------
// The model text is the canonical `export default (): RequestMessage => (\n<body>\n)` module
// (body-wrapper.ts). We hide the prefix line(s) and suffix line(s) via the editor's internal
// setHiddenAreas so only the bare <body> shows. Positions stay NATIVE model coordinates, so
// completions + squiggles need no translation — only the footer error COUNT filters out the
// (normally empty) markers that fall wholly on a hidden line.

// setHiddenAreas is real in monaco-editor 0.52.2 (codeEditorWidget.js) but stripped from the
// public monaco.d.ts. Cast through this narrow interface + optional-call so a future monaco bump
// degrades gracefully (bare object stays visible, just unhidden) rather than throwing.
type HasHiddenAreas = { setHiddenAreas?(ranges: Monaco.IRange[], source?: unknown): void };

// The visible body line span [first, last] + total, derived from line COUNT only. Robust to an
// empty body: `last` is clamped to be >= `first`.
function bodyBounds(model: Monaco.editor.ITextModel) {
  const total = model.getLineCount();
  const first = PREFIX_LINES + 1;
  const last = Math.max(first, total - SUFFIX_LINES);
  return { first, last, total };
}

function hiddenRanges(model: Monaco.editor.ITextModel): Monaco.IRange[] {
  const { last, total } = bodyBounds(model);
  return [
    { startLineNumber: 1, startColumn: 1, endLineNumber: PREFIX_LINES, endColumn: 1 },
    { startLineNumber: last + 1, startColumn: 1, endLineNumber: total, endColumn: 1 },
  ];
}

// Hide the wrapper lines (idempotent; re-call after setValue and after any body line-count
// change). Also nudges the cursor off a hidden prefix line if it landed there.
function applyHidden(editor: Monaco.editor.IStandaloneCodeEditor) {
  const model = editor.getModel();
  const ha = editor as unknown as HasHiddenAreas;
  if (!model || typeof ha.setHiddenAreas !== "function") return;
  ha.setHiddenAreas(hiddenRanges(model), "grpcview-body-wrapper");
  const pos = editor.getPosition();
  if (pos && pos.lineNumber <= PREFIX_LINES) {
    editor.setPosition({ lineNumber: PREFIX_LINES + 1, column: 1 });
  }
}

interface EditorProps {
  data: string;
  onChange: (value: string) => void;
  currentKey: string; // request identity — reload the buffer when it changes
  // T2 typed-body inputs (all optional). descriptorSet is the workspace-global merged
  // FileDescriptorSet; the input triple identifies the active method's request message so the
  // body types against its generated `<Message>Json`.
  descriptorSet?: Uint8Array;
  inputPackage?: string;
  inputName?: string;
  inputFile?: string;
  // Composition (T3 + §P5): the workspace's saved GENERATORS (name + source). Each emittable
  // generator is declared as an ambient global with its INFERRED signature so the body can call it
  // with autocomplete + real param/return typing. Optional; defaults to [].
  generators?: GeneratorDef[];
  onErrorsChange?: (errors: number) => void;
}

export function Editor({
  data,
  onChange,
  currentKey,
  descriptorSet,
  inputPackage,
  inputName,
  inputFile,
  generators = [],
  onErrorsChange,
}: EditorProps) {
  const editorRef = useRef<Monaco.editor.IStandaloneCodeEditor | null>(null);
  // Set while we reload the buffer programmatically, so the resulting
  // onDidChangeModelContent isn't reported as a user edit. @monaco-editor/react
  // only suppresses onChange for its own controlled `value` prop, not for an
  // external editor.setValue() — without this guard, a tab switch looks like a
  // keystroke and schedules a spurious save (which cancels the previous
  // request's pending debounced save). Also used by the hidden-wrapper Layer-3
  // backstop so its repair edit isn't reported as a user edit.
  const suppressChange = useRef(false);
  // The last known-canonical model text. The hidden-wrapper structural backstop
  // (onMount Layer 3) restores this if an exotic edit corrupts the wrapper. Seeded on mount
  // and on every programmatic reload.
  const lastGood = useRef<string>("");
  const monaco = useMonaco();

  // Load the active request's draft when the request identity changes. Guarded so
  // it never clobbers the buffer mid-typing (onChange keeps `data` === buffer).
  useEffect(() => {
    const ed = editorRef.current;
    if (ed && ed.getValue() !== data) {
      suppressChange.current = true;
      ed.setValue(data);
      suppressChange.current = false;
      // Hidden areas do NOT survive setValue — re-hide the wrapper and reset the
      // structural backstop's last-good snapshot to the freshly loaded body.
      lastGood.current = data;
      applyHidden(ed);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [currentKey]);

  // Report TS/schema error count for the footer validity line.
  useEffect(() => {
    if (!monaco || !onErrorsChange) return;
    const sub = monaco.editor.onDidChangeMarkers(() => {
      const model = editorRef.current?.getModel();
      if (!model) return;
      // In hidden-wrapper mode, drop error markers that lie WHOLLY on a hidden prefix/suffix
      // line so they don't inflate the footer count. A canonical wrapper is valid, but a
      // mid-edit transient can briefly flash a marker on the `=> (` prefix. Markers touching a
      // visible line are kept and land on the correct visible line (native model coordinates).
      // Gated on isCanonical(getValue()) so the effect stays self-contained; a (transient) non-
      // canonical body counts all markers.
      const canonical = isCanonical(model.getValue());
      const bounds = canonical ? bodyBounds(model) : null;
      const errors = monaco.editor
        .getModelMarkers({ resource: model.uri })
        .filter((mk) => mk.severity === monaco.MarkerSeverity.Error)
        .filter((mk) => {
          if (!bounds) return true;
          const firstSuffix = bounds.last + 1;
          const whollyInPrefix = mk.endLineNumber <= PREFIX_LINES;
          const whollyInSuffix = mk.startLineNumber >= firstSuffix;
          return !(whollyInPrefix || whollyInSuffix);
        }).length;
      onErrorsChange(errors);
    });
    return () => sub.dispose();
  }, [monaco, onErrorsChange]);

  // T2 — type the TS body against the input message's generated `<Message>Json` type.
  // With a descriptor set + a known input file: dynamically import the client-side generator
  // (keeps protoc-gen-es + typescript off the main chunk), run it over the workspace descriptor
  // set (memoized by reference), then addExtraLib each generated `_pb.ts` under
  // file:///grpcview/request/gen/<protopath> (extensionless relative imports in the generated
  // files resolve to these) plus a RequestMessage alias d.ts at a constant path. typescriptDefaults
  // is GLOBAL with no per-URI fileMatch, but only one body editor is live, so we track the libs in
  // a ref and DISPOSE-BEFORE-ADD on every re-run (method change / descriptor change) — same-path
  // replace can throw "Duplicate definition". Cleanup disposes all libs so no stale `declare
  // global` survives.
  const typeLibs = useRef<Monaco.IDisposable[]>([]);
  useEffect(() => {
    // descriptorSet is best-effort on the backend (empty when a reflection source is
    // unreachable) — an empty set has no files to generate, so skip and let the body stay
    // untyped rather than aliasing RequestMessage to a missing import.
    if (!monaco || !descriptorSet?.length || !inputFile) return;
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
  }, [monaco, descriptorSet, inputPackage, inputName, inputFile]);

  // Composition (T3 + §P5) — ambient autocomplete for the workspace's saved GENERATORS WITH their
  // inferred signatures. The backend injects each referenced generator as `globalThis.<name>` and
  // the body calls it directly (e.g. `mkid()`); we mirror that here so those names autocomplete +
  // type-check (instead of erroring "Cannot find name") AND carry each generator's real params +
  // return type. registerGeneratorLibs registers, per emittable generator, its source as an
  // isolated module plus one ambient-globals .d.ts declaring `const <name>: typeof
  // import(...).default` (see generator-libs.ts for the mechanism + the name-safety / failure-
  // isolation invariants). scope="body" namespaces the module + globals URIs away from the metadata
  // editor's. It returns MULTIPLE disposables (one per module + the globals .d.ts); we dispose them
  // ALL before re-adding on any change, and on unmount — typescriptDefaults is global with no
  // per-URI fileMatch, and same-path re-add can throw "Duplicate definition".
  const genLibs = useRef<Monaco.IDisposable[]>([]);
  useEffect(() => {
    if (!monaco) return;
    const tsDefaults = monaco.languages.typescript.typescriptDefaults;
    genLibs.current.forEach((d) => d.dispose());
    genLibs.current = registerGeneratorLibs(tsDefaults, generators, "body");
    return () => {
      genLibs.current.forEach((d) => d.dispose());
      genLibs.current = [];
    };
  }, [monaco, generators]);

  const onMount: OnMount = (editor, m) => {
    editorRef.current = editor;
    // ⌘S / Ctrl+S formats. Format only the visible BODY range and re-hide — a full-document
    // format reflows the wrapper across the hidden boundary.
    editor.addCommand(m.KeyMod.CtrlCmd | m.KeyCode.KeyS, () => {
      const model = editor.getModel();
      if (!model) return;
      const { first, last } = bodyBounds(model);
      editor.setSelection({
        startLineNumber: first,
        startColumn: 1,
        endLineNumber: last,
        endColumn: model.getLineMaxColumn(last),
      });
      void Promise.resolve(editor.getAction("editor.action.formatSelection")?.run()).then(() =>
        applyHidden(editor)
      );
    });

    // Hide the wrapper lines + guard their integrity. onMount is captured at mount; the body is
    // always a canonical hidden-wrapper module (migrated on load), so this always applies.
    lastGood.current = editor.getValue();
    applyHidden(editor);

    // Layer 1 (proactive) — swallow the two boundary keystrokes that would merge a visible
    // body line into a hidden wrapper line (Backspace at body start, Delete at body end).
    editor.onKeyDown((e) => {
      const model = editor.getModel();
      const sel = editor.getSelection();
      if (!model || !sel || !sel.isEmpty()) return;
      const { first, last } = bodyBounds(model);
      const atBodyStart = sel.startLineNumber === first && sel.startColumn === 1;
      const atBodyEnd =
        sel.startLineNumber === last && sel.startColumn === model.getLineMaxColumn(last);
      if (e.keyCode === m.KeyCode.Backspace && atBodyStart) {
        e.preventDefault();
        e.stopPropagation();
      }
      if (e.keyCode === m.KeyCode.Delete && atBodyEnd) {
        e.preventDefault();
        e.stopPropagation();
      }
    });

    // Layer 2 (proactive) — ⌘/Ctrl+A selects the BODY region only (shadows the built-in
    // select-all for this editor), so "select-all + type / delete / paste" can't touch the
    // wrapper.
    editor.addAction({
      id: "grpcview.selectBody",
      label: "Select Body",
      keybindings: [m.KeyMod.CtrlCmd | m.KeyCode.KeyA],
      run: (ed) => {
        const model = ed.getModel();
        if (!model) return;
        const { first, last } = bodyBounds(model);
        ed.setSelection({
          startLineNumber: first,
          startColumn: 1,
          endLineNumber: last,
          endColumn: model.getLineMaxColumn(last),
        });
      },
    });

    // Layer 3 (fail-safe) — after ANY change the model must stay canonical; if an exotic
    // vector (IME, drag-drop, replace-all) breaks the wrapper, restore last-good via an
    // undo-coherent executeEdits (not setValue). Also re-hides after body line-count changes
    // (Enter at end, paste). suppressChange keeps the repair from being reported as an edit.
    editor.onDidChangeModelContent(() => {
      if (suppressChange.current) return;
      const model = editor.getModel();
      if (!model) return;
      const v = model.getValue();
      if (isCanonical(v)) {
        lastGood.current = v;
        applyHidden(editor);
        return;
      }
      suppressChange.current = true;
      const sel = editor.getSelection();
      editor.executeEdits("wrapper-guard", [
        { range: model.getFullModelRange(), text: lastGood.current },
      ]);
      if (sel) editor.setSelection(sel);
      suppressChange.current = false;
      applyHidden(editor);
    });
  };

  return (
    <MonacoEditor
      path={TS_MODEL_URI}
      language="typescript"
      theme={NOCTURNE_MONACO_THEME}
      defaultValue={data}
      onMount={onMount}
      onChange={(v: string | undefined) => {
        if (!suppressChange.current) onChange(v ?? "");
      }}
      options={{
        // Hidden-wrapper mode: disable format-on-type/paste (they reflow across the hidden
        // wrapper boundary), disable folding (its controller contends over the hidden-area
        // buckets), and remap the gutter so the first VISIBLE body line reads "1".
        formatOnType: false,
        formatOnPaste: false,
        folding: false,
        lineNumbers: (n: number) => (n <= PREFIX_LINES ? "" : String(n - PREFIX_LINES)),
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
