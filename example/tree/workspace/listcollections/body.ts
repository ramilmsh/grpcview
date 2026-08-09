export default async (): Promise<RequestMessage> => (
// grpcview:script start
{
  // ListCollectionsRequest has no fields: grpcview listing its own collections
  // needs no arguments. What you are looking at is the region between the two
  // `// grpcview:script` marker comments, and in the editor it is all you see —
  // the `export default` line above and the closing `)` below are hidden, and
  // the gutter numbers the region from 1.
  //
  // Nothing is in scope for free. This file has no import block because the
  // region references nothing; the moment it does reference something —
  // `params`, `invoke`, a function exported by a script in this collection —
  // accepting the completion writes the import on a line above the start
  // marker, out of sight. That block is machine-maintained: it is regenerated
  // in sorted order, and an entry nothing uses any more is pruned back out.
  //
  // No target either. The request falls back to the collection's first
  // reflection source, localhost:10000, which is grpcview's own workspace
  // server reflecting grpcview.v1.WorkspaceService.
}
// grpcview:script end
)
