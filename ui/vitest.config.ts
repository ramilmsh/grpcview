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
    // Needed the moment a test renders a real component tree (Tree.portable.test.tsx,
    // components/tree/'s T0 acceptance test) rather than just calling plain functions,
    // which is every earlier test here. Under bazel's aspect_rules_js node_modules
    // layout, @phosphor-icons/react (pulled in via TreeRow -> icons.ts's CaretDown/
    // CaretRight, and reachable from the icon-map even when unused) resolves its own
    // peer "react" to a physically different module instance than the top-level react
    // react-dom/server renders with — two copies, two dispatcher contexts, and Phosphor's
    // useContext call reaches a dispatcher that was never entered ("Cannot read
    // properties of null (reading 'useContext')"). Same class of bug vite.config.ts's
    // own `dedupe` already documents and fixes for the app bundle; vitest.config.ts
    // needs its own copy of the fix since it deliberately does not extend that config
    // (see the comment above).
    dedupe: ["react", "react-dom"],
  },
  test: {
    include: ["src/**/*.test.ts", "src/**/*.test.tsx"],
    environment: "node",
    // Bazel test actions get a writable TEST_TMPDIR and an otherwise read-only
    // sandbox, so vite's default node_modules/.vite cache location would fail.
    ...(process.env.TEST_TMPDIR ? { cache: false as const } : {}),
    server: {
      // Forces these through Vite's resolver (where `dedupe` above applies) instead
      // of vitest's default of loading node_modules packages as plain Node externals.
      // Externalized is fine for everything else here, but under bazel's
      // aspect_rules_js layout react/react-dom/@phosphor-icons-react each carry their
      // own peer-resolved "react" — externalizing them lets Node's own resolution
      // find a DIFFERENT physical react module per package, which is the actual
      // "two copies of React" (see `dedupe`'s comment above).
      deps: { inline: ["react", "react-dom", "@phosphor-icons/react"] },
    },
  },
  cacheDir: process.env.TEST_TMPDIR
    ? join(process.env.TEST_TMPDIR, "vite-cache")
    : "node_modules/.vite",
});
