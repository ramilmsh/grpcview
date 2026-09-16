import {
  useEffect,
  useRef,
  useState,
  type ChangeEvent,
  type KeyboardEvent,
  type ReactNode,
} from "react";
import clsx from "clsx";
import { Dialog } from "@/components/ui/Dialog";
import { Input, Field } from "@/components/ui/Input";
import { Combobox } from "@/components/ui/Combobox";
import { Button } from "@/components/ui/Button";
import {
  CaretDown,
  CaretRight,
  FileArchive,
  Hammer,
  PlugsConnected,
} from "@/components/ui/icons";
import { useBazelTargets } from "@/lib/workspace-query";

// bazelHint is what the label picker says when it has nothing to offer. A failed listing is
// reported in bazel's own words — the untrusted-workspace refusal names trust as the fix and
// a query failure names the package that broke — because none of it stops the user: the
// field takes a typed label either way, which is what this sentence has to make obvious.
// eslint-disable-next-line react-refresh/only-export-components
export function bazelHint(targets: {
  labels: readonly string[];
  isPending: boolean;
  error: unknown;
}): string {
  if (targets.isPending) return "";
  if (targets.error) {
    const reason =
      targets.error instanceof Error
        ? targets.error.message
        : "the target listing failed";
    return `Targets could not be listed, so type the label: ${reason}`;
  }
  if (targets.labels.length === 0) {
    return "No proto_library or proto_descriptor_set target in this bazel workspace.";
  }
  return "";
}

// The three kinds this modal can add, in card order. Narrower than source-row's SourceKind
// (which also covers "reference") because that state never applies to something being added.
type Mode = "reflection" | "bazel" | "descriptorSet";

// One card in the mode selector: header always visible, body only for the expanded card.
// An accordion, not independent expand/collapse, because exactly one mode is ever what the
// footer button (or, for descriptorSet, the in-card upload button) submits.
function ModeCard({
  icon: Icon,
  iconColor,
  title,
  description,
  expanded,
  onExpand,
  children,
}: {
  icon: typeof PlugsConnected;
  iconColor: string;
  title: string;
  description: string;
  expanded: boolean;
  onExpand: () => void;
  children: ReactNode;
}) {
  return (
    <div className={clsx("mode-card", expanded && "on")}>
      <button
        type="button"
        className="mode-card-head"
        aria-expanded={expanded}
        onClick={onExpand}
      >
        <Icon size={18} style={{ color: iconColor, flex: "none" }} />
        <div style={{ minWidth: 0 }}>
          <div>{title}</div>
          <div className="text-muted" style={{ fontSize: 12 }}>
            {description}
          </div>
        </div>
        {expanded ? (
          <CaretDown size={14} className="caret" />
        ) : (
          <CaretRight size={14} className="caret" />
        )}
      </button>
      {expanded && <div className="mode-card-body">{children}</div>}
    </div>
  );
}

// commit_descriptors is a field on DescriptorSource itself, not on any one oneof arm — it
// applies the same way to all three kinds. Only the upload case gets an extra sentence,
// because it alone has no address to refetch from if it stays uncommitted.
function commitCopy(mode: Mode | null): string {
  const base =
    "Descriptors land in the repo (descriptors/….json), so a clone resolves this source " +
    "with no network or local state — at the cost of a large file in git history. Off, " +
    "they are cached locally only.";
  if (mode === "descriptorSet") {
    return (
      `${base} An upload has no address to re-fetch from: while uncommitted, its only ` +
      "copy is local state, and a clone has no schema for it until it is uploaded again."
    );
  }
  return base;
}

