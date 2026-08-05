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
  onAddReflection: (address: string, tls: boolean, commitDescriptors: boolean) => void;
  onAddDescriptorSet: (
    bytes: Uint8Array,
    fileName: string,
    commitDescriptors: boolean
  ) => void;
  pending?: boolean;
}) {
  // Must stay empty: a pre-filled default becomes the added source's real address.
  const [address, setAddress] = useState("");
  const [tls, setTls] = useState(false);
  // Off by default, matching the store: committing is a deliberate choice to put descriptors
  // in git history, and it applies to whichever of the two sources below is added.
  const [commit, setCommit] = useState(false);
  const fileRef = useRef<HTMLInputElement>(null);

  const submit = () => {
    if (address.trim()) onAddReflection(address.trim(), tls, commit);
  };

  const onEnter = (e: KeyboardEvent) => {
    if (e.key === "Enter") submit();
  };

  const onFile = async (e: ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    e.target.value = ""; // reset so re-selecting the same file fires onChange
    if (!file) return;
    onAddDescriptorSet(new Uint8Array(await file.arrayBuffer()), file.name, commit);
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

      {/* Between the two kinds, and not below them, because uploading a file adds the source
          on the spot — an option under that button would never be seen in time. */}
      <div style={{ borderTop: "1px solid var(--line)" }} />
      <label
        className="flex items-start gap-[8px]"
        style={{ fontSize: 14, cursor: "pointer" }}
      >
        <input
          type="checkbox"
          checked={commit}
          onChange={(e) => setCommit(e.target.checked)}
          style={{ accentColor: "var(--color-accent)", marginTop: 3 }}
        />
        <span>
          Commit its descriptors to this collection
          <span className="text-muted" style={{ display: "block", fontSize: 12, lineHeight: 1.5 }}>
            Either kind. The resolved descriptors land in the repo (descriptors/….json), so a
            fresh clone resolves this source with no local state and no network — at the cost
            of a large file in git history. Off, they are cached in local state only, which for
            an uploaded set means a clone has no schema for it until someone uploads the file
            again.
          </span>
        </span>
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
