// A script with an npm dependency. `dayjs` is bundled into the sandbox by
// esbuild at run time from the curated embedded allowlist — no node_modules, no
// network fetch. The whole import graph is resolved in one pass, so a package
// name works from an imported file exactly as it works from a body.
//
// The sandbox clock is virtualised and starts pinned at 2022-01-01T00:00:00Z, so
// this is deterministic: two runs of the same script produce the same stamp.

import dayjs from "dayjs";

export const stamp = (format = "YYYY-MM-DDTHH:mm:ss[Z]"): string =>
  dayjs().format(format);
