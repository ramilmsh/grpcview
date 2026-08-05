import { useRef, useState, type ChangeEvent, type KeyboardEvent } from "react";
import { Dialog } from "@/components/ui/Dialog";
import { Input, Field } from "@/components/ui/Input";
import { Button } from "@/components/ui/Button";

// AddSourceModal adds a reflection target, a bazel label, or an uploaded FileDescriptorSet.
export function AddSourceModal({
  open,
  onClose,
  onAddReflection,
  onAddBazel,
  onAddDescriptorSet,
  pending,
}: {
  open: boolean;
  onClose: () => void;
  onAddReflection: (address: string, tls: boolean, commitDescriptors: boolean) => void;
  onAddBazel: (label: string, commitDescriptors: boolean) => void;
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
  // Same rule, same reason: a pre-filled //pkg:target is the label that gets built.
  const [label, setLabel] = useState("");
  // Off by default, matching the store: committing is a deliberate choice to put descriptors
  // in git history, and it applies to whichever of the kinds below is added.
  const [commit, setCommit] = useState(false);
  const fileRef = useRef<HTMLInputElement>(null);

  const submitReflection = () => {
    if (address.trim()) onAddReflection(address.trim(), tls, commit);
  };

  const submitBazel = () => {
    if (label.trim()) onAddBazel(label.trim(), commit);
  };

  // The footer button adds whichever single field is filled. Both filled is ambiguous and the
  // first field wins; each field's own Enter is the unambiguous way to say which one.
  const submit = () => {
    if (address.trim()) submitReflection();
    else submitBazel();
  };

  const onEnter = (submitOne: () => void) => (e: KeyboardEvent) => {
    if (e.key === "Enter") submitOne();
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
          onKeyDown={onEnter(submitReflection)}
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
      <Field label="Bazel target">
        <Input
          value={label}
          onChange={(e) => setLabel(e.target.value)}
          placeholder="//pkg:target"
          onKeyDown={onEnter(submitBazel)}
        />
        <span
          className="text-muted"
          style={{ display: "block", fontSize: 12, lineHeight: 1.5, marginTop: 5 }}
        >
          A label whose default outputs are descriptor sets — a plain proto_library is enough.
          Adding or refreshing it runs bazel build, so the workspace has to be trusted.
        </span>
      </Field>

      {/* Between the kinds, and not below them, because uploading a file adds the source
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
            Any kind. The resolved descriptors land in the repo (descriptors/….json), so a
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
        <Button
          variant="primary"
          onClick={submit}
          disabled={pending || (!address.trim() && !label.trim())}
        >
          {pending ? "Adding…" : "Add source"}
        </Button>
      </div>
    </Dialog>
  );
}
