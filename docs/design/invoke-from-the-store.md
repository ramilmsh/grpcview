# Invoke resolves from the store, not from the wire

**Status:** Design + migration plan. **Nothing implemented.** Verified against the
tree on **2026-08-02**.

**Premise.** The collection on disk is the source of truth. The editor buffer is a
*representation* of it — loaded from the store on startup, edited, written back. It
is not a separate authority, and Send should not treat it as one.

Today it does: the UI sends the buffer over the wire and the backend invokes those
bytes without ever reading what is saved. This document argues the inversion — **Send
resolves the saved request server-side, exactly as `gv.invoke` already does** — lists
the six things in the way (all verified), and sequences the migration.

---

## 1. What happens today (verified)

`ui/src/features/workspace/RequestWorkspace.tsx:251-280` — pressing Send calls the
`Invoke` RPC with:

```ts
{ workspaceName, path, itemName,
  service: request.service, method: request.method,
  body: b,                  // the live editor buffer
  metadataScript: md,       // the live editor buffer
  target: targetOverride }  // draft.target ?? request.target
```

Everything that decides *what gets sent* travels on the wire. `path`/`itemName` are
along for the ride so the server can record history and fold in ancestor-folder
metadata — they do not select the body.

Persistence is separate and **debounced**: `DEBOUNCE_MS = 400`
(`RequestWorkspace.tsx:27`), one timer per field slot (`body`, `meta`, `target`;
`:164-180`) firing `UpdateRequest`. Some edits already bypass the debounce and persist
immediately — method changes (`:203`), middleware (`:222`), rename (`:236`).

The proto states the rationale for the wire body: it "carries the editor's current
source… so a send never depends on a prior `UpdateRequest` landing first."

## 2. Why that is the wrong shape

**It makes the buffer a second source of truth.** After a send, "what ran" and "what
is saved" can differ — for the 400 ms debounce window, and indefinitely if an
`UpdateRequest` failed silently. The Timeline then records a run that cannot be
reproduced from the collection, which is the one artifact the collection exists to
hold.

**It forces every other surface to reimplement resolution.** A CLI, an MCP tool or a
VS Code command has no editor buffer. To reach the same behavior each one must `Get`
the workspace, walk the tree, pull `draftBody`/`draftMetadataScript` out of the item
and echo them back — a read-modify-send round trip that duplicates resolution on the
client and races anyone editing in the UI. That is three reimplementations of
something the server already does.

**The server already has the correct path, and it is in production use.**
`Collection.ResolveRequest` (`service/store/fs.go:427`) → `invokeUnary`
(`service/workspace/invoke.go:96`) is what `gv.invoke` runs for script-to-script calls
(`service/workspace/gvinvoke.go:98-145`). It resolves the saved body, metadata script,
middleware chain, folder-inherited metadata and target, and it accepts `params`. The
UI is the only caller that does *not* use it.

**The consistency argument is symmetric, and the other direction is cheaper.** Either
every surface sends a body, or none does. "Every surface sends a body" means shipping
the editor's job to three clients that have no editor. "None does" means one flush
before Send.

## 3. Target shape

**Send becomes:** flush pending debounced saves → await them → call the
saved-request invoke RPC with `{path, item_name, params}`. The server resolves
everything else. The response shape is unchanged, so the Timeline, the response pane
and the history plumbing are untouched.

`Invoke`-with-a-body does not disappear — it becomes what it should always have been:
the **ad-hoc** call, for a method that has no saved request. That is a real use case
(a shell user with a JSON file, an agent invoking a method it just discovered), it is
simply not what the UI's Send button is.

**Naming, once the UI has migrated.** The end state is `Invoke(path, item_name,
params)` — the primary verb, named after what the product calls it — and the ad-hoc
one renamed to something that says so (`InvokeMethod`). Getting there without a
flag-day: land the new RPC as `InvokeSaved` alongside today's `Invoke`, migrate the
UI, then do the rename as a mechanical commit. Breaking the proto is explicitly
acceptable at this stage; doing it while two callers are mid-migration is not.

## 4. What is in the way (all verified)

Six blockers. B1–B3 must be solved; B4–B6 are decisions.

**B1 — the 400 ms debounce.** Send currently cannot lose an edit because the edit is
on the wire. Once it is not, Send must flush every pending timer for the active
request, `await` the resulting `UpdateRequest`s, and only then invoke. A failed save
must **fail the send with a visible error**, not send the previously saved state
silently. This is the whole of the "a send never depends on a prior `UpdateRequest`
landing first" invariant, deliberately reversed — the proto comment gets rewritten,
not worked around.

**B2 — compose extras for client-streaming/bidi are ephemeral.** Verified
`RequestWorkspace.tsx:196-201`: `onMessagesChange` persists only `messages[0]` as
`draft_body`; the extras live in the draft and are never written to the store. A
saved-request invoke therefore cannot reproduce a multi-message send. Two options:

