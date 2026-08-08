// An open tab is a client the server cannot see. The idle timer is armed by silence, not by
// disconnection, so a tab left open on a daemon that was started for it — `grpcview open` —
// loses its server after the idle window while looking perfectly alive. A heartbeat is the tab
// saying it is still here.
import { createClient } from "@connectrpc/connect";
import { ServerService } from "@grpcview/v1/service_pb";

import { transport } from "./client";
import { durationMs } from "./format";

const MIN_INTERVAL_MS = 30_000;
const MAX_INTERVAL_MS = 10 * 60_000;
const BEATS_PER_IDLE = 3;

// Read from the server rather than assumed, so a daemon started with a different --idle-timeout
// retunes the tab that is holding it open.
function intervalFor(idleMs: number): number {
  return Math.min(MAX_INTERVAL_MS, Math.max(MIN_INTERVAL_MS, idleMs / BEATS_PER_IDLE));
}

// Runs for the page's lifetime and is never torn down: there is no unmount to hang a disposer on.
export function startKeepalive(): void {
  const client = createClient(ServerService, transport);
  let timer: ReturnType<typeof setTimeout> | undefined;
  let stopped = false;

  const schedule = (ms: number) => {
    if (stopped) return;
    clearTimeout(timer);
    timer = setTimeout(beat, ms);
  };

  const beat = async () => {
    if (stopped) return;
    let next = MIN_INTERVAL_MS;
    try {
      const info = await client.serverInfo({});
      const idleMs = durationMs(info.idleTimeout);
      // Nothing to hold open: a hand-run server lives until someone stops it.
      if (idleMs <= 0) {
        stopped = true;
        return;
      }
      next = intervalFor(idleMs);
    } catch {
      // A server mid-restart is not a reason to give up holding the next one open.
    }
    schedule(next);
  };

  // A backgrounded tab has its timers throttled and eventually frozen, so the beat that was due
  // while it was hidden never fired. Beating on the way back cannot revive a server that died
  // in the meantime — a page cannot start a process — but it is the earliest point the tab can
  // tell whether the one it is holding is still there.
  const onVisible = () => {
    if (document.visibilityState === "visible") void beat();
  };
  document.addEventListener("visibilitychange", onVisible);

  void beat();
}
