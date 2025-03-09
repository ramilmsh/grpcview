import { defineConfig } from "vite";
import { svelte } from "@sveltejs/vite-plugin-svelte";
import { viteSingleFile } from "vite-plugin-singlefile";
// @ts-ignore
import path from "path";

export default defineConfig({
  resolve: {
    alias: {
      "@": path.resolve("../"),
      "@frontend": path.resolve("./src"),
      "@grpcview": path.resolve("../service/proto"),
    },
  },
  plugins: [svelte(), viteSingleFile()],
  build: {
    rollupOptions: {
      output: {
        entryFileNames: "bundle.js",
      },
    },
  },
  server: {
    hmr: { overlay: true },
    watch: {
      usePolling: true,
    },
  },
});
