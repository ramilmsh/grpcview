# go-git integration for the git-aware UI — research

Source: completed exploration agent (2026-07-16). Read-only research; verify the
one flagged API-name detail against the pinned version before coding. Distilled
recommendations live in `../storage.md` §10; this file keeps the implementation
detail.

## Grounding

- Git wiring lands **alongside** the file-tree storage rewrite (not on the current
  sqlite/blob code). Tree items map to the filesystem: folder = dir, request = dir
  with config + `body.json`. So every item is a directory and its status is the
  rollup of files beneath it.
- RPC style to mirror: Connect, one unary method per op, keyed by `workspace_name`
  + `repeated string path` (parent-folder segments) + `string item_name`.
- The UI already ships Monaco (`monaco-editor` + `@monaco-editor/react`), which
  includes a **DiffEditor** — decisive for the diff design.
- Bazel: Go deps flow from `go.mod` via `go_deps.from_file` + explicit `use_repo`.
  Add go-git = edit `go.mod` + `bazel mod tidy` + gazelle. go-git is pure Go /
  CGO-free (`pjbgf/sha1cd`), ~8–10 pure-Go transitive deps; adds no CGO (the repo's
  only CGO today is `mattn/go-sqlite3`, which the rewrite removes).

**Version:** use **`github.com/go-git/go-git/v5` v5.18.0**, pinned. v6 is alpha
(transport rewrite; alpha.1/.2 had CVE-2026-45022, fixed alpha.3) — revisit only
for network sync.

## Recommendation: hybrid, go-git-first

Default to go-git for everything; detect a `git` binary on `PATH` and use it only
for the ops where go-git is weak/slow. Baseline must be pure-Go go-git (the product
promise is "download one binary, open a directory, it works" — can't assume a git
toolchain, esp. containers/locked-down machines). exec-`git`, when present,
is a graceful perf/correctness upgrade for a small set of ops. Keep both behind one
`gitBackend` interface. MVP ships go-git-only; exec upgrade is phase 2+. Do NOT go
exec-only (breaks no-dependency promise) or go-git-only forever (leaves monorepo
users slow, blocks credential-helper sync).

## Capability matrix (go-git v5.18)

