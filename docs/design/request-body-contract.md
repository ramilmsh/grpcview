# The request-body contract

**A request body is protojson. TypeScript is an authoring affordance layered over
it, not the contract.**

This is cross-cutting and authoritative: the web UI, the VS Code extension, the CLI
and the MCP server all send a body through one seam, and all four must accept the
same forms. Anything in `docs/design/**` that contradicts this file is stale.

## Why this is a contract question and not a formatting preference

grpcview's headline design decision is that bodies are authored as TypeScript, which
buys IntelliSense and type-checking against the reflected input message. But the
*value* that goes on the wire is a protojson object, and every authoring mode
converges on it.

So TypeScript is how a human gets help writing the object. It must not be the price
of admission for anyone who already has the object — a `curl`-style pipe, an agent
filling in an MCP tool argument, a fixture committed by a script, a body copied out
of a log. Requiring `export default () => ({ … })` around a value that is already
protojson is a tax with no payer.

## Why TypeScript is here at all: it replaced `{{ }}`

TypeScript was not adopted to make bodies typed. It was adopted to **delete a template
language**. The previous model was JSON plus `{{ uuid() }}` tokens, resolved by a
regex-driven substitution chain (`tokens.go` / `tokens.ts`, both since deleted). Type
safety and IntelliSense are what made the replacement obviously better, but the problem
being solved was templating.

The reason every Postman-class tool ends up with `{{ }}` is that its bodies are **data** —
a JSON string. Text is the only thing you can interpolate into, so injecting a computed
value requires a second, worse language embedded in the first: its own escaping rules, no
autocomplete, no type-checking, no way for one token to call another, and errors that
surface as a failed substitution rather than a compile error.

grpcview's bodies are **expressions**, so none of that machinery is needed. A computed
value is a function call written where the value goes:

```ts
{ "userId": uuid(), "createdAt": now() }
```

That is why the `=> ( … )` expression position is load-bearing rather than cosmetic: a
*statement* position can't hold a value, and a value position is exactly what a call
needs. IntelliSense, real types, and composition (a generator calling another generator)
then come free from the host language instead of being features someone has to build into
a token syntax.

### This is what decides the contract

The static and dynamic cases sit on **one continuous gradient**, not on two sides of a
mode switch:

```ts
{ "userId": "u_1" }      // static — plain protojson
{ "userId": uuid() }     // dynamic — one call, same file, same path
```

Going from the first to the second is an *edit*. Not a conversion, not a mode toggle, not
a re-authoring. That property is the entire payoff, and it only holds because both forms
run the same evaluation path.

Which is why splitting protojson onto its own non-evaluated path — as an earlier draft of
this document did — was wrong in a way worth naming: a user who authored a static JSON
body and then needed one generated value would hit "this body can't call functions,
switch to TypeScript mode." That is the `{{ }}` problem wearing different clothes, and it
would have reintroduced the exact wart TypeScript was adopted to remove. The unified path
is not a simplification of the design; it *is* the design.

## Two forms, one evaluation path

**Valid JSON is valid TypeScript.** A JSON object *is* a TS object literal in
expression position — `{"userId": "u_1"}` needs no conversion, no special case, and no
separate code path to be understood as TS. This is true by design, not by luck: tc39's
"JSON superset" proposal (ES2019) closed the last syntactic gap (raw U+2028/U+2029 in
string literals) precisely so that JSON would be a strict subset of ECMAScript
expressions.

So there are exactly two forms, distinguished by one existing predicate:

**1. A module** — the source contains a default export (`entry.go`'s
`hasDefaultExport`). Run on the entry path; the default export may be sync or `async`
and is awaited either way. Its returned object is the body.

```ts
export default async () => ({ userId: uuid() })
```

**2. An expression** — everything else. Wrapped in
`export default async () => ( … )` and run on the same entry path.

```ts
{ "userId": "u_1" }             // protojson — an expression like any other
{ userId: uuid(), retries: 3 }  // unquoted keys, a generator call, a trailing comma
```

Those two examples are **the same case**. Plain protojson is not a mode, a fast path,
or a compatibility shim — it is an expression that happens to also be valid JSON.
Treating it as its own form would mean two implementations that have to agree, two sets
of error messages, and a contract with a distinction users would have to learn for no
benefit.

An empty or whitespace-only body is `{}`, which is form 2. This retires `emptyTSBody`
(`invoke.go:48`) — not because bare `{}` is special-cased, but because wrapping puts it
in expression position where it means what it looks like.

### Why wrapping is what makes form 2 work

A bare `{ … }` at *statement* position parses as a **block**, not an object literal. The
existing fallback path (`bundler.compile`, last-expression eval) therefore misreads
every bare object — silently. Wrapping in `=> ( … )` puts it in expression position and
the misparse disappears. `ui/src/features/workspace/body-wrapper.ts` has documented this
for a while; the bug was that only the frontend knew.

## The structural change: the sniff belongs to the backend — **implemented**

Normalization used to live in the UI — `migrateBodyToTs` in `body-wrapper.ts` — with the
backend assuming every body it received was already a module. That was sound when the
browser was the only client. With four surfaces it was the defect: a CLI pipe, an MCP
`invoke`, or a hand-edited file reaches `resolveInvokeBody` without passing through any
browser code, hit the last-expression path, and misparsed.

