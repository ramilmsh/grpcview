import { mount } from "svelte";
import App from "./App.svelte";
import "bootstrap";
import "@frontend/components/editor/workers";

const app = mount(App, {
  target: document.getElementById("app")!,
});

document.addEventListener(
  "keydown",
  (e) => {
    if (e.key === "s" && (e.metaKey || e.ctrlKey)) {
      e.preventDefault();
    }
  },
  false
);

export default app;
