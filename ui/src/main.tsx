import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import App from "./App.tsx";
import { startKeepalive } from "./lib/keepalive.ts";

// Import order is the cascade order: the app token layer must come last to win.
import "./index.css";
import "./theme/fonts.css";
import "./theme/nocturne.css";
import "./theme/app-tokens.css";
import "./theme/monaco-nocturne";

// Outside the tree on purpose: it holds the server for the whole page, not for a component,
// and StrictMode would otherwise mount it twice.
startKeepalive();

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App />
  </StrictMode>
);
