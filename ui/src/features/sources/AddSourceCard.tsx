import {
  useRef,
  useState,
  type CSSProperties,
  type DragEvent,
  type KeyboardEvent,
  type ReactNode,
} from "react";
import { Copy, ShieldCheck, UploadSimple, X } from "@/components/ui/icons";
import { Button, IconButton } from "@/components/ui/Button";
import { Combobox } from "@/components/ui/Combobox";
import { Input } from "@/components/ui/Input";
import { Tag } from "@/components/ui/Tag";
import { useBazelTargets } from "@/lib/workspace-query";
import {
  bazelHint,
  commitFor,
  draftValid,
  formatSize,
  KIND_META,
  positionOf,
  priorityOptions,
  PROTOC_COMMAND,
  type DraftKind,
} from "./add-source-draft";
import { KIND_ICON, KIND_TINT } from "./source-row";

export type DraftSubmit =
  | { kind: "reflection"; address: string; tls: boolean }
  | { kind: "bazel"; label: string }
  | { kind: "descriptorSet"; bytes: Uint8Array; fileName: string };

export interface DraftCommon {
  commitDescriptors: boolean;
  // Insert index, 0 = top; undefined appends at the lowest priority (the backend's default).
  position: number | undefined;
}

const fieldLabel: CSSProperties = {
  display: "block",
  fontSize: 12,
  marginBottom: 5,
  color: "color-mix(in srgb, var(--color-text) 70%, transparent)",
};

const inset: CSSProperties = {
  display: "flex",
  alignItems: "center",
  gap: 8,
  padding: "8px 10px",
  borderRadius: 8,
  background: "var(--panel)",
  border: "1px solid var(--line)",
  fontSize: 12,
};

const inlineCode: CSSProperties = {
  fontFamily: "var(--mono)",
  fontSize: 11.5,
  padding: "1px 5px",
  borderRadius: 4,
  background: "var(--color-surface)",
  color: "var(--color-text)",
};

const divider = <div style={{ borderTop: "1px solid var(--line)" }} />;

function Check({
  checked,
  onChange,
  children,
}: {
  checked: boolean;
  onChange: (v: boolean) => void;
  children: ReactNode;
}) {
  return (
    <label
      className="flex items-start gap-[8px]"
      style={{ fontSize: 13, cursor: "pointer" }}
    >
      <input
        type="checkbox"
        checked={checked}
        onChange={(e) => onChange(e.target.checked)}
        style={{ accentColor: "var(--color-accent)", marginTop: 2 }}
      />
      <span>{children}</span>
    </label>
  );
}

