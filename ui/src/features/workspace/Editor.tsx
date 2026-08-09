import { useEffect, useRef } from "react";
import { Editor as MonacoEditor, useMonaco, type OnMount } from "@monaco-editor/react";
import type * as Monaco from "monaco-editor";
import { NOCTURNE_MONACO_THEME } from "@/theme/monaco-nocturne";
// Side-effect import: global TS defaults. Our own TS_MODEL_URI keeps this model distinct.
import "@/features/scripts/monaco-scripts";
// Side-effect import: virtual `@bufbuild/protobuf` d.ts the generated `_pb.ts` files need.
import "./vendor/bufbuild-stubs";
import { BODY_SKELETON, bodyBounds as boundsOf, hiddenLineRanges } from "./body-wrapper";
import { findRegion, type Region } from "./script-region";
import { modeSwitchFor, unwrapEdits, wrapEdits } from "./region-edits";
import { pruneEdits, type UnusedSpan } from "./import-block";
import {
  candidatesFrom,
  resolveOrBail,
  unresolvedNamesIn,
  type NameSpan,
} from "./resolve-imports";
import { getAutoImportContext } from "./module-auto-import";
import { workspacePathForUri } from "./module-specifier";
import { registerEditorForDebug } from "@/lib/editor-debug";

const TS_MODEL_URI = "file:///grpcview/request/body.ts";

// D7 + D8 (docs/design/planned/script-region.md): the hidden import block is pruned of unused
// entries, driven off the TS worker's own suggestion diagnostics (getSuggestionDiagnostics) — not
// a hand-rolled parser. Idle-triggered off the last keystroke, so a run of typing doesn't hammer
// the worker with a getSuggestionDiagnostics round trip per character. The same timer carries the
// D6 resolve-or-bail pass, which a paste arms — one debounce, one worker round trip per idle.
const PRUNE_DEBOUNCE_MS = 800;
// Unused-identifier codes observed from the vendored ts.worker.js, confirmed against the
// `typescript` package's own language service (see import-block.ts's UnusedSpan doc for the two
// span shapes 6133 can take). Every other code is ignored.
const UNUSED_IMPORT_CODES = new Set([6133, 6192]);

// setHiddenAreas exists in monaco 0.52.2 but is stripped from the public monaco.d.ts.
type HasHiddenAreas = { setHiddenAreas?(ranges: Monaco.IRange[], source?: unknown): void };

// monaco merges hidden areas across sources; a private tag lets us replace only ours.
const HIDDEN_SOURCE = "grpcview-body-wrapper";

