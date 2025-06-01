import { createRouter, createWebHashHistory } from "vue-router";

import Workspace from "./Workspace.vue";
import Home from "./Home.vue";

export const router = createRouter({
  history: createWebHashHistory(),
  routes: [
    { path: "/", component: Home },
    { path: "/ws/:name", component: Workspace },
  ],
});
