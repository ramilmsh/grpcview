export default async (): Promise<RequestMessage> => (
{
  // ListCollectionsRequest has no fields: grpcview listing its own collections
  // needs no arguments. The empty object is still a whole TypeScript module —
  // the `export default` wrapper above is the part you normally never write.
  //
  // No target either. The request falls back to the collection's first
  // reflection source, localhost:10000, which is grpcview's own workspace
  // server reflecting grpcview.v1.WorkspaceService.
}
)
