// Virtual `@bufbuild/protobuf` type stubs for Monaco (ts-request-body-plan §T2/§4.6).
//
// The typed request body is checked against the generated `<Message>Json` type that
// `proto-types.ts` produces by running the real `protoc-gen-es` in-browser over the
// workspace descriptor set. Those generated `_pb.ts` files import from
// `@bufbuild/protobuf`, `@bufbuild/protobuf/wkt`, and `@bufbuild/protobuf/codegenv2`.
// Monaco's TS worker resolves modules with `moduleResolution: NodeJs` (classic node10,
// set in monaco-scripts.ts), which does NOT read a package's `exports` map — so it
// cannot reach the real installed `@bufbuild/protobuf` files (they live behind
// `exports`). We therefore hand-feed tiny virtual `.d.ts` stubs at the node10 lookup
// paths (`file:///node_modules/@bufbuild/protobuf/{,wkt,codegenv2}/index.d.ts`), so the
// generated `_pb.ts` imports resolve and the `…Json` types type-check.
//
// Runs as a side effect on import (the monaco-scripts.ts / monaco-nocturne.ts idiom):
// Editor.tsx imports this for its effects. The libs are added ONCE and never disposed
// (they are fixed, unlike the per-method generated files). We only need the surface the
// `…Json` types transitively reference: JsonValue/JsonObject/Message, the fixed set of
// WKT `*Json` aliases, and the codegenv2 Gen* types + the value fns (fileDesc/…) the
// raw `_pb.ts` calls at module scope (those consts degrade to `any`; the Json TYPES,
// which are all we type against, are self-contained).
import * as monaco from "monaco-editor";

const ts = monaco.languages.typescript.typescriptDefaults;

// package.json first — node10 reads it and follows "types" to the .d.ts (dayjs idiom).
ts.addExtraLib(
  `{"name":"@bufbuild/protobuf","version":"2","types":"index.d.ts"}`,
  "file:///node_modules/@bufbuild/protobuf/package.json"
);
ts.addExtraLib(
  `
  export type JsonValue = string | number | boolean | null | JsonObject | JsonValue[];
  export type JsonObject = { [k: string]: JsonValue };
  export type Message<T extends string = string> = { readonly $typeName: T };
`,
  "file:///node_modules/@bufbuild/protobuf/index.d.ts"
);

// @bufbuild/protobuf/wkt — the fixed set of well-known-type Json aliases (enumerated
// from the real package's wkt/gen/google/protobuf/*_pb.d.ts). protoc-gen-es imports
// these by name rather than emitting them locally, so the stub must cover them.
ts.addExtraLib(
  `
  import type { JsonObject, JsonValue } from "@bufbuild/protobuf";
  export type TimestampJson = string;
  export type DurationJson = string;
  export type FieldMaskJson = string;
  export type AnyJson = JsonObject;
  export type StructJson = JsonObject;
  export type ValueJson = JsonValue;
  export type ListValueJson = JsonValue[];
  export type EmptyJson = Record<string, never>;
  export type NullValueJson = "NULL_VALUE";
  export type DoubleValueJson = number;
  export type FloatValueJson = number;
  export type Int32ValueJson = number;
  export type UInt32ValueJson = number;
  export type Int64ValueJson = string;
  export type UInt64ValueJson = string;
  export type BoolValueJson = boolean;
  export type StringValueJson = string;
  export type BytesValueJson = string;
`,
  "file:///node_modules/@bufbuild/protobuf/wkt/index.d.ts"
);

// @bufbuild/protobuf/codegenv2 — the Gen* type aliases + the value fns the raw
// `_pb.ts` invokes at module scope (fileDesc/messageDesc/enumDesc). Loose shapes are
// fine: we never type against the runtime consts, only the self-contained `…Json` types.
ts.addExtraLib(
  `
  import type { Message } from "@bufbuild/protobuf";
  export type GenFile = unknown;
  export type GenMessage<S extends Message, O = unknown> = { __s: S; __o: O };
  export type GenEnum<S extends number, J = unknown> = { __s: S };
  export type GenExtension<A = unknown, B = unknown> = unknown;
  export type GenService<T = unknown> = unknown;
  export declare function fileDesc(b: string, ...i: number[]): GenFile;
  export declare function messageDesc<S extends Message, O = unknown>(f: GenFile, ...p: number[]): GenMessage<S, O>;
  export declare function enumDesc(f: GenFile, ...p: number[]): unknown;
`,
  "file:///node_modules/@bufbuild/protobuf/codegenv2/index.d.ts"
);
