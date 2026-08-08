import { useState } from "react";
import {
  ArrowsSplit,
  CaretDown,
  CaretUp,
  Plus,
  Trash,
  Warning,
} from "@/components/ui/icons";
import { Button, IconButton } from "@/components/ui/Button";
import { Dialog } from "@/components/ui/Dialog";
import { Input } from "@/components/ui/Input";
import { Tag } from "@/components/ui/Tag";
import { useActiveWorkspace } from "@/lib/workspace-query";
import type { Script } from "@grpcview/v1/workspace_pb";

// `Request.middleware` holds specifiers (script-imports/decisions.md §6), not display names:
// `~/scripts/x.ts` resolves against this request's own collection, `@/lib/mw/y.ts` against the
// workspace root. Only `~/` specifiers are checkable here — `workspace.scripts` is scoped to
// the active collection; a `@/` one may point anywhere else in the workspace, which there is no
// listing for yet (that RPC is the next frontend phase), so it is trusted rather than flagged.
const SPECIFIER_RE = /^[@~]\/.+\.ts$/;

const ownSpecifier = (script: Script): string => `~/${script.path}`;

function describe(script: Script): string {
  const firstLine = script.source
    .split("\n")
    .map((l) => l.trim())
    .find((l) => l.length > 0);
  if (firstLine?.startsWith("//")) {
    const text = firstLine.replace(/^\/\/+\s?/, "").trim();
    if (text) return text.length > 72 ? `${text.slice(0, 72)}…` : text;
  }
  return "middleware";
}

export function MiddlewareTab({
  middleware,
  onChange,
}: {
  middleware: string[];
  onChange: (next: string[]) => void;
}) {
  const { workspace } = useActiveWorkspace();
  const [pickerOpen, setPickerOpen] = useState(false);

  const scripts = workspace?.scripts ?? [];
  const byOwnSpecifier = new Map(scripts.map((s) => [ownSpecifier(s), s]));
  const attachedSet = new Set(middleware);
  const candidates = scripts.filter((s) => !attachedSet.has(ownSpecifier(s)));

  const move = (i: number, dir: -1 | 1) => {
    const j = i + dir;
    if (j < 0 || j >= middleware.length) return;
    const next = middleware.slice();
    [next[i], next[j]] = [next[j], next[i]];
    onChange(next);
  };
  const detach = (i: number) => onChange(middleware.filter((_, k) => k !== i));
  const attach = (specifier: string) => {
    if (attachedSet.has(specifier)) return;
    onChange([...middleware, specifier]);
    setPickerOpen(false);
  };

  return (
    <div style={{ flex: 1, overflow: "auto", padding: "14px" }}>
      <p className="text-muted" style={{ fontSize: 12, marginBottom: 12 }}>
        Middleware runs in order before invoke — each can rewrite body &amp; metadata.
        Attached by specifier: <span className="font-mono">~/…</span> for a script in this
        collection, <span className="font-mono">@/…</span> for one anywhere in the workspace.
      </p>

      <div className="flex flex-col" style={{ gap: 8 }}>
        {middleware.map((specifier, i) => (
          <MiddlewareRow
            // eslint-disable-next-line react/no-array-index-key
            key={`${specifier}:${i}`}
            order={i + 1}
            specifier={specifier}
            script={byOwnSpecifier.get(specifier)}
            first={i === 0}
            last={i === middleware.length - 1}
            onUp={() => move(i, -1)}
            onDown={() => move(i, 1)}
            onDetach={() => detach(i)}
          />
        ))}

        <Button
          variant="secondary"
          onClick={() => setPickerOpen(true)}
          style={{ justifyContent: "center", fontSize: 13, gap: 6, borderStyle: "dashed" }}
        >
          <Plus size={14} /> Attach middleware
        </Button>

        {scripts.length === 0 && (
          <p className="text-muted" style={{ fontSize: 12, margin: "2px 2px 0", lineHeight: 1.6 }}>
            No scripts in this collection yet. Author one in the{" "}
            <span style={{ color: "var(--color-accent-300)" }}>Scripts</span> view, then attach
            it here — or type an <span className="font-mono">@/…</span> specifier for one
            elsewhere in the workspace.
          </p>
        )}
      </div>

      <AttachMiddlewareDialog
        open={pickerOpen}
        onClose={() => setPickerOpen(false)}
        candidates={candidates}
        onPick={(s) => attach(ownSpecifier(s))}
        onPickCustom={attach}
      />
    </div>
  );
}

