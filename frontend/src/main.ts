import { mount } from "svelte";
import App from "./App.svelte";
import "@frontend/editor/workers";

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
