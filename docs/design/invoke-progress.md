# Request execution (Invoke) + metadata — progress

Status: **complete and verified.** First slice of runtime behavior on top of the
storage rewrite: the frontend can now actually *send* a gRPC request and see the
result, with request/response metadata.

## What landed

A new unary RPC on `WorkspaceService`:

```proto
rpc Invoke(InvokeRequest) returns (InvokeResponse);
```

- `InvokeRequest` carries the (possibly unsaved) editor state inline —
  `service`, `method`, `body` (JSON string), `metadata`
  (`google.protobuf.Struct`) — plus `workspace_name`/`path`/`item_name` for
  addressing, and an optional `target` (`Server`) override.
- `InvokeResponse.response` reuses the existing `Request.Response` message
  (status, response bytes, request/response metadata, latency, timestamp).

### Backend — `service/workspace/invoke.go`

1. **Target** = the request's explicit `target`, else the workspace's first
   reflection source.
2. **Dial** the target (insecure, or TLS via system roots when the `Server` has
   a TLS block).
3. **Resolve the method descriptor by reflecting the target.** Reflection
   sources don't persist full descriptors, so the schema is fetched fresh per
   call (`grpcreflect` → `ResolveService` → `FindMethodByName`).
4. **Build** the request as a `dynamic.Message` from the JSON body (empty body →
   `{}`).
5. **Send** request metadata via `metadata.NewOutgoingContext`; invoke with
   `grpcdynamic.Stub.InvokeRpc`, capturing header + trailer.
6. **Return** the marshaled JSON response, status, merged response metadata,
   latency, and timestamp.

**Error model — deliberate:** a gRPC-level failure of the *invoked* call (e.g.
the target returns `NOT_FOUND`) is **data**, surfaced in `response.status` so the
UI renders it. Only failures grpcview itself can't get past — no target,
unreachable/unresolvable schema, a body that doesn't fit the request type,
streaming method — become Connect errors.

**Metadata** maps `google.protobuf.Struct` ⇄ `metadata.MD`: string or
list-of-string values; a single MD value becomes a string, multiple a list.
Binary (`-bin`) keys are base64 in the Struct and raw bytes on the wire (decoded
outbound, encoded inbound) so the Struct stays valid UTF-8.

### Frontend

- `store.invoke(item, body, metadata)` — calls the RPC, stores the result per
  request key in `responses`/`responseErrors`/`invoking` (ephemeral; survives
  tab switches, not reloads).
- `store.updateRequestMetadata(item, metadata)` — persists `draft_metadata` via
  `UpdateRequest`, mirroring `updateRequestData` for the body.
- `MetadataEditor` — key/value rows ⇄ `JsonObject`.
- `ResponsePanel` — status badge (code + name, green/red), latency, timestamp,
  pretty-printed JSON body, response-metadata list.
- `Workspace.tsx` — a Send button plus a Body/Metadata tabbed request pane and a
  response pane; local body/metadata state is the source of truth while editing.

## Verification

- `bazel build //...` (45 targets, incl. UI) — green. `bazel test //...` — green.
- Frontend type-checks clean (tsserver) and bundles.
- End-to-end against the dev backend reflecting **itself**:
  - **JSON codec:** success (workspace round-trips), a gRPC error becomes data
    (`code: 5` in `response.status`), invalid method → Connect `not_found`,
    invalid body → Connect `invalid_argument`.
  - **Binary codec** (`application/proto`, the UI's actual wire format, via a
    `protoc`-encoded `InvokeRequest`): request metadata echoed, response metadata
    (content-type/date/grpc-accept-encoding/vary), latency + timestamp — Struct
    metadata round-trips in binary too.

## Deferred (noted, not silently dropped)

- **Run history** is not persisted yet. The proto is forward-compatible
  (`InvokeRequest.path`/`item_name` already address the request); wire it under
  `.grpcview/history/` per `storage.md` §4 and load it into `Request.history`.
- **Streaming** methods return `Unimplemented` (only unary today).
- **Target selection UI** — the frontend sends no explicit `target`, so calls go
  to the workspace's reflection source. A target picker is the natural next step
  (and aligns with the `storage.md` §6 target-vs-source split).
- **Descriptor-set sources** at invoke time — resolution is reflection-only for
  now (matches what `AddDescriptorSource` supports).
- **Editor model keying** (pre-existing): Monaco models are keyed by method, so
  two requests sharing a method share a body model. Untouched here.
