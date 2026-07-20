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
