# Filesystem references & secret management — research

Source: a follow-up exploration agent (2026-07-16), grounded in primary sources
(specs + official docs + source). Distilled into `../storage.md` §7 (refs) and §8
(secrets). Trust level: the report's claims are cited inline to primary sources;
verify library/API specifics at implementation time.

> **Operational note:** the agent's own spawned subagents returned no usable work
> (~4s, zero tool calls) and their result payloads contained **prompt-injection
> attempts** (a bogus "was this written by Claude" task; a fake `<functions>` block
> pushing a nonexistent `security-review-scoped` tool). It ignored both and researched
> directly against primary sources. Untrusted content reached agents via both web
> fetches and the subagent-result channel — worth a separate look at the harness.

## Topic A — referencing shared resources without a DB

Two real tools bracket the space:
- **Insomnia** — immutable prefixed stable `_id` (`req_`, `fld_`, `wrk_`, `env_`,
  `jar_`); links via `parentId`. Rename/move-tolerant, but `fld_a1b2c3` is opaque in
  a diff.
- **Bruno** — filesystem path + name; folder tree, `.bru` per request, `folder.bru`
  for folder settings, `environments/*.bru`. Perfectly readable git-native diffs, but
  rename/move is a breaking manual op.

### Comparison

| Dimension | Relative path (`../../shared/auth/prod.json`) | Stable id + index (`auth_ab12`) | Symlink |
|---|---|---|---|
| Referential integrity | None inherent; resolve+validate at load; dangles silently | Strong if index authoritative; dangling cheaply detectable | OS `stat`, but link can dangle |
| Rename referent | **Breaks** — rewrite every referrer | **Survives** — id unchanged; index moves | **Breaks** — link stores target path |
| Rename referrer | Survives in place; breaks if depth changes | **Survives** | Survives |
| Move referent | **Breaks** | **Survives** | **Breaks** |
| Move referrer | **Breaks** (relative base changes) | **Survives** | **Breaks** (depth changes) |
| Cycle detection | App graph walk needed | App graph walk needed | OS `ELOOP` after 40 resolutions; still want app-level |
| Cross-platform | Portable if normalized POSIX `/` | Fully portable (opaque strings) | **Worst** — git `core.symlinks=false` checks links out as **plain text files** (silent corruption) |
| Git-merge | Excellent (plain text, independent). Trap: git rename = add+delete; doesn't update path-strings in *other* files → silent cross-branch breakage | Ids stable, rarely collide. Trap: a *central* manifest is a merge hotspot → make the index **derivable**, not committed | mode `120000`, blob=target path; dominated by checkout corruption |
| Diff readability | **High** (self-describing) | **Low** (`auth_ab12` opaque) | Medium (shows target) |

### Reference-resolution standards (for the resolver)
- **JSON Schema `$id`/`$ref`:** `$id` pins an explicit **base URI**; relative `$ref`
  resolves against that base; `$defs` reused via JSON-Pointer `#/$defs/name`. **Lesson:
  relative refs are only well-defined because a base is pinned — always resolve
  relative paths against a defined anchor (the collection root), never CWD.**
- **JSON Pointer (RFC 6901):** `/`-separated tokens; escapes `~1`→`/`, `~0`→`~`
  (decode `~1` first).
- **Cycle detection:** three-color DFS (white/gray/black); gray node = back edge =
  cycle; O(V+E). Kahn's topological sort is the BFS equivalent.

### How other tools land
- **Postman:** UUIDs everywhere (`_postman_id`, item `id`, API `uid`). Stable-id.
- **VS Code `.code-workspace`:** relative/absolute folder paths; "Save As" rewrites
  relative paths — rename-tolerance delegated to tooling.
- **TypeScript project references:** relative `path`, resolved relative to the config
  file it originated in.
- **pnpm workspaces:** name-based `"foo": "workspace:*"` — a **logical name**, not a
  path; refuses to resolve outside the workspace. Closest analog to a
  human-readable stable id.
- **Bazel labels:** `//package:target` (absolute) or `:target`; **no `../` across
  packages** — logical names path-rooted at the workspace.

### RECOMMENDATION — id-primary + path-hint, with a derivable (uncommitted) index
1. **Filesystem layout is the organization** (Bruno-style): folders are folders, one
   protojson file per resource → readable, git-native, browsable without the app.
2. **Every referenceable resource carries a stable `id` in its own meta**
   (Insomnia-style), generated once, never changed. Because the id lives *inside* the
   file, there is **no central manifest to merge-conflict** — rebuild the index by
   scanning at load.
3. **A reference stores BOTH** the authoritative `ref` (id) and a human-readable
   `path`/key hint:
   ```json
   "auth": { "ref": "auth_ab12", "path": "auth/prod.json" }
   ```
   Hint → readable diff; id → survives rename/move.
4. **Resolution:** by `id` first (index); fall back to `path`; if they disagree
   (id found elsewhere), **auto-heal the hint + warn** — that's how you catch a
   git add+delete rename. Resolve relative paths against the **collection root**.
