import { useEffect, useRef } from "react";
import { Editor as MonacoEditor, useMonaco, type OnMount } from "@monaco-editor/react";
import type * as Monaco from "monaco-editor";
import { NOCTURNE_MONACO_THEME } from "@/theme/monaco-nocturne";
// Side-effect import: configure the GLOBAL TypeScript defaults (compiler options, eager model
// sync, diagnostics ON) the same way the body Editor + Scripts editor do, so the metadata TS
// model below gets sane options + live error markers. We mount on our OWN file:// URI
// (TS_MODEL_URI), distinct from the body editor's body.ts, so the two never share a model.
import "@/features/scripts/monaco-scripts";
// The canonical `export default (): Metadata => (\n<object>\n)` module shape + helpers. We HIDE
// the prefix/suffix lines so the user edits a bare object.
import { META_PREFIX_LINES, META_SUFFIX_LINES, isCanonical } from "./metadata-wrapper";
// §P5 typed generator signatures — the SAME shared helper the body editor uses (see
// generator-libs.ts). scope="metadata" keeps the per-generator module + globals URIs distinct from
// the body editor's, so one editor's dispose never yanks the other's libs.
import { registerGeneratorLibs, type GeneratorDef } from "./generator-libs";
// Dev/verification hook: register this editor by its model URI on window.__grpcviewEditors (there
// is no single global monaco). App code never reads it.
import { registerEditorForDebug } from "@/lib/editor-debug";

// The request metadata is authored as TypeScript: a generator whose returned {[key]: string[]}
// object becomes the outgoing gRPC metadata (multi-valued). It mounts language="typescript" on a
// DISTINCT file:// URI so it never collides with the body editor's model. The model text is the
// canonical hidden-wrapper module (metadata-wrapper.ts); we HIDE the wrapper's prefix/suffix
// lines via setHiddenAreas so the user sees only a bare object, typed against an injected ambient
// `type Metadata` and able to call the workspace's saved generators as ambient globals. This is a
// focused parallel to the body Editor.tsx: the hidden-wrapper hosting logic is intentionally
// duplicated here (rather than extracted) so the browser-verified body editor stays untouched.
const TS_MODEL_URI = "file:///grpcview/request/metadata.ts";

// The ambient Metadata type the `(): Metadata =>` annotation checks against. gRPC metadata is
// multi-valued, so values are string[]; the index signature allows arbitrary header keys. A
// .d.ts with no import/export is a GLOBAL (ambient) script, so `Metadata` is visible inside the
// module body — the same idiom as the generator globals below.
const METADATA_TYPE_DTS = "type Metadata = { [key: string]: string[] };";

// --- Hidden-wrapper geometry (mirrors Editor.tsx) ---------------------------------------------
// setHiddenAreas is real in monaco-editor 0.52.2 but stripped from the public monaco.d.ts. Cast
// through this narrow interface + optional-call so a future monaco bump degrades gracefully.
type HasHiddenAreas = { setHiddenAreas?(ranges: Monaco.IRange[], source?: unknown): void };

// The source tag under which we register our hidden areas (distinct from the body editor's). monaco
// merges hidden areas across sources, so a stable private tag lets the force path clear/replace
// ONLY ours.
const HIDDEN_SOURCE = "grpcview-metadata-wrapper";

// The visible object line span [first, last] + total, derived from line COUNT only. Robust to an
// empty object: `last` is clamped to be >= `first`.
function metaBounds(model: Monaco.editor.ITextModel) {
  const total = model.getLineCount();
  const first = META_PREFIX_LINES + 1;
  const last = Math.max(first, total - META_SUFFIX_LINES);
  return { first, last, total };
}

function hiddenRanges(model: Monaco.editor.ITextModel): Monaco.IRange[] {
  const { last, total } = metaBounds(model);
  return [
    { startLineNumber: 1, startColumn: 1, endLineNumber: META_PREFIX_LINES, endColumn: 1 },
    { startLineNumber: last + 1, startColumn: 1, endLineNumber: total, endColumn: 1 },
  ];
}

