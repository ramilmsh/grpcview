import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import App from "./App.tsx";

// Theme: Tailwind layers + root sizing first (index.css), then the vendored
// Nocturne sheet and the app token/class layer on top so its look wins.
import "./index.css";
import "./theme/fonts.css";
import "./theme/nocturne.css";
import "./theme/app-tokens.css";
// Monaco: bundle the editor + workers and register the Nocturne theme (side
// effects) before anything mounts an editor.
import "./theme/monaco-nocturne";

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App />
  </StrictMode>
);
