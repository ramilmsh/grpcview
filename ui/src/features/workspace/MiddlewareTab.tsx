import { useState } from "react";
import clsx from "clsx";
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
import { Tag } from "@/components/ui/Tag";
import { useWorkspace } from "@/lib/workspace-query";
import { ScriptKind, type Script } from "@grpcview/v1/workspace_pb";

// MiddlewareTab manages a request's ordered attached middleware (plan §S3). The
// backend runs each MIDDLEWARE-kind script — by display name, in this order —
// before every invoke, so it can rewrite body / metadata / target. The attached
// list is SERVER state (rides the Get snapshot on request.middleware, like the
// request's service/method); every attach / detach / reorder persists the full
// ordered list via UpdateRequest (updateMiddleware:true) and the re-seeded Get
// cache flows the fresh list back down — there is no parallel copy in ui-store.
//
// Candidates are the collection's middleware scripts (workspace.scripts filtered
// to ScriptKind.MIDDLEWARE). A rename changes a script's name-key, so an attached
// name that no longer resolves to a middleware script renders as a broken
// "missing" row that can still be detached (auto-rewrite on rename is out of
// scope — the backend errors "no middleware script by that name" at invoke).
export function MiddlewareTab({
  middleware,
  onChange,
}: {
  middleware: string[];
  onChange: (next: string[]) => void;
}) {
  const { workspace } = useWorkspace();
  const [pickerOpen, setPickerOpen] = useState(false);

  const middlewareScripts = (workspace?.scripts ?? []).filter(
    (s) => s.kind === ScriptKind.MIDDLEWARE
  );
  const byName = new Map(middlewareScripts.map((s) => [s.name, s]));
  const attachedSet = new Set(middleware);
  const candidates = middlewareScripts.filter((s) => !attachedSet.has(s.name));

  // Reorder by swapping neighbours; detach drops one; attach appends. Each writes
  // the whole ordered list (never a parallel copy) so the server stays canonical.
  const move = (i: number, dir: -1 | 1) => {
    const j = i + dir;
    if (j < 0 || j >= middleware.length) return;
    const next = middleware.slice();
    [next[i], next[j]] = [next[j], next[i]];
    onChange(next);
  };
  const detach = (i: number) => onChange(middleware.filter((_, k) => k !== i));
  const attach = (name: string) => {
    if (attachedSet.has(name)) return;
    onChange([...middleware, name]);
    setPickerOpen(false);
  };

  return (
    <div style={{ flex: 1, overflow: "auto", padding: "14px" }}>
      <p className="text-muted" style={{ fontSize: 12, marginBottom: 12 }}>
        Middleware runs in order before invoke — each can rewrite body &amp; metadata.
      </p>

      <div className="flex flex-col" style={{ gap: 8 }}>
        {middleware.map((name, i) => (
          <MiddlewareRow
            // eslint-disable-next-line react/no-array-index-key
            key={`${name}:${i}`}
            order={i + 1}
            name={name}
            script={byName.get(name)}
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
          disabled={middlewareScripts.length === 0}
          style={{ justifyContent: "center", fontSize: 13, gap: 6, borderStyle: "dashed" }}
        >
          <Plus size={14} /> Attach middleware
        </Button>

        {middlewareScripts.length === 0 && (
          <p className="text-muted" style={{ fontSize: 12, margin: "2px 2px 0", lineHeight: 1.6 }}>
            No middleware scripts in this collection. Author one in the{" "}
            <span style={{ color: "var(--color-accent-300)" }}>Scripts</span> view (a{" "}
            <span className="font-mono">MIDDLEWARE</span>-kind script), then attach it here.
          </p>
        )}
      </div>

      <AttachMiddlewareDialog
        open={pickerOpen}
        onClose={() => setPickerOpen(false)}
        candidates={candidates}
        onPick={attach}
      />
    </div>
  );
}

// describe pulls a one-line subtitle from the script's leading `//` comment (the
// starter skeletons open with one), falling back to a plain kind label. Cheap
// orientation without a dedicated description field on the Script wire type.
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

function MiddlewareRow({
  order,
  name,
  script,
  first,
  last,
  onUp,
  onDown,
  onDetach,
}: {
  order: number;
  name: string;
  script?: Script;
  first: boolean;
  last: boolean;
  onUp: () => void;
  onDown: () => void;
  onDetach: () => void;
}) {
  const missing = !script;
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
          className="flex items-center gap-[6px]"
          style={{
            fontSize: 13,
            color: missing ? "var(--err-fg)" : "var(--color-text)",
          }}
        >
          {missing && <Warning weight="fill" size={13} style={{ flex: "none" }} />}
          <span
            className={clsx(missing && "font-mono")}
            style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}
          >
            {name}
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
          {missing ? "missing — no middleware script by this name" : describe(script)}
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
}: {
  open: boolean;
  onClose: () => void;
  candidates: Script[];
  onPick: (name: string) => void;
}) {
  return (
    <Dialog open={open} onClose={onClose} title="Attach middleware" width={420}>
      {candidates.length === 0 ? (
        <p className="text-muted" style={{ margin: 0, fontSize: 13, lineHeight: 1.6 }}>
          All middleware scripts in this collection are already attached.
        </p>
      ) : (
        <div className="flex flex-col" style={{ gap: 4, maxHeight: 320, overflow: "auto" }}>
          {candidates.map((s) => (
            <button
              key={s.name}
              type="button"
              className="scriptrow"
              style={{
                textAlign: "left",
                border: "none",
                background: "transparent",
                width: "100%",
                fontFamily: "inherit",
              }}
              onClick={() => onPick(s.name)}
            >
              <ArrowsSplit size={16} style={{ color: "var(--color-accent)", flex: "none" }} />
              <div style={{ minWidth: 0 }}>
                <div style={{ fontSize: 13, color: "var(--color-text)" }}>{s.name}</div>
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
    </Dialog>
  );
}
