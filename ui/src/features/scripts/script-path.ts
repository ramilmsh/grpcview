// Mirrors resolveScriptPath in service/store/scripts.go: relative, first segment "scripts",
// no ".." segment anywhere, ends in ".ts". Client-side so the New/rename dialogs can reject
// before round-tripping to the backend rule they are re-stating.
export const DEFAULT_SCRIPT_PATH = "scripts/";

const RULE = 'must be a relative path under scripts/, with no ".." segment, ending in .ts';

export function validateScriptPath(path: string): string | null {
  if (!path || path.startsWith("/") || !path.endsWith(".ts")) return RULE;
  const segments = path.split("/");
  if (segments.some((seg) => seg === "..")) return RULE;
  const clean = segments.filter((seg) => seg !== "." && seg !== "");
  if (clean[0] !== "scripts") return RULE;
  return null;
}
