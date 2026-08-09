import { requestId } from "#/scripts/ids";
import { stamp } from "#/scripts/stamp";

export default async (): Promise<RequestMessage> => (
// grpcview:script start
{
  collection: "example",
  // `requestId` and `stamp` are files in this collection, imported by path.
  // Nothing is bound by name and nothing is pulled in implicitly: the import
  // block above the start marker is exactly what the sandbox bundles, and it
  // holds these two entries only because the region below references them.
  //
  // JSON.stringify turns the generated text into a quoted string literal, so
  // the scratchpad grpcview runs is a single expression and its value comes
  // straight back in the response.
  source: JSON.stringify(`${requestId("gv")} at ${stamp()}`),
}
// grpcview:script end
)
