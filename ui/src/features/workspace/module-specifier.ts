// Pure logic behind the module-specifier completion provider (module-specifier-complete.ts):
// what `@/` and `#/` should offer once the cursor sits inside an import's quotes.
//
// The TS worker cannot supply this. Path completion inside a module specifier is the one
// completion TS answers from the FILESYSTEM rather than from the program: it calls
// `readDirectory`/`getDirectories`/`directoryExists` on the LanguageServiceHost, and the vendored
// worker (node_modules/monaco-editor/esm/vs/language/typescript/ts.worker.js) implements none of
// the three — only `fileExists`/`readFile` over its extra libs. So `paths` resolution WORKS
// (hover, go-to-definition and diagnostics on `#/scripts/ids` are all correct) while the
// specifier itself completes to nothing but monaco's word-based suggestions. Same data source as
// auto-import.ts: the workspace module list the frontend already holds.
import { collectionPathPrefix, stripModuleExtension, WS_PREFIX } from "./workspace-modules";

// The specifier's own text, as typed so far, plus where its last `/`-delimited segment starts —
// the range a completion replaces, so accepting `scripts/` after `#/scr` does not double the
// prefix.
export interface SpecifierPrefix {
  typed: string;
  segmentOffset: number;
}

// `from "…`, `import("…`, `require("…`, and the bare `import "…` side-effect form. Anchored at
// the end of the text before the cursor, and rejecting a closing quote inside the specifier, so
// this only ever matches an UNTERMINATED specifier the cursor is sitting in.
const SPECIFIER_RE = /(?:\bfrom\s*|\bimport\s*\(\s*|\brequire\s*\(\s*|\bimport\s+)(["'])([^"'\n]*)$/;

export function specifierPrefixAt(lineUntilCursor: string): SpecifierPrefix | undefined {
  const m = SPECIFIER_RE.exec(lineUntilCursor);
  if (!m) return undefined;
  const typed = m[2];
  const start = lineUntilCursor.length - typed.length;
  const lastSlash = typed.lastIndexOf("/");
  return { typed, segmentOffset: lastSlash === -1 ? start : start + lastSlash + 1 };
}

export interface SpecifierCompletion {
  // What the segment being typed becomes. A folder keeps its trailing "/" so the next
  // segment can be completed straight after it.
  insertText: string;
  kind: "folder" | "module";
  // The full specifier the completion builds up to, shown as the item's detail.
  specifier: string;
}

export function workspacePathForUri(uri: string): string | undefined {
  const prefix = `${WS_PREFIX}/`;
  return uri.startsWith(prefix) ? uri.slice(prefix.length) : undefined;
}

// specifierCompletions answers what can follow the text typed so far. An empty specifier offers
// the two sigils themselves; `@/…` walks the workspace root and `#/…` the active collection,
// one path segment at a time — folders first, then the modules directly inside the segment.
export function specifierCompletions(
  typed: string,
  modules: readonly { path: string }[],
  collectionId: string | null | undefined,
  currentPath: string | undefined
): SpecifierCompletion[] {
  if (typed === "" || typed === "@" || typed === "#") {
    const sigils: SpecifierCompletion[] = [
      { insertText: "#/", kind: "folder", specifier: "#/" },
      { insertText: "@/", kind: "folder", specifier: "@/" },
    ];
    return sigils.filter((c) => typed === "" || c.insertText.startsWith(typed));
  }

  const sigil = typed.slice(0, 2);
  if (sigil !== "@/" && sigil !== "#/") return [];
  const root = sigil === "#/" ? collectionPathPrefix(collectionId) : "";
  const rest = typed.slice(2);
  // What a completion replaces is the segment being typed, not the whole specifier, so every
  // insertText below is measured from the last "/" — matching segmentOffset in SpecifierPrefix.
  const dir = rest.slice(0, rest.lastIndexOf("/") + 1);

  const folders = new Set<string>();
  const out: SpecifierCompletion[] = [];
  for (const m of modules) {
    if (m.path === currentPath) continue;
    if (!m.path.startsWith(root)) continue;
    const rel = stripModuleExtension(m.path.slice(root.length));
    if (!rel.startsWith(rest)) continue;
    const segment = rel.slice(dir.length);
    const slash = segment.indexOf("/");
    if (slash === -1) {
      out.push({ insertText: segment, kind: "module", specifier: `${sigil}${rel}` });
      continue;
    }
    const folder = segment.slice(0, slash);
    if (folders.has(folder)) continue;
    folders.add(folder);
    out.push({
      insertText: `${folder}/`,
      kind: "folder",
      specifier: `${sigil}${dir}${folder}/`,
    });
  }
  // Folders first, then modules; each group alphabetical — the order VS Code's own path
  // completion presents.
  return out.sort((a, b) => {
    if (a.kind !== b.kind) return a.kind === "folder" ? -1 : 1;
    return a.insertText.localeCompare(b.insertText);
  });
}
