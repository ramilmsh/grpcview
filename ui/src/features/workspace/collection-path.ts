// Turns whatever a user typed into the directory field into the workspace-relative
// path the CreateCollection RPC expects: "." for the workspace root, no leading
// "./", no trailing slash.
export function normalizeCollectionPath(input: string): string {
  const trimmed = input.trim().replace(/\\/g, "/");
  if (!trimmed || trimmed === ".") return ".";
  let path = trimmed;
  while (path.startsWith("./")) path = path.slice(2);
  path = path.replace(/\/+$/, "");
  return path || ".";
}

// The display name CreateCollection defaults to when `name` is left blank: the
// directory's own base name. Used only to make that default visible as a placeholder.
export function collectionBaseName(input: string): string {
  const path = normalizeCollectionPath(input);
  if (path === ".") return "workspace root";
  const segments = path.split("/").filter(Boolean);
  return segments[segments.length - 1] ?? path;
}
