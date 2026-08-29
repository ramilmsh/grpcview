import type { Service } from "@grpcview/v1/workspace_pb";
import { resolveMethod, type ItemWithPath } from "@/lib/format";
import type { InvokeTarget } from "./proto-types";

// collectInvokeTargets walks the collection tree and lists every saved request `invoke()`
// (from "grpcview:invoke") can actually reach, paired with its response message. The result
// feeds `gvRequestMapDts`.
//
// `invoke()` resolves within ONE collection (scriptInvoker closes over the collection id), so
// the caller passes that collection's root items and its services.
export function collectInvokeTargets(
  items: ItemWithPath[],
  services: Service[],
): InvokeTarget[] {
  const targets: InvokeTarget[] = [];

  const walk = (list: ItemWithPath[]) => {
    for (const entry of list) {
      if (entry.children) {
        walk(entry.children);
        continue;
      }
      if (entry.item.content.case !== "request") continue;
      const request = entry.item.content.value;

      const method = resolveMethod(services, request.service, request.method);
      // Streaming targets are out: invoke() rejects them, so there is no single response
      // message to name.
      if (!method || method.clientStreaming || method.serverStreaming) continue;
      const output = method.output;
      if (!output?.file) continue;

      const segments = [...entry.path, entry.item.name];
      // splitInvokePath (gvinvoke.go) splits on "/" unconditionally, so a display name
      // containing one addresses something else — or nothing — at runtime. Don't advertise a
      // path the backend cannot resolve back to this request.
      if (segments.some((segment) => segment.includes("/"))) continue;

      targets.push({
        path: segments.join("/"),
        pkg: output.package,
        name: output.name,
        file: output.file,
      });
    }
  };

  walk(items);
  return targets;
}
