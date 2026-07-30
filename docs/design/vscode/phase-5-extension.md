# Phase 5 — the extension

**Prereqs:** phases [1](./phase-1-collection-dir.md), [2](./phase-2-body-files.md),
[3](./phase-3-type-sinks.md), [4](./phase-4-request-management.md).
See [`README.md`](./README.md) for the approach and the sink model.

## What an extension actually is

A `.vsix` is a manifest plus JS that runs in the **extension host** — a Node process,
separate from VS Code's UI. It cannot touch VS Code's DOM; all custom UI is a webview
(an iframe). Rough size, with phases 1–4 done: **1.5–2.5k lines**, nearly all
plumbing.

## Deliverables

### 1. Manifest

- `contributes.customEditors` bound to `**/tree/**/request.json`.
- `contributes.viewsContainers` + `views` — the collection tree in the activity bar.
- `contributes.commands` + `keybindings` — mapped 1:1 from the phase-4 command registry.
- `contributes.configuration` — binary path, default target.
- `activationEvents` — on a `grpcview.json` in the workspace.

### 2. Backend supervisor

Spawn the Go binary with `--dir <workspaceFolder> --port 0`, read the chosen port from
stdout, health-check, restart on crash, kill on `deactivate`.

**Requires a backend change:** `http.ListenAndServe` (`service/service.go:82`) cannot
report the port the OS chose for `:0`. Switch to `net.Listen` + `http.Serve` and print
the resolved address. Also bind `127.0.0.1` rather than `net.IPv4zero`
(`service.go:80`) — an editor extension has no business listening on all interfaces.

### 3. Connect client

`@connectrpc/connect-node` in the extension host against that port. In remote/WSL/
Codespaces the extension host runs on the remote machine, so the binary runs there too
and this is a local connection; only webview→localhost needs `asExternalUri`.

### 4. Custom editor provider

A webview per `request.json` hosting the *existing* React cockpit, with a
`postMessage` ↔ Connect bridge (or `asExternalUri` and let the webview speak Connect
directly, with the CSP `connect-src` opened for it).

**All body/metadata mutations go through `WorkspaceEdit`/`TextEdit`, never
`fs.writeFile`.** The custom editor owns `request.json`, but bodies live in sibling
files (phase 2), so editing the body from the cockpit makes *that* document dirty —
possibly on a tab that is not open. Routing through `WorkspaceEdit` keeps undo and
dirty state coherent between the cockpit and any open `body.ts`. The cockpit's ⌘S
saves the related document set.

"Reopen with → Text Editor" then comes free, which is Bruno's dual-view feature.

### 5. Tree data provider

The collection, with `resourceUri` set on nodes so git decorations land on them, and
`TreeDragAndDropController` calling the existing reorder RPCs.

### 6. Commands

Invoke, Pick method (`QuickPick`), Set target, New request/folder, Refresh sources,
Reveal body as file. Plus a CodeLens strip on `body.ts` (`▶ Invoke · Target: … ·
⟳ Types`) if reveal-as-file gets used in practice.

### 7. `DiskSink` + producer in the extension host — optional

Gated on wanting reveal-body-as-file to actually work. Generate on activation and on
the invalidating mutations, write `.grpcview/types/` plus `types.stamp`.

- The producer is the phase-3 module, imported into the extension host (Node runs
  `protoc-gen-es` exactly as the browser does). It must live here rather than in the
  webview, because types have to stay fresh with no cockpit open.
- `DiskSink` renders the per-request part as a generated `import type … as
  RequestMessage` line in `body.ts`, rewritten on method change — the `declare global`
  form collides across requests once tsserver sees them all.
- Write only when the digest differs, so the standalone UI and the extension producing
  simultaneously does not churn mtimes.
- **Commit `tsconfig.json`; gitignore `.grpcview/types/`.** Consequence to accept: a
  fresh clone shows squiggles until the backend has run once. The extension activates
  on `grpcview.json`, so in practice types exist before a body can be opened.

### 8. Packaging

Platform-specific `.vsix` (`vsce package --target darwin-arm64|linux-x64|win32-x64`),
since a Go binary is bundled. Publish to Marketplace **and** Open VSX.

## Verify

In VS Code, against this repo's `requests/` collection:

- Invoke a request from the custom editor and see the response.
- Tabs, dirty state, ⌘S, ⌘P, git decorations in both the Explorer and the custom tree.
- Rename via the tree; confirm the open editor follows.
- Drag to reorder; confirm `grpcview.json` ordering updates.
- Kill the backend process externally; confirm restart and recovery.
- "Reopen with → Text Editor" on a `request.json` shows raw protojson.
- With `DiskSink` on: reveal `body.ts`, confirm native IntelliSense on the input
  message, change the method in the cockpit, confirm the import line is rewritten.

## Open questions

- Bridge via `postMessage` or let the webview speak Connect over `asExternalUri`? The
  latter is less code but puts a localhost URL in the CSP and behaves differently in
  remote workspaces.
- One webview per request, or one reused panel? Per-request is what makes tabs work,
  but each carries a Monaco instance — watch memory with many tabs open, and consider
  `retainContextWhenHidden` off plus state serialization.
- Does the extension ship the standalone UI build verbatim, or a variant build with the
  Nocturne theme swapped for VS Code's `--vscode-*` CSS variables?
- Where does the collection tree live — a custom view container, or lean entirely on
  the file Explorer plus a `FileDecorationProvider`?
