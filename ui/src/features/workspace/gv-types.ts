// The collection-scoped Monaco libs behind `gv.invoke()`'s typed paths, registered APP-LEVEL
// rather than per-editor: every surface that can call gv.invoke (request body, metadata,
// middleware, the Scripts scratchpad) needs them, and the Scripts view mounts no body editor.
import { useEffect, useMemo, useRef } from "react";
// Direct import, NOT useMonaco(): that returns null until @monaco-editor/react's loader has run,
// which happens on the first editor mount — the coupling this hook exists to remove.
// theme/monaco-nocturne.ts does loader.config({ monaco }), so this is the loader's own instance.
import * as monaco from "monaco-editor";
import type * as Monaco from "monaco-editor";
// Side-effect imports: the global TS defaults, and the virtual `@bufbuild/protobuf` d.ts the
// generated `_pb.ts` files registered below import from.
import "@/features/scripts/monaco-scripts";
import "./vendor/bufbuild-stubs";
import { useActiveWorkspace, useRootItems } from "@/lib/workspace-query";
import { collectInvokeTargets } from "./gv-requests";

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
