import type { DescriptorSource } from "@grpcview/v1/workspace_pb";
import { NO_DEFINITION_LABEL, sourceKind, type SourceKind } from "./source-row";

// What the top bar's definition-sources dropdown decides without rendering — kept apart from
// SourcesPopover.tsx, which calls connect-query hooks and so needs a transport.

// The same words the Sources page's kind pills say.
export const KIND_LABEL: Record<SourceKind, string> = {
  reflection: "reflection",
  descriptorSet: "descriptor set",
  bazel: "bazel",
  reference: NO_DEFINITION_LABEL,
};

export const TARGET_TITLE =
  "Requests are sent here unless they set their own target";

// The one line a failed row shows under itself: the first non-blank line of the server's
// error, and whether there is more to expand into the log box.
export function errorSummary(error: string): { line: string; more: boolean } {
  const lines = error
    .split("\n")
    .map((l) => l.trimEnd())
    .filter((l) => l.trim() !== "");
  const line = lines[0]?.trim() ?? "";
  return { line, more: lines.length > 1 || line.length > 72 };
}

// What "Refresh all" refreshes, in priority order. A pathless upload is left out: it came from
// a file picker, so its refresh is a re-drop on the Sources page and the server refuses it.
export function refreshAllIds(sources: readonly DescriptorSource[]): string[] {
  return sources
    .filter((s) => !(s.source.case === "upload" && s.source.value.path === ""))
    .map((s) => s.id);
}

// The source the top bar names: the collection's first reflection source, which is where
// requests go unless they set their own target (firstReflectionSource, by id).
export function targetSourceId(
  sources: readonly DescriptorSource[],
): string | null {
  return sources.find((s) => sourceKind(s) === "reflection")?.id ?? null;
}
