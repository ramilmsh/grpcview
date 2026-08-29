// Virtual `@bufbuild/protobuf` type stubs, added as an import side effect (Editor.tsx). Monaco's
// TS worker resolves with node10 moduleResolution, which does NOT read a package's `exports` map,
// so it cannot reach the real installed files — hence hand-fed stubs at the node10 lookup paths.
import * as monaco from "monaco-editor";

const ts = monaco.languages.typescript.typescriptDefaults;

// package.json first — node10 reads it and follows "types" to the .d.ts.
ts.addExtraLib(
  `{"name":"@bufbuild/protobuf","version":"2","types":"index.d.ts"}`,
  "file:///node_modules/@bufbuild/protobuf/package.json",
);
ts.addExtraLib(
  `
  export type JsonValue = string | number | boolean | null | JsonObject | JsonValue[];
  export type JsonObject = { [k: string]: JsonValue };
  export type Message<T extends string = string> = { readonly $typeName: T };
`,
  "file:///node_modules/@bufbuild/protobuf/index.d.ts",
);

// protoc-gen-es imports these WKT Json aliases by name rather than emitting them locally.
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
  "file:///node_modules/@bufbuild/protobuf/wkt/index.d.ts",
);

// Loose shapes are fine: nothing types against the runtime consts, only the `…Json` types.
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
  "file:///node_modules/@bufbuild/protobuf/codegenv2/index.d.ts",
);
