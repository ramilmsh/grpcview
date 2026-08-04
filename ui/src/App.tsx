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
//
// Both defaults exist because the server is on THIS machine, always reachable whether
// or not the machine has internet:
//   - networkMode "always": react-query's default pauses a fetch (and, worse, a retry
//     of one that already failed) whenever its onlineManager thinks the browser is
//     offline. A paused query sits at status "pending" forever, so a failure never
//     surfaces — the NotFound that drives <NoCollection> would never render.
//   - retry false: a Connect error from localhost is an answer, not a network blip, and
//     retrying one costs seconds of a stale view before the real state appears.
const queryClient = new QueryClient({
  defaultOptions: {
    queries: { networkMode: "always", retry: false },
    mutations: { networkMode: "always", retry: false },
  },
});

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
