// SCENARIO — the whole test harness is `assert`: it throws on the first failure,
// so silence is a pass. Run it from the Scripts view, or with
//
//   grpcview script run scripts/smoke.ts --collection example
//
// Nothing grpcview-specific is a global. `invoke` and `assert` are imported from
// the `grpcview:` modules, the same way `dayjs` is imported from npm and
// `requestId` from a file. `console` and `fetch` stay ambient, because a
// third-party library expects to find them there.
//
// Every request this drives is a SAVED one, invoked through the same pipeline the
// UI uses — bodies, metadata, folder inheritance and middleware all included.
// The one request it cannot drive is Workspace/Streaming/InvokeStreaming:
// invoke refuses a streaming path outright.

import { invoke } from "grpcview:invoke";
import { assert } from "grpcview:assert";

const listed = await invoke("Workspace/ListCollections");
assert("a request with no target of its own invokes", listed.ok);
assert(
  "grpcview lists the collection this scenario lives in",
  listed.body.collections.some((c: { id: string }) => c.id === "example"),
);

// Folder metadata is inherited even though the request never spreads it itself.
assert(
  "the Workspace folder's metadata is inherited",
  listed.requestMetadata["x-demo-suite"][0] === "workspace",
);

// A plain protojson body — valid JSON is already valid TypeScript.
const described = await invoke("Workspace/DescribeMethod (JSON)");
assert("a plain protojson body invokes", described.ok);
assert(
  "the bazel source wins over reflection",
  described.body.sourceId === "bazel://grpcview/v1:grpcviewv1_proto",
);
assert(
  "and its descriptor kept the .proto doc comments reflection strips",
  described.body.protoText.includes("Deliberately cheap"),
);

// The second argument of invoke lands in the target's `params`.
const params = await invoke("Workspace/RunScript (params)", { expression: "6 * 7" });
assert("params reach the target's body", params.body.value === "42");

// Imports compose: this request's body and its metadata both import scripts.
const generated = await invoke("Workspace/RunScript (generators)");
assert(
  "the imported id round-trips through the server",
  /^"gv_[0-9a-f]{12} at 2022-01-01T00:00:00Z"$/.test(generated.body.value),
);
assert(
  "dayjs runs on the sandbox's pinned clock",
  generated.requestMetadata["x-sent-at"][0] === "2022-01-01T00:00:00Z",
);

// Middleware runs last, on the outgoing message, and imports the same script the
// bodies do — something the old composed-generator path could not do.
const traced = await invoke("Workspace/RunScript (middleware)");
assert(
  "middleware rewrote the body",
  traced.body.value === '"[traced] before middleware"',
);
assert(
  "middleware stamped a trace id built by an imported script",
  /^trace_[0-9a-f]{12}$/.test(traced.requestMetadata["x-trace-id"][0]),
);

// invoke chaining: a body that invokes another saved request to build itself,
// asking grpcview to then call grpcview again.
const chained = await invoke("Workspace/Invoke (chained)");
assert("the chained body resolved and invoked", chained.ok);
assert("the inner call came back with a response", chained.body.response.response.length > 0);

"13 assertions passed"
