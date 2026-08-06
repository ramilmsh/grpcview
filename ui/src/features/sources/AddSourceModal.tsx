import { useRef, useState, type ChangeEvent, type KeyboardEvent } from "react";
import { Dialog } from "@/components/ui/Dialog";
import { Input, Field } from "@/components/ui/Input";
import { Combobox } from "@/components/ui/Combobox";
import { Button } from "@/components/ui/Button";
import { useBazelTargets } from "@/lib/workspace-query";

// bazelHint is what the label picker says when it has nothing to offer. A failed listing is
// reported in bazel's own words — the untrusted-workspace refusal names trust as the fix and
// a query failure names the package that broke — because none of it stops the user: the
// field takes a typed label either way, which is what this sentence has to make obvious.
export function bazelHint(targets: {
  labels: readonly string[];
  isPending: boolean;
  error: unknown;
}): string {
  if (targets.isPending) return "";
  if (targets.error) {
    const reason =
      targets.error instanceof Error ? targets.error.message : "the target listing failed";
    return `Targets could not be listed, so type the label: ${reason}`;
  }
  if (targets.labels.length === 0) {
    return "No proto_library or proto_descriptor_set target in this bazel workspace.";
  }
  return "";
}

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

  // Suggestions for the label field, queried only while this form is open and never waited
  // on: the field renders and accepts typing on the first frame, and the list drops in
  // whenever `bazel query` gets back. A failure (an untrusted workspace, most often) is a
  // hint under the field, not an error — typing a label still works.
  const targets = useBazelTargets(open);

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
        <Combobox
          value={label}
          onChange={setLabel}
          options={targets.labels}
          loading={targets.isPending}
          emptyHint={bazelHint(targets)}
          placeholder="//pkg:target"
          ariaLabel="Bazel target"
          onSubmit={submitBazel}
        />
        <span
          className="text-muted"
          style={{ display: "block", fontSize: 12, lineHeight: 1.5, marginTop: 5 }}
        >
          A label whose default outputs are descriptor sets — a plain proto_library is enough.
          Adding or refreshing it runs bazel build, so the workspace has to be trusted.
          {targets.warning && (
            <span style={{ display: "block", color: "var(--warn)" }}>
              Some packages could not be listed: {targets.warning}
            </span>
          )}
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
