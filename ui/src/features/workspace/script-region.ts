// The marker seam shared by body-wrapper.ts and metadata-wrapper.ts (docs/design/planned/
// script-region.md, decision D13: one marker text, two skeletons picked by filename).
//
// The old wrapper decided "is this wrapped?" with an exact prefix/suffix string match against a
// constant, and derived hidden-line geometry from a matching line-count constant. Accepting an
// auto-import inserts a line above the region, the exact match breaks, and the body silently
// stops being wrapped. Markers make the mode STATED in the text instead of inferred from a
// constant: a region is delimited by two marker lines, found by scanning, so anything above the
// start marker — an import block that grows and shrinks — is free to change without touching
// what "wrapped" means.
//
// findRegion is a pure line scan; it does not parse strings or comments. A line that reads,
// after trimming, exactly as the marker text is treated as a marker line even if it sits inside a
// string or template literal. This is deliberate (mirrors D8's rejection of a hand-rolled parser
// for a much smaller surface): the false-positive is rare enough, and undetectable without a full
// lexer, that a plain scan is preferred over one that almost-but-not-quite understands syntax.
export const START_MARKER = "// grpcview:script start";
export const END_MARKER = "// grpcview:script end";

export interface Region {
  startLine: number;
  endLine: number;
  total: number;
}

// A region needs exactly one start marker line and exactly one end marker line, with at least one
// line of region text between them. Zero, one, duplicate, out-of-order, or zero-line markers all
// read as "no region" — a plain script, shown whole. The zero-line case (endLine === startLine +
// 1) is unreachable through the editor (W2 key-swallows the boundary), so treating it as plain is
// the safe degenerate rather than a region with nothing in it.
export function findRegion(text: string): Region | undefined {
  const lines = text.split("\n");
  let startLine: number | undefined;
  let startCount = 0;
  let endLine: number | undefined;
  let endCount = 0;
  for (let i = 0; i < lines.length; i++) {
    const trimmed = lines[i].trim();
    if (trimmed === START_MARKER) {
      startCount++;
      startLine = i + 1;
    } else if (trimmed === END_MARKER) {
      endCount++;
      endLine = i + 1;
    }
  }
  if (startCount !== 1 || endCount !== 1 || startLine === undefined || endLine === undefined) {
    return undefined;
  }
  if (endLine <= startLine + 1) return undefined;
  return { startLine, endLine, total: lines.length };
}

export interface Bounds {
  first: number;
  last: number;
  total: number;
}

// The editable region: strictly between the markers, or the whole document when there is none.
// Derived from marker line numbers, not from a line-count constant, so this is unaffected by the
// store's trailing-newline normalization (an extra blank line at EOF moves `total` but not the
// marker lines) and by growth of whatever precedes the start marker.
export function regionBounds(text: string): Bounds {
  const region = findRegion(text);
  if (!region) {
    const total = text.split("\n").length;
    return { first: 1, last: total, total };
  }
  return { first: region.startLine + 1, last: region.endLine - 1, total: region.total };
}

export interface HiddenLineRange {
  startLine: number;
  endLine: number;
}

// [] for a plain script — nothing to hide. Otherwise line 1 through the start marker (inclusive),
// and the end marker through EOF (inclusive) — the two ranges the editor hides.
export function regionHiddenRanges(text: string): HiddenLineRange[] {
  const region = findRegion(text);
  if (!region) return [];
  return [
    { startLine: 1, endLine: region.startLine },
    { startLine: region.endLine, endLine: region.total },
  ];
}

// The author-visible text strictly between the markers, or undefined for a plain script.
export function regionText(text: string): string | undefined {
  const region = findRegion(text);
  if (!region) return undefined;
  const lines = text.split("\n");
  return lines.slice(region.startLine, region.endLine - 1).join("\n");
}

export interface BuildWrappedOptions {
  imports?: readonly string[];
  skeleton: string;
  region: string;
}

// No trailing newline — the store (service/store/codec.go's writeSourceFile) adds exactly one on
// write, so producing one here would double up on the round trip.
export function buildWrapped(opts: BuildWrappedOptions): string {
  const { imports = [], skeleton, region } = opts;
  const parts: string[] = [];
  if (imports.length > 0) {
    parts.push(imports.join("\n"));
    parts.push("");
  }
  parts.push(skeleton);
  parts.push(START_MARKER);
  parts.push(region);
  parts.push(END_MARKER);
  parts.push(")");
  return parts.join("\n");
}

// D10: regenerate the skeleton in place at the UI read seam, so a shim-version bump repairs files
// as they are opened without a daemon pass rewriting the workspace. Only the `export default`
// line, the markers and the closing `)` are regenerated; the import block above it is derived
// content the author (or an earlier auto-import) produced, not shim, and is preserved verbatim.
// Returns `text` unchanged when there is no region — normalizeSkeleton never touches a plain
// script.
export function normalizeSkeleton(text: string, skeleton: string): string {
  const region = findRegion(text);
  if (!region) return text;
  const lines = text.split("\n");
  const header = lines.slice(0, region.startLine - 1);

  const dropTrailingBlank = (): void => {
    while (header.length > 0 && header[header.length - 1].trim() === "") header.pop();
  };
  dropTrailingBlank();
  if (header.length > 0 && /^\s*export\s+default\b/.test(header[header.length - 1])) {
    header.pop();
  }
  dropTrailingBlank();

  return buildWrapped({
    imports: header.length > 0 ? header : undefined,
    skeleton,
    region: lines.slice(region.startLine, region.endLine - 1).join("\n"),
  });
}

// D4 wrapped -> plain: delete exactly the two marker lines, keep everything else (the skeleton,
// the import block and the closing `)` all stay, now visible and author-owned). undefined when
// there is no region to unwrap.
export function unwrapToPlain(text: string): string | undefined {
  const region = findRegion(text);
  if (!region) return undefined;
  const lines = text.split("\n");
  lines.splice(region.endLine - 1, 1);
  lines.splice(region.startLine - 1, 1);
  return lines.join("\n");
}
