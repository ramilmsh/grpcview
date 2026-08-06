import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { TransportProvider } from "@connectrpc/connect-query";
import { transport } from "@/lib/client";
import { AppShell } from "@/components/shell/AppShell";
import { Centered } from "@/components/ui/Centered";
import { useUIStore } from "@/lib/ui-store";
import { useActiveWorkspace, useCollections } from "@/lib/workspace-query";
import { useGvInvokeTypes } from "@/features/workspace/gv-types";
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
  const { collections, isPending, error } = useCollections();
  const { notFound } = useActiveWorkspace();
  // Above the early returns and outside the view switch: the Scripts view calls gv.invoke too,
  // so these types cannot be owned by the body editor.
  useGvInvokeTypes();
  // Nothing at all until the listing resolves: a flash of "No collection here" over a
  // workspace that has one reads as data loss.
  if (isPending) return null;
  // A listing that FAILED is not a workspace without collections — the scan may have hit
  // the size cap, or the root may be unreadable. Say so, because offering to create one
  // would be advice based on an answer we never got.
  if (error) return <Centered>Could not list this workspace: {error.message}</Centered>;
  // Sources and Scripts are equally meaningless without a collection: one gate
  // in front of the switch covers all three views. Two ways to have none — the
  // workspace lists nothing, or the collection we address is not there anymore.
  if (collections.length === 0 || notFound) return <NoCollection />;
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
