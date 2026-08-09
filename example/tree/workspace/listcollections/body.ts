import { invoke } from "grpcview:invoke";
import { params } from "grpcview:request";

export default async (): Promise<RequestMessage> => (
{
  // ListCollectionsRequest has no fields: grpcview listing its own collections
  // needs no arguments. The empty object is still a whole TypeScript module —
  // the imports and the `export default` above are the part you normally never
  // write, and never see: a body whose first token is `{` gets them supplied,
  // and the editor hides them. `invoke` and `params` are in scope right here.
  //
  // No target either. The request falls back to the collection's first
  // reflection source, localhost:10000, which is grpcview's own workspace
  // server reflecting grpcview.v1.WorkspaceService.
}
)
