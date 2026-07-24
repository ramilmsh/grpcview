# Plan: remove the dead `BodyLanguage`

**Status:** DONE 2026-07-24 — executed and browser-verified (`WorkspaceService/Get →
0 OK` against the self-reflecting prod binary on an isolated HOME; `bazel build
//ui:ui //service/cmd` + `bazel test //service/...` 4/4 green). **Deviation:** executed
WITHOUT the `reserved` markers Phase 1 prescribes — per the pre-release stance now
recorded in `AGENTS.md` (no users; simplicity over back-compat), the freed proto field
numbers/names were simply deleted; `protojson`'s `DiscardUnknown` keeps old
`request.json` files readable regardless. Cleanup tail of the ts-request-body
track — see `ts-request-body-plan.md` §0 P3, which KEPT `body_language` "for
back-compat". This plan reverses that decision (the back-compat value turned out to
be nil; see below).

## Goal

Delete the `BodyLanguage` enum and every `body_language` field end-to-end. Since the
2026-07-24 direction change (TypeScript is the only body mode), the field is
**vestigial**: it is still written, converted, and sent on the wire, but **never
read to make a decision**.

## Why it's safe (verified 2026-07-24)

- **Backend never branches on it.** `service/workspace/invoke.go` and `body.go`
  have zero `body_language` references — `resolveInvokeBody` always-evals TS (the
  `lang` bypass was deleted in P3, commit `b09ffc4`). The only Go reads are the
  `convert.go` disk⇄wire round-trip itself.
- **Frontend hardcodes it.** `ui/src/features/workspace/RequestWorkspace.tsx:251`
  sends `bodyLanguage: BodyLanguage.TYPESCRIPT` unconditionally, with a comment
  stating it ignores `request.bodyLanguage`.
- **Old on-disk files stay readable.** `service/store/codec.go:24` reads every
  managed file with `protojson.UnmarshalOptions{DiscardUnknown: true}`, so a
  `request.json` that still contains `"bodyLanguage": ...` parses fine with the
  field gone (the unknown key is silently dropped).
- **No external wire clients.** Single self-contained binary — the Go backend and
  the TS frontend build from the same protos and ship together, so there is no
  version-skew boundary the wire field protects. (The disk⇄wire schema split is
  worth keeping for *other* reasons — committed/diffable storage vs materialized
  runtime tree — but `BodyLanguage`'s mirror specifically buys nothing.)

Only the mirrored `BodyLanguage` enum is dead. The parallel `ScriptKind` bridge in
`convert.go` is **live** (generator/middleware/scenario) — leave it alone.

## Generated-code flow (what regenerates vs what's hand-edited)

- **Go `.pb.go`** — pure Bazel output (`go_proto_library`), NOT committed;
  `bazel build` regenerates it. No manual step.
- **TS build types** — `//ui:ui` depends on
  `//proto/grpcview/v1:grpcviewv1_ts_proto` (`ui/BUILD.bazel:46`); regenerated from
  the `.proto` on build. No manual step.
- **Committed `*_pb.d.ts` / connect stubs** — editor/tsc-only, consumed via the
  `tsconfig` path `@grpcview/* → ../proto/grpcview/*` (`ui/tsconfig.json:26`). These
  must be refreshed by hand from the `ts_proto_library` outputs (Phase 5).

> Line numbers below are current-state guides; anchor each edit on the quoted
> content — editing an earlier block shifts later line numbers within the same file.

## Phase 1 — Proto (source of truth)

Delete the enum + field at each site and `reserved` the freed number **and** proto
name. (`reserved` is hygiene, not required for safety: protojson is name-keyed and
`DiscardUnknown` already covers old files; reserving the name blocks a future field
from re-declaring `body_language` and silently re-binding stale data.)

1. `proto/grpcview/store/v1/storage.proto`
   - delete `enum BodyLanguage { ... }` (~31–40, incl. doc comment)
   - in `message Request`, delete `BodyLanguage body_language = 7;` (~83–85, incl.
     comment); add `reserved 7;` and `reserved "body_language";`
2. `proto/grpcview/v1/workspace.proto`
   - delete `enum BodyLanguage { ... }` (~140–148, incl. doc comment)
   - in `message Request`, delete `BodyLanguage body_language = 8;` (~111–115, incl.
     comment); add `reserved 8;` and `reserved "body_language";`
3. `proto/grpcview/v1/service.proto` — all three are non-terminal (fields follow
   them), so all need `reserved`:
   - `UpdateRequestRequest`: delete `optional BodyLanguage body_language = 11;`
     (~85–88, incl. comment); add `reserved 11;` and `reserved "body_language";`
   - `InvokeRequest`: delete `BodyLanguage body_language = 9;` (~133–138, incl.
     comment); add `reserved 9;` and `reserved "body_language";`
   - `InvokeStreamRequest`: delete `BodyLanguage body_language = 9;` (~175–178,
     incl. comment); add `reserved 9;` and `reserved "body_language";`

## Phase 2 — Go

4. `service/store/convert.go`
   - remove the `BodyLanguage: ...` line in `diskToWireRequest` (~29) and in
     `wireToDiskRequest` (~44)
   - delete `diskToWireBodyLanguage` + `wireToDiskBodyLanguage` and their shared doc
     block (~88–114)
   - leave the `ScriptKind` bridge intact
   - imports unaffected (`grpcviewstorev1`, `grpcviewv1`, `proto`, `descriptorpb`
     are all still used elsewhere in the file)

## Phase 3 — Frontend

5. `ui/src/features/workspace/RequestWorkspace.tsx`
   - line 3: `import { BodyLanguage, ScriptKind } from "@grpcview/v1/workspace_pb";`
     → drop `BodyLanguage`, keep `ScriptKind`
   - remove `bodyLanguage: BodyLanguage.TYPESCRIPT` from both invoke payloads
     (~251–252, incl. the now-moot comment; and ~291)
   - forced, not cosmetic: once the generated type loses the field these become tsc
     errors

## Phase 4 — Tests & docs

6. delete the `BodyLanguage: ...TYPESCRIPT` line from each `InvokeRequest` (both
   suites exercise the always-eval path and pass without it):
   - `service/workspace/body_test.go:131`
   - `service/workspace/metadata_test.go:104`
7. `docs/design/ts-request-body-plan.md` — the §0 P3 note lists `body_language`
   under "KEPT for back-compat"; append a short note that it was removed by this
   plan (nothing read it; `DiscardUnknown` covers old files).

## Phase 5 — Regenerate, verify, commit

8. Refresh the committed TS stubs from the `ts_proto_library` outputs:
   `bazelisk build //proto/grpcview/v1:grpcviewv1_ts_proto`, then copy the generated
   `*_pb.d.ts` (and the connect / connectquery `.d.ts`) from
   `bazel-bin/proto/grpcview/v1/` over the source copies in `proto/grpcview/v1/`.
   Confirm the exact output paths from the build output.
9. Gates:
   - `bazelisk build //ui:ui //service/cmd`
   - `bazelisk test //service/...` (expect the suites green)
10. Browser-verify (verify-invoke-in-browser flow): run the prod binary reflecting
    itself on an isolated `:1000x` HOME, invoke a request → `0 OK`, and confirm a
    pre-existing request (whose `request.json` still carries `bodyLanguage`) still
    loads and sends. Then one checkpoint commit on `trunk`
    (e.g. `refactor(request-body): remove dead BodyLanguage enum/fields`).

## Scope

7 hand-edited files + regen, one atomic commit. No behavior change — removal of an
unread field — so the browser check is a regression guard, not a feature check.
