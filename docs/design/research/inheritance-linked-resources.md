# Inheritance, shared resources, environments & secrets — research

Source: completed exploration agent (2026-07-16), grounded in primary sources
(Bruno, Postman, Insomnia v5, Kreya, grpcurl). Distilled into `../storage.md`
§6–§7, §11; this file keeps the full detail. The agent noted it ignored
prompt-injection attempts found in fetched third-party content; all findings below
come from the cited primary sources.

## 0. TL;DR — key decisions

- **Every level of the tree carries an optional protojson `Config`**
  (`collection.json` at root, `folder.json` per folder-dir, `request.json` per
  request-dir). Absence of a field = *inherit*; `mode:none` = *explicitly
  disabled*; a value/ref = *override*.
- **Structural props** (target, auth, deadline, descriptor source) resolve
  **nearest-in-tree-wins**: `request → folder(nearest→root) → collection root →
  active-environment fallback`. **Map props** (metadata, vars) **merge** down the
  whole chain with per-key precedence and an `enabled:false` tombstone to disable an
  inherited key.
- **Shared resources are referenced by logical key into well-known top-level dirs**
  (`targets/`, `auth/`, `environments/`, `descriptors/`). A ref is just `"prod"` →
  `targets/prod.json`. Beats relative-paths (move-fragile), opaque-id+manifest
  (merge-hotspot, unreadable diffs), and symlinks (Windows/git-fragile). It's
  Kreya's id-ref model made human-readable, with the directory listing acting as the
  manifest.
- **Secrets never hit git.** Committed files reference secrets by *name only*
  (`{{secret.TOKEN}}` / `{{process.env.TOKEN}}`); env files list secret var *names*
  but not values; values live in the gitignored `.grpcview/` (file now, OS keychain
  later). Mirrors Bruno's `vars:secret [names]` + `.env`.
- **Derived data is local, not committed:** run history, resolved-schema/descriptor
  cache, active-environment selection, and secret values under gitignored
  `.grpcview/`.

## 1. Inheritance model (spec table)

Mechanics: **Structural** = pick one winner (nearest non-inherit ancestor).
**Merge** = union across chain, per-key winner by precedence. **Value ref** =
variable substitution at send-time.

| Property | Cascades? | Semantics | Resolution order (winner) | On-disk inherit/override/none |
|---|---|---|---|---|
| **Target** (host:port) | Yes | Replace (nearest) | request → folder(near→root) → collection → env `default_target` → error-if-unset | absent=inherit · `target:{mode:none}` · `target:{mode:set,ref:"prod"|inline:{…}}` |
| **Auth / call-creds** | Yes | Replace (nearest) | request → folder → collection → env `default_auth` → none | absent=inherit · `mode:none` stops cascade · `mode:set,ref/inline`. Postman "Inherit auth from parent"; Bruno `auth{mode:inherit\|none\|bearer…}` |
| **Metadata / headers** | Yes | Merge map; child can disable | union of levels; clash: request>folder(near→root)>collection>env; `enabled:false`=tombstone | entry present=add/override · `{key,enabled:false}`=disable inherited · omit=inherit |
| **Variables** | Yes | Merge namespace | runtime/script > request > **active-env** > folder(near→root) > collection | per key; absent inherits. Env-above-tree = Postman (open Q1) |
| **Deadline/timeout** | Yes | Replace (nearest) | request → folder → collection → target `default_deadline` → built-in | absent=inherit · `mode:none` · `mode:set,value:"30s"` |
| **Descriptor source** | Yes | Replace active; collection holds pool | request → folder → collection default; pool in `descriptors/` | absent=inherit · `descriptor:{mode:set,refs:["main"]}` |
| **TLS config** | Via target | Part of resolved target profile | inherits with target | fields on `targets/<key>.json` `tls{…}` |
| **Pre/post scripts** | Yes (later) | Chain (all run) | pre: collection→folder→request; post reversed | reserved `scripts`; deferred |
| **Environment (active)** | N/A overlay | Supplies var values + bottom-of-chain target/auth fallback | selected at runtime; `.grpcview/state.json` | one active at a time |