// AddSourceCard is the one draft source, in place at the top of the list. Its type is the
// caller's (the header type menu sets it); its fields survive a type switch.
export function AddSourceCard({
  kind,
  sourceLabels,
  pending,
  onCancel,
  onSubmit,
}: {
  kind: DraftKind;
  // The current rows, top first, for the priority select.
  sourceLabels: readonly string[];
  pending: boolean;
  onCancel: () => void;
  onSubmit: (source: DraftSubmit, common: DraftCommon) => void;
}) {
  // Must stay empty: a pre-filled default becomes the added source's real address.
  const [address, setAddress] = useState("");
  const [tls, setTls] = useState(false);
  // Same rule, same reason: a pre-filled //pkg:target is the label that gets built.
  const [label, setLabel] = useState("");
  // Staged, not read on selection: "Add source" is the one gesture that submits every kind.
  const [file, setFile] = useState<File | null>(null);
  const [dragging, setDragging] = useState(false);
  const [commitTouched, setCommitTouched] = useState<boolean | null>(null);
  const [priority, setPriority] = useState("0");
  const [copied, setCopied] = useState(false);
  const fileRef = useRef<HTMLInputElement>(null);

  // Queried only while the bazel field is showing, never waited on: the field takes typing on
  // the first frame and the list drops in whenever `bazel query` gets back.
  const targets = useBazelTargets(kind === "bazel");

  const commit = commitFor(kind, commitTouched);
  const options = priorityOptions(sourceLabels);
  // A row removed meanwhile can take the chosen gap with it; fall back to the default.
  const priorityValue = options.some((o) => o.value === priority)
    ? priority
    : "0";
  const valid = draftValid(kind, { address, label, file });
  const meta = KIND_META[kind];
  const KindIcon = KIND_ICON[kind];
  const tint = KIND_TINT[kind];

  const submit = async () => {
    if (!valid || pending) return;
    const common = {
      commitDescriptors: commit,
      position: positionOf(priorityValue),
    };
    if (kind === "reflection") {
      onSubmit({ kind, address: address.trim(), tls }, common);
    } else if (kind === "bazel") {
      onSubmit({ kind, label: label.trim() }, common);
    } else if (file) {
      onSubmit(
        {
          kind,
          bytes: new Uint8Array(await file.arrayBuffer()),
          fileName: file.name,
        },
        common,
      );
    }
  };

  const onEnter = (e: KeyboardEvent) => {
    if (e.key === "Enter") void submit();
  };

  // Scoped to the card, not the window: a window listener would also fire under the remove
  // dialog's own Escape. The Combobox stops the keystroke that only closes its list.
  const onKeyDown = (e: KeyboardEvent) => {
    if (e.key === "Escape") {
      e.preventDefault();
      onCancel();
    }
  };

  const onDrop = (e: DragEvent) => {
    e.preventDefault();
    setDragging(false);
    const dropped = e.dataTransfer.files?.[0];
    if (dropped) setFile(dropped);
  };

  const copyCommand = () => {
    void navigator.clipboard?.writeText(PROTOC_COMMAND).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    });
  };

  const commitBox = (
    <Check checked={commit} onChange={setCommitTouched}>
      Commit its descriptors to this collection
      <span
        className="text-muted"
        style={{ display: "block", fontSize: 12, lineHeight: 1.5 }}
      >
        Clones resolve it offline, at the cost of a large file in git history.
      </span>
    </Check>
  );

  return (
    <div
      role="group"
      aria-label={meta.title}
      onKeyDown={onKeyDown}
      className="flex flex-col"
      style={{
        background: "var(--panel-2)",
        border: "1px solid var(--color-accent)",
        borderRadius: 9,
        boxShadow:
          "0 0 0 3px color-mix(in srgb, var(--color-accent) 14%, transparent), 0 8px 24px color-mix(in srgb, var(--color-accent) 10%, transparent)",
      }}
    >
      <div
        className="flex items-center gap-[11px]"
        style={{ padding: "11px 13px" }}
      >
        <span
          className="flex items-center justify-center"
          style={{
            width: 30,
            height: 30,
            flex: "none",
            borderRadius: 7,
            background: `color-mix(in srgb, ${tint} 14%, transparent)`,
          }}
        >
          <KindIcon size={17} style={{ color: tint }} />
        </span>
        <div style={{ minWidth: 0, flex: 1 }}>
          <div
            className="flex items-center gap-[8px]"
            style={{ fontSize: 13.5, fontWeight: 600 }}
          >
            {meta.title}
            <Tag variant="accent">Draft</Tag>
          </div>
          <div className="text-muted truncate" style={{ fontSize: 12 }}>
            {meta.subtitle}
          </div>
        </div>
        <IconButton
          aria-label="Discard draft"
          data-tooltip="Discard draft"
          onClick={onCancel}
        >
          <X size={15} />
        </IconButton>
      </div>

      {divider}

      <div className="flex flex-col" style={{ gap: 12, padding: "13px" }}>
        {kind === "reflection" && (
          <>
            <div>
              <label style={fieldLabel} htmlFor="add-source-address">
                Address
              </label>
              <Input
                id="add-source-address"
                className="font-mono"
                value={address}
                onChange={(e) => setAddress(e.target.value)}
                placeholder="localhost:50051"
                autoFocus
                onKeyDown={onEnter}
              />
            </div>
            <Check checked={tls} onChange={setTls}>
              Use TLS
            </Check>
          </>
        )}

        {kind === "bazel" && (
          <>
            <div>
              <span style={fieldLabel}>Target label</span>
              <div className="font-mono">
                <Combobox
                  value={label}
                  onChange={setLabel}
                  options={targets.labels}
                  loading={targets.isPending}
                  emptyHint={bazelHint(targets)}
                  placeholder="//pkg:target"
                  ariaLabel="Target label"
                  autoFocus
                  onSubmit={() => void submit()}
                />
              </div>
              <span
                className="text-muted"
                style={{ display: "block", fontSize: 12, marginTop: 5 }}
              >
                A plain proto_library is enough.
                {targets.warning && (
                  <span style={{ display: "block", color: "var(--warn)" }}>
                    Some packages could not be listed: {targets.warning}
                  </span>
                )}
              </span>
            </div>
            <div style={inset}>
              <ShieldCheck
                size={15}
                style={{ flex: "none", color: "var(--color-accent)" }}
              />
              <span>
                Adding or refreshing runs{" "}
                <code style={inlineCode}>bazel build</code>, so this workspace
                has to be trusted.
              </span>
            </div>
          </>
        )}

        {kind === "descriptorSet" && (
          <>
            <div>
              <span style={fieldLabel}>Descriptor set</span>
              <input
                ref={fileRef}
                type="file"
                accept=".pb,.binpb,.protoset"
                onChange={(e) => {
                  const chosen = e.target.files?.[0];
                  e.target.value = ""; // reset so re-selecting the same file fires onChange
                  if (chosen) setFile(chosen);
                }}
                style={{ display: "none" }}
              />
              <div
                className="flex items-center gap-[11px]"
                onDragOver={(e) => {
                  e.preventDefault();
                  setDragging(true);
                }}
                onDragLeave={() => setDragging(false)}
                onDrop={onDrop}
                style={{
                  padding: "12px 13px",
                  borderRadius: 8,
                  border: `1px dashed ${dragging ? "var(--color-accent)" : "var(--line)"}`,
                  background: dragging
                    ? "color-mix(in srgb, var(--color-accent) 8%, transparent)"
                    : "var(--panel)",
                }}
              >
                <span
                  className="flex items-center justify-center"
                  style={{
                    width: 30,
                    height: 30,
                    flex: "none",
                    borderRadius: 7,
                    background: "var(--color-surface)",
                  }}
                >
                  <UploadSimple
                    size={16}
                    style={{ color: "var(--color-neutral-400)" }}
                  />
                </span>
                <div style={{ minWidth: 0, flex: 1 }}>
                  {file ? (
                    <>
                      <div
                        className="font-mono truncate"
                        style={{ fontSize: 13 }}
                        title={file.name}
                      >
                        {file.name}
                      </div>
                      <div className="text-muted" style={{ fontSize: 12 }}>
                        {formatSize(file.size)}
                      </div>
                    </>
                  ) : (
                    <>
                      <div style={{ fontSize: 13 }}>
                        Drop a descriptor set here
                      </div>
                      <div className="text-muted" style={{ fontSize: 12 }}>
                        .pb, .binpb or .protoset — built with --include_imports
                      </div>
                    </>
                  )}
                </div>
                <Button
                  autoFocus
                  style={{ flex: "none", padding: "5px 11px", fontSize: 13 }}
                  onClick={() => fileRef.current?.click()}
                >
                  Browse…
                </Button>
              </div>
            </div>
            <div style={inset}>
              <span className="text-muted" style={{ flex: "none" }}>
                Generate one
              </span>
              <code
                className="truncate"
                style={{
                  fontFamily: "var(--mono)",
                  fontSize: 11.5,
                  flex: 1,
                  minWidth: 0,
                }}
                title={PROTOC_COMMAND}
              >
                {PROTOC_COMMAND}
              </code>
              <IconButton
                aria-label="Copy command"
                data-tooltip={copied ? "Copied" : "Copy command"}
                onClick={copyCommand}
                style={{ flex: "none" }}
              >
                <Copy size={14} />
              </IconButton>
            </div>
          </>
        )}

        {commitBox}
      </div>

      {divider}

      <div
        className="flex items-center gap-[8px]"
        style={{ padding: "10px 13px" }}
      >
        <label
          className="text-muted"
          htmlFor="add-source-priority"
          style={{ fontSize: 12 }}
        >
          Priority
        </label>
        <select
          id="add-source-priority"
          className="input"
          value={priorityValue}
          onChange={(e) => setPriority(e.target.value)}
          style={{
            width: "auto",
            maxWidth: 260,
            minHeight: 30,
            padding: "3px 8px",
            fontSize: 13,
            colorScheme: "dark",
          }}
        >
          {options.map((o) => (
            <option key={o.value} value={o.value}>
              {o.label}
            </option>
          ))}
        </select>
        <Button className="ml-auto" onClick={onCancel}>
          Cancel
        </Button>
        <Button
          variant="primary"
          onClick={() => void submit()}
          disabled={!valid || pending}
        >
          {pending ? "Adding…" : "Add source"}
        </Button>
      </div>
    </div>
  );
}
