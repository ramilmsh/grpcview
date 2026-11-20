import { useActiveWorkspace, useCollections } from "@/lib/workspace-query";
import { useUIStore } from "@/lib/ui-store";
import { Button } from "@/components/ui/Button";
import { Plus, TreeStructure } from "@/components/ui/icons";
import { panelTiered } from "./panel-tree";

// Main-panel empty state: no request tab is open. The CTA is the tree's "+" (startNewRequest).
export function NoRequestOpen() {
  const { collection, services } = useActiveWorkspace();
  const { collections } = useCollections();
  const startNewRequest = useUIStore((s) => s.startNewRequest);

  const start = () =>
    startNewRequest(collection ?? "", null, panelTiered(collections));

  return (
    <div
      className="flex flex-col items-center justify-center"
      style={{ flex: 1, minHeight: 0, padding: 16, textAlign: "center" }}
    >
      <div
        aria-hidden="true"
        className="flex items-center justify-center"
        style={{
          width: 48,
          height: 48,
          borderRadius: 12,
          background: "var(--v-raised)",
          boxShadow: "var(--v-raise)",
          color: "var(--v-text-2)",
        }}
      >
        <TreeStructure size={20} />
      </div>
      <h2
        style={{
          margin: "18px 0 0",
          fontSize: 16,
          lineHeight: 1.3,
          fontWeight: 600,
          color: "var(--v-text)",
        }}
      >
        No request open
      </h2>
      <p
        style={{
          margin: "6px 0 0",
          fontSize: 13,
          lineHeight: 1.45,
          color: "var(--v-text-2)",
        }}
      >
        Pick a request from the tree, or start a new one.
      </p>
      <Button
        variant="primary"
        onClick={start}
        disabled={services.length === 0}
        style={{ marginTop: 18 }}
      >
        <Plus size={14} />
        New request
      </Button>
    </div>
  );
}