Drivers: **auth=replace, metadata=merge** is the universal convention (Postman,
Bruno, Insomnia). **Scripts chain, not replace** (confirmed Bruno *and* Postman:
collection→folder→request). **absent=inherit** exploits protojson field-omission —
a clean `folder.json` that only sets a header is tiny and diffs beautifully.

## 2. Shared/linked-resource representation

**Pick: logical-key reference resolved against fixed well-known top-level dirs.** A
ref is the resource's key (filename stem): `auth:{ref:"bearer-main"}` →
`auth/bearer-main.json`.

| Dimension | (a) Relative path | (b) Opaque id + manifest | (c) Symlink | **(chosen) Logical key + well-known dir** |
|---|---|---|---|---|
| Referential integrity | Weak; silent dangling on move | Strong but needs central index | OS-enforced but brittle | Load-time validated; ref=key, one canonical location |
| Breaks on **rename** of resource | Yes | No (id stable) | Yes | Ref must update — app does it as refactor; git-mv + validation flags strays |
| Breaks on **move of referrer** (reorg — the common op) | **Yes** | No | Depends | **No** — keys resolve from collection root, independent of referrer ✅ |
| Move of resource | Yes | No | Yes | Only if it leaves the well-known dir |
| Cycle detection | Manual | Manual | Hard | DAG check at load (only if resources ref resources) |
| Cross-platform | OK | OK | **Bad** (Windows symlinks) | OK — plain strings |
| Git-merge | Path noise | **Bad** — index file write-hotspot → conflicts | Bad | **Best** — ref is a word (`"prod"`); no central index; add/remove touches one file |
| Diff readability | Medium | **Poor** (`auth_ab12`) | Poor | **High** (`"auth":"prod"`) |

**Why it wins:** the most frequent action is reorganizing the tree; relative paths
break exactly then, logical keys don't (resolved from root, not referrer). Avoids
the id+manifest merge-hotspot by making the **directory listing the manifest**.
This is Kreya's `authId`/`importStreamId` model with human-readable keys.

**The one cost — renaming a shared resource is O(referrers).** Mitigations: (1)
app-driven rename rewrites all refs atomically; (2) manual `git mv` leaves refs
dangling → **load-time validation surfaces a broken-link badge**; (3) keys are
greppable. Cycles only possible once resources reference each other → DAG visit at
load.

## 3. On-disk layout

```
my-api/                              # user picks this dir, `git init` here
├── grpcview.json                    # manifest: formatVersion, name, type, ignore
├── collection.json                  # root Config: defaults for the whole tree
├── .gitignore                       # ".grpcview/\n.env\n"
├── .env.example                     # committed: secret var NAMES, no values
│
├── targets/                         # well-known shared-resource dirs
│   ├── local.json                   #   TargetProfile
│   └── prod.json
├── auth/
│   └── bearer-main.json             #   AuthProfile (token = {{secret.PROD_TOKEN}})
├── environments/
│   ├── local.json                   #   Environment (vars + secret var names)
│   └── prod.json
├── descriptors/
│   └── main.json                    #   DescriptorSource (reflection/protoset/proto)
├── scripts/                         #   (reserved, later) reusable *.js
│
├── UserService/                     # folder  = directory
│   ├── folder.json                  #   folder Config (overrides/adds)
│   ├── GetUser/                     # request = directory
│   │   ├── request.json             #     Config + service/method/type
│   │   └── body.json                #     the request message (protojson)
│   └── ListUsers/{request.json, body.json}
└── BillingService/{folder.json, Charge/{request.json, body.json}}

.grpcview/                           # LOCAL STATE — gitignored, never committed
├── state.json                       # activeEnvironment, UI last-opened, prefs
├── secrets.json                     # secret VALUES keyed by env→varname (or keychain)
├── history/UserService/GetUser/*.json   # run history (was Request.history[])
└── cache/descriptors/main.pb        # resolved FileDescriptorSet + JSON schema (derived)
```

