// Mirrors the backend's hasDefaultExport (service/scripting/entry.go): a body/metadata script
// that already exports a default value is a module and must not be wrapped a second time — only
// a bare expression gets wrapped. A false positive here (treating an expression as a module)
// leaves a bare `{ … }` unwrapped, which parses as a block and fails confusingly, so bias toward
// wrapping when unsure.
const EXPORT_DEFAULT_RE = /\bexport\s+default\b/;

// Blanks comment AND string/template-literal interiors, mirroring the Go bundler's maskLiterals
// (service/scripting/bundler.go) byte-for-byte. Masking comments alone is not enough: a `/*` or
// `//` that lives INSIDE a string literal opens a fake comment token that a naive scanner can't
// tell from a real one. The `//` case only swallows to end of line, but the `/*` case has no
// such bound — an unterminated fake block comment eats everything to EOF, including a real
// `export default` further down, which then reads as a bare expression and gets wrapped a
// second time. This is the same class of bug the Go side hit before it grew maskLiterals; a
// dropped match here is not the safe direction, it is the corruption this file exists to
// prevent, so string/template interiors are masked exactly like comments are.
export function maskLiterals(source: string): string {
  const b = source.split("");
  const n = b.length;
  const blank = (i: number): void => {
    if (b[i] !== "\n") b[i] = " ";
  };
  let i = 0;
  while (i < n) {
    if (b[i] === "/" && i + 1 < n && b[i + 1] === "/") {
      i += 2;
      while (i < n && b[i] !== "\n") {
        blank(i);
        i++;
      }
    } else if (b[i] === "/" && i + 1 < n && b[i + 1] === "*") {
      i += 2;
      while (i < n && !(b[i] === "*" && i + 1 < n && b[i + 1] === "/")) {
        blank(i);
        i++;
      }
      if (i < n) i += 2;
    } else if (b[i] === '"' || b[i] === "'" || b[i] === "`") {
      const q = b[i];
      i++;
      while (i < n && b[i] !== q) {
        if (b[i] === "\\" && i + 1 < n) {
          blank(i);
          i++;
        }
        blank(i);
        i++;
      }
      if (i < n) i++;
    } else {
      i++;
    }
  }
  return b.join("");
}

// isModule reports whether `source` is already a module (carries its own default export) rather
// than a bare expression that still needs wrapping.
export const isModule = (source: string): boolean => EXPORT_DEFAULT_RE.test(maskLiterals(source));

// leadsWithBrace reports whether the first token of `source` — skipping whitespace and leading
// comments — is `{`. This, not isModule, is what decides whether a body/metadata script gets the
// canonical wrapper: an object literal is the JSON-like form the wrapper exists for, and
// EVERYTHING else is taken at face value as a module the author owns.
//
// maskLiterals is no help here: it blanks comment INTERIORS but leaves the `//` and `/*`
// delimiters in place, so a file opening with a banner comment would read as leading with `/`.
// This scans past whole comments instead.
export const leadsWithBrace = (source: string): boolean => {
  let i = 0;
  while (i < source.length) {
    const c = source[i];
    if (c === " " || c === "\t" || c === "\r" || c === "\n") {
      i++;
    } else if (c === "/" && source[i + 1] === "/") {
      i += 2;
      while (i < source.length && source[i] !== "\n") i++;
    } else if (c === "/" && source[i + 1] === "*") {
      i += 2;
      while (i < source.length && !(source[i] === "*" && source[i + 1] === "/")) i++;
      i += 2;
    } else {
      return c === "{";
    }
  }
  return false;
};
