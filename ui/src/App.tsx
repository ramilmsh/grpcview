import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { TransportProvider } from "@connectrpc/connect-query";
import { transport } from "@/lib/client";
import { AppShell } from "@/components/shell/AppShell";
import { useUIStore } from "@/lib/ui-store";
import { WorkspaceView } from "@/features/workspace/WorkspaceView";
import { SourcesView } from "@/features/sources/SourcesView";

// TransportProvider OUTSIDE QueryClientProvider (verified pattern — plan §13).
const queryClient = new QueryClient();

function CurrentView() {
  const activeView = useUIStore((s) => s.activeView);
  return activeView === "sources" ? <SourcesView /> : <WorkspaceView />;
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