The sniff now lives in **`resolveInvokeBody` (`service/workspace/invoke.go`)**, the one
choke point all three call sites funnel through — unary `Invoke`, `InvokeStreaming`, and
`gv.invoke`'s re-entry. The whole implementation is:

```go
if !scripting.HasDefaultExport(body) {
    body = "export default async () => (\n" + body + "\n)"
}
```

It runs *before* `transitiveGenerators` scans the source, and both see the same wrapped
string — otherwise an expression body's generator call sites go undetected and composition
silently stops working for form 2. `scripting.HasDefaultExport` is exported precisely so
this decision uses the same regex the entry-point convention does.

One branch, one evaluation path, and every surface inherits it including ones not yet
designed. The UI keeps `wrap` / `isCanonical` / the hidden-wrapper machinery, but
strictly as a **view** concern — how Monaco presents a body and gets it type-checked. It
stopped being what makes invoke work.

## Consequences worth stating

**Every body is evaluated, including one that looks like JSON.** That is the point of
having one path, and it is not a cost worth optimizing away: the engine is unconditional
in production (`workspace.New` fails if it cannot initialize — `workspace.go:49`), so a
"no engine needed" path would be a second implementation serving nobody. If body
evaluation ever shows up in a profile, the fix is caching keyed on the source, not a
forked parse.

**64-bit integers must be strings.** Since every body goes through JS, an integer
literal above 2^53 loses precision — `{"id": 12345678901234567890}` is not the number
you wrote. This is exactly why protojson's canonical encoding for `int64` / `uint64` /
`fixed64` is a **string**, and it is pre-existing behavior rather than anything this
contract changes. Worth documenting in the MCP tool description and CLI help, because a
hand-written or model-written body is where it will bite.

**Do not rewrite a body the user did not edit.** Normalization on the way *in* to an
editor must not be persisted on its own. If the CLI writes `{"userId":"u_1"}` and the
user later opens that request in the web UI, displaying it inside the hidden wrapper is
fine; **saving** it back as `export default async () => ({ … })` is not. In a
file-backed world ([VS Code phase 2](./active/vscode/phase-2-body-files.md)) that silent
conversion is a spurious git diff on a file the user never touched. Persist the form the
user authored; convert only on a real edit.

## What each surface does with this

| Surface | Sends | Notes |
|---|---|---|
| Web UI | form 1 | Hidden wrapper is view-only; the return annotation is what enables excess-property checking, so authoring stays module-shaped |
| VS Code | either | `body.ts` or `body.json` on disk; the extension drives editor tooling, **not** the sniff |
| CLI | either | `-f body.json`, `-f body.ts`, or stdin; bytes passed through unchanged |
| MCP | form 2 | An agent is told "a JSON object", with the module form as the escape hatch for generated values |

Sniffing is by **content**, never by file extension or by which surface sent it. A
`.json` file containing a module works; so does a `.ts` file containing plain protojson.
The extension is a hint to editors and nothing more — the same reason `kubectl -f` does
not trust the suffix.

## Metadata gets the identical treatment

Request metadata and folder metadata are the same problem with a fixed target type
(`{[key: string]: string[]}`), so they take the same two forms through
`resolveInvokeMetadata`:

```json
{ "authorization": ["Bearer t"] }
```

is a valid metadata body. Two follow-ons: the `metadata` `Struct` field on
`InvokeRequest` becomes redundant — the same information in a worse encoding — and the
field name `metadata_script` becomes a misnomer once an object literal is equally valid.
Both are contract-level cleanups for whichever track next touches the proto.

## What this does *not* change

- **No JSON-schema layer comes back.** The removed design converted proto descriptors
  into JSON schemas to validate bodies; that stays removed. Accepting protojson as input
  is unrelated — validation is still protojson-unmarshal against the dynamic message,
  which is the only real enforcement either way.
- **Type-checking is unchanged for form 1.** Form 2 opts out of the editor layers; it
  does not weaken them. See [the enforcement layers](./active/vscode/body-contract.md), whose
  own conclusion — layers 1–3 are authoring UX and layer 4 is the only actual
  enforcement — is what makes accepting a bare expression safe.
- **Generators and `gv` stay reachable from both forms**, since both end up on the entry
  path. This is not a detail — it is the no-mode-switch property above. A body that is
  currently pure JSON has nothing to call, but it never *loses the ability* to call.
- **`{{ }}` does not come back, in any surface.** If a future CLI, MCP or VS Code feature
  seems to want string interpolation into a body, that is a signal the body should be an
  expression calling something — which it already can be. The parameter channel for
  external values is `gv.request.params` (`--param k=v` on the CLI), not text
  substitution.

## Risks

- The form-1 sniff is a regex, so `export default` inside a string literal
  false-positives. That hazard exists today and this contract does not add to it; fixing
  it means a real parse, which is not worth it yet.
- Wrapping shifts line numbers by one, so evaluation errors must be remapped to author
  coordinates or they point at the wrong line. `sourcemap.go`'s
  `authorPreludeLines` offset already exists for exactly this and must cover the wrap.
