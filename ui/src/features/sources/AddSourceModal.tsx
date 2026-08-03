import { useRef, useState, type ChangeEvent, type KeyboardEvent } from "react";
import { Dialog } from "@/components/ui/Dialog";
import { Input, Field } from "@/components/ui/Input";
import { Button } from "@/components/ui/Button";

// AddSourceModal adds a reflection target or an uploaded FileDescriptorSet.
export function AddSourceModal({
  open,
  onClose,
  onAddReflection,
  onAddDescriptorSet,
  pending,
}: {
  open: boolean;
  onClose: () => void;
  onAddReflection: (address: string, tls: boolean) => void;
  onAddDescriptorSet: (bytes: Uint8Array, fileName: string) => void;
  pending?: boolean;
}) {
  // Must stay empty: a pre-filled default becomes the added source's real address.
  const [address, setAddress] = useState("");
  const [tls, setTls] = useState(false);
  const fileRef = useRef<HTMLInputElement>(null);

  const submit = () => {
    if (address.trim()) onAddReflection(address.trim(), tls);
  };

  const onEnter = (e: KeyboardEvent) => {
    if (e.key === "Enter") submit();
  };

  const onFile = async (e: ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    e.target.value = ""; // reset so re-selecting the same file fires onChange
    if (!file) return;
    onAddDescriptorSet(new Uint8Array(await file.arrayBuffer()), file.name);
  };

  return (
    <Dialog open={open} onClose={onClose} title="Add definition source" width={460}>
      <Field label="Server reflection">
        <Input
          value={address}
          onChange={(e) => setAddress(e.target.value)}
          placeholder="host:port (e.g. localhost:50051)"
          autoFocus
          onKeyDown={onEnter}
        />
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
        <Button variant="primary" onClick={submit} disabled={pending || !address.trim()}>
          {pending ? "Adding…" : "Add source"}
        </Button>
      </div>
    </Dialog>
  );
}
