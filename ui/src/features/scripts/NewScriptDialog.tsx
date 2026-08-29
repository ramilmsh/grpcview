import { useEffect, useState } from "react";
import { Button } from "@/components/ui/Button";
import { Dialog } from "@/components/ui/Dialog";
import { Input, Field } from "@/components/ui/Input";
import { DEFAULT_SCRIPT_PATH, validateScriptPath } from "./script-path";

export function NewScriptDialog({
  open,
  onClose,
  onCreate,
  pending,
  error,
  existingPaths,
}: {
  open: boolean;
  onClose: () => void;
  onCreate: (path: string) => void;
  pending: boolean;
  error: Error | null;
  existingPaths: string[];
}) {
  const [path, setPath] = useState(DEFAULT_SCRIPT_PATH);

  useEffect(() => {
    if (open) setPath(DEFAULT_SCRIPT_PATH);
  }, [open]);

  const trimmed = path.trim();
  const ruleError = trimmed ? validateScriptPath(trimmed) : null;
  const collision = !ruleError && existingPaths.includes(trimmed);
  const canCreate = !!trimmed && !ruleError && !collision && !pending;
  const submit = () => {
    if (canCreate) onCreate(trimmed);
  };

  return (
    <Dialog open={open} onClose={onClose} title="New script" width={420}>
      <Field label="Path">
        <Input
          autoFocus
          className="font-mono"
          placeholder="scripts/uuid.ts"
          value={path}
          onChange={(e) => setPath(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") submit();
          }}
        />
      </Field>
      {ruleError && (
        <p style={{ margin: 0, fontSize: 12, color: "var(--err-fg)" }}>
          {ruleError}
        </p>
      )}
      {collision && (
        <p style={{ margin: 0, fontSize: 12, color: "var(--err-fg)" }}>
          A script at “{trimmed}” already exists.
        </p>
      )}
      {error && !ruleError && !collision && (
        <p style={{ margin: 0, fontSize: 12, color: "var(--err-fg)" }}>
          {error.message}
        </p>
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
