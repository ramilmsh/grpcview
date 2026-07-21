import { useRef, useState, type ChangeEvent, type KeyboardEvent } from "react";
import { Dialog } from "@/components/ui/Dialog";
import { Input, Field } from "@/components/ui/Input";
import { Button } from "@/components/ui/Button";

// AddSourceModal adds a definition source: either a server-reflection target
// (host:port + optional TLS) or an uploaded protobuf FileDescriptorSet. Both are
// wired to the backend (the reflection and descriptor-set branches of
// AddDescriptorSource). A descriptor set is what `protoc --include_imports
// --descriptor_set_out` emits; uploading one reads the file to bytes and sends
// them as the descriptorSet oneof case.
export function AddSourceModal({
  open,
  onClose,
  onAddReflection,
  onAddDescriptorSet,
  pending,
}: {
  open: boolean;
  onClose: () => void;
  onAddReflection: (host: string, port: number, tls: boolean) => void;
  onAddDescriptorSet: (bytes: Uint8Array) => void;
  pending?: boolean;
}) {
  const [host, setHost] = useState("127.0.0.1");
  const [port, setPort] = useState("10000");
  const [tls, setTls] = useState(false);
  const fileRef = useRef<HTMLInputElement>(null);

  const submit = () => {
    const p = parseInt(port, 10);
    if (host.trim() && Number.isFinite(p)) onAddReflection(host.trim(), p, tls);
  };

  const onEnter = (e: KeyboardEvent) => {
    if (e.key === "Enter") submit();
  };

  const onFile = async (e: ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    e.target.value = ""; // reset so re-selecting the same file fires onChange
    if (!file) return;
    onAddDescriptorSet(new Uint8Array(await file.arrayBuffer()));
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

      {/* descriptor-set upload — an alternative to reflection */}
      <div style={{ borderTop: "1px solid var(--line)" }} />
      <Field label="Descriptor set">
        <input
          ref={fileRef}
          type="file"
          onChange={onFile}
          style={{ display: "none" }}
        />
        <div className="flex items-center gap-[8px]">
          <Button onClick={() => fileRef.current?.click()} disabled={pending}>
            Upload descriptor set…
          </Button>
          <span className="text-muted" style={{ fontSize: 12 }}>
            protoc --include_imports --descriptor_set_out
          </span>
        </div>
      </Field>

      <div className="dialog-actions">
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="primary" onClick={submit} disabled={pending || !host.trim()}>
          {pending ? "Adding…" : "Add source"}
        </Button>
      </div>
    </Dialog>
  );
}
