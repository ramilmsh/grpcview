import { useState } from "react";
import clsx from "clsx";
import { MagnifyingGlass, Plus, Shield } from "@/components/ui/icons";
import { IconButton } from "@/components/ui/Button";
import type { Script } from "@grpcview/v1/workspace_pb";
import { kindMeta, SIDEBAR_ORDER } from "./script-kinds";

export function ScriptSidebar({
  scripts,
  selectedName,
  onSelect,
  onNew,
}: {
  scripts: Script[];
  selectedName: string | null;
  onSelect: (name: string) => void;
  onNew: () => void;
}) {
  const [filter, setFilter] = useState("");
  const q = filter.trim().toLowerCase();
  const visible = q
    ? scripts.filter((s) => s.name.toLowerCase().includes(q))
    : scripts;

  const sections = SIDEBAR_ORDER.map((kind) => ({
    kind,
    meta: kindMeta(kind),
    rows: visible.filter((s) => s.kind === kind),
  })).filter((sec) => sec.rows.length > 0);

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
            No scripts yet. Use + to create a generator, middleware, or scenario.
          </div>
        ) : sections.length === 0 ? (
          <div
            className="text-muted"
            style={{ fontSize: 12, padding: "16px 6px", lineHeight: 1.6 }}
          >
            No scripts match “{filter.trim()}”.
          </div>
        ) : (
          sections.map(({ kind, meta, rows }, i) => {
            const SectionIcon = meta.Icon;
            return (
              <div key={kind}>
                <div
                  className="flex items-center gap-[6px]"
                  style={{ padding: `${i === 0 ? 2 : 14}px 6px 6px` }}
                >
                  <SectionIcon size={14} style={{ color: meta.color }} />
                  <span
                    style={{
                      fontSize: 10,
                      letterSpacing: ".1em",
                      textTransform: "uppercase",
                      color: "var(--color-neutral-500)",
                    }}
                  >
                    {meta.section}
                  </span>
                </div>
                {rows.map((s) => (
                  <div
                    key={s.name}
                    className={clsx("scriptrow", s.name === selectedName && "on")}
                    onClick={() => onSelect(s.name)}
                  >
                    <SectionIcon size={16} style={{ color: meta.color, flex: "none" }} />
                    <span
                      style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}
                    >
                      {s.name}
                    </span>
                  </div>
                ))}
              </div>
            );
          })
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
