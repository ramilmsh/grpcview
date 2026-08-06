import {
  ArrowClockwise,
  CaretDown,
  CaretUp,
  FileArchive,
  GitBranch,
  Hammer,
  HardDrives,
  Link,
  LinkBreak,
  PlugsConnected,
  Trash,
} from "@/components/ui/icons";
import { IconButton } from "@/components/ui/Button";
import { Tag } from "@/components/ui/Tag";
import { hostLabel } from "@/lib/workspace-query";
import { SourceOrigin, type DescriptorSource } from "@grpcview/v1/workspace_pb";

// One row of the priority-ordered source list, kept apart from SourcesView because the view
// is hooks all the way down: rendered from a plain DescriptorSource, a row's markup can be
// asserted on directly (source-row.test.tsx).

// How the row presents a source. "reference" is a WORKSPACE-origin entry whose oneof arm is
// unset — a reference grpcview.work.json no longer defines. It has no kind to claim, since
// the manifest held the address and the address is what said reflection or upload, so the
// row must not label it a descriptor set; Resolved.error already names the file to fix.
export type SourceKind = "reflection" | "descriptorSet" | "bazel" | "reference";

export function sourceKind(s: DescriptorSource): SourceKind {
  if (s.source.case === "reflection") return "reflection";
  if (s.source.case === "upload") return "descriptorSet";
  if (s.source.case === "bazel") return "bazel";
  return "reference";
}

export function sourceLabel(s: DescriptorSource): string {
  if (s.source.case === "reflection") return hostLabel(s.source.value);
  if (s.source.case === "upload") return s.source.value.fileName;
  // The bazel label IS what the row points at, the way an address is for reflection: it is
  // config, it is what a refresh builds, and it is the only readable half of the id.
  if (s.source.case === "bazel") return s.source.value.label;
  return s.id;
}

// The workspace-root-relative path a source's bytes were last read from, empty when there
// isn't one. Only an upload has one to show: reflection has an address instead, and a bazel
// label's outputs are the build's business, not a recipe a user retypes.
export function refreshRecipe(s: DescriptorSource): string {
  return s.source.case === "upload" ? s.source.value.path : "";
}

export function contribution(s: DescriptorSource): { text: string; tone: "ok" | "muted" | "warn" } {
  const r = s.resolved;
  if (r?.error) return { text: r.error, tone: "warn" };
  if (!r) return { text: "not resolved yet", tone: "muted" };
  const defined = r.serviceNames.length;
  const won = r.wonServiceNames.length;
  const files = `${r.fileCount} file${r.fileCount === 1 ? "" : "s"}`;
  if (defined === 0) return { text: `${files}, no services`, tone: "muted" };
  if (won === 0) {
    const shadowed = defined === 1 ? "its 1 service" : `all ${defined} services`;
    return { text: `${files}, ${shadowed} shadowed`, tone: "muted" };
  }
  if (won < defined) {
    return { text: `${files}, ${won} of ${defined} services`, tone: "ok" };
  }
  return { text: `${files}, ${won} service${won === 1 ? "" : "s"}`, tone: "ok" };
}

// The two states of commit_descriptors, as words a user who has read no design doc can act
// on: the flag only moves where the store writes this source's descriptors.
export const COMMITTED_LABEL = "committed";
export const LOCAL_LABEL = "local only";

// Said in the kind's place, because a reference the manifest does not define has no kind and
// cannot get one back until that file does. It is not always an error the server can report:
// descriptors this collection already committed or cached keep resolving from the store, so
// the row can hold real file and service counts while its config is gone — and then this pill
// is the ONLY thing that says why the address is missing and the id is showing.
export const NO_DEFINITION_LABEL = "no definition";

const noDefinitionTitle = (id: string): string =>
  `grpcview.work.json does not define ${id}, so this collection has only a reference to ` +
  "config that is not there. Restore the definition in that file, or remove the reference.";

const SHARED_TITLE =
  "Defined once in grpcview.work.json and shared by every collection that references it — " +
  "this collection's list holds only the reference. Its address can only be changed in that " +
  "file; priority, refresh and where its descriptors are stored stay per collection.";