// Hide the wrapper lines (idempotent; re-call after setValue, after any object line-count change,
// and on relayout — hidden areas live on the editor, not the model, so a visibility/size
// transition drops them). Also nudges the cursor off a hidden prefix line if it landed there.
//
// `force` re-applies the hiding even when the ranges are UNCHANGED. A full-buffer replace (setValue
// on a request switch, or the Layer-3 restore below) resets the editor's VIEW line-projections to
// all-visible but leaves monaco's internal HiddenAreasModel cache intact — so a following
// setHiddenAreas with the SAME geometry (switching between two objects of equal line count → the
// byte-identical [prefix, suffix] ranges) is judged "unchanged" and skipped, leaving the wrapper
// visible. force clears OUR source's ranges first so the re-set is always a real re-projection.
// Layout / incremental-content callers stay UNFORCED so the idempotent short-circuit still
// suppresses needless re-projection (mirrors Editor.tsx).
function applyHidden(editor: Monaco.editor.IStandaloneCodeEditor, force = false) {
  const model = editor.getModel();
  const ha = editor as unknown as HasHiddenAreas;
  if (!model || typeof ha.setHiddenAreas !== "function") return;
  if (force) ha.setHiddenAreas([], HIDDEN_SOURCE);
  ha.setHiddenAreas(hiddenRanges(model), HIDDEN_SOURCE);
  const pos = editor.getPosition();
  if (pos && pos.lineNumber <= META_PREFIX_LINES) {
    editor.setPosition({ lineNumber: META_PREFIX_LINES + 1, column: 1 });
  }
}

interface MetadataEditorProps {
  data: string;
  onChange: (value: string) => void;
  currentKey: string; // request identity — reload the buffer when it changes
  // The workspace's saved GENERATORS (name + source). Each emittable generator is declared as an
  // ambient global with its INFERRED signature so the metadata module can call it with autocomplete
  // + real param/return typing. Optional; defaults [].
  generators?: GeneratorDef[];
  onErrorsChange?: (errors: number) => void;
}