| Capability | Support | Notes / gotchas |
|---|---|---|
| Open repo | Good | `PlainOpen(path)` → `ErrRepositoryNotExists`. |
| Detect repo / find root / nested-in-parent | Good, DIY | `PlainOpenWithOptions(path, &PlainOpenOptions{DetectDotGit:true})` walks parents. Recommend own `findRepoRoot(dir)` walking up for `.git` — answers is-repo, nested-in-parent (root ≠ workspace dir), and gives the root path for the path-prefix math. No clean `Repository.RootPath()`. |
| Bare vs worktree | Good | `PlainOpen` auto-detects; `Worktree()` → `ErrIsBareRepository`. Treat bare as "no git UI." |
| Working-tree status per path | Good (correctness) / Poor (perf) | `Worktree.Status()` → `map[string]*FileStatus` keyed **relative to repo root, forward-slash**. `FileStatus{Staging, Worktree StatusCode}`; codes Unmodified/Modified/Added/Deleted/Renamed/Copied/UpdatedButUnmerged. Two-axis like porcelain. |
| Status perf lever | Partial | `StatusWithOptions(StatusOptions{Strategy})`; an Empty/sparse strategy returns only changed files. **Confirm exact exported constant name against v5.18.** Doesn't remove the walk cost. |
| Diff commit-vs-commit (patch) | Good | `tree.Patch(other).String()` = unified diff; rename detection since v5.1. |
| Diff worktree-vs-HEAD / staged-vs-HEAD (patch) | Poor (no one-call helper) | No `git diff`/`--cached` for working tree/index. **Sidestep: return old/new text, diff in Monaco.** |
| History/log for a path | Good | `Log(&LogOptions{FileName})` or `PathFilter`. No `--follow` (renames not tracked across history). |
| Blame | Works / slow | `git.Blame(commit, path)`; slow + memory-heavy. Defer. |
| Current branch | Good | `Head()` → `ref.Name().Short()`; detached when not a branch. |
| List branches | Good | `Branches()` (local); `References()` for remotes. |
| Ahead/behind vs upstream | Not built-in | No `rev-list --left-right --count`. Compute via `commit.MergeBase` + count. Needs a fresh `Fetch` (network+auth). Defer, or label "as of last fetch." |
| Stage (add) | Good | `Worktree.Add(path)` / `AddWithOptions{..., SkipStatus:true}` (faster). |
| Commit | Good | `Worktree.Commit(msg, &CommitOptions{...})`. **Gotcha:** identity from `user.name`/`user.email`; **fails if unset** → settings fallback. |
| Unstage a single path | Poor | No per-file unstage; `Reset{Mixed}` resets whole index. → exec `git restore --staged <file>`. |
| Discard a single worktree change | Poor | `Checkout{Force}` discards everything; no per-path scope. → exec `git restore <file>`. |
| Create branch | Good | `CreateBranch(...)` or `Checkout{Create:true, Branch}`. |
| .gitignore honored by status | Mostly | Reads `.gitignore` + `.git/info/exclude`. Global `core.excludesFile` only via `/etc/gitconfig` — `~/.config/git/ignore` not reliably honored. Descends into nested sub-repos/ignored dirs (issue #1896), inflating cost + false untracked. |
| Remotes / fetch / push | Partial, complex | HTTP(token)/SSH, but **no OS credential-helper** — can't reuse the user's stored git creds. Defer sync; strongest case for exec-`git`. |

## Per-item status aggregation

Item at UI path `[a,b,c]` + name `n` → dir `<repoRelWorkspaceDir>/a/b/c/n`, where
`repoRelWorkspaceDir` = workspace dir relative to repo root (empty when workspace
*is* the root; non-empty when nested in a monorepo).

Single-call + prefix-index (never per-item `Status()`):

1. One `StatusWithOptions(sparse)` → map of only-changed files (the one expensive
   call).
2. One pass: for each changed file, derive a badge, then walk its ancestor dirs up
   to the workspace root, merging the badge into each by fixed precedence. Cost
   O(changedFiles × depth).
3. Item query = prefix lookup: dir present ⇒ its rolled-up badge; absent ⇒ clean.

Badge derivation (from the two-axis code):
- staging≠Unmodified && worktree≠Unmodified → `staged+modified`
- staging Untracked/Added not in HEAD → `new`
- staging≠Unmodified → `staged`
- worktree Modified/Deleted/Renamed → `modified`
- UpdatedButUnmerged → `conflict`

Folder precedence (worst-wins): `conflict > staged+modified > staged > modified >
new > clean`.

Caching (essential — Status() is O(all worktree files)): cache rollup keyed by
`{repoRoot, HEAD hash, index mtime}` with short debounce; invalidate on mutation
RPCs and after WorkspaceService writes `body.json`. Phase 2: back with `fsnotify`
watch + streaming `WatchStatus`. Ignore hygiene matters (ensure `bazel-*`,
`node_modules/` ignored); scope the walk to the workspace subdir when nested.

## Proposed RPC surface — new `GitService`

Separate service in `grpcview/v1/git.proto`, registered next to
`WorkspaceService` in `service/service.go`, implemented in `service/git/git.go`.
Reuses the same addressing (`workspace_name` + `path` + `item_name`).

```proto
message ItemRef {
  string workspace_name = 1;
  repeated string path = 2;   // parent-folder segments
  string item_name = 3;       // empty => whole workspace root
}

enum GitBadge {
  GIT_BADGE_UNSPECIFIED = 0;
  GIT_BADGE_CLEAN = 1;
  GIT_BADGE_NEW = 2;              // untracked / added-not-in-HEAD
  GIT_BADGE_MODIFIED = 3;        // unstaged changes
  GIT_BADGE_STAGED = 4;
  GIT_BADGE_STAGED_MODIFIED = 5;
  GIT_BADGE_CONFLICT = 6;
}

message ItemStatus { repeated string path = 1; string name = 2; GitBadge badge = 3; }

// Reads
message RepoInfoRequest { string workspace_name = 1; }
message RepoInfoResponse {
  bool is_repo = 1; bool is_bare = 2;
  string repo_root = 3; string workspace_subdir = 4;   // "" if root; nested-in-parent when non-empty
  string current_branch = 5; bool detached_head = 6; string head_short_hash = 7;
  bool has_upstream = 8; int32 ahead = 9; int32 behind = 10;   // 0/deferred until Fetch
  bool git_binary_available = 11;                              // hybrid hint for the UI
}

message StatusRequest { string workspace_name = 1; }
message StatusResponse { repeated ItemStatus items = 1; int32 changed_file_count = 2; bool truncated = 3; }

enum DiffKind { DIFF_KIND_WORKTREE_VS_HEAD = 0; DIFF_KIND_STAGED_VS_HEAD = 1; }
message FileDiff {
  string file = 1; string old_text = 2; string new_text = 3;
  bool exists_in_head = 4; bool is_binary = 5;
}
message DiffRequest { ItemRef item = 1; DiffKind kind = 2; }
message DiffResponse { repeated FileDiff files = 1; }   // request-item => body.json + config

message LogRequest { ItemRef item = 1; int32 limit = 2; string before_hash = 3; }
message CommitInfo {
  string hash = 1; string short_hash = 2;
  string author_name = 3; string author_email = 4;
  google.protobuf.Timestamp when = 5; string summary = 6; string message = 7;
}
message LogResponse { repeated CommitInfo commits = 1; bool has_more = 2; }

message ListBranchesRequest { string workspace_name = 1; }
message ListBranchesResponse { repeated string branches = 1; string current = 2; }

// Writes (phase 2)
message StageRequest   { repeated ItemRef targets = 1; }
message UnstageRequest { repeated ItemRef targets = 1; }
message DiscardRequest { repeated ItemRef targets = 1; }   // restore worktree to HEAD
message CommitRequest  {
  string workspace_name = 1; string message = 2; repeated ItemRef targets = 3; bool all = 4;
  string author_name = 5; string author_email = 6;         // identity fallback
}
message MutationResponse { StatusResponse status = 1; }    // fresh status so UI re-renders
message CreateBranchRequest { string workspace_name = 1; string name = 2; bool checkout = 3; }
message SwitchBranchRequest { string workspace_name = 1; string name = 2; }

service GitService {
  rpc GetRepoInfo(RepoInfoRequest) returns (RepoInfoResponse);
  rpc GetStatus(StatusRequest) returns (StatusResponse);
  rpc GetDiff(DiffRequest) returns (DiffResponse);
  rpc GetLog(LogRequest) returns (LogResponse);
  rpc ListBranches(ListBranchesRequest) returns (ListBranchesResponse);
  // writes (phase 2)
  rpc Stage(StageRequest) returns (MutationResponse);
  rpc Unstage(UnstageRequest) returns (MutationResponse);
  rpc Discard(DiscardRequest) returns (MutationResponse);
  rpc Commit(CommitRequest) returns (MutationResponse);
  rpc CreateBranch(CreateBranchRequest) returns (MutationResponse);
  rpc SwitchBranch(SwitchBranchRequest) returns (MutationResponse);
  // phase 3
  rpc WatchStatus(StatusRequest) returns (stream StatusResponse);   // server-stream on fs changes
  // Fetch/Pull/Push … deferred (auth)
}
```

All unary for MVP (matches existing style; UI refetches after mutations, and
mutation responses embed fresh status). Only good streaming candidate is
`WatchStatus` (phase 3). **Diff returns old/new text, not a server patch** — the UI
renders with Monaco DiffEditor; sidesteps go-git's missing worktree/index diff and
is better UX for small protojson files. Server patch text only for commit-to-commit
history (`tree.Patch().String()`).

## Performance & limitations

- `Status()` is the bottleneck (long-standing issue; walks + hashes untracked
  files, descends into ignored/nested dirs). Mitigate as above; exec-`git status
  --porcelain=v2` fallback for large/monorepo cases.
- No worktree/index diff patch; no `--follow`; no native ahead/behind; no per-file
  unstage/discard; no credential helpers. Each designed-around or an exec candidate.
- Commit identity must be resolved or commits fail.
- Global gitignore not reliably honored (cosmetic).

## MVP vs deferred

- **MVP (go-git only):** `GetRepoInfo` (is-repo gate, branch, git_binary_available),
  `GetStatus` (badges via single-call + prefix-index), `GetDiff`
  (WORKTREE_VS_HEAD → Monaco). Zero write risk, zero network/auth.
- **Phase 2 (writes, local):** `GetLog`, `Stage`, `Commit`, `Discard`, `Unstage`
  (last two via exec `git restore` when present), branches, STAGED_VS_HEAD diff.
- **Phase 3 (sync + reactive):** `WatchStatus` (fsnotify), ahead/behind (with
  Fetch), Fetch/Pull/Push — favor exec-`git` (credential helpers).
- **Deferred indefinitely:** blame, merge/rebase, stash.

## Open questions

1. Nested-in-monorepo: support workspace as a subdir (scope status/diff/commit) or
   only when it IS the repo root? Affects path-prefix design + whether commits touch
   files outside the subtree.
2. Auto-init: offer `git init` on a plain dir, or only surface git UI for repos?
3. Commit identity: read from git config (error if unset) or a settings screen?
4. Sync auth: require `git` binary (OS credential helpers) or token/SSH settings UI
   for go-git native transport?
5. Big-repo ceiling: is opening a large monorepo real? If so, exec-`git status`
   fallback in MVP rather than phase 2?
6. Multi-workspace lifetime: multiple repos open at once? Governs repo-handle +
   cache + fs-watch lifecycle (one cache per repo root).
7. Diff granularity for a request item (config + body.json): combined multi-file
   diff or body.json only by default?

## Implementation touch-list

`grpcview/v1/git.proto` (new) + regen; `service/git/git.go` (new;
`gitBackend` interface + `goGit` impl, `execGit` later) + `service/git/BUILD.bazel`;
register handler in `service/service.go`; add go-git to `go.mod` + `bazel mod tidy`
+ gazelle; frontend `ui/src/lib/gitStore.ts` (new zustand slice) + badges in
`ui/src/components/TreeView.tsx` + diff view reusing Monaco DiffEditor.
