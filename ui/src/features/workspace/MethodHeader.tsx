import { useState } from "react";
import { CaretDown, Code, FileArchive, Play, PlugsConnected } from "@/components/ui/icons";
import type { Service, Method, Server, Request } from "@grpcview/v1/workspace_pb";
import { Button, IconButton } from "@/components/ui/Button";
import { EditableName } from "@/components/ui/EditableName";
import { MethodKindTag, type MethodKind } from "@/components/ui/Tag";
import { serviceName } from "@/lib/format";
import { MethodPickerModal } from "./MethodPickerModal";
import { TargetBar } from "./TargetBar";

// MethodHeader: request name (click to rename inline; persists via UpdateRequest),
// the service/method selector (opens the picker; persists via UpdateRequest), the
// schema-source chip (read-only), and Invoke. `reflection` is the source backing
// THIS request (its service's origin, else the first reflection source) — it is both
// the default invoke target (when there is no override) and the enable/disable signal.
// `schemaSource` is a separate question — see schemaSourceFor.
export function MethodHeader({
  request,
  services,
  kind,
  reflection,
  schemaSource,
  targetOverride,
  invoking,
  onChangeMethod,
  onRename,
  onInvoke,
  onTargetChange,
  onShowTypes,
}: {
  request: Request;
  services: Service[];
  kind: MethodKind;
  // reflection is the source backing THIS request (its service's origin, else the
  // first reflection source), the live default target when there's no override.
  reflection: Server | null;
  // schemaSource is the definition source the method's schema was resolved from
  // (id + whether it is a dialable reflection source), or null when no source
  // defines the service. Independent of the target: see schemaSourceFor.
  schemaSource: { id: string; live: boolean } | null;
  targetOverride?: Server;
  invoking: boolean;
  onChangeMethod: (service: string, method: string) => void;
  onRename: (name: string) => void;
  onInvoke: () => void;
  onTargetChange: (t: Server) => void;
  // message-shape-visibility plan §Feature 2: opens the read-only request/response types
  // modal (TypesModal). Gated below on a selected service + method.
  onShowTypes: () => void;
}) {
  const [pickerOpen, setPickerOpen] = useState(false);
  const [editingName, setEditingName] = useState(false);
  // An explicit per-request target lets you invoke even without a saved source
  // (the schema still reflects off that target — resolveMethod handles it).
  const canInvoke = !!(targetOverride ?? reflection);
  // message-shape-visibility plan §Feature 2: the types viewer needs a resolved method
  // (service + method name) to look up input/output coordinates from.
  const hasMethod = !!(request.service && request.method);

  const onPick = (service: Service, method: Method) => {
    onChangeMethod(serviceName(service), method.name);
  };

  return (
    <div
      style={{
        flex: "none",
        padding: "11px 16px 12px",
        background: "var(--color-bg)",
        borderBottom: "1px solid var(--line)",
      }}
    >
      <div className="flex items-center gap-[10px]" style={{ marginBottom: 10 }}>
        <EditableName
          value={request.name}
          editing={editingName}
          onEditingChange={setEditingName}
          onCommit={onRename}
          activateOnClick
          className="font-heading"
          title="Click to rename"
          ariaLabel="Request name"
          style={{
            fontSize: 15,
            fontWeight: 600,
            color: "var(--color-text)",
            maxWidth: 240,
            overflow: "hidden",
            textOverflow: "ellipsis",
            whiteSpace: "nowrap",
          }}
          inputStyle={{
            fontSize: 15,
            fontWeight: 600,
            color: "var(--color-text)",
            maxWidth: 240,
          }}
        />
        <MethodKindTag kind={kind} />

        {/* service / method selector — both open the same picker */}
        <div
          className="flex items-center font-mono"
          style={{
            fontSize: 13,
            background: "var(--panel-2)",
            border: "1px solid var(--line)",
            borderRadius: 7,
          }}
        >
          <Button
            className="font-mono"
            style={{ padding: "4px 9px", gap: 6, fontSize: 13, color: "var(--color-neutral-400)" }}
            title="Select service"
            onClick={() => setPickerOpen(true)}
          >
            <span style={{ color: "var(--color-neutral-500)" }}>
              {request.service || "select service"}
            </span>
            <CaretDown size={10} style={{ opacity: 0.6 }} />
          </Button>
          <span style={{ color: "var(--color-neutral-600)" }}>/</span>
          <Button
            className="font-mono"
            style={{
              padding: "4px 9px",
              gap: 6,
              fontSize: 13,
              fontWeight: 600,
              color: "var(--color-text)",
            }}
            title="Select method"
            onClick={() => setPickerOpen(true)}
          >
            {request.method || "select method"}
            <CaretDown size={10} style={{ opacity: 0.6 }} />
          </Button>
        </div>

        <div className="ml-auto flex items-center gap-[9px]">
          {/* message-shape-visibility: request/response TS shape viewer (TypesModal) */}
          <IconButton
            onClick={onShowTypes}
            disabled={!hasMethod}
            style={{ opacity: hasMethod ? 1 : 0.35 }}
            title={hasMethod ? "View request/response message types" : "Select a method first"}
          >
            <Code size={15} />
          </IconButton>
          {/* schema-source chip (read-only in Phase 1) — which definition source this
              method's schema was resolved from, NOT where the request is sent: an
              upload can win the protos while the target bar below points at a server. */}
          <Button
            variant="secondary"
            className="font-mono"
            style={{ padding: "4px 9px", fontSize: 11, gap: 6, cursor: "default" }}
            title={
              schemaSource
                ? schemaSource.live
                  ? "Schema resolved from this reflection source"
                  : "Schema resolved from this uploaded descriptor set — the target below is where requests go"
                : "No definition source defines this service"
            }
            disabled
          >
            {schemaSource?.live === false ? (
              <FileArchive size={14} style={{ color: "var(--color-neutral-400)" }} />
            ) : (
              <PlugsConnected
                size={14}
                style={{ color: schemaSource ? "var(--ok)" : "var(--color-neutral-600)" }}
              />
            )}
            {schemaSource ? schemaSource.id : "no source"}
          </Button>
          <Button
            variant="primary"
            style={{ padding: "6px 16px", fontSize: 14, gap: 7 }}
            onClick={onInvoke}
            disabled={invoking || !canInvoke}
            title={!canInvoke ? "Set a target or add a reflection source to invoke" : "Invoke"}
          >
            <Play weight="fill" size={13} />
            {invoking ? "Invoking…" : "Invoke"}
          </Button>
        </div>
      </div>

      <TargetBar reflection={reflection} override={targetOverride} onChange={onTargetChange} />

      <MethodPickerModal
        open={pickerOpen}
        services={services}
        onClose={() => setPickerOpen(false)}
        onSelect={onPick}
      />
    </div>
  );
}
