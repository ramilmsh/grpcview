import { useEffect, useRef } from "react";
import { Editor as MonacoEditor, useMonaco, type OnMount } from "@monaco-editor/react";
import type * as Monaco from "monaco-editor";
import { NOCTURNE_MONACO_THEME } from "@/theme/monaco-nocturne";
import { scanTokens, type Token } from "./tokens";

// The request editor uses one JSON model at a fixed URI; the per-method JSON
// schema is swapped on the shared jsonDefaults (matched to that URI) and the
// buffer is reloaded when the active request changes — so two requests on the
// same method still show their own draft. Ported from the previous Editor.tsx
// (plan §7), retargeted at the bundled Monaco + Nocturne theme.
//
// S2: `{{ generator(args?) }}` tokens in the body get an accent-2 chip decoration
// (Monaco can't host React chips inline, so this is an inline-className decoration
// recomputed on every edit) and a mouse-down on a token opens the binding editor
// for the generator it names.
const MODEL_URI = "grpcview://request/body.json";

interface EditorProps {
  schema?: object; // the current method's input JSON schema (resolved upstream)
  data: string;
  onChange: (value: string) => void;
  currentMethod: { service: string; method: string };
  currentKey: string; // request identity — reload the buffer when it changes
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
  onErrorsChange,
  onTokenClick,
}: EditorProps) {
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
    if (!monaco) return;
    monaco.languages.json.jsonDefaults.setDiagnosticsOptions({
      validate: true,
      schemaValidation: "error",
      enableSchemaRequest: false,
      schemas: schema
        ? [
            {
              uri: `grpcview://schemas/${currentMethod.service}/${currentMethod.method}`,
              fileMatch: [MODEL_URI],
              schema,
            },
          ]
        : [],
    });
  }, [monaco, schema, currentMethod.service, currentMethod.method]);

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

  const onMount: OnMount = (editor, m) => {
    editorRef.current = editor;
    // ⌘S / Ctrl+S formats the document (plan §7).
    editor.addCommand(m.KeyMod.CtrlCmd | m.KeyCode.KeyS, () => {
      editor.getAction("editor.action.formatDocument")?.run();
    });

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
      path={MODEL_URI}
      language="json"
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
