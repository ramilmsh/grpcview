# Design language

**v4** — "elevation, rim light, push and pull": dark layered surfaces (ground → shell →
panel → raised/hover, sunken wells for inputs/editors), single indigo accent `#6366F1`,
10px radii (16 panels/menus, 24 app card, 6 badges), raised controls with an inset top rim,
controls brighten on hover and `:active` pushes in; cards stay static, only their controls
react. Inter UI 14px, JetBrains Mono for code, Phosphor icons.

`--v-*` in `nocturne.css` is the token contract; use it in new code. Nocturne-era names
(`--color-*`, `--radius-*`, `--shadow-*`, `--panel*`, `--line`, `--ok/--err/--warn`) are
aliases onto it. `app-tokens.css` holds app component classes; `theme.json` is the Monaco
theme (background = `--v-well`), loaded by `monaco-theme.ts`.
