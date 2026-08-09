import { useEffect, useRef } from "react";
import { Editor as MonacoEditor, useMonaco, type OnMount } from "@monaco-editor/react";
import type * as Monaco from "monaco-editor";
import { NOCTURNE_MONACO_THEME } from "@/theme/monaco-nocturne";
// Side-effect import: global TS defaults. Our own TS_MODEL_URI keeps this model distinct.
import "@/features/scripts/monaco-scripts";
import { META_SKELETON, metaBounds as boundsOf, hiddenLineRanges } from "./metadata-wrapper";
import { findRegion, type Region } from "./script-region";
import { modeSwitchFor, unwrapEdits, wrapEdits } from "./region-edits";
import { registerEditorForDebug } from "@/lib/editor-debug";

const TS_MODEL_URI = "file:///grpcview/request/metadata.ts";

// A .d.ts with no import/export is ambient, so `Metadata` is visible in the module body.
const METADATA_TYPE_DTS = "type Metadata = { [key: string]: string[] };";

// setHiddenAreas exists in monaco 0.52.2 but is stripped from the public monaco.d.ts.
type HasHiddenAreas = { setHiddenAreas?(ranges: Monaco.IRange[], source?: unknown): void };

// monaco merges hidden areas across sources; a private tag lets us replace only ours.
const HIDDEN_SOURCE = "grpcview-metadata-wrapper";

// Both derive from the live model text (via metadata-wrapper.ts's findRegion), so a module —
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
  // The region the buffer is CURRENTLY in, not the region it loaded in: the gutter offset is the
  // start marker's line number, which moves as the import block above it grows or shrinks.
  const regionRef = useRef<Region | undefined>(undefined);

  const monaco = useMonaco();

  const lineNumbersFor = (n: number) => {
    const r = regionRef.current;
    if (!r) return String(n);
    if (n <= r.startLine || n >= r.endLine) return "";
    return String(n - r.startLine);
  };

  // monaco skips an options update whose value compares equal, and lineNumbers compares by
  // function identity — so a fresh closure is what forces the gutter to repaint. Returns true
  // when the geometry actually moved (presence, startLine or endLine), so callers know to force a
  // re-hide: an inserted import moves startLine without changing wrapped-ness, and the gutter has
  // to follow it.
  const setRegion = (
    editor: Monaco.editor.IStandaloneCodeEditor,
    next: Region | undefined
  ): boolean => {
    const prev = regionRef.current;
    const same =
      prev === next ||
      (!!prev && !!next && prev.startLine === next.startLine && prev.endLine === next.endLine);
    if (same) return false;
    regionRef.current = next;
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
    }
    setRegion(ed, findRegion(data));
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
      const r = findRegion(model.getValue());
      const errors = monaco.editor
        .getModelMarkers({ resource: model.uri })
        .filter((mk) => mk.severity === monaco.MarkerSeverity.Error)
        .filter((mk) => {
          if (!r) return true;
          const whollyInPrefix = mk.endLineNumber <= r.startLine;
          const whollyInSuffix = mk.startLineNumber >= r.endLine;
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

    setRegion(editor, findRegion(editor.getValue()));
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

    // D3 + D4 (docs/design/planned/script-region.md): the region's first token decides the mode,
    // and a mode switch is a text edit — not a display-only toggle — so a shim the author edited
    // while plain never gets silently re-hidden with their edits inside it.
    //
    // No suppressChange here: the text genuinely changed, and the draft (onChange, below) must
    // see it. The old wrapper-guard's revert never reached the draft, which is exactly why the
    // editor and the saved script used to disagree until a reload.
    editor.onDidChangeModelContent(() => {
      if (suppressChange.current) return;
      const model = editor.getModel();
      if (!model) return;
      const v = model.getValue();
      const switchTo = modeSwitchFor(v);
      if (switchTo !== "none") {
        const edits =
          switchTo === "toPlain" ? unwrapEdits(findRegion(v)!) : wrapEdits(v, META_SKELETON);
        // Merge with the keystroke so one Cmd-Z restores the prior state. If monaco refuses, one
        // extra Cmd-Z still leaves a consistent state — text without markers, mode recomputed as
        // plain from that text — never corruption.
        editor.popUndoStop();
        editor.executeEdits("grpcview.mode-switch", edits);
        // executeEdits re-enters this handler synchronously: after toPlain the text starts with
        // `export default` and after toWrapped the region's first token is `{`, so modeSwitchFor
        // reads "none" on the way back out and this branch does not recurse further.
        applyHidden(editor, setRegion(editor, findRegion(model.getValue())));
        return;
      }
      applyHidden(editor, setRegion(editor, findRegion(v)));
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
