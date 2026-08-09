import { requestId } from "#/scripts/ids";
import { stamp } from "#/scripts/stamp";

export default async (): Promise<RequestMessage> => (
{
  collection: "example",
  // `requestId` and `stamp` are files in this collection, imported by path.
  // Nothing is bound by name and nothing is pulled in implicitly: the import
  // graph above is exactly what the sandbox bundles.
  //
  // JSON.stringify turns the generated text into a quoted string literal, so
  // the scratchpad grpcview runs is a single expression and its value comes
  // straight back in the response.
  source: JSON.stringify(`${requestId("gv")} at ${stamp()}`),
}
)
