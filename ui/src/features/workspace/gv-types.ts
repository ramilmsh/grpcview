// The collection-scoped Monaco libs behind `invoke()`'s typed paths, registered APP-LEVEL
// rather than per-editor: every surface that can import "grpcview:invoke" (request body,
// metadata, middleware, the Scripts scratchpad) needs them, and the Scripts view mounts no
// body editor.
import { useEffect, useMemo, useRef } from "react";
// Direct import, NOT useMonaco(): that returns null until @monaco-editor/react's loader has run,
// which happens on the first editor mount — the coupling this hook exists to remove.
// theme/monaco-nocturne.ts does loader.config({ monaco }), so this is the loader's own instance.
import * as monaco from "monaco-editor";
import type * as Monaco from "monaco-editor";
// Side-effect import: the virtual `@bufbuild/protobuf` d.ts the generated `_pb.ts` files
// registered below import from.
import "./vendor/bufbuild-stubs";
// Also a side-effect import (sets the global TS defaults) — baseCompilerOptions is the exported
// base useWorkspaceModuleTypes below extends with `paths` rather than duplicating.
import { baseCompilerOptions } from "@/features/scripts/monaco-scripts";
import { useActiveWorkspace, useRootItems, useWorkspaceModules } from "@/lib/workspace-query";
import { collectInvokeTargets } from "./gv-requests";
// Side-effect import (registers the auto-import completion provider) — colocated with the
// other Monaco global setup above. setAutoImportContext is what useWorkspaceModuleTypes below
// feeds live workspace-module data into, since the provider is a monaco-global singleton and
// cannot call a hook itself.
import { setAutoImportContext } from "./module-auto-import";
import { workspaceModulePaths, workspaceModuleUri } from "./workspace-modules";

// Editor.tsx registers the per-method `file:///grpcview/request/request-message.d.ts` alias; it
// must keep this same `file:///grpcview/request/` prefix, because that shared prefix is what
// resolves its relative "./gen/…" import against the modules registered here.
const GEN_PREFIX = "file:///grpcview/request/gen/";
const MAP_PATH = "file:///grpcview/request/gv-requests.d.ts";

export function useGvInvokeTypes(): void {
  const { workspace, services } = useActiveWorkspace();
  const rootItems = useRootItems(workspace);
  const descriptorSet = workspace?.descriptorSet;

  // Recomputed on any tree or descriptor change: a rename moves a path.
  const targets = useMemo(
    () => collectInvokeTargets(rootItems, services),
    [rootItems, services]
  );

  // typescriptDefaults is global and a same-path re-add throws "Duplicate definition".
  const libs = useRef<Monaco.IDisposable[]>([]);
  useEffect(() => {
    // descriptorSet is best-effort on the backend (empty when a source is unreachable).
    if (!descriptorSet?.length) return;
    let cancelled = false;
    void (async () => {
      const { generateWorkspaceTypes, gvRequestMapDts } = await import("./proto-types");
      const files = generateWorkspaceTypes(descriptorSet);
      if (cancelled) return;
      const tsDefaults = monaco.languages.typescript.typescriptDefaults;
      libs.current.forEach((d) => d.dispose());
      libs.current = [];
      for (const [path, content] of files) {
        libs.current.push(tsDefaults.addExtraLib(content, `${GEN_PREFIX}${path}`));
      }
      const map = gvRequestMapDts(files, targets);
      if (map) libs.current.push(tsDefaults.addExtraLib(map, MAP_PATH));
    })();
    return () => {
      cancelled = true;
      libs.current.forEach((d) => d.dispose());
      libs.current = [];
    };
  }, [descriptorSet, targets]);
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
    const tsDefaults = monaco.languages.typescript.typescriptDefaults;
    libs.current.forEach((d) => d.dispose());
    libs.current = modules.map((m) => tsDefaults.addExtraLib(m.content, workspaceModuleUri(m.path)));
    return () => {
      libs.current.forEach((d) => d.dispose());
      libs.current = [];
    };
  }, [modules]);

  useEffect(() => {
    setAutoImportContext({ modules, collectionId: collection });
  }, [modules, collection]);

  // setCompilerOptions REPLACES the whole object — baseCompilerOptions restates everything
  // monaco-scripts.ts already sets, so this never regresses it back to the defaults.
  useEffect(() => {
    monaco.languages.typescript.typescriptDefaults.setCompilerOptions({
      ...baseCompilerOptions,
      // paths requires a baseUrl; the substitutions below are already fully-rooted
      // `file:///…` URIs, so its value only has to be non-empty to satisfy that requirement.
      baseUrl: "file:///",
      paths: workspaceModulePaths(collection),
    });
  }, [collection]);
}
