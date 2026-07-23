// Frontend token scanner — a faithful port of the backend resolver's grammar
// (service/workspace/tokens.go) so the UI recognizes exactly the `{{ name(args?) }}`
// tokens the server resolves on invoke. Used to (1) count/decorate tokens in the
// request body (Monaco) and (2) detect a metadata value that is exactly one token
// (rendered as a clickable generator chip). Recognition is textual and best-effort,
// matching the backend: a `{{ … }}` whose inner text is not name(args?) — or whose
// args are not valid JSON — is left as literal text.

// A recognized `{{ … }}` token and its byte/char offsets in the scanned string.
export interface Token {
  raw: string; // the full "{{…}}" substring
  name: string; // the (possibly dotted) generator display name
  inner: string; // the trimmed inner text (name + args), for chip display
  start: number; // index of the leading "{{"
  end: number; // index just past the trailing "}}"
}

// Mirrors tokenGrammarRe in tokens.go: a dotted identifier optionally followed by a
// parenthesized argument list (the args group keeps its surrounding parens).
const GRAMMAR_RE =
  /^([A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*)\s*(\([\s\S]*\))?$/;

// tokenClose returns the index of the "}" that opens the "}}" closing a token whose
// inner text starts at `from`, or -1. A "}}" inside a JSON string literal in the args
// (e.g. `{{ f("}}") }}`) does not close the token — mirrors tokenClose in tokens.go.
function tokenClose(s: string, from: number): number {
  let inString = false;
  let escaped = false;
  for (let i = from; i < s.length; i++) {
    const c = s[i];
    if (escaped) {
      escaped = false;
    } else if (inString && c === "\\") {
      escaped = true;
    } else if (c === '"') {
      inString = !inString;
    } else if (!inString && c === "}" && i + 1 < s.length && s[i + 1] === "}") {
      return i;
    }
  }
  return -1;
}

// parseTokenBody parses a token's inner text into its name, or null when it is not
// name(args?) or the args are not valid JSON — mirrors parseTokenBody in tokens.go.
function parseTokenName(inner: string): string | null {
  const m = GRAMMAR_RE.exec(inner.trim());
  if (!m) return null;
  const argsPart = m[2];
  if (argsPart) {
    const body = argsPart.slice(1, -1).trim();
    if (body) {
      try {
        // Comma-separated JSON values are the elements of a JSON array literal.
        JSON.parse("[" + body + "]");
      } catch {
        return null;
      }
    }
  }
  return m[1];
}

// scanTokens returns every recognized token in s, in order — mirrors scanTokens in
// tokens.go. It never throws: an unbalanced "{{" or a non-name(args?) body is skipped.
export function scanTokens(s: string): Token[] {
  const toks: Token[] = [];
  for (let i = 0; i + 1 < s.length; ) {
    if (s[i] === "{" && s[i + 1] === "{") {
      const ci = tokenClose(s, i + 2);
      if (ci >= 0) {
        const innerRaw = s.slice(i + 2, ci);
        const name = parseTokenName(innerRaw);
        if (name !== null) {
          toks.push({
            raw: s.slice(i, ci + 2),
            name,
            inner: innerRaw.trim(),
            start: i,
            end: ci + 2,
          });
          i = ci + 2;
          continue;
        }
      }
    }
    i++;
  }
  return toks;
}

// wholeToken reports the token when s (ignoring surrounding whitespace) is EXACTLY one
// token — the metadata rule (a value that is exactly `{{ … }}` binds to a generator).
// Mirrors wholeToken in tokens.go. Returns null otherwise.
export function wholeToken(s: string): Token | null {
  const trimmed = s.trim();
  const toks = scanTokens(trimmed);
  if (toks.length === 1 && toks[0].start === 0 && toks[0].end === trimmed.length) {
    return toks[0];
  }
  return null;
}
