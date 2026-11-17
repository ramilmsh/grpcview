// Lists every grpcview daemon registered on this machine (ServerService.ListServers, not
// WorkspaceService — see AGENTS.md "The workspace daemon"), and lets the user open one in a
// new tab or stop it. Scope is deliberately exactly that: no "forget", no "start by path", no
// restart-on-skew action.
import { useState } from "react";
import {
  useMutation,
  useQuery,
  useTransport,
  createConnectQueryKey,
} from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import {
  listServers,
  stopServer,
} from "@grpcview/v1/service-ServerService_connectquery";
import type { ServerEntry } from "@grpcview/v1/service_pb";
import { ArrowClockwise, ArrowSquareOut, Stop } from "@/components/ui/icons";
import { Button, IconButton } from "@/components/ui/Button";
import { MainPanel } from "@/components/ui/MainPanel";
import { Dialog } from "@/components/ui/Dialog";
import { Tag } from "@/components/ui/Tag";
import { Centered } from "@/components/ui/Centered";
import { idleTimeoutLabel, uptimeLabel } from "@/lib/format";
import { daemonLabel, daemonStatus, sortedDaemonRows } from "./daemon-rows";

// Modest, not aggressive: this is a machine inventory, not a live dashboard, and every tick
// is one more request the app makes for a view that is usually not even open.
const POLL_INTERVAL_MS = 5000;

function DaemonRow({
  entry,
  busy,
  onOpen,
  onStop,
}: {
  entry: ServerEntry;
  busy: boolean;
  onOpen: (entry: ServerEntry) => void;
  onStop: (entry: ServerEntry) => void;
}) {
  const running = daemonStatus(entry) === "running";
  const label = daemonLabel(entry.workspaceRoot);

  return (
    <div
      // The same static v4 raised card as a definition source row.
      className="srcrow flex items-center gap-[14px]"
      style={{ padding: "14px 16px" }}
    >
      <span
        className="dot"
        style={{
          background: running ? "var(--v-ok)" : "var(--v-text-muted)",
        }}
        title={
          running
            ? "Answered just now"
            : "Registered, but nothing answered on its port — the process is probably gone"
        }
      />
      <div style={{ minWidth: 0, flex: 1 }}>
        <div
          className="font-mono truncate"
          style={{ fontSize: 14, color: "var(--v-text)" }}
          title={entry.workspaceRoot}
        >
          {label}
        </div>
        <div
          className="truncate"
          style={{ fontSize: 12, color: "var(--v-text-2)" }}
          title={entry.workspaceRoot}
        >
          {entry.workspaceRoot}
        </div>
      </div>
      <span
        className="font-mono truncate"
        style={{
          fontSize: 12,
          color: running ? "var(--v-text-2)" : "var(--v-text-muted)",
          flex: "none",
        }}
        title={
          running ? "port · pid · version" : "Registration outlived its process"
        }
      >
        {running
          ? `:${entry.port} · pid ${entry.pid} · ${entry.version || "dev"}`
          : "stale registration"}
      </span>
      {running && (
        <span
          className="font-mono truncate"
          style={{
            fontSize: 12,
            color: "var(--v-text-muted)",
            flex: "none",
          }}
          title="uptime · idle timeout"
        >
          up {uptimeLabel(entry.startedUnix)} ·{" "}
          {idleTimeoutLabel(entry.idleTimeout)}
        </span>
      )}
      {entry.current && (
        <Tag variant="accent" title="This page is being served by this daemon">
          this one
        </Tag>
      )}
      {entry.skewed && (
        <Tag
          variant="neutral"
          title="Answers for this workspace, but is running a different build than the server answering this page"
        >
          skew
        </Tag>
      )}
      <IconButton
        title="Open in a new tab"
        aria-label={`Open ${label}`}
        onClick={() => onOpen(entry)}
        disabled={!running}
      >
        <ArrowSquareOut size={15} />
      </IconButton>
      <IconButton
        title="Stop this server"
        aria-label={`Stop ${label}`}
        onClick={() => onStop(entry)}
        disabled={!running || busy}
      >
        <Stop size={15} />
      </IconButton>
    </div>
  );
}

