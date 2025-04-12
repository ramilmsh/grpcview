import { sveltekit } from "@sveltejs/kit/vite";
import { defineConfig } from "vite";

export default defineConfig({
  plugins: [sveltekit()],
  server: {
    hmr: { overlay: true },
    watch: {
      usePolling: true,
    },
    fs: {
      allow: ["/"],
    },
  },
  build: {
    assetsInlineLimit: Infinity,
  },
});