1. **Persist them** — add `repeated string draft_messages` to `Request` and have the
   compose list write to it. Makes cs/bd requests reproducible from the collection,
   which they currently are not (a property worth having on its own merits, and a
   prerequisite for the VS Code track ever representing them as files).
2. **Leave streaming on the wire form** — homogenize unary only. Cheaper, but leaves
   exactly the split this document is trying to remove.

Recommendation: (1), sequenced as its own step so the unary migration is not blocked
behind it.

**B3 — lazy body migration means the store can hold a body the server cannot
evaluate.** Verified `RequestWorkspace.tsx:84-100`: the seed runs
`migrateBodyToTs(request.draftBody || "{}")` in the editor, and the comment is
explicit that "the migrated form is only persisted once the user edits". So a legacy
JSON/token body opened but not edited is canonical **in the buffer only**; the store
still holds the legacy form. Today that is invisible because the buffer is what gets
sent. Under the new shape the server would read the legacy body and evaluate it as
TypeScript, where a bare JSON object misparses (the same reason
`migrateBodyToTs` exists for the compose extras, `:290-294`). Fix: migrate on read in
the store/handler, or run a one-time migration over every request on load. Either
way this must land **before** the UI switches, or old collections break on first send.

**B4 — history re-run passes values that are not the saved draft.** Verified
`:366-370`: `onRerunHistory` calls `setDraft` and immediately invokes with the
historical body/metadata, and `setDraft` does **not** schedule a save — so re-running
from history deliberately does not mutate the stored request. Under the new shape,
re-run uses the RPC's per-run `body` override rather than persisting first. That
override needs to exist for this reason alone; a saved-request RPC without it forces
re-run to either overwrite the user's draft on disk or drop the feature.

**B5 — the target override is draft state with its own debounce slot.** Verified
`:162` (`draft?.target ?? request.target`) and `:209-212` (`updateTarget` set-flag,
separate timer). Same treatment as B1: flush before send. Worth noting because it is
the one field where "unsaved" is currently invisible in the UI — a stale target sends
to the wrong server, which is a worse failure than a stale body.

**B6 — "unsaved" becomes a user-visible state.** Once the store decides what runs,
the user needs to be able to tell that a save is pending or failed. Today there is no
indicator because there is nothing to indicate. Minimum: disable/annotate Send while
a flush is in flight, and surface a save error where the invoke error would go.

## 5. Migration

Each step ends green on `bazel test //...` and is verified in a browser against the
real binary under an isolated `HOME`.

**U0 — migrate-on-read for legacy bodies (B3).** Server-side, in the store or the
resolve path. No UI change. Verify: a collection whose `draft_body` is raw JSON
invokes correctly through `gv.invoke`, which already reads from the store and
therefore already has this bug latent.

**U1 — flush-before-send (B1, B5, B6).** Still sending the wire body, so behavior is
unchanged and the change is independently revertible. Extract the per-slot timers into
a `flushPending(key): Promise<void>`; `onInvoke` awaits it; a rejected save aborts the
send with an error in the response pane; Send shows a pending state while flushing.
Verify: type, hit Send within 400 ms, confirm the `UpdateRequest` lands *before* the
invoke in the network panel.

**U2 — unary Send switches to the saved-request RPC.** Delete `body`,
`metadataScript`, `service`, `method` from the unary call site; keep `target` only if
B5's flush proves insufficient (it should not). Verify against the echo server that a
generator-produced body, a folder-inherited metadata chain and an attached middleware
all still apply — those are the three things that now resolve server-side instead of
being partially assembled client-side.

**U3 — `draft_messages` (B2), then streaming Send switches.** Proto field, compose
list persists to it, streaming invoke resolves from the store. Verify a cs/bd request
reproduces its full message list after a reload with no edits.

**U4 — collapse the RPC surface.** With no caller sending a body for a saved request,
`Invoke`'s `body`/`metadata_script` are ad-hoc-only. Rename per §3, delete what is
unused, rewrite the proto comments that describe the old invariant.

## 6. What it buys

- One resolution path, server-side, for every surface — the UI stops being the
  exception.
- Runs are reproducible from the collection: what the Timeline records is what the
  store holds.
- A dry-run ("show me the evaluated body, metadata and target without sending") is
  suddenly available to the UI too, because resolution is a server operation with a
  stopping point — today the UI would have to reimplement the evaluation it is trying
  to inspect.
- Client-streaming requests become fully persisted (via B2), which they are not now.

## 7. Risks

1. **A save failure now blocks a send.** Correct behavior, new failure mode. It is
   only reachable when the store is unwritable, which is already fatal to everything
   else.
2. **One extra round trip on the first Send after an edit.** Localhost `UpdateRequest`
   against a filesystem store; measure, but the debounce it replaces is 400 ms and
   this is not.
3. **Old collections.** B3 is the one that breaks existing data if sequenced wrong —
   U0 before U2, no exceptions.
4. **Two invoke RPCs during the migration.** Bounded by finishing U4 promptly; the
   alternative (a flag day) is worse.
