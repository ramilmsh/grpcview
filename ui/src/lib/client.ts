import { createConnectTransport } from "@connectrpc/connect-web";

// Dev runs Vite on its own port with the backend on :10000; the release build is
// served same-origin from the Go binary. connect-query reads this transport via
// <TransportProvider> in App.tsx.
export const transport = createConnectTransport({
  baseUrl: import.meta.env.PROD ? window.location.origin : "http://127.0.0.1:10000",
  useBinaryFormat: true,
});
