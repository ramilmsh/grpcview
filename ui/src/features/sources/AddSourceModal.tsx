import { useRef, useState, type ChangeEvent, type KeyboardEvent } from "react";
import { Dialog } from "@/components/ui/Dialog";
import { Input, Field } from "@/components/ui/Input";
import { Button } from "@/components/ui/Button";

// AddSourceModal adds a definition source: either a server-reflection target
// (host:port + optional TLS) or an uploaded protobuf FileDescriptorSet. Both are
// wired to the backend (the reflection and descriptor-set branches of
// AddDescriptorSource). A descriptor set is what `protoc --include_imports
// --descriptor_set_out` or `buf build -o` emits; uploading one reads the file to
// bytes and sends them with the file's name, which becomes the source's identity —
// so re-uploading a rebuilt file refreshes that source instead of adding another.
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
  // The address starts empty (a placeholder hints the host:port form) rather than
  // pre-filled: pre-seeding grpcview's own listen port baked that misleading value
  // into every added source, so a request then defaulted its target to grpcview
  // itself instead of the reflected service.
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
        <Button variant="primary" onClick={submit} disabled={pending || !address.trim()}>
          {pending ? "Adding…" : "Add source"}
        </Button>
      </div>
    </Dialog>
  );
}
