import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import App from "./App.tsx";

// Import order is the cascade order: the app token layer must come last to win.
import "./index.css";
import "./theme/fonts.css";
import "./theme/nocturne.css";
import "./theme/app-tokens.css";
import "./theme/monaco-nocturne";

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App />
  </StrictMode>
);