export function MetadataEditor({
  data,
  onChange,
  currentKey,
  generators = [],
  onErrorsChange,
}: MetadataEditorProps) {
  const editorRef = useRef<Monaco.editor.IStandaloneCodeEditor | null>(null);
  // Set while we reload the buffer programmatically, so the resulting onDidChangeModelContent
  // isn't reported as a user edit (mirrors Editor.tsx). Also used by the Layer-3 backstop.
  const suppressChange = useRef(false);
  // The last known-canonical model text, restored by the structural backstop (Layer 3).
  const lastGood = useRef<string>("");
  const monaco = useMonaco();

  // Load the active request's metadata draft when the request identity changes. Guarded so it
  // never clobbers the buffer mid-typing (onChange keeps `data` === buffer).
  useEffect(() => {
    const ed = editorRef.current;
    if (!ed) return;
    if (ed.getValue() !== data) {
      suppressChange.current = true;
      ed.setValue(data);
      suppressChange.current = false;
      // Hidden areas do NOT survive setValue — reset the backstop snapshot.
      lastGood.current = data;
    }
    // Re-hide, FORCED. On a view/subtab REMOUNT the model is cached by path so getValue() already
    // equals data (the setValue branch is skipped), yet the fresh editor instance starts with NO
    // hidden areas — re-hiding here covers that remount case. The setValue branch above needs the
    // force: replacing the buffer resets the VIEW to all-visible but not monaco's HiddenAreasModel
    // cache, so switching between two SAME-line-count objects would otherwise short-circuit the
    // re-hide and leave the wrapper visible (see applyHidden). force is a harmless no-op on the
    // fresh-remount path.
    applyHidden(ed, /* force */ true);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [currentKey]);

  // Report TS error count for the footer validity line, dropping markers wholly on a hidden
  // prefix/suffix line so a transient mid-edit marker on the wrapper doesn't inflate the count
  // (mirrors Editor.tsx).
  useEffect(() => {
    if (!monaco || !onErrorsChange) return;
    const sub = monaco.editor.onDidChangeMarkers(() => {
      const model = editorRef.current?.getModel();
      if (!model) return;
      const canonical = isCanonical(model.getValue());
      const bounds = canonical ? metaBounds(model) : null;
      const errors = monaco.editor
        .getModelMarkers({ resource: model.uri })
        .filter((mk) => mk.severity === monaco.MarkerSeverity.Error)
        .filter((mk) => {
          if (!bounds) return true;
          const firstSuffix = bounds.last + 1;
          const whollyInPrefix = mk.endLineNumber <= META_PREFIX_LINES;
          const whollyInSuffix = mk.startLineNumber >= firstSuffix;
          return !(whollyInPrefix || whollyInSuffix);
        }).length;
      onErrorsChange(errors);
    });
    return () => sub.dispose();
  }, [monaco, onErrorsChange]);

  // Inject the ambient `type Metadata` so `(): Metadata =>` type-checks (index signature → values
  // must be string[]; arbitrary keys OK). Registered ONCE at a CONSTANT path DISTINCT from the
  // body editor's proto libs so the two never collide (only one editor is mounted at a time, but
  // distinct paths are safe regardless). typescriptDefaults is global; dispose on cleanup.
  const typeLib = useRef<Monaco.IDisposable | null>(null);
  useEffect(() => {
    if (!monaco) return;
    const tsDefaults = monaco.languages.typescript.typescriptDefaults;
    typeLib.current?.dispose();
    typeLib.current = tsDefaults.addExtraLib(
      METADATA_TYPE_DTS,
      "file:///grpcview/request/metadata-type.d.ts"
    );
    return () => {
      typeLib.current?.dispose();
      typeLib.current = null;
    };
  }, [monaco]);

  // Ambient autocomplete for the workspace's saved GENERATORS WITH their inferred signatures — the
  // SAME mechanism the body gets (Editor.tsx), so a metadata value can call `apiToken()` / `uuid()`
  // directly and see its real params + return. registerGeneratorLibs with scope="metadata" keeps
  // the per-generator module URIs (gen/metadata/…) and the globals .d.ts (metadata-generators.d.ts)
  // distinct from the body editor's, so the two never collide / "Duplicate definition". It returns
  // multiple disposables; dispose them all before re-adding on change and on unmount.
  const genLibs = useRef<Monaco.IDisposable[]>([]);
  useEffect(() => {
    if (!monaco) return;
    const tsDefaults = monaco.languages.typescript.typescriptDefaults;
    genLibs.current.forEach((d) => d.dispose());
    genLibs.current = registerGeneratorLibs(tsDefaults, generators, "metadata");
    return () => {
      genLibs.current.forEach((d) => d.dispose());
      genLibs.current = [];
    };
  }, [monaco, generators]);

  const onMount: OnMount = (editor, m) => {
    editorRef.current = editor;
    registerEditorForDebug(TS_MODEL_URI, editor);
    // ⌘S / Ctrl+S formats. Format only the visible object range, then dedent one level and re-hide.
    // (A full-document format reflows the wrapper across the hidden boundary; a range format of the
    // object over-indents it — see the outdent step below.)
    editor.addCommand(m.KeyMod.CtrlCmd | m.KeyCode.KeyS, () => {
      const model = editor.getModel();
      if (!model) return;
      // Select the visible object range [first, last], recomputed each call (formatting can change
      // the line count).
      const selectObject = () => {
        const { first, last } = metaBounds(model);
        editor.setSelection({
          startLineNumber: first,
          startColumn: 1,
          endLineNumber: last,
          endColumn: model.getLineMaxColumn(last),
        });
      };
      selectObject();
      void Promise.resolve(editor.getAction("editor.action.formatSelection")?.run())
        .then(() => {
          // formatSelection reindents the object to match its nesting one level inside the hidden
          // `=> (` wrapper, shifting the whole visible region right by one indent. Re-select and
          // OUTDENT once to pull the base back to column 0 (relative nesting preserved; outdent
          // clamps at column 0, so repeated ⌘S is stable and the empty seed stays put). The wrapper
          // lines stay outside the selection, so the model remains canonical (Layer 3 no-ops).
          selectObject();
          return Promise.resolve(editor.getAction("editor.action.outdentLines")?.run());
        })
        .then(() => applyHidden(editor));
    });

    // Hide the wrapper lines + guard their integrity (the module is always canonical, migrated
    // on load).
    lastGood.current = editor.getValue();
    applyHidden(editor);

    // Re-hide on relayout. setHiddenAreas state lives on the EDITOR (viewModel), not the model,
    // so when this editor mounts inside a hidden/zero-size container (a view or subtab switch
    // remounts it) and the container later becomes visible, monaco's first real layout re-projects
    // the lines and DROPS the hidden areas set just above — leaving the wrapper prefix/suffix
    // visible until the next edit. Re-hiding on every layout change closes that window. Idempotent:
    // monaco short-circuits an identical setHiddenAreas (no re-projection, no view events), so this
    // never re-triggers a layout — no loop. Tied to the editor, so disposed when it is.
    editor.onDidLayoutChange(() => applyHidden(editor));

    // Layer 1 (proactive) — swallow the two boundary keystrokes that would merge a visible object
    // line into a hidden wrapper line (Backspace at object start, Delete at object end).
    editor.onKeyDown((e) => {
      const model = editor.getModel();
      const sel = editor.getSelection();
      if (!model || !sel || !sel.isEmpty()) return;
      const { first, last } = metaBounds(model);
      const atStart = sel.startLineNumber === first && sel.startColumn === 1;
      const atEnd =
        sel.startLineNumber === last && sel.startColumn === model.getLineMaxColumn(last);
      if (e.keyCode === m.KeyCode.Backspace && atStart) {
        e.preventDefault();
        e.stopPropagation();
      }
      if (e.keyCode === m.KeyCode.Delete && atEnd) {
        e.preventDefault();
        e.stopPropagation();
      }
    });

    // Layer 2 (proactive) — ⌘/Ctrl+A selects the OBJECT region only (shadows select-all for this
    // editor) so "select-all + type / delete / paste" can't touch the wrapper.
    editor.addAction({
      id: "grpcview.selectMetadata",
      label: "Select Metadata",
      keybindings: [m.KeyMod.CtrlCmd | m.KeyCode.KeyA],
      run: (ed) => {
        const model = ed.getModel();
        if (!model) return;
        const { first, last } = metaBounds(model);
        ed.setSelection({
          startLineNumber: first,
          startColumn: 1,
          endLineNumber: last,
          endColumn: model.getLineMaxColumn(last),
        });
      },
    });

    // Layer 3 (fail-safe) — after ANY change the model must stay canonical; if an exotic vector
    // (IME, drag-drop, replace-all) breaks the wrapper, restore last-good via an undo-coherent
    // executeEdits (not setValue). Also re-hides after object line-count changes.
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
      // FORCED: the full-range replace above is the same "reset the view, keep the cache" desync
      // as setValue, so restoring identical-geometry last-good must force the re-hide.
      applyHidden(editor, /* force */ true);
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
        // Hidden-wrapper mode: disable format-on-type/paste + folding (they reflow across the
        // hidden boundary), and remap the gutter so the first VISIBLE object line reads "1".
        formatOnType: false,
        formatOnPaste: false,
        folding: false,
        lineNumbers: (n: number) => (n <= META_PREFIX_LINES ? "" : String(n - META_PREFIX_LINES)),
        // Auto-trigger completions inside string literals too (monaco defaults
        // quickSuggestions.strings=false — microsoft/monaco-editor#2883); matches the body
        // editor so metadata values get in-quote suggestions, not only manual Ctrl+Space.
        quickSuggestions: { other: true, comments: false, strings: true },
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