Grounding: request=dir with `body.json` split out is validated by Kreya (bodies in
separate files to avoid "ugly diffs of JSON in a string"). History moves out of the
request into `.grpcview/history/`. Resolved `Service`/`Method`/`Message` (today
`Workspace.services[]`) become a derived cache in `.grpcview/cache/`, not committed.

### collection.json (root defaults)
```json
{
  "meta": { "name": "My API" },
  "target":     { "mode": "set", "ref": "prod" },
  "auth":       { "mode": "set", "ref": "bearer-main" },
  "descriptor": { "mode": "set", "refs": ["main"] },
  "deadline":   { "mode": "set", "value": "30s" },
  "metadata": [ { "key": "x-client", "value": "grpcview", "enabled": true } ],
  "vars": { "apiVersion": "v1" }
}
```

### folder.json (override + add + disable)
```json
{
  "meta": { "name": "BillingService", "order": 20 },
  "auth":     { "mode": "set", "ref": "bearer-billing" },
  "deadline": { "mode": "set", "value": "10s" },
  "metadata": [
    { "key": "x-team",   "value": "payments", "enabled": true },
    { "key": "x-client", "enabled": false }
  ]
}
```

### request.json + body.json
```json
{
  "meta": { "name": "GetUser", "order": 1 },
  "service": "user.v1.UserService", "method": "GetUser", "type": "UNARY",
  "target": { "mode": "inherit" }, "auth": { "mode": "inherit" },
  "deadline": { "mode": "inherit" },
  "metadata": [ { "key": "x-request-id", "value": "{{ $uuid }}", "enabled": true } ]
}
```
```json
{ "userId": "{{ userId }}", "includeProfile": true }
```

### shared-resource bodies
```jsonc
// targets/prod.json
{ "host": "{{ host }}", "port": 443,
  "tls": { "enabled": true, "serverName": "{{ authority }}", "insecureSkipVerify": false, "caCert": "certs/ca.pem" },
  "defaultDeadline": "30s" }
// auth/bearer-main.json
{ "bearer": { "token": "{{ secret.PROD_TOKEN }}" } }
// environments/prod.json
{ "name": "prod",
  "vars": { "host": "api.example.com", "authority": "api.example.com", "userId": "42" },
  "secretVars": [ "PROD_TOKEN" ], "defaultTarget": "prod", "defaultAuth": "bearer-main" }
// descriptors/main.json
{ "reflection": { "target": "prod" } }   // or { "protosetPath": "descriptors/main.pb" } or { "protoFiles": {…} }
```

### proto sketch
See `../storage.md` §11 for the message set (`Binding`, `*Binding`, `Config`,
`RequestFile`, `Target`, `TLS`, `Auth`, `Environment`, `MetadataEntry`, `RpcType`).
mTLS client cert/key = channel credential on `Target.TLS`; bearer/apikey = call
credentials injected as metadata.

## 4. gRPC-specific concerns

