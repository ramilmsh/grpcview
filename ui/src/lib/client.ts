import { createConnectTransport } from "@connectrpc/connect-web";

// Mirrors //ui/src/vite-env.d.ts's ImportMeta augmentation: per-package
// ts_project compiles are isolated (see platform.ts's `process` comment), so
// that ambient .d.ts isn't visible here — restate it locally instead.
declare global {
  interface ImportMetaEnv {
    readonly PROD: boolean;
  }
  interface ImportMeta {
    readonly env: ImportMetaEnv;
  }
}

export const transport = createConnectTransport({
  baseUrl: import.meta.env.PROD ? window.location.origin : "http://127.0.0.1:10000",
  useBinaryFormat: true,
});
