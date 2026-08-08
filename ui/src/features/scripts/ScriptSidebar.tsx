import { useMemo, useState } from "react";
import clsx from "clsx";
import { Code, Folder, MagnifyingGlass, Plus, Shield } from "@/components/ui/icons";
import { IconButton } from "@/components/ui/Button";
import type { Script } from "@grpcview/v1/workspace_pb";

// path is collection-relative and always under scripts/ (decisions.md §5): "scripts/uuid.ts"
// or "scripts/mw/trace-headers.ts". Strips the implied "scripts/" prefix and splits what's
// left into the group (everything before the last segment) and the label (the filename).
function splitScriptPath(path: string): { group: string; label: string } {
  const segments = path.split("/");
  if (segments[0] === "scripts") segments.shift();
  const label = segments.pop() ?? path;
  return { group: segments.join("/"), label };
}

interface ScriptGroup {
  group: string;
  rows: Script[];
}

// One pass over the path-sorted list: a new group starts whenever the directory changes,
// so groups come out in path order rather than a separate alphabetical pass over directories.
function groupScripts(scripts: Script[]): ScriptGroup[] {
  const sorted = [...scripts].sort((a, b) => a.path.localeCompare(b.path));
  const groups: ScriptGroup[] = [];
  for (const s of sorted) {
    const { group } = splitScriptPath(s.path);
    const last = groups[groups.length - 1];
    if (last && last.group === group) last.rows.push(s);
    else groups.push({ group, rows: [s] });
  }
  return groups;
}

export function ScriptSidebar({
  scripts,
  selectedPath,
  onSelect,
  onNew,
}: {
  scripts: Script[];
  selectedPath: string | null;
  onSelect: (path: string) => void;
  onNew: () => void;
}) {
  const [filter, setFilter] = useState("");
  const q = filter.trim().toLowerCase();
  const visible = q ? scripts.filter((s) => s.path.toLowerCase().includes(q)) : scripts;

  const groups = useMemo(() => groupScripts(visible), [visible]);

  return (
    <div
      className="bg-panel flex flex-col"
      style={{ width: 280, flex: "none", borderRight: "1px solid var(--line)", minHeight: 0 }}
    >
      <div
        className="flex items-center gap-[8px]"
        style={{ height: 40, flex: "none", padding: "0 12px", borderBottom: "1px solid var(--line)" }}
      >
        <MagnifyingGlass size={14} style={{ color: "var(--color-neutral-500)" }} />
        <input
          className="bare"
          style={{ fontSize: 13 }}
          placeholder="Filter scripts…"
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
        />
        <IconButton title="New script" onClick={onNew}>
          <Plus />
        </IconButton>
      </div>

      <div style={{ flex: 1, overflow: "auto", padding: "10px 8px" }}>
        {scripts.length === 0 ? (
          <div
            className="text-muted"
            style={{ fontSize: 12, padding: "16px 6px", lineHeight: 1.6 }}
          >
            No scripts yet. Use + to create one.
          </div>
        ) : groups.length === 0 ? (
          <div
            className="text-muted"
            style={{ fontSize: 12, padding: "16px 6px", lineHeight: 1.6 }}
          >
            No scripts match “{filter.trim()}”.
          </div>
        ) : (
          groups.map(({ group, rows }, i) => (
            <div key={group || "\0root"}>
              {group && (
                <div
                  className="flex items-center gap-[6px]"
                  style={{ padding: `${i === 0 ? 2 : 14}px 6px 6px` }}
                >
                  <Folder size={14} style={{ color: "var(--color-neutral-500)" }} />
                  <span
                    className="font-mono"
                    style={{
                      fontSize: 10,
                      letterSpacing: ".04em",
                      color: "var(--color-neutral-500)",
                      overflow: "hidden",
                      textOverflow: "ellipsis",
                      whiteSpace: "nowrap",
                    }}
                  >
                    {group}
                  </span>
                </div>
              )}
              {rows.map((s) => (
                <div
                  key={s.path}
                  className={clsx("scriptrow", s.path === selectedPath && "on")}
                  onClick={() => onSelect(s.path)}
                  title={s.path}
                >
                  <Code size={16} style={{ color: "var(--color-accent-300)", flex: "none" }} />
                  <span
                    className="font-mono"
                    style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}
                  >
                    {splitScriptPath(s.path).label}
                  </span>
                </div>
              ))}
            </div>
          ))
        )}
      </div>

      <div
        className="flex items-center gap-[7px] font-mono"
        style={{
          flex: "none",
          padding: "9px 12px",
          borderTop: "1px solid var(--line)",
          fontSize: 11,
          color: "var(--color-neutral-500)",
        }}
        title="QuickJS compiled to WASM, run in-process by wazero — hard memory + wall-clock bounds; filesystem and process denied, network open to every script"
      >
        <Shield size={14} />
        QuickJS·WASM<span style={{ color: "var(--color-neutral-700)" }}>·</span>bounded
      </div>
    </div>
  );
}
