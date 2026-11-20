// The collection-scoped Monaco libs behind `invoke()`'s typed paths, registered APP-LEVEL
// rather than per-editor: every surface that can import "grpcview:invoke" (request body,
// metadata, middleware, the Scripts scratchpad) needs them, and the Scripts view mounts no
// body editor.
import { useEffect, useMemo, useRef } from "react";
// Direct import, NOT useMonaco(): that returns null until @monaco-editor/react's loader has run,
// which happens on the first editor mount — the coupling this hook exists to remove.
// theme/monaco-theme.ts does loader.config({ monaco }), so this is the loader's own instance.
import * as monaco from "monaco-editor";
import type * as Monaco from "monaco-editor";
// Also a side-effect import (sets the global TS defaults) — baseCompilerOptions is the exported
// base useWorkspaceModuleTypes below extends with `paths` rather than duplicating.
import { baseCompilerOptions } from "@/features/scripts/monaco-scripts";
import {
  useActiveWorkspace,
  useDefinitions,
  useRootItems,
  useWorkspaceModules,
} from "@/lib/workspace-query";
import { collectInvokeTargets } from "./gv-requests";
import { gvRequestMapDts } from "./proto-types";
// Side-effect import (registers the auto-import completion provider) — colocated with the
// other Monaco global setup above. setAutoImportContext is what useWorkspaceModuleTypes below
// feeds live workspace-module data into, since the provider is a monaco-global singleton and
// cannot call a hook itself.
import { setAutoImportContext } from "./module-auto-import";
// Side-effect import for the same reason: the `@/…` / `#/…` specifier completion provider is a
// monaco global, and it reads the context set below.
import "./module-specifier-complete";
import { requireTypesDts } from "./require-types";
import { workspaceModulePaths, workspaceModuleUri } from "./workspace-modules";

// Editor.tsx registers the per-method `file:///grpcview/request/request-message.d.ts` alias; it
// must keep this same `file:///grpcview/request/` prefix, because that shared prefix is what
// resolves its relative "./gen/…" import against the modules registered here. Every
// GetDefinitions file lands here, the `node_modules/@bufbuild/protobuf/*` stubs included:
// node10 finds them walking up from any `_pb.ts`.
const GEN_PREFIX = "file:///grpcview/request/gen/";
const MAP_PATH = "file:///grpcview/request/gv-requests.d.ts";
const MODULES_PATH = "file:///grpcview/request/gv-modules.d.ts";

export function useGvInvokeTypes(): void {
  const { services, workspace } = useActiveWorkspace();
  const rootItems = useRootItems(workspace);
  const { files } = useDefinitions();

  // Recomputed on any tree or service change: a rename moves a path.
  const targets = useMemo(
    () => collectInvokeTargets(rootItems, services),
    [rootItems, services],
  );

  // typescriptDefaults is global and a same-path re-add throws "Duplicate definition".
  // `files` changes identity only with the digest, so this re-registers only then.
  const libs = useRef<Monaco.IDisposable[]>([]);
  useEffect(() => {
    const tsDefaults = monaco.typescript.typescriptDefaults;
    libs.current = [...files].map(([path, content]) =>
      tsDefaults.addExtraLib(content, `${GEN_PREFIX}${path}`),
    );
    return () => {
      libs.current.forEach((d) => d.dispose());
      libs.current = [];
    };
  }, [files]);

  // Its own effect: a tree edit moves a target without touching the definitions.
  const mapLib = useRef<Monaco.IDisposable | undefined>(undefined);
  useEffect(() => {
    const map = gvRequestMapDts(files, targets);
    if (!map) return;
    mapLib.current = monaco.typescript.typescriptDefaults.addExtraLib(
      map,
      MAP_PATH,
    );
    return () => {
      mapLib.current?.dispose();
      mapLib.current = undefined;
    };
  }, [files, targets]);
}

// Registers every importable `.ts` in the workspace as a Monaco extra lib, and points
// `compilerOptions.paths` at the active collection — the frontend half of the `@/` / `#/`
// resolver plugin (script-imports/decisions.md §8). App-level for the same reason as
// useGvInvokeTypes: an import is legal from every script surface, including the Scripts view,
// which mounts no body editor. registerGeneratorLibs (the old ambient-globals mechanism) is
// gone; auto-import over these real modules is what replaces the ergonomic it provided.
export function useWorkspaceModuleTypes(): void {
  const { collection } = useActiveWorkspace();
  const { modules } = useWorkspaceModules();

  // typescriptDefaults is global and a same-path re-add throws "Duplicate definition".
  const libs = useRef<Monaco.IDisposable[]>([]);
  useEffect(() => {
    const tsDefaults = monaco.typescript.typescriptDefaults;
    libs.current.forEach((d) => d.dispose());
    libs.current = modules.map((m) =>
      tsDefaults.addExtraLib(m.content, workspaceModuleUri(m.path)),
    );
    return () => {
      libs.current.forEach((d) => d.dispose());
      libs.current = [];
    };
  }, [modules]);

  // The `require("#/scripts/ids")` lookup table, in its own effect: its `#/` keys move with the
  // active collection, while the module libs above do not.
  const requireLib = useRef<Monaco.IDisposable | undefined>(undefined);
  useEffect(() => {
    const dts = requireTypesDts(modules, collection);
    if (!dts) return;
    requireLib.current =
      monaco.typescript.typescriptDefaults.addExtraLib(
        dts,
        MODULES_PATH,
      );
    return () => {
      requireLib.current?.dispose();
      requireLib.current = undefined;
    };
  }, [modules, collection]);

  useEffect(() => {
    setAutoImportContext({ modules, collectionId: collection });
  }, [modules, collection]);

  // setCompilerOptions REPLACES the whole object — baseCompilerOptions restates everything
  // monaco-scripts.ts already sets, so this never regresses it back to the defaults.
  useEffect(() => {
    monaco.typescript.typescriptDefaults.setCompilerOptions({
      ...baseCompilerOptions,
      // paths requires a baseUrl; the substitutions below are already fully-rooted
      // `file:///…` URIs, so its value only has to be non-empty to satisfy that requirement.
      baseUrl: "file:///",
      paths: workspaceModulePaths(collection),
    });
  }, [collection]);
}
