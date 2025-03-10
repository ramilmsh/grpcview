import { mount } from "svelte";
import "./main.css";
import App from "./App.svelte";
import "./editor/workers";

const app = mount(App, {
  target: document.getElementById("app")!,
});

export default app;
