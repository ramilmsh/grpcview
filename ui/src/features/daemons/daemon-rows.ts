// Pure helpers for the Daemons view's row presentation. Split out from DaemonsView.tsx so
// they are testable without pulling in react-query/Dialog — the same reason
// features/workspace/delete-confirm.ts is its own module.
import type { ServerEntry } from "@grpcview/v1/service_pb";

// A registration file outliving its process is the ordinary case (AGENTS.md "The workspace
// daemon"), so "not running" is a normal state and not an error — the status dot just dims.
export type DaemonStatus = "running" | "stale";

export function daemonStatus(entry: ServerEntry): DaemonStatus {
  return entry.running ? "running" : "stale";
}

// The row's primary label: the workspace root's basename, falling back to the full root for
// "/" or a root with no separator. Trailing slashes are stripped first so ".../foo/" reads as
// "foo", not "".
export function daemonLabel(root: string): string {
  const trimmed = root.replace(/\/+$/, "");
  const idx = trimmed.lastIndexOf("/");
  const base = idx >= 0 ? trimmed.slice(idx + 1) : trimmed;
  return base || root;
}

// Stable sort by workspace root — the server already orders ListServersResponse this way, but
// a client-side re-sort keeps the view correct even if that ever changes, and gives the row
// `key` (workspaceRoot) a defined order to render in.
export function sortedDaemonRows(
  entries: readonly ServerEntry[],
): ServerEntry[] {
  return [...entries].sort((a, b) =>
    a.workspaceRoot.localeCompare(b.workspaceRoot),
  );
}
