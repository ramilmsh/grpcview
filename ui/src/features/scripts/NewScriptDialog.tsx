import { useEffect, useState } from "react";
import clsx from "clsx";
import { Button } from "@/components/ui/Button";
import { Dialog } from "@/components/ui/Dialog";
import { Input, Field } from "@/components/ui/Input";
import { ScriptKind } from "@grpcview/v1/workspace_pb";
import { kindMeta, NEW_KIND_ORDER } from "./script-kinds";

export function NewScriptDialog({
  open,
  onClose,
  onCreate,
  pending,
  error,
  existingNames,
}: {
  open: boolean;
  onClose: () => void;
  onCreate: (name: string, kind: ScriptKind) => void;
  pending: boolean;
  error: Error | null;
  existingNames: string[];
}) {
  const [name, setName] = useState("");
  const [kind, setKind] = useState<ScriptKind>(ScriptKind.GENERATOR);

  useEffect(() => {
    if (open) {
      setName("");
      setKind(ScriptKind.GENERATOR);
    }
  }, [open]);

  const trimmed = name.trim();
  const collision = existingNames.includes(trimmed);
  const canCreate = !!trimmed && !collision && !pending;
  const submit = () => {
    if (canCreate) onCreate(trimmed, kind);
  };

  return (
    <Dialog open={open} onClose={onClose} title="New script" width={420}>
      <div className="kindseg">
        {NEW_KIND_ORDER.map((k) => {
          const m = kindMeta(k);
          const OptIcon = m.Icon;
          return (
            <button
              key={k}
              type="button"
              className={clsx("kindopt", k === kind && "on")}
              onClick={() => setKind(k)}
            >
              <OptIcon size={14} />
              {m.label}
            </button>
          );
        })}
      </div>
      <Field label="Name">
        <Input
          autoFocus
          placeholder="e.g. uuid, sign-request"
          value={name}
          onChange={(e) => setName(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") submit();
          }}
        />
      </Field>
      {collision && (
        <p style={{ margin: 0, fontSize: 12, color: "var(--err-fg)" }}>
          A script named “{trimmed}” already exists.
        </p>
      )}
      {error && !collision && (
        <p style={{ margin: 0, fontSize: 12, color: "var(--err-fg)" }}>{error.message}</p>
      )}
      <div className="dialog-actions">
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="primary" onClick={submit} disabled={!canCreate}>
          {pending ? "Creating…" : "Create"}
        </Button>
      </div>
    </Dialog>
  );
}
