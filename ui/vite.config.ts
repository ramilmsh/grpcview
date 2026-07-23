import { fileURLToPath, URL } from "node:url";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { viteSingleFile } from "vite-plugin-singlefile";

// https://vitejs.dev/config/
export default defineConfig(({ command, mode }) => ({
  plugins: [react(), command === "build" && viteSingleFile()],
  resolve: {
    alias: {
      "@": fileURLToPath(new URL("./src", import.meta.url)),
      "@grpcview": fileURLToPath(new URL("../proto/grpcview", import.meta.url)),
      // The in-browser proto type generator (proto-types.ts → @bufbuild/protoc-gen-es)
      // statically pulls @bufbuild/protoplugin, which imports the ~10MB `typescript`
      // package + `@typescript/vfs` (the only fs-touching module in the chain) for its
      // `.d.ts` transpile path. We only call run() with `target=ts`, which emits source
      // verbatim and never reaches that path, so we alias both to inert stubs — dropping
      // ~9.6MB from the singlefile bundle and removing any latent fs reference
      // (ts-request-body-plan §T2, risk #7; proven inert by the de-risk spike).
      typescript: fileURLToPath(
        new URL("./src/features/workspace/vendor/ts-stub.ts", import.meta.url)
      ),
      "@typescript/vfs": fileURLToPath(
        new URL("./src/features/workspace/vendor/ts-vfs-stub.ts", import.meta.url)
      ),
    },
    // Force a single copy of these. Under pnpm's isolated store, connect-query's
    // peer @tanstack/react-query (and React) can resolve to a different path than
    // the app's, so rollup would bundle two copies → two React contexts →
    // "No QueryClient set" at runtime. Dedupe collapses them to one.
    dedupe: [
      "react",
      "react-dom",
      "@tanstack/react-query",
      "@connectrpc/connect-query",
      "@connectrpc/connect",
    ],
  },
  server: {
    watch: {
      usePolling: true,
    },
  },
}));
