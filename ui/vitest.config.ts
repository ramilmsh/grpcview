import { fileURLToPath, URL } from "node:url";
import { join } from "node:path";
import { defineConfig } from "vitest/config";

// Deliberately NOT an extension of vite.config.ts: that config carries the
// singlefile release plugin, the react plugin, and the typescript/@typescript/vfs
// stubs that only make sense for the browser bundle. Tests cover the DOM-FREE
// modules (lib/ helpers, components/tree/'s flatten/selection/keymap/typeahead), so
// the node environment is correct and no jsdom/happy-dom dependency is needed.
// What must be shared with the app is the path aliases — a test importing "@/lib/x"
// has to resolve exactly as the app does.
export default defineConfig({
  resolve: {
    alias: {
      "@": fileURLToPath(new URL("./src", import.meta.url)),
      "@grpcview": fileURLToPath(new URL("../proto/grpcview", import.meta.url)),
    },
  },
  test: {
    include: ["src/**/*.test.ts", "src/**/*.test.tsx"],
    environment: "node",
    // Bazel test actions get a writable TEST_TMPDIR and an otherwise read-only
    // sandbox, so vite's default node_modules/.vite cache location would fail.
    ...(process.env.TEST_TMPDIR ? { cache: false as const } : {}),
  },
  cacheDir: process.env.TEST_TMPDIR
    ? join(process.env.TEST_TMPDIR, "vite-cache")
    : "node_modules/.vite",
});
