export default async (): Promise<RequestMessage> => (
// grpcview:script start
{
  // InvokeStreaming is server-streaming, so THIS saved request is a streaming
  // one: the UI streams it frame by frame, `grpcview invoke` prints one frame
  // per line, and the MCP tool for it is invoke_saved_streaming. The unary
  // paths refuse it outright.
  //
  // What it streams is grpcview streaming grpcview: the workspace server is
  // asked to run its own InvokeStreaming, which in turn calls the unary
  // ListCollections. Every frame the inner call emits is forwarded here, so the
  // run ends with two message frames and one terminal result.
  spec: {
    collection: "example",
    service: "grpcview.v1.WorkspaceService",
    method: "InvokeStreaming",
  },
  // A streaming body's `messages` are the client frames, in send order. Here
  // there is one: the InvokeStreamRequest the middle call receives.
  messages: [
    JSON.stringify({
      spec: {
        collection: "example",
        service: "grpcview.v1.WorkspaceService",
        method: "ListCollections",
      },
      messages: ["{}"],
    }),
  ],
}
// grpcview:script end
)
