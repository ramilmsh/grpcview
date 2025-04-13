import { Workspace } from "@grpcview/v1/service_pb";
import {
  createClient as _createClient,
  type Client,
} from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
export type { AddRequest, AddResponse } from "@grpcview/v1/service_pb";

export const createClient = (): Client<typeof Workspace> => {
  return _createClient(
    Workspace,
    createConnectTransport({
      baseUrl: "http://127.0.0.1:10000",
      useBinaryFormat: true,
    })
  );
};
