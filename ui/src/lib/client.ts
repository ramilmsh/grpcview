import { createConnectTransport } from "@connectrpc/connect-web";

export const transport = createConnectTransport({
  baseUrl: import.meta.env.PROD ? window.location.origin : "http://127.0.0.1:10000",
  useBinaryFormat: true,
});