Minimal per-call surface (floor = grpcurl's flag set):

| Concern | grpcurl | Our model | Inherit/share |
|---|---|---|---|
| Target/endpoint | `host:port`, `-authority`, `-servername` | `targets/<key>.json` host/port/TLS.server_name | shared; **separated from descriptor source** (today conflated in `DescriptorSource.reflection = Server`) |
| Plaintext vs TLS | `-plaintext`/`-insecure` | `TLS.enabled`, `insecure_skip_verify` | part of target |
| Channel creds (mTLS) | `-cacert -cert -key` | `TLS.ca_cert/client_cert/client_key` | client_key is a **secret** → gitignored `certs/` or keychain ref |
| Call creds (bearer/apikey) | `-H "authorization: Bearer …"` | `auth/<key>.json` injected as metadata | shared; token=`{{secret.X}}` |
| Metadata/headers | `-H` (repeatable), `-bin` | ordered `MetadataEntry[]` + `binary` | **merge down tree**, `enabled:false` disables |
| Deadline | `-max-time`, `-connect-timeout` | `DeadlineBinding` + target default | nearest-wins |
| Descriptor source | reflection / `-protoset` / `-proto`+`-import-path` | `descriptors/<key>.json` oneof {reflection→target, protosetPath, protoFiles} | collection-level shared; reflection references a target(+auth) |
| Resolved schema | (re-fetches) | cached FileDescriptorSet + JSON schema in `.grpcview/cache/` | derived → local (optionally commit `descriptors/main.pb` for offline — open Q5) |

**Descriptor sources as shared resources:** keep the `DescriptorSource` oneof but
(a) move to `descriptors/<key>.json`, (b) reflection references a target (reuses
TLS/auth/metadata), (c) add a `proto_files` variant, (d) cache resolved set + schema
locally, not in `Workspace.services[]`. Kreya models this as `importStreamId`.

**Streaming (forward-compat now):** add `RpcType`; `body.json` tolerates an array
(`{"messages":[…]}`); responses stream into `.grpcview/history/`. Lock the shape so
unary→streaming isn't a format break; no streaming UX in MVP.

## 5. DB-vs-filesystem consequences

| DB capability (SQLite today) | Lost on FS | Mitigation |
|---|---|---|
| FK referential integrity | Refs can dangle after manual rename/delete | Resolve+validate at load; **broken-link badge** in UI; app-driven rename rewrites referrers |
| Transactions | Multi-file ops can partially fail | write-temp-then-`rename`; one-file-per-change; validate on load; **git is undo** |
| Unique constraints | Dup names; case-insensitive FS collisions | filename=unique key (FS enforces); guard case-fold on create; slug (open Q7) |
| Indexes/queries/joins | No cross-tree query | build in-memory model + indexes by walking tree at open; ref→referrers index |
| Cascade delete | Deleting shared resource orphans referrers | scan referrers → warn/confirm, or allow + flag dangling |
| Concurrency/locking | External editor/2nd process clobbers | fs-watch → reload; last-write-wins; **git merge** for multi-user; optional advisory lock |
| Schema migrations | No ALTER for format changes | `formatVersion` in `grpcview.json`; migrate-on-load; protojson ignores unknown fields |
| Deterministic ordering | No ORDER BY | explicit `meta.order` (Bruno `seq`) — **git-merge pain point**; alt: ordered `items:[]` in parent (open Q3) |
| Single-file backup | State across many files | the directory *is* the unit; clone/zip the folder; `.grpcview/` excluded |

## 6. MVP subset

**Build first:** (1) dir-tree loader/writer (folders/requests=dirs; grpcview/
collection/folder/request.json + body.json as protojson; walk-to-build in-memory
model replacing blob load/save). (2) Cascade engine for metadata (merge+tombstone),
target, deadline, descriptor (nearest-wins) + auth limited to bearer+raw metadata;
`INHERIT/NONE/SET` + absent=inherit. (3) Shared resources `targets/ auth/
environments/ descriptors/` with logical-key refs + load-time validation +
broken-link surfacing. (4) Environments + `{{var}}` substitution + active-env in
`.grpcview/state.json`; secrets by name via `.grpcview/secrets.json` + `.env`. (5)
Local state dir wiring (history, descriptor cache, secrets) + auto `.gitignore`. (6)
**Separate target from descriptor source.**

**Defer:** scripts & chaining (reserve block + `scripts/`); OAuth2 flows, mTLS
cert-management UX (support fields, skip UX); reusable body/script templates;
streaming UX (lock shape now); OS keychain (`zalando/go-keyring` later); cycle
detection beyond a trivial guard; committing resolved descriptor set.

## 7. Open questions

1. **Variable precedence: env above or below tree vars?** Recommended env *above*
   (Postman `Local>Data>Environment>Collection>Global`); Insomnia inverts it. Assumed
   Postman-style.
2. **Can an environment override structural props** (target/auth), or only supply
   vars + a bottom-of-chain default? Recommended: vars + fallback default only.
3. **Ordering representation:** per-item `meta.order` int (simple, merge-churn-prone)
   vs. ordered `items:[]` in parent `folder.json` vs. alphabetical.
4. **TLS/cert material location:** committed `certs/` (portable but commits CA;
   client key must NOT be committed) vs. `.grpcview/` bytes vs. keychain.
5. **Descriptor cache: commit or local-only?** Local = clean diffs but needs
   reflection/proto access; committing `descriptors/main.pb` enables offline use.
6. **Reflection coupling:** reflection source references a target key (recommended;
   DAG check covers cycles) vs. carries its own endpoint (today's `Server`).
7. **On-disk name vs display name:** slug dir/file + `meta.name` vs. forbid unsafe
   chars and use the name directly (Bruno uses the name).
8. **Metadata disable:** `enabled:false` tombstone (chosen) vs. `removeMetadata:[keys]`.
9. **Multi-collection repos / discovery:** find collection root by walking up to the
   nearest `grpcview.json` (like `.git`)?

## Appendix — prior-art cheat-sheet

- **Bruno** (closest): folder=dir, **request = single `.bru`** (we go dir-per-request),
  `bruno.json` + `collection.bru` + `folder.bru` + `environments/*.bru` + gitignored
  `.env`. Blocks: `meta{name,type,seq,tags}`, method, `headers`, `auth{mode:…}`,
  `vars:pre-request`, `script:pre-request/post-response`, `tests`. Auth modes: none,
  inherit, basic, bearer, apikey, digest, oauth2, awsv4, ntlm, wsse; `mode:inherit`
  inherits, `mode:none` disables. Cascade collection→folder→request; headers merge;
  auth nearest-non-inherit wins; scripts chain. Env: `vars{k:v}` + `vars:secret[names]`
  (values local, AES256). Secrets via `.env` (`{{process.env.X}}`) + committed
  `.env.sample`. `meta.seq` ordering (known merge-churn issue).
- **Postman**: "Inherit auth from parent" (≠ "No Auth"); collection/folder headers
  merged; pre-request+test scripts chain collection→folder→request; var precedence
  Local>Data>Environment>Collection>Global; Postman Vault = local un-synced secrets;
  secret variable type (masked). Storage = single Collection v2.1 JSON (no git-native
  file-per-request).
- **Insomnia v5** (`collection.insomnia.rest/5.0`, YAML): request-groups/requests/
  environments; stable `_id` prefixes `req_/fld_/grp_/env_/wrk_/spc_` for refs; base
  vs sub-envs (sub inherits, override diffs); var resolution folder-env > collection
  sub-env > base > global sub > global base; private (`isPrivate`) envs not synced;
  Nunjucks `{{ _.var }}`.
- **Kreya** (gRPC, git-friendly JSON): `.krproj` (auth, certs, import/reflection
  streams), `.krop` per operation (`methodFqn`, `metadata`, `authId`, `importStreamId`),
  `.krenv`, `.krpref`; **bodies in separate files** for clean diffs; user-specific
  data (secrets, active env, responses) local in appdata SQLite, `{{ env.x }}`; TLS
  certs by relative/env-var path, `insecureSkipVerify`. Strongest evidence for our
  approach.
- **grpcurl**: `-H name:value` (repeatable, `-bin`), `-plaintext`/`-insecure`/
  `-cacert`/`-cert`/`-key`/`-authority`/`-servername`, reflection vs `-protoset` vs
  `-proto`+`-import-path`, `-max-time`/`-connect-timeout`, `-d`/`-d @`.

### Sources
- Bruno: usebruno docs (collections, bru-lang tag reference, DeepWiki BRU format &
  auth systems), dotenv secrets, managing/manage variables blogs, issues #4829
  (seq), #2579 (inherit-auth).
- Postman: authorization details, variables & scopes docs, issue #11352.
- Insomnia: import/export reference + DeepWiki, environments docs, v5 proposal #7866.
- Kreya: bring-your-own-storage, default settings, ssl-tls, grpc importers.
- grpcurl: fullstorydev/grpcurl README.