function commitTitle(s: DescriptorSource): string {
  if (s.commitDescriptors) {
    return (
      "Descriptors are committed to this collection (descriptors/….json), so a fresh clone " +
      "resolves this source with no local state and no network — at the cost of a large file " +
      "in git history. Click to cache them in local state instead."
    );
  }
  const cached =
    "Descriptors are cached in this machine's local state, never in the repo. Click to commit " +
    "them to this collection so a fresh clone resolves this source with no refresh and no " +
    "network.";
  // Worth spelling out on an upload and nowhere else: an upload has no address, so there is
  // nothing for a clone to re-fetch from — the flag is the difference between a schema and none.
  if (s.source.case === "upload") {
    return (
      `${cached} An upload has no address to re-fetch from, so while it is uncommitted its ` +
      "only copy is local state: a clone of this repo has no schema for it at all."
    );
  }
  return cached;
}

// Said on the refresh control rather than only after a failed refresh, because for a pathless
// upload the answer is not "that went wrong" but "the refresh is a different gesture" — and
// the button is where a user asks the question. The failure path still carries the server's
// FailedPrecondition into the view's banner, so nothing is lost by answering early too.
export const REDROP_TO_REFRESH =
  "This descriptor set came from a file picker, so there is no path to re-read. Add the same " +
  "file again to refresh it: the file name is the source's identity, so re-adding replaces " +
  "these descriptors in place rather than adding a second source.";

export function refreshTitle(s: DescriptorSource): string {
  switch (sourceKind(s)) {
    case "reflection":
      return "Re-reflect this target";
    case "descriptorSet": {
      const path = refreshRecipe(s);
      return path ? `Re-read this descriptor set from ${path}` : REDROP_TO_REFRESH;
    }
    case "bazel":
      // Refusable: an untrusted workspace runs no build, and says so in Resolved.error.
      return "Run bazel build for this label and re-read the descriptor sets it writes";
    default:
      // A reference with no definition has no pointer to re-acquire from, so this reports
      // what is missing rather than fetching anything.
      return "Re-resolve this reference";
  }
}

const REDERIVED = "This collection's definitions are re-derived from the sources that remain.";

// Removing a WORKSPACE-origin source drops THIS collection's reference and leaves the shared
// definition alone — the one place the two origins differ in what a client's remove means. The
// undefined reference gets its own sentence rather than the shared one, which would promise a
// definition survives that is not there in the first place.
export function removeConsequence(s: DescriptorSource): string {
  if (sourceKind(s) === "reference") {
    return `Nothing else goes with it: grpcview.work.json does not define it. ${REDERIVED}`;
  }
  if (s.origin === SourceOrigin.WORKSPACE) {
    return (
      "Its definition stays in grpcview.work.json for the collections that reference it; only " +
      `this collection's reference goes. ${REDERIVED}`
    );
  }
  return REDERIVED;
}

// A bazel source EXECUTES to produce its bytes, which is why it gets the tool and not another
// file icon — the same distinction the trust gate is about.
const KIND_ICON: Record<SourceKind, typeof PlugsConnected> = {
  reflection: PlugsConnected,
  descriptorSet: FileArchive,
  bazel: Hammer,
  reference: LinkBreak,
};

const BAZEL_TITLE =
  "Refreshing this source runs `bazel build` for the label and reads the descriptor sets it " +
  "writes. That runs this repo's build code, so it is refused until the workspace is trusted.";

export interface SourceRowCallbacks {
  onMove: (from: number, to: number) => void;
  onRefresh: (s: DescriptorSource) => void;
  onRemove: (s: DescriptorSource) => void;
  onSetCommit: (s: DescriptorSource, commit: boolean) => void;
}

