package workspace

import grpcviewv1 "codeberg.org/ramilmsh/grpcview/grpcview/v1"

// stubSource labels the files GetDefinitions serves from the binary rather than from a source.
const stubSource = "builtin"

// protobufStubs are the `@bufbuild/protobuf` type stubs every generated `_pb.ts` imports from,
// at node10 lookup paths: Monaco's TS worker resolves with node10, which ignores a package's
// `exports` map, so it cannot use the real package. Loose shapes are fine: nothing types against
// the runtime consts, only the `…Json` types. Sorted by path, after every generated file.
var protobufStubs = []*grpcviewv1.DefinitionFile{
	{
		Path: "node_modules/@bufbuild/protobuf/codegenv2/index.d.ts",
		Content: `import type { Message } from "@bufbuild/protobuf";
export type GenFile = unknown;
export type GenMessage<S extends Message, O = unknown> = { __s: S; __o: O };
export type GenEnum<S extends number, J = unknown> = { __s: S };
export type GenExtension<A = unknown, B = unknown> = unknown;
export type GenService<T = unknown> = unknown;
export declare function fileDesc(b: string, ...i: number[]): GenFile;
export declare function messageDesc<S extends Message, O = unknown>(f: GenFile, ...p: number[]): GenMessage<S, O>;
export declare function enumDesc(f: GenFile, ...p: number[]): unknown;
`,
	},
	{
		Path: "node_modules/@bufbuild/protobuf/index.d.ts",
		Content: `export type JsonValue = string | number | boolean | null | JsonObject | JsonValue[];
export type JsonObject = { [k: string]: JsonValue };
export type Message<T extends string = string> = { readonly $typeName: T };
`,
	},
	{
		// node10 reads package.json first and follows "types".
		Path:    "node_modules/@bufbuild/protobuf/package.json",
		Content: `{"name":"@bufbuild/protobuf","version":"2","types":"index.d.ts"}` + "\n",
	},
	{
		// protoc-gen-es imports these WKT Json aliases by name rather than emitting them locally.
		Path: "node_modules/@bufbuild/protobuf/wkt/index.d.ts",
		Content: `import type { JsonObject, JsonValue } from "@bufbuild/protobuf";
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
	},
}

func init() {
	for _, f := range protobufStubs {
		f.Source = stubSource
	}
}
