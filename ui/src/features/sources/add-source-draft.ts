import type { SourceKind } from "./source-row";

// The pure half of AddSourceCard: what each kind is called, when a draft may be added, and
// what the priority select offers. Kept apart from the component so it is testable without a
// transport (the card calls a connect-query hook).

export type DraftKind = Exclude<SourceKind, "reference">;

export const DRAFT_KINDS: readonly DraftKind[] = [
  "reflection",
  "bazel",
  "descriptorSet",
];

export const KIND_META: Record<
  DraftKind,
  { menu: string; title: string; subtitle: string }
> = {
  reflection: {
    menu: "Server reflection",
    title: "New server reflection source",
    subtitle: "A running gRPC server's reflection endpoint",
  },
  bazel: {
    menu: "Bazel target",
    title: "New Bazel target source",
    subtitle: "A label whose default outputs are descriptor sets",
  },
  descriptorSet: {
    menu: "Descriptor file",
    title: "New descriptor file source",
    subtitle: "An uploaded protoc --descriptor_set_out file",
  },
};

export const PROTOC_COMMAND =
  "protoc --include_imports --descriptor_set_out=out.binpb -I. *.proto";

export interface DraftFields {
  address: string;
  label: string;
  file: { name: string; size: number } | null;
}

// Whether the draft names something to add. Pending is the caller's business.
export function draftValid(kind: DraftKind, f: DraftFields): boolean {
  switch (kind) {
    case "reflection":
      return f.address.trim() !== "";
    case "bazel":
      return f.label.trim() !== "";
    case "descriptorSet":
      return f.file !== null;
  }
}

// Committing defaults on for an upload only: it has no address to re-fetch from, so left
// uncommitted a clone has no schema for it at all. Reflection and bazel can re-resolve, and
// committing them is a deliberate choice to put descriptors in git history. `touched` is the
// user's own choice, which survives a type switch; null follows the kind.
export function commitFor(kind: DraftKind, touched: boolean | null): boolean {
  return touched ?? kind === "descriptorSet";
}

// The priority select's value is the insert index as a string; BOTTOM appends, which is the
// backend's unset position rather than an index, so a list that grows meanwhile still lands
// the source last.
export const BOTTOM = "bottom";

export interface PriorityOption {
  value: string;
  label: string;
}

// Top first (the default), one "After N." per gap between existing rows, then Bottom. With
// no rows there is only one place to go.
export function priorityOptions(labels: readonly string[]): PriorityOption[] {
  const out: PriorityOption[] = [{ value: "0", label: "Top — wins conflicts" }];
  for (let i = 1; i < labels.length; i++) {
    out.push({ value: String(i), label: `After ${i}. ${labels[i - 1]}` });
  }
  if (labels.length > 0) out.push({ value: BOTTOM, label: "Bottom" });
  return out;
}

export function positionOf(value: string): number | undefined {
  return value === BOTTOM ? undefined : Number(value);
}

export function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

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
