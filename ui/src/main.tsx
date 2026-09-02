import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import App from "./App";
import { startKeepalive } from "./lib/keepalive";
import "./theme/monaco-theme";

// The CSS cascade (tailwind.css, then the theme layer in ./theme/theme.css) is
// assembled by separate esbuild/tailwindcss passes and linked from index.html —
// see ui/BUILD.bazel — not JS-imported here, so this bundle stays JS-only.

// Outside the tree on purpose: it holds the server for the whole page, not for a component,
// and StrictMode would otherwise mount it twice.
startKeepalive();

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
