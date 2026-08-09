import { invoke } from "grpcview:invoke";
import { params } from "grpcview:request";

export default async (): Promise<RequestMessage> => (
{
  spec: {
    // invoke runs another SAVED request — body, metadata, folder inheritance,
    // middleware and all — and resolves to its response. The path is display
    // names joined by "/", and it is typed: `body` is the response message of
    // whatever request the path names.
    collection: (await invoke("Workspace/ListCollections"))
      .body.collections.find((c: { id: string }) => c.id === "example").id,
    // grpcview invoking grpcview: the workspace server is asked to place a
    // second call, to itself. The inner spec carries no target either, so it
    // falls back to the same reflection source this request did.
    service: "grpcview.v1.WorkspaceService",
    method: "ListCollections",
  },
  body: "{}",
}
)
