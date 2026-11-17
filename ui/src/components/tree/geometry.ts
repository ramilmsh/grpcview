// The one indent rule. A row is [pad][depth*indent][twistie][gap][content]; leaves reserve
// the twistie too, so a request's tag sits in the column of a folder icon at its level.
// Defaults match the v4 design (folder icon / request tag at 30 + depth*18).
export const ROW_PAD = 8; // .treerow's horizontal padding (app-tokens.css)
export const TWISTIE_WIDTH = 14;
export const DEFAULT_INDENT = 18;

// x of the twistie at `depth` — also where a between-rows drop line at that depth starts.
export const depthLeft = (depth: number, indent: number): number =>
  ROW_PAD + depth * indent;

// x of the guide for ancestor `level`: under the centre of that ancestor's twistie.
export const guideLeft = (level: number, indent: number): number =>
  depthLeft(level, indent) + TWISTIE_WIDTH / 2;

// The guide levels a row at `depth` draws: one per ancestor. Guides are per-row segments,
// so a folder's guide spans exactly its descendant rows and stops at the last one.
export const guideLevels = (depth: number): number[] =>
  Array.from({ length: depth }, (_, i) => i);