export function DaemonsView() {
  const transport = useTransport();
  const qc = useQueryClient();
  const query = useQuery(
    listServers,
    {},
    { refetchInterval: POLL_INTERVAL_MS },
  );
  const stop = useMutation(stopServer);
  // The one row this page is served by, held only while its stop is pending confirmation —
  // every other row stops with no prompt.
  const [confirmCurrent, setConfirmCurrent] = useState<ServerEntry | null>(
    null,
  );

  const refresh = () => {
    void qc.invalidateQueries({
      queryKey: createConnectQueryKey({
        schema: listServers,
        transport,
        input: {},
        cardinality: "finite",
      }),
    });
  };

  const doStop = (entry: ServerEntry) => {
    stop.mutate(
      { workspaceRoot: entry.workspaceRoot },
      {
        onSuccess: () => {
          setConfirmCurrent(null);
          refresh();
        },
      },
    );
  };

  const onStop = (entry: ServerEntry) => {
    // Stopping the daemon serving this very page kills the connection this view is about to
    // use to refresh itself, so it is the one row that gets a confirmation.
    if (entry.current) {
      setConfirmCurrent(entry);
      return;
    }
    doStop(entry);
  };

  const onOpen = (entry: ServerEntry) => {
    window.open(`http://127.0.0.1:${entry.port}`, "_blank", "noopener");
  };

  const rows = sortedDaemonRows(query.data?.servers ?? []);

  return (
    <MainPanel>
      <div
        className="flex items-start gap-[16px]"
        style={{
          flex: "none",
          padding: "22px 28px 20px",
          borderBottom: "1px solid var(--v-line)",
        }}
      >
        <div>
          <h1
            style={{
              margin: 0,
              fontSize: 24,
              fontWeight: 500,
              letterSpacing: "-0.01em",
            }}
          >
            Daemons
          </h1>
          <p style={{ margin: "4px 0 0", color: "var(--v-text-2)" }}>
            Every grpcview server registered on this machine.
          </p>
        </div>
        <Button
          className="ml-auto"
          onClick={refresh}
          disabled={query.isFetching}
        >
          <ArrowClockwise size={14} />
          Refresh
        </Button>
      </div>

      <div style={{ flex: 1, overflow: "auto", padding: "20px 28px 32px" }}>
        {query.isPending ? (
          <Centered>Loading…</Centered>
        ) : query.isError ? (
          <div
            className="text-muted"
            style={{ fontSize: 13, padding: "16px 0", lineHeight: 1.6 }}
          >
            Could not list daemons: {query.error.message}
          </div>
        ) : rows.length === 0 ? (
          <div
            className="text-muted"
            style={{ fontSize: 13, padding: "16px 0", lineHeight: 1.6 }}
          >
            No grpcview daemons are registered on this machine.
          </div>
        ) : (
          <div className="flex flex-col" style={{ gap: 10, maxWidth: 880 }}>
            {rows.map((entry) => (
              <DaemonRow
                key={entry.workspaceRoot}
                entry={entry}
                busy={stop.isPending}
                onOpen={onOpen}
                onStop={onStop}
              />
            ))}
          </div>
        )}
      </div>

      <Dialog
        open={confirmCurrent !== null}
        onClose={() => setConfirmCurrent(null)}
        title="Stop this server"
        width={420}
      >
        <p className="dialog-body">
          <strong>
            {confirmCurrent ? daemonLabel(confirmCurrent.workspaceRoot) : ""}
          </strong>{" "}
          is the server answering this very page. Stopping it will disconnect
          this tab — the page will stop working until another daemon is started
          for this workspace.
        </p>
        <div className="dialog-actions">
          <Button onClick={() => setConfirmCurrent(null)}>Cancel</Button>
          <Button
            variant="danger"
            onClick={() => confirmCurrent && doStop(confirmCurrent)}
            disabled={stop.isPending}
          >
            {stop.isPending ? "Stopping…" : "Stop it anyway"}
          </Button>
        </div>
      </Dialog>
    </MainPanel>
  );
}
