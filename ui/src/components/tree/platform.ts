// A minimal local shape instead of a full @types/node dep: this is a
// browser-first bundle (see //ui/src:app), and per-package ts_project
// compiles don't reliably pick up @types/node's ambient globals via a
// deps-only reference (no typeRoots wiring for it here).
declare const process: { versions?: { node?: string }; platform?: string } | undefined;

function detectIsMac(): boolean {
  // Node 21+ ships its own global `navigator` with a host-derived userAgent and
  // platform, so the Node/Electron check has to come first.
  if (typeof process !== "undefined" && typeof process.versions?.node === "string") {
    return process.platform === "darwin";
  }
  if (typeof navigator === "undefined") return false;
  const userAgent = navigator.userAgent ?? "";
  const platform = navigator.platform ?? "";
  // Either signal alone is enough: some browsers freeze `navigator.platform`.
  return userAgent.includes("Macintosh") || platform.includes("Mac");
}

export const IS_MAC = detectIsMac();