// Both derive from the live model text (via body-wrapper.ts's findRegion), so a module — never
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
  // The region the buffer is CURRENTLY in, not the region it loaded in: the gutter offset is the
  // start marker's line number, which moves as the import block above it grows or shrinks.
  const regionRef = useRef<Region | undefined>(undefined);
  // The monaco namespace `onMount` is handed, not the reactive useMonaco() value: onMount's `m`
  // is guaranteed non-null by the time it fires, so the debounced prune pass never has to guard
  // against a not-yet-loaded monaco.
  const monacoNsRef = useRef<typeof Monaco | null>(null);
  const pruneTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  // D6: set by onDidPaste, consumed by the next debounced pass. Only a paste arms the
  // resolve-or-bail step; typing must never arm it (see resolve-imports.ts's header).
  const resolveArmedRef = useRef(false);

  const monaco = useMonaco();

  const clearPruneTimer = () => {
    if (pruneTimerRef.current !== null) {
      clearTimeout(pruneTimerRef.current);
      pruneTimerRef.current = null;
    }
  };

  // D7 + D8: prune the hidden import block of names the TS worker reports unused. Async — it
  // waits on the worker round trip — so the model is re-read on resolve and the pass bails if the
  // text changed underneath it, rather than applying edits against stale offsets.
  const runPrune = async () => {
    const editor = editorRef.current;
    const m = monacoNsRef.current;
    if (!editor || !m) return;
    const model = editor.getModel();
    if (!model) return;
    const before = model.getValue();
    if (!findRegion(before)) return;
    const getWorker = await m.languages.typescript.getTypeScriptWorker();
    const client = await getWorker(model.uri);
    const diags = await client.getSuggestionDiagnostics(model.uri.toString());
    if (editor.getModel() !== model || model.getValue() !== before) return;
    const unused: UnusedSpan[] = [];
    for (const d of diags) {
      if (!UNUSED_IMPORT_CODES.has(d.code) || d.start == null || d.length == null) continue;
      unused.push({ start: d.start, length: d.length, code: d.code });
    }
    const edits = pruneEdits(before, BODY_SKELETON, unused);
    if (!edits) return;
    // No popUndoStop: this is a debounced machine edit with no keystroke to merge into. It is its
    // own undo stop, unlike the mode-switch edits above.
    editor.executeEdits("grpcview.prune-imports", edits);
  };

  // D6: resolve-or-bail, armed by a paste only. Semantic diagnostics, not the suggestion ones the
  // prune reads — an unresolved name is TS2304. Returns whether it edited the buffer, so the
  // caller can leave the prune to the pass that edit reschedules.
  const runResolve = async (): Promise<boolean> => {
    const editor = editorRef.current;
    const m = monacoNsRef.current;
    if (!editor || !m) return false;
    const model = editor.getModel();
    if (!model) return false;
    const before = model.getValue();
    const region = findRegion(before);
    if (!region) return false;
    const getWorker = await m.languages.typescript.getTypeScriptWorker();
    const client = await getWorker(model.uri);
    const diags = await client.getSemanticDiagnostics(model.uri.toString());
    if (editor.getModel() !== model || model.getValue() !== before) return false;
    const spans: NameSpan[] = [];
    for (const d of diags) {
      if (d.start == null || d.length == null) continue;
      spans.push({ start: d.start, length: d.length, code: d.code });
    }
    const outcome = resolveOrBail({
      text: before,
      skeleton: BODY_SKELETON,
      unresolved: unresolvedNamesIn(before, spans),
      candidates: candidatesFrom(getAutoImportContext(), workspacePathForUri(model.uri.toString())),
    });
    if (outcome.kind === "addImports") {
      editor.executeEdits("grpcview.resolve-imports", outcome.edits);
      return true;
    }
    if (outcome.kind === "bail") {
      editor.executeEdits("grpcview.resolve-bail", unwrapEdits(before, region));
      applyHidden(editor, setRegion(editor, findRegion(model.getValue())));
      return true;
    }
    return false;
  };

  const runIdlePass = async () => {
    if (!resolveArmedRef.current) {
      await runPrune();
      return;
    }
    resolveArmedRef.current = false;
    // An edit of its own reschedules this pass through onDidChangeModelContent, and the TS worker
    // has not seen the new text yet — pruning against diagnostics for the pre-edit buffer would
    // drop the import just added.
    if (await runResolve()) return;
    await runPrune();
  };

  const schedulePrune = () => {
    clearPruneTimer();
    pruneTimerRef.current = setTimeout(() => {
      pruneTimerRef.current = null;
      void runIdlePass();
    }, PRUNE_DEBOUNCE_MS);
  };

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
    // A pending prune targets the buffer being left behind; its offsets have no meaning once the
    // request identity changes. The arm flag goes with it: the setValue below is a document swap,
    // not an author edit, and a saved file that opens with red squiggles must not be unwrapped.
    clearPruneTimer();
    resolveArmedRef.current = false;
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

  useEffect(() => clearPruneTimer, []);

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
    monacoNsRef.current = m;
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

    setRegion(editor, findRegion(editor.getValue()));
    applyHidden(editor);

    // Hidden areas live on the editor's viewModel: the first real layout drops them.
    editor.onDidLayoutChange(() => applyHidden(editor));

    // D6: the only signal that arms resolve-or-bail. codeEditorWidget.js fires this from _paste
    // solely when the source is "keyboard", and monaco exposes no drop event at all, so a text
    // DROP into the editor does not arm the pass — paste only, deliberately, rather than a
    // fabricated drop path.
    editor.onDidPaste(() => {
      resolveArmedRef.current = true;
    });

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

    // D3 + D4 (docs/design/planned/script-region.md): the region's first token decides the mode,
    // and a mode switch is a text edit — not a display-only toggle — so a shim the author edited
    // while plain never gets silently re-hidden with their edits inside it.
    //
    // No suppressChange here: the text genuinely changed, and the draft (onChange, below) must
    // see it. The old wrapper-guard's revert never reached the draft, which is exactly why the
    // editor and the saved body used to disagree until a reload.
    editor.onDidChangeModelContent(() => {
      if (suppressChange.current) return;
      const model = editor.getModel();
      if (!model) return;
      const v = model.getValue();
      const switchTo = modeSwitchFor(v);
      if (switchTo !== "none") {
        const edits =
          switchTo === "toPlain" ? unwrapEdits(v, findRegion(v)!) : wrapEdits(v, BODY_SKELETON);
        // Merge with the keystroke so one Cmd-Z restores the prior state. If monaco refuses, one
        // extra Cmd-Z still leaves a consistent state — text without markers, mode recomputed as
        // plain from that text — never corruption.
        editor.popUndoStop();
        editor.executeEdits("grpcview.mode-switch", edits);
        // executeEdits re-enters this handler synchronously: after toPlain the text starts with
        // `export default` and after toWrapped the region's first token is `{`, so modeSwitchFor
        // reads "none" on the way back out and this branch does not recurse further.
        applyHidden(editor, setRegion(editor, findRegion(model.getValue())));
      } else {
        applyHidden(editor, setRegion(editor, findRegion(v)));
      }
      // Reschedule on every change: a wrapped buffer gets a fresh idle-debounced prune pass, a
      // plain one (or one that just switched to plain) drops any pass in flight.
      if (findRegion(editor.getModel()?.getValue() ?? "")) {
        schedulePrune();
      } else {
        clearPruneTimer();
      }
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
