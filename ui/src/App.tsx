import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { TransportProvider } from "@connectrpc/connect-query";
import { transport } from "@/lib/client";
import { AppShell } from "@/components/shell/AppShell";
import { useUIStore } from "@/lib/ui-store";
import { useWorkspace } from "@/lib/workspace-query";
import { WorkspaceView } from "@/features/workspace/WorkspaceView";
import { NoCollection } from "@/features/workspace/NoCollection";
import { SourcesView } from "@/features/sources/SourcesView";
import { ScriptsView } from "@/features/scripts/ScriptsView";

// TransportProvider must sit OUTSIDE QueryClientProvider.
const queryClient = new QueryClient();

function CurrentView() {
  const activeView = useUIStore((s) => s.activeView);
  const { notFound } = useWorkspace();
  // Sources and Scripts are equally meaningless without a collection: one gate
  // in front of the switch covers all three views.
  if (notFound) return <NoCollection />;
  switch (activeView) {
    case "sources":
      return <SourcesView />;
    case "scripts":
      return <ScriptsView />;
    default:
      return <WorkspaceView />;
  }
}

export default function App() {
  return (
    <TransportProvider transport={transport}>
      <QueryClientProvider client={queryClient}>
        <AppShell>
          <CurrentView />
        </AppShell>
      </QueryClientProvider>
    </TransportProvider>
  );
}
