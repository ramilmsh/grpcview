# Frontend gates

Three, checking different things — all must be green:

```bash
cd ui && ./node_modules/.bin/tsc --noEmit -p tsconfig.json  # the real typecheck
bazel test //ui:test                                        # vitest
bazel build //ui:ui                                         # release bundle
```

**`bazel build //ui:ui` does not typecheck** — Vite/esbuild strips types without checking;
`tsc --noEmit` isn't a Bazel target, run by hand. `//ui:test` runs vitest with
`environment: "node"`, no jsdom — layout/focus/events need a browser (see root AGENTS.md,
"Verify through MCP or the CLI, not the browser").

See `src/AGENTS.md` for the SPA's view model, `src/components/tree/AGENTS.md` for the
collection tree, `src/theme/AGENTS.md` for the design language.
