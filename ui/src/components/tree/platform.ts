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
