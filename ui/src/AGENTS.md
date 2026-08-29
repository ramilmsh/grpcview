# Views (no router)

The SPA has no URL router. `App.tsx` renders one `AppShell`, switching the main pane on a
zustand field (`activeView`, `lib/ui-store.ts`): **Workspace** (default) — tree + editor +
response pane; **Sources** — priority-ordered source list; **Scripts** — authoring `.ts`
files, addressed by path; **Daemons** (`features/daemons/`) — every grpcview daemon
registered on this machine (`ServerService.ListServers`, not `WorkspaceService`), open one
in a new tab or stop it — deliberately not a live dashboard (5s poll) and not a control
panel (no restart/forget). Server state via `@connectrpc/connect-query` +
`@tanstack/react-query`; local/view state in `zustand`.

# Browser verification hook (editors)

The body and metadata editors register on `window` keyed by model URI
(`lib/editor-debug.ts`): `window.__grpcviewEditors["file:///grpcview/request/body.ts"]`
(body) and `.../metadata.ts` (metadata). Each is a Monaco `IStandaloneCodeEditor`:
`.getValue()` reads the exact buffer, `.setValue(src)` drives it (sidesteps auto-closing
brackets that corrupt naively *typed* code). The Scripts scratchpad editor is not
registered.
