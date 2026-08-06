# Enforcing the body/metadata export contract

Cross-cutting: layer 4 ships with [phase 2](./phase-2-body-files.md); layers 2 and 3
depend on `DiskSink` and land in [phase 5](./phase-5-extension.md) or
[6](./phase-6-optional.md).

> **Scope.** This doc is about *editor enforcement of the module form*, which is one of
> the two accepted body forms. The authoritative cross-surface contract is
> [`request-body-contract.md`](../../request-body-contract.md): a body is protojson, and
> a bare expression — which includes plain protojson, since valid JSON is valid TS — is
> equally valid and reaches the wire without any of the layers below. Nothing here may
> be read as "TypeScript is required."

## The problem

When a body *is* authored as a module, it must default-export a callable returning the
selected method's input shape:

```ts
export default async (): Promise<RequestMessage> => (
  { userId: "u_1" }
)
```

**TypeScript cannot express that constraint on a module from the outside.** There is no
`declare module` form that constrains a file you did not write. So enforcement is
layered — and the layers catch genuinely different failures.

## The four layers

**1. Inline annotation** — what exists today. The return annotation is what makes the
object literal *excess-property-checked*; `body-wrapper.ts:14-19` already documents why
the `=> ( … )` expression position is load-bearing (a bare `{ … }` at statement position
parses as a block).

**2. Generated check file** — a sibling the user never opens, under `.grpcview/check/`:

```ts
import fn from "../../../tree/users/get-user/body"
import type { GetUserRequestJson } from "../../types/gen/user/v1/user_pb"
const _c: () => GetUserRequestJson | Promise<GetUserRequestJson> = fn
```

True external enforcement: a missing default export, a non-callable export, or a
wrong-shaped return all fail to compile here. Requires `DiskSink`.

**3. AST lint** — assert the default export is a function whose annotation matches the
generated text. Cheap via the TS API. Surfaced as a `DiagnosticCollection` in VS Code, a
Monaco marker standalone.

**4. Runtime validation at the QuickJS boundary** — assert a callable default export,
await it, then `protojson`-unmarshal into a dynamic message of the input type.

## Why all four — the coverage matrix

| User does | 1. Annotation | 2. Check file | 3. AST lint | 4. Runtime |
|---|---|---|---|---|
| typo'd extra field | **✓** | ✗ | ✗ | ✓ |
| missing required field | ✓ | ✓ | ✗ | ✓ |
| wrong field type | ✓ | ✓ | ✗ | ✓ |
| deletes the default export | ✗ | **✓** | ✓ | ✓ |
| exports a non-function | ✗ | **✓** | ✓ | ✓ |
| annotates as `any` | ✗ | ✗ | **✓** | ✓ |
| deletes the annotation | ✗ | ✓ *(shape only)* | ✓ | ✓ |
| commits code that never compiled | ✗ | ✗ | ✗ | **✓** |
| stale types after a method change | ✗ | ✗ | ✗ | **✓** |

Two rows are non-obvious and worth internalizing:

- **The check file cannot catch an excess property.** Function return types are
  covariant, and assignability has no object-literal freshness — `() => {a, bTypo}` is
  assignable to `() => {a}`. Only the *inline* annotation catches typos.
- **The annotation cannot catch a deleted export.** Nothing inside the file constrains
  its own existence.

So they are complementary, not redundant.

## Which layers apply to which body form

The matrix above assumes the module form. The expression form opts out of two layers —
it does not weaken enforcement, because layer 4 is where enforcement lives:

| Body form | 1. Annotation | 2. Check file | 3. AST lint | 4. Runtime |
|---|---|---|---|---|
| module (`export default …`) | ✓ | ✓ | ✓ | ✓ |
| expression (incl. plain protojson) | ✓ *when hosted in the hidden wrapper* | ✗ — no default export to import | n/a | **✓** |

The nuance in row 2 is where the value is: an expression **in an editor** still gets
layer 1, because the hidden wrapper supplies the return annotation that makes the
literal excess-property-checked. The same expression **in a file opened cold**, or piped
from a shell, gets layer 4 only. So the form does not determine the checking — the
authoring context does.

What deliberately does *not* happen is generating a JSON Schema per method to drive the
editor's JSON language service for `body.json`. That is the layer AGENTS.md records as
removed entirely, and reintroducing it to serve the "I don't want to speak TypeScript"
escape hatch would trade the reason the escape hatch exists for a validation tier we
already decided against. A user who wants checking opens the body in an editor that
supplies the wrapper; a user who pastes an object gets a clean runtime error naming the
file and field.

This is also why the file extension must not gate anything (see
[phase 2](./phase-2-body-files.md)): `body.json` and `body.ts` differ in what VS Code
does *for the author*, never in what the backend accepts.

## The conclusion that matters

Layers 1–3 are **authoring UX** — fast, local, and all bypassable. **Layer 4 is the
only actual enforcement**, because a body can be committed without ever having
compiled, or hand-edited outside any editor, and phase 2 makes both of those easy.

Its error message must name `body.ts` and the offending field. Today the `protojson`
unmarshal failure is an implementation detail of invoke; once bodies are files it
becomes the authoritative contract check and needs a real error surface.

One consequence for the UI: an invoke that fails layer 4 should point at the file and
line, not just print a gRPC-ish error in the response pane.

## Metadata is easier

The target type is fixed (`Record<string, string[]>`) rather than per-method, so there
is no per-request generated type and one shared check shape covers every request.
