import adapter from "@sveltejs/adapter-static";
import { vitePreprocess } from "@sveltejs/vite-plugin-svelte";
// @ts-ignore
import path from "path";

/** @type {import('@sveltejs/kit').Config} */
const config = {
  preprocess: vitePreprocess(),
  kit: {
    adapter: adapter({}),
    output: {
      bundleStrategy: "inline",
    },
    router: {
      type: "hash",
    },
    alias: {
      "@": path.resolve("../"),
      "@frontend": path.resolve("./src"),
      "@grpcview": path.resolve("../service/proto"),
    },
  },
};

export default config;
