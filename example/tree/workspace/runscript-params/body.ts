import { params } from "grpcview:request";

export default async (): Promise<RequestMessage> => (
// grpcview:script start
{
  collection: "example",
  // `params` holds whatever the caller passed for THIS run: the CLI's --param,
  // the MCP tool's `params`, or the second argument of invoke(). It is empty on
  // a plain run, so every param needs a default. The import that binds it sits
  // above the start marker, where the editor put it when the completion was
  // accepted and where it stays only while the region still uses the name.
  //
  // RunScript has one rule: a source with `export default` is compiled as an
  // entry and its default export is called; anything else is evaluated as a
  // scratchpad and its last expression is the value. `source` here is an
  // expression, so the response echoes the arithmetic back.
  source: String(params.expression ?? "1 + 1"),
}
// grpcview:script end
)