export function SourceRow({
  source: s,
  index,
  count,
  busy,
  cb,
}: {
  source: DescriptorSource;
  index: number;
  count: number;
  busy: boolean;
  cb: SourceRowCallbacks;
}) {
  const kind = sourceKind(s);
  const label = sourceLabel(s);
  const info = contribution(s);
  const failed = Boolean(s.resolved?.error);
  const toneColor =
    info.tone === "warn"
      ? "var(--err-fg)"
      : info.tone === "ok"
        ? "var(--color-neutral-500)"
        : "var(--color-neutral-600)";
  const KindIcon = KIND_ICON[kind];
  const kindIconColor = failed
    ? "var(--err-fg)"
    : kind === "reflection"
      ? "var(--ok)"
      : kind === "bazel"
        ? "var(--color-accent)"
        : kind === "reference"
          ? "var(--warn)"
          : "var(--color-neutral-400)";
  // The recipe, on the one kind that has one to show and can lose it. Short by construction
  // (workspace-root-relative), so it rides the existing secondary line.
  const recipe = refreshRecipe(s);
  // Filled pill = the flag is on, outlined = off, so the state reads without hovering; warn
  // outline is reserved for the case where "off" costs a clone its schema entirely.
  const uncommittedUpload = kind === "descriptorSet" && !s.commitDescriptors;
  const commitStyle = s.commitDescriptors
    ? { background: "var(--color-neutral-800)", color: "var(--color-neutral-100)" }
    : uncommittedUpload
      ? { borderColor: "var(--warn)", color: "var(--warn)" }
      : { borderColor: "var(--line)", color: "var(--color-neutral-500)" };

  return (
    <div
      className="flex items-center gap-[11px]"
      style={{
        padding: "11px 13px",
        background: "var(--panel-2)",
        border: "1px solid var(--line)",
        borderRadius: 9,
      }}
    >
      <span
        className="font-mono"
        style={{ fontSize: 11, color: "var(--color-neutral-600)", width: "2ch" }}
      >
        {index + 1}
      </span>
      <KindIcon size={18} style={{ color: kindIconColor }} />
      <div style={{ minWidth: 0, flex: 1 }}>
        <div
          className="font-mono truncate"
          style={{ fontSize: 13, color: "var(--color-text)" }}
          title={label}
        >
          {label}
        </div>
        <div className="truncate" style={{ fontSize: 11, color: toneColor }}>
          {info.text}
          {recipe && (
            <span
              className="font-mono"
              style={{ color: "var(--color-neutral-600)" }}
              title={`Refreshing re-reads this descriptor set from ${recipe}, relative to the workspace root.`}
            >
              {` · ${recipe}`}
            </span>
          )}
        </div>
      </div>
      {s.origin === SourceOrigin.WORKSPACE && (
        <Tag variant="neutral" title={SHARED_TITLE}>
          <Link size={11} style={{ marginRight: 5 }} />
          shared
        </Tag>
      )}
      {kind === "reflection" && <Tag variant="accent">reflection</Tag>}
      {kind === "descriptorSet" && <Tag variant="neutral">descriptor set</Tag>}
      {kind === "bazel" && (
        <Tag variant="accent" title={BAZEL_TITLE}>
          bazel
        </Tag>
      )}
      {kind === "reference" && (
        <span
          className="tag"
          title={noDefinitionTitle(s.id)}
          style={{ background: "var(--warn-bg)", color: "var(--warn)" }}
        >
          {NO_DEFINITION_LABEL}
        </span>
      )}
      <button
        type="button"
        className="tag"
        title={commitTitle(s)}
        aria-label={`Descriptors of ${label} are ${
          s.commitDescriptors ? COMMITTED_LABEL : LOCAL_LABEL
        } — click to ${s.commitDescriptors ? "stop committing them" : "commit them"}`}
        onClick={() => cb.onSetCommit(s, !s.commitDescriptors)}
        disabled={busy}
        style={{
          gap: 5,
          background: "transparent",
          border: "1px solid transparent",
          cursor: busy ? "default" : "pointer",
          ...commitStyle,
        }}
      >
        {s.commitDescriptors ? <GitBranch size={11} /> : <HardDrives size={11} />}
        {s.commitDescriptors ? COMMITTED_LABEL : LOCAL_LABEL}
      </button>
      <div className="flex items-center">
        <IconButton
          title="Raise priority"
          aria-label={`Raise priority of ${label}`}
          onClick={() => cb.onMove(index, index - 1)}
          disabled={busy || index === 0}
        >
          <CaretUp size={14} />
        </IconButton>
        <IconButton
          title="Lower priority"
          aria-label={`Lower priority of ${label}`}
          onClick={() => cb.onMove(index, index + 1)}
          disabled={busy || index === count - 1}
        >
          <CaretDown size={14} />
        </IconButton>
      </div>
      <IconButton
        title={refreshTitle(s)}
        aria-label={`Refresh ${label}`}
        onClick={() => cb.onRefresh(s)}
        disabled={busy}
      >
        <ArrowClockwise size={15} />
      </IconButton>
      <IconButton
        title={
          s.origin === SourceOrigin.WORKSPACE
            ? "Remove this collection's reference"
            : "Remove source"
        }
        aria-label={`Remove ${label}`}
        onClick={() => cb.onRemove(s)}
        disabled={busy}
      >
        <Trash size={15} />
      </IconButton>
    </div>
  );
}