// AddSourceModal adds a reflection target, a bazel label, or an uploaded FileDescriptorSet —
// one card per kind, accordion-style, so the fields shown are only ever the ones that apply.
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
  onAddReflection: (
    address: string,
    tls: boolean,
    commitDescriptors: boolean,
  ) => void;
  onAddBazel: (label: string, commitDescriptors: boolean) => void;
  onAddDescriptorSet: (
    bytes: Uint8Array,
    fileName: string,
    commitDescriptors: boolean,
  ) => void;
  pending?: boolean;
}) {
  // Null until a card is clicked: no kind is pre-picked, the user makes the first move.
  const [mode, setMode] = useState<Mode | null>(null);
  // Must stay empty: a pre-filled default becomes the added source's real address.
  const [address, setAddress] = useState("");
  const [tls, setTls] = useState(false);
  // Same rule, same reason: a pre-filled //pkg:target is the label that gets built.
  const [label, setLabel] = useState("");
  // One flag shared by all three cards, matching the backend field it becomes: switching
  // cards is switching which kind gets added, not a separate setting per kind.
  const [commit, setCommit] = useState(false);
  const fileRef = useRef<HTMLInputElement>(null);

  // Collapse back to nothing-expanded on every fresh open, not just on first mount — otherwise
  // reopening after a cancel or a successful add resurrects whichever card was left open.
  useEffect(() => {
    if (open) setMode(null);
  }, [open]);

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

  const onEnter = (submitOne: () => void) => (e: KeyboardEvent) => {
    if (e.key === "Enter") submitOne();
  };

  const onFile = async (e: ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    e.target.value = ""; // reset so re-selecting the same file fires onChange
    if (!file) return;
    onAddDescriptorSet(
      new Uint8Array(await file.arrayBuffer()),
      file.name,
      commit,
    );
  };

  // Shared across all three cards (it sets one field on the source being added, not one per
  // kind), so it renders once below the card list rather than once per card body.
  const commitField = (
    <div className="commit-field">
      <label
        className="flex items-start gap-[8px]"
        style={{ fontSize: 13, cursor: "pointer" }}
      >
        <input
          type="checkbox"
          checked={commit}
          onChange={(e) => setCommit(e.target.checked)}
          style={{ accentColor: "var(--color-accent)", marginTop: 3 }}
        />
        <span>
          Commit its descriptors to this collection
          <span
            className="text-muted"
            style={{ display: "block", fontSize: 12, lineHeight: 1.5 }}
          >
            {commitCopy(mode)}
          </span>
        </span>
      </label>
    </div>
  );

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title="Add definition source"
      width={480}
    >
      <div className="flex flex-col" style={{ gap: 10 }}>
        <ModeCard
          icon={PlugsConnected}
          iconColor="var(--ok)"
          title="Server reflection"
          description="A running gRPC server's reflection endpoint"
          expanded={mode === "reflection"}
          onExpand={() => setMode("reflection")}
        >
          <Field label="Address">
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
        </ModeCard>

        <ModeCard
          icon={Hammer}
          iconColor="var(--color-accent)"
          title="Bazel target"
          description="A label whose default outputs are descriptor sets"
          expanded={mode === "bazel"}
          onExpand={() => setMode("bazel")}
        >
          <Field label="Label">
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
          </Field>
          <span
            className="text-muted"
            style={{ display: "block", fontSize: 12, lineHeight: 1.5 }}
          >
            A plain proto_library is enough. Adding or refreshing it runs
            bazel build, so the workspace has to be trusted.
            {targets.warning && (
              <span style={{ display: "block", color: "var(--warn)" }}>
                Some packages could not be listed: {targets.warning}
              </span>
            )}
          </span>
        </ModeCard>

        <ModeCard
          icon={FileArchive}
          iconColor="var(--color-neutral-400)"
          title="Descriptor file"
          description="An uploaded protoc --descriptor_set_out file"
          expanded={mode === "descriptorSet"}
          onExpand={() => setMode("descriptorSet")}
        >
          <Field label="File">
            <input
              ref={fileRef}
              type="file"
              onChange={onFile}
              style={{ display: "none" }}
            />
            <div className="flex items-center gap-[8px]">
              <Button
                onClick={() => fileRef.current?.click()}
                disabled={pending}
              >
                Upload descriptor set…
              </Button>
              <span className="text-muted" style={{ fontSize: 12 }}>
                protoc --include_imports --descriptor_set_out
              </span>
            </div>
          </Field>
        </ModeCard>
      </div>

      {mode !== null && commitField}

      <div className="dialog-actions">
        <Button onClick={onClose}>Cancel</Button>
        {/* No footer submit for descriptor file (its own upload button is the add action) or
            for nothing expanded (there's no kind yet to submit). */}
        {(mode === "reflection" || mode === "bazel") && (
          <Button
            variant="primary"
            onClick={mode === "reflection" ? submitReflection : submitBazel}
            disabled={
              pending ||
              (mode === "reflection" ? !address.trim() : !label.trim())
            }
          >
            {pending ? "Adding…" : "Add source"}
          </Button>
        )}
      </div>
    </Dialog>
  );
}
