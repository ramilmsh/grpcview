import { defineConfig } from "vite";
import { svelte } from "@sveltejs/vite-plugin-svelte";
import { viteSingleFile } from "vite-plugin-singlefile";
import tailwindcss from "@tailwindcss/vite";
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
  plugins: [svelte(), tailwindcss(), viteSingleFile()],
  server: {
    hmr: { overlay: true },
    watch: {
      usePolling: true,
    },
  },
});