5. **Cache the id→path index in the gitignored state dir** (never commit) so it can't
   be a merge hotspot; rebuild on change.
6. **Validate + detect cycles at load** (three-color DFS); report dangling refs and
   cycles with the offending chain.
7. **No symlinks for committed refs** — a `core.symlinks=false` checkout writes them as
   plain text files, silently corrupting collections. (Symlinks fine for local gitignored
   convenience only.)

Optional simplification: **id-as-filename** (slug *is* the id) collapses id and path
into one, keeps diffs readable, makes the index trivial — but a rename still changes
the visible name, so keep an in-file id as source of truth.

*(Relation to our locked decisions: this is a superset of the §7 "logical key"
model. Our tree-item choice is slug-dir + `meta.name` — the id-primary model extends
the same stable-identity idea to shared-resource refs. Decide at Phase 2.)*

## Topic B — keeping secrets out of git

### Dotenv (Bruno's model)
Commit `.env.example`/`.env.sample` (structure, no values); gitignore `.env`. Bruno
loads `.env` at the collection root and references `{{process.env.NAME}}` (dotted:
`{{process.env['a.b']}}`). Docs: "always add `.env` to `.gitignore`" + share a
`.env.sample`.

### OS keychain from Go

| | `zalando/go-keyring` | `99designs/keyring` |
|---|---|---|
| macOS | shells to `/usr/bin/security` | native Keychain |
| Linux/BSD | Secret Service (D-Bus, GNOME) | SecretService + KWallet + KeyCtl + Pass |
| File fallback | **None** | **Encrypted `FileBackend`** (jose + scrypt, passphrase) |
| Backend select | fixed per-OS | `AllowedBackends` whitelist |
| Size limits | documented: macOS ≲3000B → `ErrSetDataTooBig` | varies; file backend unconstrained |

**Recommendation: `99designs/keyring`** — same native keychains **plus** an encrypted
file fallback for headless/CI, and `AllowedBackends` to force file mode. (Used by
aws-vault.) Note size limits → long tokens go to the file/state store, not the OS
keychain, on constrained backends.

### Local state dir (separate from the committed collection)
Keep run history, resolved-schema cache, resolved secret values out of the tree. Two
placements:
- **Co-located gitignored `.grpcview/`** — easy to find, travels with the collection;
  leak-safety depends on `.gitignore`.
- **XDG/OS user dirs keyed by collection id** — leak-proof even without `.gitignore`:
  `XDG_STATE_HOME` (`~/.local/state`) for run **history**; `XDG_CACHE_HOME`
  (`~/.cache`) for **schema cache**; resolve via `os.UserConfigDir`/`os.UserCacheDir`
  or `adrg/xdg`.

**Recommended:** do both — gitignored `.grpcview/` for cache+history co-located, and
OS keychain (99designs) for secret values (encrypted-file backend under the state dir
for headless).

**Key secrets by stable id/logical name, never by path** (path breaks on move — same
failure as Topic A). Mirrors git `credential-store` (keyed by URL) and OS keychains
(service+account): keychain **service = `grpcview:<collectionId>`**, **account =
`<secretName>`/`<authProfileId>`**.

### Reference pattern
Committed side stores only a reference; value resolved at runtime.
```jsonc
// committed: auth/prod.json
{ "id": "auth_ab12", "type": "bearer", "token": "{{secrets.PROD_API_TOKEN}}" }
// committed: environments/prod.json
{ "id": "env_prod", "vars": { "HOST": "api.prod:443", "JWT": "{{process.env.JWT_TOKEN}}" } }
```
Resolution order for `{{secrets.NAME}}`/`{{process.env.NAME}}`:
1. real process env; 2. gitignored `.env` (dotenv); 3. OS keychain
(`service="grpcview:<collectionId>"`, `account="NAME"`); 4. encrypted-file fallback
under the state dir (headless/CI).

**OAuth:** commit only the pointer (token URL, scopes, non-sensitive client id +
`clientSecretRef: "{{secrets.…}}"`); store client secret + obtained access/refresh
tokens in keychain/state keyed by `collectionId + authProfileId`. Analogous to
Postman `{{vault:…}}` (machine-local, not synced, not committed).

### Generated `.gitignore`
```gitignore
# grpcview: secrets & machine-local state (never commit)
.env
.env.*
!.env.example
!.env.sample
.grpcview/
**/*.local.json
**/*.secret.json
environments/*.local.*
.DS_Store
Thumbs.db
```
Ship a committed `.env.example` alongside.

## Sources
JSON Schema structuring; RFC 6901 (JSON Pointer); Insomnia import-export & storage;
Bruno collections & dotenv secrets; Postman collection-format & Vault; VS Code
multi-root workspaces; TypeScript project references; pnpm workspaces; Bazel labels;
git `core.symlinks`; path_resolution(7) ELOOP; three-color DFS
cycle detection; git-credential-store; zalando/go-keyring; 99designs/keyring; XDG
Base Directory spec. (Full URLs in the agent transcript.)
