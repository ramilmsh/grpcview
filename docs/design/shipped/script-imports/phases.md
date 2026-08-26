# script imports — phases

Read [`decisions.md`](./decisions.md) first; it is normative. [`README.md`](./README.md)
carries the reasoning and the esbuild measurements.

Each phase is one background agent unless noted. A phase lands on `trunk` as a checkpoint
commit once the orchestrator has re-run its gates.

| phase | scope | status |
|---|---|---|
| P0 | protos + store: `.ts` files replace `script.json`, `ScriptKind` deleted | landed |
| P1 | scripting: `@/` + `#/` resolver plugin, metafile-keyed cache, computed-specifier rejection | landed |
| P2 | scripting: `grpcview:` modules replace the `gv` global; prelude and `compose.go` delete | landed |
| P3 | workspace: generator machinery deleted, middleware by path, `RunScript` without kind | landed |
| P4 | CLI + MCP surfaces | landed |
| P5 | new RPC + Monaco resolution + Scripts view | landed |
| P6 | `example/` migrated by hand, AGENTS.md rewritten, docs reconciled | landed |
| P7 | the two defects the browser pass found: the hidden wrapper, and auto-import | landed |

## P0 — protos and store

- `grpcview/store/v1/storage.proto`: delete `ScriptKind` and `Script`; delete
  `Collection.scripts`.
- `grpcview/v1/workspace.proto`: delete `ScriptKind`; `Script` becomes
  `{ string path = 1; string source = 2; }`.
- `grpcview/v1/service.proto`: `CreateScriptRequest{collection, path}`,
  `UpdateScriptRequest{collection, path, source, new_path}`,
  `DeleteScriptRequest{collection, path}`, `RunScriptRequest` drops `kind`.
- `service/store/scripts.go`: a recursive walk of `<collection>/scripts/**/*.ts`;
  create/update/delete write, rewrite and remove files; rename is a move. Delete
  `writeScriptOrder`, `scriptSlugSet`, `reconcileScripts`, `scriptEntry`.
- `service/store/convert.go`: delete both kind converters.

## P1 — the resolver

- `service/scripting/bundler.go`: `Roots{Workspace, Collection}` threaded through
  `compile` / `compileEntry` / `esbuildBundle`; `pathResolverPlugin` with filter
  `^[@#]/` in the `extra` slot; `withinDir` guard on both roots.
- `Metafile: true` on every build; cache key over every resolved input's path + digest.
- Reject computed `require(…)` / `import(…)` before the build.
- `service/scripting/profiles.go`: `WithWorkspaceRoot`.

## P2 — `grpcview:` modules

- Four shims — `grpcview:invoke`, `grpcview:assert`, `grpcview:metadata`,
  `grpcview:request` — resolved like the capability modules.
- `service/scripting/marshal.go`: delete `buildGvPrelude`, `writeGlobal`, the
  `request`/`vars`/`secrets`/`env` globals and `Input.Vars`/`Secrets`/`Env`. `console` and
  `fetch` preludes stay.
- Delete `service/scripting/compose.go`, `generatorResolverPlugin`,
  `buildBundleComposed`, `buildEntryBundleComposed`.
- Per-run data moves to internal `__grpcview_*` prelude names the shims read.
- Test that a failed `assert` still reports the author's line (`gvAssert` frame filtering
  survives bundling).

## P3 — the workspace layer

- `service/workspace/invoke.go`: delete `loadGenerators`, `transitiveGenerators`,
  `calledNames`, `genCallRe`; `mentionsInherit`'s text gate becomes an import-graph check
  (keep the separate empty-script case, which inherits unconditionally).
- `service/workspace/middleware.go`: resolve specifiers to files; delete
  `loadMiddlewareSources`.
- `service/workspace/runscript.go`: no kind; `script` addresses by path.
- `service/workspace/workspace.go`: pass the workspace root to the engine.

## P4 — CLI and MCP

- `service/cli/write.go`: `script ls` prints paths; no `--kind`.
- `service/mcp`: `create_script` / `update_script` / `delete_script` / `run_script`
  schemas lose kind and address by path.

## P5 — frontend

- New RPC listing workspace `.ts` sources as `{path, content}[]`.
- Monaco `compilerOptions.paths` for `@/*` and `#/*`; app-level registration.
- `gv.d.ts` becomes four `declare module "grpcview:…"` blocks plus `GvMiddleware`.
- Scripts view without kinds; middleware picker by path.
- Delete `generator-libs.ts`, `script-kinds.ts`.

## P6 — dogfood and docs

- `example/`: `scripts/*.ts` files, bodies rewritten to `require(…)`, middleware by path.
- AGENTS.md: "Request authoring model", "The `gv` global", the scripts parts of "The CLI"
  and "The MCP server".
- Move this directory to `docs/design/shipped/`.

## P7 — what the browser pass found

Both defects were invisible to the Go tests and to `bazel test //ui/...`, and both were
found by opening a migrated request in the browser.

- The editors treated "starts with the hidden wrapper" as the only canonical shape, so a
  body that now begins with `import` lines was wrapped a second time — two default exports.
  `module-sniff.ts` decides the two forms instead: a module is shown whole with nothing
  hidden, an expression keeps the hidden wrapper it always had.
- Auto-import did not work at all, for a reason no amount of configuration would fix; see
  `decisions.md` §8 for what Monaco's worker refuses and what we ship instead.
