import type { ComponentType } from "react";
import {
  ArrowsSplit,
  Flask,
  Function as FunctionIcon,
  type IconProps,
} from "@/components/ui/icons";
import { ScriptKind } from "@grpcview/v1/workspace_pb";

type IconComp = ComponentType<IconProps>;

interface KindMeta {
  label: string;
  section: string;
  Icon: IconComp;
  color: string;
  tag: "accent" | "accent-2" | "neutral";
}

const SCENARIO_META: KindMeta = {
  label: "Scenario",
  section: "Scenarios",
  Icon: Flask,
  color: "var(--ok)",
  tag: "neutral",
};

const KIND_META: Partial<Record<ScriptKind, KindMeta>> = {
  [ScriptKind.MIDDLEWARE]: {
    label: "Middleware",
    section: "Middleware",
    Icon: ArrowsSplit,
    color: "var(--color-accent)",
    tag: "accent",
  },
  [ScriptKind.GENERATOR]: {
    label: "Generator",
    section: "Generators",
    Icon: FunctionIcon,
    color: "var(--color-accent-2-300)",
    tag: "accent-2",
  },
  [ScriptKind.SCENARIO]: SCENARIO_META,
};

export const kindMeta = (kind: ScriptKind): KindMeta => KIND_META[kind] ?? SCENARIO_META;

// Two orderings on purpose: the new-script picker leads with its own default kind.
export const SIDEBAR_ORDER: ScriptKind[] = [
  ScriptKind.MIDDLEWARE,
  ScriptKind.GENERATOR,
  ScriptKind.SCENARIO,
];
export const NEW_KIND_ORDER: ScriptKind[] = [
  ScriptKind.GENERATOR,
  ScriptKind.MIDDLEWARE,
  ScriptKind.SCENARIO,
];

export function starterSource(kind: ScriptKind): string {
  switch (kind) {
    case ScriptKind.GENERATOR:
      return `// Generator — its default export is invoked by name: call name() from a
// request body/metadata or from another generator. Test run calls it with no arguments.
export default () => {
  return new Date().toISOString();
};
`;
    case ScriptKind.MIDDLEWARE:
      return `// Middleware — runs before invoke. Mutate ctx (body / metadata / target) and
// return it. Test run calls handle with an empty ctx.
export function handle(ctx) {
  console.log("middleware ran");
  return ctx;
}
`;
    default:
      return `// Scenario — runs as a scratchpad: the value is the last expression. dayjs is
// available; there are no capabilities or workspace inputs.
import dayjs from "dayjs";

console.log("running in QuickJS (wasm)");

({ today: dayjs().format("YYYY-MM-DD"), engine: "quickjs-wasm" })
`;
  }
}
