import clsx from "clsx";
import { TreeStructure, Stack, BracketsCurly } from "@/components/ui/icons";
import { useUIStore, type ActiveView } from "@/lib/ui-store";

// Rail: the left view switcher. Workspace + Definition sources are the Phase-1
// views; Scripts is the S1 authoring view (create/edit/test-run sandboxed scripts).
// The rest (Scenarios/Environments/Git/History) arrive with their backends (plan §8).
const VIEWS: Array<{ view: ActiveView; title: string; icon: React.ReactNode }> = [
  { view: "workspace", title: "Collection", icon: <TreeStructure /> },
  { view: "sources", title: "Definition sources", icon: <Stack /> },
  { view: "scripts", title: "Scripts", icon: <BracketsCurly /> },
];

export function Rail() {
  const activeView = useUIStore((s) => s.activeView);
  const setView = useUIStore((s) => s.setView);

  return (
    <div
      className="bg-panel flex flex-col items-center"
      style={{
        width: 54,
        flex: "none",
        borderRight: "1px solid var(--line)",
        padding: "9px 0",
        gap: 5,
      }}
    >
      {VIEWS.map(({ view, title, icon }) => (
        <button
          key={view}
          className={clsx("rail-btn", activeView === view && "on")}
          title={title}
          onClick={() => setView(view)}
        >
          {icon}
        </button>
      ))}
    </div>
  );
}
