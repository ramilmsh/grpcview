import { describe, expect, it } from "vitest";
import type { Item, Service } from "@grpcview/v1/workspace_pb";
import type { ItemWithPath } from "@/lib/format";
import { collectInvokeTargets } from "./gv-requests";
import { gvRequestMapDts } from "./proto-types";

const COLL = ".";
const slugify = (name: string): string => name.toLowerCase();

const folder = (name: string, path: string[], children: ItemWithPath[]): ItemWithPath => ({
  item: {
    name,
    slug: slugify(name),
    content: { case: "folder", value: { items: [] } },
  } as unknown as Item,
  collection: COLL,
  path,
  slugPath: path.map(slugify),
  children,
});

const request = (name: string, path: string[], service: string, method: string): ItemWithPath => ({
  item: {
    name,
    slug: slugify(name),
    content: { case: "request", value: { service, method } },
  } as unknown as Item,
  collection: COLL,
  path,
  slugPath: path.map(slugify),
});

const message = (pkg: string, name: string, file: string) => ({ package: pkg, name, file });

const SERVICE = "fixture.Greeter";
const services = [
  {
    package: "fixture",
    name: "Greeter",
    methods: [
      {
        name: "SayHello",
        clientStreaming: false,
        serverStreaming: false,
        input: message("fixture", "SayHelloRequest", "fixture/v1/greeter.proto"),
        output: message("fixture", "SayHelloResponse", "fixture/v1/greeter.proto"),
      },
      {
        name: "ListHellos",
        clientStreaming: false,
        serverStreaming: true,
        input: message("fixture", "ListHellosRequest", "fixture/v1/greeter.proto"),
        output: message("fixture", "ListHellosResponse", "fixture/v1/greeter.proto"),
      },
      {
        name: "SendHellos",
        clientStreaming: true,
        serverStreaming: false,
        input: message("fixture", "SendHellosRequest", "fixture/v1/greeter.proto"),
        output: message("fixture", "SendHellosResponse", "fixture/v1/greeter.proto"),
      },
      {
        name: "Timestamped",
        clientStreaming: false,
        serverStreaming: false,
        input: message("fixture", "TimestampedRequest", "fixture/v1/greeter.proto"),
        output: message("google.protobuf", "Timestamp", "google/protobuf/timestamp.proto"),
      },
    ],
  },
] as unknown as Service[];

describe("collectInvokeTargets", () => {
  it("lists unary requests by display-name path, nested folders included", () => {
    const roots = [
      request("Ping", [], SERVICE, "SayHello"),
      folder("Calls", [], [
        request("Hello", ["Calls"], SERVICE, "SayHello"),
        folder("Deep", ["Calls"], [request("Nested", ["Calls", "Deep"], SERVICE, "SayHello")]),
      ]),
    ];

    expect(collectInvokeTargets(roots, services).map((t) => t.path)).toEqual([
      "Ping",
      "Calls/Hello",
      "Calls/Deep/Nested",
    ]);
  });

  it("carries the RESPONSE message, not the request's input", () => {
    const targets = collectInvokeTargets([request("Ping", [], SERVICE, "SayHello")], services);
    expect(targets).toEqual([
      {
        path: "Ping",
        pkg: "fixture",
        name: "SayHelloResponse",
        file: "fixture/v1/greeter.proto",
      },
    ]);
  });

  it("skips streaming requests — invoke() rejects them", () => {
    const roots = [
      request("Server", [], SERVICE, "ListHellos"),
      request("Client", [], SERVICE, "SendHellos"),
      request("Unary", [], SERVICE, "SayHello"),
    ];
    expect(collectInvokeTargets(roots, services).map((t) => t.path)).toEqual(["Unary"]);
  });

  it("skips a request whose method is no longer in the descriptors", () => {
    expect(collectInvokeTargets([request("Gone", [], SERVICE, "Vanished")], services)).toEqual([]);
  });

  it("skips a name containing a slash, which splitInvokePath would split elsewhere", () => {
    const roots = [
      request("a/b", [], SERVICE, "SayHello"),
      folder("x/y", [], [request("Ok", ["x/y"], SERVICE, "SayHello")]),
    ];
    expect(collectInvokeTargets(roots, services)).toEqual([]);
  });
});

// One generated file, shaped like protoc-gen-es output: the full name is what
// resolveLocalSymbol matches on.
const GEN = new Map([
  [
    "fixture/v1/greeter_pb.ts",
    `export type SayHelloResponse = Message<"fixture.SayHelloResponse"> & {};
export type SayHelloResponseJson = { greeting?: string };
`,
  ],
  [
    "other/v1/other_pb.ts",
    `export type SayHelloResponse = Message<"other.SayHelloResponse"> & {};
export type SayHelloResponseJson = { greeting?: string };
`,
  ],
]);

describe("gvRequestMapDts", () => {
  it("emits one GvRequestMap entry per target, importing from ./gen", () => {
    const dts = gvRequestMapDts(GEN, [
      { path: "Calls/Hello", pkg: "fixture", name: "SayHelloResponse", file: "fixture/v1/greeter.proto" },
    ]);
    expect(dts).toContain(
      'import type { SayHelloResponseJson as GvResponse0 } from "./gen/fixture/v1/greeter_pb";'
    );
    expect(dts).toContain('"Calls/Hello": { response: GvResponse0 };');
    expect(dts).toContain("interface GvRequestMap");
  });

  it("aliases each import so the same symbol from two files does not collide", () => {
    const dts = gvRequestMapDts(GEN, [
      { path: "A", pkg: "fixture", name: "SayHelloResponse", file: "fixture/v1/greeter.proto" },
      { path: "B", pkg: "other", name: "SayHelloResponse", file: "other/v1/other.proto" },
    ]);
    expect(dts).toContain("as GvResponse0");
    expect(dts).toContain("as GvResponse1");
    expect(dts).toContain('"A": { response: GvResponse0 };');
    expect(dts).toContain('"B": { response: GvResponse1 };');
  });

  it("returns null when nothing resolves, leaving invoke at its any fallback", () => {
    expect(gvRequestMapDts(GEN, [])).toBeNull();
    expect(
      gvRequestMapDts(GEN, [
        { path: "Unknown", pkg: "nope", name: "Nope", file: "nope/v1/nope.proto" },
      ])
    ).toBeNull();
  });

  it("skips a well-known-type response: generateWorkspaceTypes never emits those", () => {
    expect(
      gvRequestMapDts(GEN, [
        {
          path: "Stamp",
          pkg: "google.protobuf",
          name: "Timestamp",
          file: "google/protobuf/timestamp.proto",
        },
      ])
    ).toBeNull();
  });
});
