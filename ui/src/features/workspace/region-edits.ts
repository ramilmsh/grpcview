// The two mode-switch edits (D3 + D4, docs/design/planned/script-region.md) as plain data:
// executeEdits accepts IRange literals, so nothing here needs the monaco namespace, and the pure
// shape is what makes this testable without mounting an editor (mirrors module-specifier.ts).
import { END_MARKER, START_MARKER, findRegion, regionText, type Region } from "./script-region";
import { leadsWithBrace } from "./module-sniff";

export interface LineEdit {
  range: {
    startLineNumber: number;
    startColumn: number;
    endLineNumber: number;
    endColumn: number;
  };
  text: string;
}

// wrapped -> plain: delete exactly the two marker lines (including each one's trailing newline),
// as two separate non-overlapping edits rather than a full-range replace. Two edits let monaco
// compute the composite transform itself and adjust the cursor/selection accordingly; a full
// replace would reset the cursor to the top and lose the selection.
export function unwrapEdits(region: Region): LineEdit[] {
  return [
    {
      range: {
        startLineNumber: region.startLine,
        startColumn: 1,
        endLineNumber: region.startLine + 1,
        endColumn: 1,
      },
      text: "",
    },
    {
      range: {
        startLineNumber: region.endLine,
        startColumn: 1,
        endLineNumber: region.endLine + 1,
        endColumn: 1,
      },
      text: "",
    },
  ];
}

// plain -> wrapped: skeleton + start marker inserted before line 1, end marker + `)` inserted
// after the last line. Two insertions, computed against the ORIGINAL text, for the same reason as
// unwrapEdits: monaco applies both against the pre-edit document and adjusts the cursor itself.
export function wrapEdits(text: string, skeleton: string): LineEdit[] {
  const lines = text.split("\n");
  const lastLine = lines.length;
  const lastColumn = lines[lastLine - 1].length + 1;
  return [
    {
      range: { startLineNumber: 1, startColumn: 1, endLineNumber: 1, endColumn: 1 },
      text: `${skeleton}\n${START_MARKER}\n`,
    },
    {
      range: {
        startLineNumber: lastLine,
        startColumn: lastColumn,
        endLineNumber: lastLine,
        endColumn: lastColumn,
      },
      text: `\n${END_MARKER}\n)`,
    },
  ];
}

export type ModeSwitch = "none" | "toPlain" | "toWrapped";

// D3: what mode the text should be in, given the mode it is in now.
//
// Wrapped with an empty (or whitespace-only) region: "none" — an empty region HOLDS the current
// mode, so deleting the last `}` of `{}` does not rip the shim out and put it back as you retype.
//
// Plain with a leading `{`: "toWrapped" only when the WHOLE FILE's first token is `{`. A
// just-unwrapped document starts with the (now-visible) skeleton's `export default`, so it does
// not immediately re-wrap.
export function modeSwitchFor(text: string): ModeSwitch {
  const region = findRegion(text);
  if (region) {
    const body = regionText(text) ?? "";
    if (body.trim() === "") return "none";
    return leadsWithBrace(body) ? "none" : "toPlain";
  }
  return leadsWithBrace(text) ? "toWrapped" : "none";
}