function MiddlewareRow({
  order,
  specifier,
  script,
  first,
  last,
  onUp,
  onDown,
  onDetach,
}: {
  order: number;
  specifier: string;
  script?: Script;
  first: boolean;
  last: boolean;
  onUp: () => void;
  onDown: () => void;
  onDetach: () => void;
}) {
  // `@/` specifiers are never resolvable here (no cross-collection listing yet), so only a
  // `~/` one absent from this collection's own script list counts as missing.
  const external = specifier.startsWith("@/");
  const missing = !external && !script;
  return (
    <div
      className="flex items-center"
      style={{
        gap: 10,
        background: "var(--panel-2)",
        border: `1px solid ${missing ? "var(--err-border)" : "var(--line)"}`,
        borderRadius: 8,
        padding: "9px 11px",
      }}
    >
      <Tag variant="neutral" className="font-mono">
        {order}
      </Tag>
      <div style={{ flex: 1, minWidth: 0 }}>
        <div
          className="flex items-center gap-[6px] font-mono"
          style={{
            fontSize: 13,
            color: missing ? "var(--err-fg)" : "var(--color-text)",
          }}
        >
          {missing && <Warning weight="fill" size={13} style={{ flex: "none" }} />}
          <span
            style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}
          >
            {specifier}
          </span>
        </div>
        <div
          className="font-mono"
          style={{
            fontSize: 11,
            color: missing ? "var(--err-fg)" : "var(--color-neutral-500)",
            overflow: "hidden",
            textOverflow: "ellipsis",
            whiteSpace: "nowrap",
          }}
        >
          {missing ? (
            "missing — no script at this path in the collection"
          ) : external ? (
            "elsewhere in the workspace"
          ) : script ? (
            describe(script)
          ) : null}
        </div>
      </div>
      <div className="flex items-center" style={{ gap: 1, flex: "none" }}>
        <IconButton
          title="Move up"
          disabled={first}
          onClick={onUp}
          style={{ width: 24, height: 24, fontSize: 14, opacity: first ? 0.35 : 1 }}
        >
          <CaretUp size={14} />
        </IconButton>
        <IconButton
          title="Move down"
          disabled={last}
          onClick={onDown}
          style={{ width: 24, height: 24, fontSize: 14, opacity: last ? 0.35 : 1 }}
        >
          <CaretDown size={14} />
        </IconButton>
        <IconButton
          title="Detach middleware"
          onClick={onDetach}
          style={{ width: 24, height: 24, fontSize: 14 }}
        >
          <Trash size={14} />
        </IconButton>
      </div>
    </div>
  );
}

function AttachMiddlewareDialog({
  open,
  onClose,
  candidates,
  onPick,
  onPickCustom,
}: {
  open: boolean;
  onClose: () => void;
  candidates: Script[];
  onPick: (script: Script) => void;
  onPickCustom: (specifier: string) => void;
}) {
  const [custom, setCustom] = useState("");
  const trimmed = custom.trim();
  const customError = trimmed && !SPECIFIER_RE.test(trimmed);
  const submitCustom = () => {
    if (trimmed && !customError) {
      onPickCustom(trimmed);
      setCustom("");
    }
  };

  return (
    <Dialog
      open={open}
      onClose={() => {
        setCustom("");
        onClose();
      }}
      title="Attach middleware"
      width={420}
    >
      {candidates.length === 0 ? (
        <p className="text-muted" style={{ margin: 0, fontSize: 13, lineHeight: 1.6 }}>
          Every script in this collection is already attached.
        </p>
      ) : (
        <div className="flex flex-col" style={{ gap: 4, maxHeight: 280, overflow: "auto" }}>
          {candidates.map((s) => (
            <button
              key={s.path}
              type="button"
              className="scriptrow"
              style={{
                textAlign: "left",
                border: "none",
                background: "transparent",
                width: "100%",
                fontFamily: "inherit",
              }}
              onClick={() => onPick(s)}
            >
              <ArrowsSplit size={16} style={{ color: "var(--color-accent)", flex: "none" }} />
              <div style={{ minWidth: 0 }}>
                <div className="font-mono" style={{ fontSize: 13, color: "var(--color-text)" }}>
                  {ownSpecifier(s)}
                </div>
                <div
                  className="font-mono"
                  style={{
                    fontSize: 11,
                    color: "var(--color-neutral-500)",
                    overflow: "hidden",
                    textOverflow: "ellipsis",
                    whiteSpace: "nowrap",
                  }}
                >
                  {describe(s)}
                </div>
              </div>
            </button>
          ))}
        </div>
      )}

      <div style={{ marginTop: 14, paddingTop: 14, borderTop: "1px solid var(--line)" }}>
        <p className="text-muted" style={{ margin: "0 0 8px", fontSize: 12 }}>
          Or attach by specifier — a <span className="font-mono">@/…</span> path reaches a
          script anywhere else in the workspace.
        </p>
        <div className="flex items-center gap-[8px]">
          <Input
            className="font-mono"
            placeholder="@/lib/mw/auth.ts"
            value={custom}
            onChange={(e) => setCustom(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") submitCustom();
            }}
          />
          <Button
            variant="secondary"
            onClick={submitCustom}
            disabled={!trimmed || !!customError}
          >
            Attach
          </Button>
        </div>
        {customError && (
          <p style={{ margin: "6px 0 0", fontSize: 12, color: "var(--err-fg)" }}>
            Must start with <span className="font-mono">@/</span> or{" "}
            <span className="font-mono">~/</span> and end in{" "}
            <span className="font-mono">.ts</span>.
          </p>
        )}
      </div>
    </Dialog>
  );
}
