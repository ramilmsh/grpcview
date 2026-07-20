import { useState, type KeyboardEvent } from "react";
import { Dialog } from "@/components/ui/Dialog";
import { Input, Field } from "@/components/ui/Input";
import { Button } from "@/components/ui/Button";

// AddSourceModal adds a server-reflection source (host:port + optional TLS) —
// the only source type the backend implements. Descriptor-set upload returns
// Unimplemented server-side, so it is shown disabled (plan §1.6/§11).
export function AddSourceModal({
  open,
  onClose,
  onAddReflection,
  pending,
}: {
  open: boolean;
  onClose: () => void;
  onAddReflection: (host: string, port: number, tls: boolean) => void;
  pending?: boolean;
}) {
  const [host, setHost] = useState("127.0.0.1");
  const [port, setPort] = useState("10000");
  const [tls, setTls] = useState(false);

  const submit = () => {
    const p = parseInt(port, 10);
    if (host.trim() && Number.isFinite(p)) onAddReflection(host.trim(), p, tls);
  };

  const onEnter = (e: KeyboardEvent) => {
    if (e.key === "Enter") submit();
  };

  return (
    <Dialog open={open} onClose={onClose} title="Add definition source" width={460}>
      <Field label="Server reflection">
        <div className="flex gap-[8px]">
          <Input
            value={host}
            onChange={(e) => setHost(e.target.value)}
            placeholder="Host"
            style={{ flex: 1 }}
            autoFocus
            onKeyDown={onEnter}
          />
          <Input
            value={port}
            onChange={(e) => setPort(e.target.value)}
            placeholder="Port"
            type="number"
            style={{ width: 100 }}
            onKeyDown={onEnter}
          />
        </div>
      </Field>

      <label
        className="flex items-center gap-[8px]"
        style={{ fontSize: 14, cursor: "pointer" }}
      >
        <input
          type="checkbox"
          checked={tls}
          onChange={(e) => setTls(e.target.checked)}
          style={{ accentColor: "var(--color-accent)" }}
        />
        Use TLS
      </label>

      {/* descriptor-set upload — not implemented server-side */}
      <div
        style={{ opacity: 0.5 }}
        title="Descriptor-set upload is not implemented server-side yet (plan §11)"
      >
        <Field label="Descriptor set">
          <Input disabled placeholder="Not available in Phase 1" />
        </Field>
      </div>

      <div className="dialog-actions">
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="primary" onClick={submit} disabled={pending || !host.trim()}>
          {pending ? "Adding…" : "Add source"}
        </Button>
      </div>
    </Dialog>
  );
}
