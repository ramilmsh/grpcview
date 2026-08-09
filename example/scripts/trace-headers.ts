// MIDDLEWARE — attached per request ("RunScript (middleware)") by its specifier,
// `#/scripts/trace-headers.ts`, and run AFTER the body and metadata have been
// evaluated, on the outgoing message. It receives the ctx and returns the ctx to
// send; returning nothing keeps the ctx unchanged. Chained middleware runs in the
// order it is attached.
//
// A middleware is an ordinary module, so it imports what it needs — the same
// `#/scripts/ids` a body imports. There is no separate composition path any
// more, and nothing is reachable by name.
//
// `GvMiddleware` is a shipped type: annotating with it types `ctx` and gets a red
// squiggle in the editor for the wrong shape. Without it the shape is only
// checked at run time.

import { requestId } from "#/scripts/ids";

export const handle: GvMiddleware = (ctx) => ({
  ...ctx,
  metadata: {
    ...ctx.metadata,
    "x-trace-id": requestId("trace"),
    "x-body-bytes": String(JSON.stringify(ctx.body).length),
  },
  // The request being sent is a RunScript, so its `source` field is itself a
  // TypeScript expression. Wrapping it here changes what the workspace server
  // ends up evaluating, and the response comes back prefixed.
  body: { ...ctx.body, source: `"[traced] " + (${String(ctx.body.source)})` },
});
