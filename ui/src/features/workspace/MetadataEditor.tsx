import { useEffect, useRef } from "react";
import { Editor as MonacoEditor, useMonaco, type OnMount } from "@monaco-editor/react";
import type * as Monaco from "monaco-editor";
import { NOCTURNE_MONACO_THEME } from "@/theme/monaco-nocturne";
// Side-effect import: global TS defaults. Our own TS_MODEL_URI keeps this model distinct.
import "@/features/scripts/monaco-scripts";
import { META_PREFIX_LINES, META_SUFFIX_LINES, isCanonical } from "./metadata-wrapper";
import { registerGeneratorLibs, type GeneratorDef } from "./generator-libs";
import { registerEditorForDebug } from "@/lib/editor-debug";

const TS_MODEL_URI = "file:///grpcview/request/metadata.ts";

// A .d.ts with no import/export is ambient, so `Metadata` is visible in the module body.
const METADATA_TYPE_DTS = "type Metadata = { [key: string]: string[] };";

// setHiddenAreas exists in monaco 0.52.2 but is stripped from the public monaco.d.ts.
type HasHiddenAreas = { setHiddenAreas?(ranges: Monaco.IRange[], source?: unknown): void };

// monaco merges hidden areas across sources; a private tag lets us replace only ours.
const HIDDEN_SOURCE = "grpcview-metadata-wrapper";

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

// force: an identical re-set is skipped by monaco's cache, even after setValue reset the view.
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
  // @monaco-editor/react only suppresses onChange for its own controlled `value` prop.
  const suppressChange = useRef(false);
  const lastGood = useRef<string>("");
  const monaco = useMonaco();

  useEffect(() => {
    const ed = editorRef.current;
    if (!ed) return;
    if (ed.getValue() !== data) {
      suppressChange.current = true;
      ed.setValue(data);
      suppressChange.current = false;
      lastGood.current = data;
    }
    // Forced: a remount has no hidden areas yet, and setValue may not change the geometry.
    applyHidden(ed, /* force */ true);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [currentKey]);

  useEffect(() => {
    if (!monaco || !onErrorsChange) return;
    const sub = monaco.editor.onDidChangeMarkers(() => {
      const model = editorRef.current?.getModel();
      if (!model) return;
      // Markers wholly on a hidden wrapper line must not inflate the footer count.
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

  // typescriptDefaults is global; the path is distinct from the body editor's libs.
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

  // scope="metadata" namespaces the URIs; a same-path re-add throws "Duplicate definition".
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
    // A full-document format would reflow the wrapper across the hidden boundary.
    editor.addCommand(m.KeyMod.CtrlCmd | m.KeyCode.KeyS, () => {
      const model = editor.getModel();
      if (!model) return;
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
          // formatSelection indents one level inside the hidden `=> (`; outdent clamps at 0.
          selectObject();
          return Promise.resolve(editor.getAction("editor.action.outdentLines")?.run());
        })
        .then(() => applyHidden(editor));
    });

    lastGood.current = editor.getValue();
    applyHidden(editor);

    // Hidden areas live on the editor's viewModel: the first real layout drops them.
    editor.onDidLayoutChange(() => applyHidden(editor));

    // Swallow the boundary keystrokes that would merge an object line into a hidden one.
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

    // Shadow select-all for this editor so it covers the object region only.
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

    // Fail-safe: undo an exotic edit (IME, drag-drop, replace-all) that broke the wrapper.
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
        // These all reflow or contend across the hidden wrapper boundary.
        formatOnType: false,
        formatOnPaste: false,
        folding: false,
        lineNumbers: (n: number) => (n <= META_PREFIX_LINES ? "" : String(n - META_PREFIX_LINES)),
        // monaco defaults quickSuggestions.strings=false (microsoft/monaco-editor#2883).
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
