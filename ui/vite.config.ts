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
  },
  server: {
    watch: {
      usePolling: true,
    },
  },
}));
