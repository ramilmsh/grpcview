# grpcview — descriptor explorer plan (follow-up to feature 2)

**Status:** Planning only (this doc). **Not started.** This is the dedicated
follow-up to the message-shape-visibility feature: the *TS-types* view ships first
(see [`gv-features-plan.md`](./gv-features-plan.md) §"Feature 2"); this doc plans
the richer **`.proto` descriptor explorer** that the TS view deliberately does not
attempt.

**Why a separate track.** The TS-types view answers "what is the shape of *this*
message" cheaply by reusing the in-browser `protoc-gen-es` output. It intentionally
drops two things that view cannot recover: proto **field numbers** and leading
**comments/docstrings**. Surfacing those means rendering real `.proto` text from
descriptors — and doing that *well* means a navigable, multi-file, read-only source
browser, i.e. a small feature in its own right, not a checkbox on the TS view.

**Vision (user's framing).** A **descriptor explorer**: a dedicated read-only
Monaco view seeded with the actual `.proto` files, **one browsable set per
descriptor source**, that you navigate into when you want the full detail —
including jumping from a type reference to its definition.

---

## What we already have (feasibility)

- **`jhump/protoreflect` v1.18.0 is already a direct dependency** (`go.mod`), and
  its `desc/protoprint` package is vendored and in `go.sum` — **no new download**,
  builds offline. It is not yet imported anywhere.
- The backend already resolves full `*desc.FileDescriptor` graphs (carrying
  transitive deps, incl. WKTs) for every descriptor source — from reflection via
  `grpcreflect.NewClientAuto` + `FileContainingSymbol`, and from uploaded sets via
  `desc.CreateFileDescriptorsFromSet` (`service/workspace/workspace.go`).
- `protoprint.Printer.PrintProtoToString(fd)` renders a **whole
  `FileDescriptor`** to `.proto` text (comments + field numbers preserved). It is
  **file-granular** — which suits an explorer perfectly: we render *every* file in
  the source's graph, not one sliced message.
- The wire already carries per-message coordinates (`Message{package, name, file}`,
  `Method.input`/`output`), so a "view this message's `.proto`" deep-link has the
  file path and symbol in hand with no new resolution.

The `gRPC Request Client Design` Claude Design project (Nocturne) is the visual
reference; re-fetch with `DesignSync get_file` when building UI.

---

## Architecture

### Backend — render descriptors to `.proto` on demand

Add an on-demand RPC (do **not** carry rendered text on the `Workspace` payload —
it would bloat every load and recompute constantly):

```proto
// service.proto
rpc ExploreDescriptorSource(ExploreDescriptorSourceRequest)
    returns (ExploreDescriptorSourceResponse);

message ExploreDescriptorSourceRequest {
  string workspace_name = 1;
  int32  source_index   = 2;   // which DescriptorSource in the workspace
}
message ExploreDescriptorSourceResponse {
  repeated ProtoFile files = 1;         // every file in the source's graph
  repeated SymbolLoc symbols = 2;       // fully-qualified name -> file + line (nav index)
}
message ProtoFile { string path = 1; string text = 2; }
message SymbolLoc { string full_name = 1; string file = 2; int32 line = 3; kind kind = 4; }
```

Handler (new, e.g. `service/workspace/explore.go`):
- Take the source's resolved `[]*desc.FileDescriptor` (reuse the same resolution
  path as schema loading; for a reflection source that may re-reflect if not
  cached).
- `protoprint.PrintProtoToString(fd)` per file → `ProtoFile{path, text}`. Configure
  the `Printer` for stable, diff-friendly output (sorted elements off — preserve
  source order — comments on).
- Build the **symbol index** by walking each descriptor's messages/enums/services
  (`fd.GetMessageTypes()`, nested types, enums, services/methods) → `SymbolLoc`
  with `full_name`, `file`, and a `line` located either from `SourceCodeInfo` or by
  locating `message <Name>`/`enum <Name>`/`service <Name>` in the rendered text.
  This index is what makes go-to-definition possible **without** a proto language
  server.
- Decide WKT handling: either include `google/protobuf/*` files (complete but
  noisy) or omit them and render their references as non-navigable leaves. Lean
  **omit-and-mark** for v1.

Bazel: adding `desc/protoprint` needs a `gazelle` BUILD regen (new dep line in
`service/workspace/BUILD.bazel`).

### Frontend — a navigable read-only Monaco browser

- **Home:** the **Sources** view (`ui/src/features/sources/`) already lists the
  workspace's descriptor sources — the natural place to hang a "Browse `.proto`"
  affordance per source ("one per descriptor source").
- **The explorer surface** (new component, e.g.
  `ui/src/features/sources/DescriptorExplorer.tsx`):
  - A **file tree / list** on the left (the `ProtoFile.path`s, grouped by package
    dir), a read-only Monaco editor on the right.
  - One read-only Monaco **model per file**, at a virtual URI
    (`proto://<sourceIdx>/<path>`), language `proto` (register a minimal
    `proto` Monarch tokenizer for syntax highlighting — Monaco has none built in).
  - **Navigation:** register a Monaco `DefinitionProvider` for `proto` that, on the
    identifier under the cursor, resolves it against the `SymbolLoc` index and
    opens/reveals the target model+line. Plus a symbol quick-open (all
    `full_name`s) and plain text search. This is the "navigate to it if you want
    details" behavior.
  - Read-only throughout (`readOnly: true`); reuse the Nocturne Monaco theme.
- **Deep link from the TS-types view:** the feature-2 `TypesModal` gains a "View
  `.proto`" action that opens the explorer for the method's source and reveals the
  input/output message via the symbol index — connecting the cheap shape view to
  the full detail.

---

## Phases

1. **Backend rendering + symbol index.** `ExploreDescriptorSource` RPC + handler;
   `protoprint` wiring; `gazelle` regen. *Verify:* `bazel test
   //service/workspace/...` — for a workspace with a known source, the response
   contains the expected files with field numbers + comments, and the symbol index
   maps a known message full-name to the right file/line.
2. **Explorer view (files + read-only Monaco + `proto` highlighting).** Sources-view
   entry point; file tree; per-file models. *Verify:* browser — open a source,
   browse its files, confirm field numbers + comments render.
3. **Navigation (definition provider + symbol quick-open).** *Verify:* browser —
   click a type reference → jumps to its definition in the right file; quick-open a
   symbol by name.
4. **Deep link from `TypesModal`.** "View `.proto`" reveals the selected message.
   *Verify:* browser — from a method's TS-types modal, jump into the explorer at the
   correct message.

---

## Open questions (to resolve when this track is picked up)

- **WKT files:** render `google/protobuf/*` in-explorer (complete) or omit and mark
  references non-navigable (less noise)? — lean omit-and-mark for v1.
- **Reflection freshness:** re-reflect on explore, or require the source to be
  cached? A reflection-only source could be offline when the user explores.
- **Line numbers:** trust `SourceCodeInfo` from `protoprint`, or locate declarations
  by text scan? (Text scan is robust to printer settings.)
- **Editing:** strictly read-only (assumed), or eventually a jumping-off point for
  editing an uploaded descriptor set? — read-only for the foreseeable future.
