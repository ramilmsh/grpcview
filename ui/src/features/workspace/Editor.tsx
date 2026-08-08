import { useEffect, useRef } from "react";
import { Editor as MonacoEditor, useMonaco, type OnMount } from "@monaco-editor/react";
import type * as Monaco from "monaco-editor";
import { NOCTURNE_MONACO_THEME } from "@/theme/monaco-nocturne";
// Side-effect import: global TS defaults. Our own TS_MODEL_URI keeps this model distinct.
import "@/features/scripts/monaco-scripts";
// Side-effect import: virtual `@bufbuild/protobuf` d.ts the generated `_pb.ts` files need.
import "./vendor/bufbuild-stubs";
import {
  PREFIX_LINES,
  isCanonical,
  bodyBounds as boundsOf,
  hiddenLineRanges,
} from "./body-wrapper";
import { registerEditorForDebug } from "@/lib/editor-debug";

const TS_MODEL_URI = "file:///grpcview/request/body.ts";

// setHiddenAreas exists in monaco 0.52.2 but is stripped from the public monaco.d.ts.
type HasHiddenAreas = { setHiddenAreas?(ranges: Monaco.IRange[], source?: unknown): void };

// monaco merges hidden areas across sources; a private tag lets us replace only ours.
const HIDDEN_SOURCE = "grpcview-body-wrapper";

// Both derive from the live model text (via body-wrapper.ts's isCanonical), so a module — never
// wrapped, per the two-forms rule — naturally gets `first: 1` and no hidden ranges at all.
function bodyBounds(model: Monaco.editor.ITextModel) {
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
  const { first } = bodyBounds(model);
  // A module's `first` is 1, so this never fires — there is no hidden prefix to clamp out of.
  if (pos && pos.lineNumber < first) {
    editor.setPosition({ lineNumber: first, column: 1 });
  }
}

interface EditorProps {
  data: string;
  onChange: (value: string) => void;
  currentKey: string; // request identity — reload the buffer when it changes
  descriptorSet?: Uint8Array;
  inputPackage?: string;
  inputName?: string;
  inputFile?: string;
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
  onErrorsChange,
}: EditorProps) {
  const editorRef = useRef<Monaco.editor.IStandaloneCodeEditor | null>(null);
  // @monaco-editor/react only suppresses onChange for its own controlled `value` prop.
  const suppressChange = useRef(false);
  const lastGood = useRef<string>("");
  // Fixed for the lifetime of the loaded buffer (re-derived only when currentKey reloads it): a
  // module has no wrapper, so the corruption-guard below must never fight an edit that merely
  // keeps the buffer non-canonical, the way it would for a wrapped body's broken wrapper.
  const wrappedRef = useRef(true);
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
    wrappedRef.current = isCanonical(data);
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

  // Method-scoped, so it stays here; the `./gen/**` modules its import resolves against are
  // registered app-level by gv-types.ts, hence the shared `file:///grpcview/request/` prefix.
  // typescriptDefaults is global and a same-path re-add throws "Duplicate definition".
  const typeLibs = useRef<Monaco.IDisposable[]>([]);
  useEffect(() => {
    // descriptorSet is best-effort on the backend (empty when a source is unreachable).
    if (!monaco || !descriptorSet?.length || !inputFile) return;
    let cancelled = false;
    void (async () => {
      const { generateWorkspaceTypes, requestMessageAlias } = await import("./proto-types");
      // Free after gv-types.ts's call: generateWorkspaceTypes memoizes on the descriptor bytes.
      const files = generateWorkspaceTypes(descriptorSet);
      if (cancelled) return;
      const tsDefaults = monaco.languages.typescript.typescriptDefaults;
      typeLibs.current.forEach((d) => d.dispose());
      typeLibs.current = [];
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

  const onMount: OnMount = (editor, m) => {
    editorRef.current = editor;
    registerEditorForDebug(TS_MODEL_URI, editor);
    // A full-document format would reflow the wrapper across the hidden boundary.
    editor.addCommand(m.KeyMod.CtrlCmd | m.KeyCode.KeyS, () => {
      const model = editor.getModel();
      if (!model) return;
      const selectBody = () => {
        const { first, last } = bodyBounds(model);
        editor.setSelection({
          startLineNumber: first,
          startColumn: 1,
          endLineNumber: last,
          endColumn: model.getLineMaxColumn(last),
        });
      };
      selectBody();
      void Promise.resolve(editor.getAction("editor.action.formatSelection")?.run())
        .then(() => {
          // formatSelection indents one level inside the hidden `=> (`; outdent clamps at 0.
          selectBody();
          return Promise.resolve(editor.getAction("editor.action.outdentLines")?.run());
        })
        .then(() => applyHidden(editor));
    });

    lastGood.current = editor.getValue();
    wrappedRef.current = isCanonical(editor.getValue());
    applyHidden(editor);

    // Hidden areas live on the editor's viewModel: the first real layout drops them.
    editor.onDidLayoutChange(() => applyHidden(editor));

    // Swallow the boundary keystrokes that would merge a body line into a hidden one.
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

    // Shadow select-all for this editor so it covers the body region only.
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

    // Fail-safe: undo an exotic edit (IME, drag-drop, replace-all) that broke the wrapper. A
    // module was never wrapped, so there is nothing for it to break — every edit is accepted.
    editor.onDidChangeModelContent(() => {
      if (suppressChange.current) return;
      const model = editor.getModel();
      if (!model) return;
      const v = model.getValue();
      if (!wrappedRef.current || isCanonical(v)) {
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
        lineNumbers: (n: number) =>
          !wrappedRef.current ? String(n) : n <= PREFIX_LINES ? "" : String(n - PREFIX_LINES),
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
