import { createConnectTransport } from "@connectrpc/connect-web";

export const transport = createConnectTransport({
  baseUrl: "http://127.0.0.1:10000",
  useBinaryFormat: true,
});
