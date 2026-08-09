import { useEffect, useRef } from "react";
import { Editor as MonacoEditor, useMonaco, type OnMount } from "@monaco-editor/react";
import type * as Monaco from "monaco-editor";
import { NOCTURNE_MONACO_THEME } from "@/theme/monaco-nocturne";
// Side-effect import: global TS defaults. Our own TS_MODEL_URI keeps this model distinct.
import "@/features/scripts/monaco-scripts";
import {
  META_PREFIX_LINES,
  isCanonical,
  metaBounds as boundsOf,
  hiddenLineRanges,
} from "./metadata-wrapper";
import { isModule } from "./module-sniff";
import { registerEditorForDebug } from "@/lib/editor-debug";

const TS_MODEL_URI = "file:///grpcview/request/metadata.ts";

// A .d.ts with no import/export is ambient, so `Metadata` is visible in the module body.
const METADATA_TYPE_DTS = "type Metadata = { [key: string]: string[] };";

// setHiddenAreas exists in monaco 0.52.2 but is stripped from the public monaco.d.ts.
type HasHiddenAreas = { setHiddenAreas?(ranges: Monaco.IRange[], source?: unknown): void };

// monaco merges hidden areas across sources; a private tag lets us replace only ours.
const HIDDEN_SOURCE = "grpcview-metadata-wrapper";

// Both derive from the live model text (via metadata-wrapper.ts's isCanonical), so a module —
// never wrapped, per the two-forms rule — naturally gets `first: 1` and no hidden ranges at all.
function metaBounds(model: Monaco.editor.ITextModel) {
  return boundsOf(model.getValue());
}

function hiddenRanges(model: Monaco.editor.ITextModel): Monaco.IRange[] {
  return hiddenLineRanges(model.getValue()).map((r) => ({
    startLineNumber: r.startLine,
    startColumn: 1,
    endLineNumber: r.endLine,
    endColumn: 1,
  }));
}

// force: an identical re-set is skipped by monaco's cache, even after setValue reset the view.
function applyHidden(editor: Monaco.editor.IStandaloneCodeEditor, force = false) {
  const model = editor.getModel();
  const ha = editor as unknown as HasHiddenAreas;
  if (!model || typeof ha.setHiddenAreas !== "function") return;
  if (force) ha.setHiddenAreas([], HIDDEN_SOURCE);
  ha.setHiddenAreas(hiddenRanges(model), HIDDEN_SOURCE);
  const pos = editor.getPosition();
  const { first } = metaBounds(model);
  // A module's `first` is 1, so this never fires — there is no hidden prefix to clamp out of.
  if (pos && pos.lineNumber < first) {
    editor.setPosition({ lineNumber: first, column: 1 });
  }
}

interface MetadataEditorProps {
  data: string;
  onChange: (value: string) => void;
  currentKey: string; // request identity — reload the buffer when it changes
  onErrorsChange?: (errors: number) => void;
}

export function MetadataEditor({
  data,
  onChange,
  currentKey,
  onErrorsChange,
}: MetadataEditorProps) {
  const editorRef = useRef<Monaco.editor.IStandaloneCodeEditor | null>(null);
  // @monaco-editor/react only suppresses onChange for its own controlled `value` prop.
  const suppressChange = useRef(false);
  const lastGood = useRef<string>("");
  // Tracks the CURRENT form of the buffer, not the form it loaded in: an edit can promote a
  // wrapped script to a plain module (see the guard below), and the gutter has to follow it
  // without a reload. A module has no wrapper, so the guard must never fight an edit that merely
  // keeps the buffer non-canonical, the way it would for a wrapped script's broken wrapper.
  const wrappedRef = useRef(true);
  const monaco = useMonaco();

  const lineNumbersFor = (n: number) =>
    !wrappedRef.current
      ? String(n)
      : n <= META_PREFIX_LINES
        ? ""
        : String(n - META_PREFIX_LINES);

  // monaco skips an options update whose value compares equal, and lineNumbers compares by
  // function identity — so the fresh closure is what forces the gutter to repaint on a flip.
  const setWrapped = (editor: Monaco.editor.IStandaloneCodeEditor, next: boolean): boolean => {
    if (wrappedRef.current === next) return false;
    wrappedRef.current = next;
    editor.updateOptions({ lineNumbers: (n: number) => lineNumbersFor(n) });
    return true;
  };

  useEffect(() => {
    const ed = editorRef.current;
    if (!ed) return;
    if (ed.getValue() !== data) {
      suppressChange.current = true;
      ed.setValue(data);
      suppressChange.current = false;
      lastGood.current = data;
    }
    setWrapped(ed, isCanonical(data));
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
    setWrapped(editor, isCanonical(editor.getValue()));
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

    // Fail-safe: undo an exotic edit (IME, drag-drop, replace-all) that broke the wrapper. A
    // module was never wrapped, so there is nothing for it to break — every edit is accepted.
    //
    // An edit that leaves the canonical form but still yields a module PROMOTES the script to the
    // plain form instead of being reverted: that is the auto-import case, where the inserted
    // import displaces the wrapper's prefix. The promotion is applied live — hidden areas drop,
    // the gutter renumbers — because the alternative is the guard reverting the editor while the
    // non-canonical text has already reached the draft, so the two only agree after a reload.
    editor.onDidChangeModelContent(() => {
      if (suppressChange.current) return;
      const model = editor.getModel();
      if (!model) return;
      const v = model.getValue();
      const canonical = isCanonical(v);
      if (!wrappedRef.current || canonical || isModule(v)) {
        lastGood.current = v;
        // Forced only on a flip: dropping the areas and re-setting them on every keystroke
        // would repaint the whole view for nothing.
        applyHidden(editor, /* force */ setWrapped(editor, canonical));
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
        lineNumbers: (n: number) => lineNumbersFor(n),
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
