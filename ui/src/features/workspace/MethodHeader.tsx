import { useState } from "react";
import {
  CaretDown,
  Code,
  FileArchive,
  Play,
  PlugsConnected,
} from "@/components/ui/icons";
import type {
  Service,
  Method,
  Server,
  Request,
} from "@grpcview/v1/workspace_pb";
import { Button, IconButton } from "@/components/ui/Button";
import { EditableName } from "@/components/ui/EditableName";
import { MethodKindTag, type MethodKind } from "@/components/ui/Tag";
import { middleEllipsis, serviceName } from "@/lib/format";
import { MethodPickerModal } from "./MethodPickerModal";
import { TargetBar } from "./TargetBar";

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
  // The source backing this request; the default invoke target when there's no override.
  reflection: Server | null;
  // Where the method's schema was resolved from — independent of the invoke target.
  schemaSource: { id: string; live: boolean } | null;
  targetOverride?: Server;
  invoking: boolean;
  onChangeMethod: (service: string, method: string) => void;
  onRename: (name: string) => void;
  onInvoke: () => void;
  onTargetChange: (t: Server) => void;
  onShowTypes: () => void;
}) {
  const [pickerOpen, setPickerOpen] = useState(false);
  const [editingName, setEditingName] = useState(false);
  // The ADDRESS, not the presence of a Server: an override starts life as an empty
  // one the moment the target field is touched, and dialing "" is not an invoke.
  const canInvoke = !!(targetOverride ?? reflection)?.address;
  const hasMethod = !!(request.service && request.method);

  const onPick = (service: Service, method: Method) => {
    onChangeMethod(serviceName(service), method.name);
  };

  return (
    <div
      style={{
        flex: "none",
        padding: "14px 16px 0",
      }}
    >
      <div
        className="flex items-center gap-[10px]"
        style={{ marginBottom: 10 }}
      >
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
            fontSize: 18,
            fontWeight: 600,
            color: "var(--v-text)",
            maxWidth: 240,
            // Shrink priority across this row is set by flex-shrink, not by who comes
            // first: the service/method pair (1000, and inside it the service before the
            // method) gives up width long before the request name does — so Invoke, which
            // never shrinks, stays on screen at any pane width.
            minWidth: 0,
            flexShrink: 1,
            overflow: "hidden",
            textOverflow: "ellipsis",
            whiteSpace: "nowrap",
          }}
          inputStyle={{
            fontSize: 18,
            fontWeight: 600,
            color: "var(--v-text)",
            maxWidth: 240,
          }}
        />
        <MethodKindTag kind={kind} />

        <div
          className="flex items-center gap-[4px] font-mono"
          style={{
            fontSize: 13,
            minWidth: 0,
            flexShrink: 1000,
          }}
        >
          <Button
            className="font-mono"
            style={{
              height: 32,
              padding: "0 10px",
              gap: 6,
              fontSize: 13,
              fontWeight: 400,
              background: "transparent",
              boxShadow: "var(--v-raise)",
              color: "var(--v-text-2)",
              minWidth: 0,
              flexShrink: 1000,
            }}
            title={request.service || "Select service"}
            onClick={() => setPickerOpen(true)}
          >
            <span
              style={{
                minWidth: 0,
                overflow: "hidden",
                textOverflow: "ellipsis",
                whiteSpace: "nowrap",
              }}
            >
              {request.service || "select service"}
            </span>
            <CaretDown size={10} style={{ opacity: 0.6, flex: "none" }} />
          </Button>
          <span style={{ color: "var(--v-text-muted)" }}>/</span>
          <Button
            className="font-mono"
            style={{
              height: 32,
              padding: "0 10px",
              gap: 6,
              fontSize: 13,
              fontWeight: 500,
              background: "transparent",
              boxShadow: "var(--v-raise)",
              color: "var(--v-text)",
              minWidth: 0,
              flexShrink: 1,
            }}
            title={request.method || "Select method"}
            onClick={() => setPickerOpen(true)}
          >
            <span
              style={{
                minWidth: 0,
                overflow: "hidden",
                textOverflow: "ellipsis",
                whiteSpace: "nowrap",
              }}
            >
              {request.method || "select method"}
            </span>
            <CaretDown size={10} style={{ opacity: 0.6, flex: "none" }} />
          </Button>
        </div>

        {/* No minWidth:0 here on purpose: min-content keeps the group from shrinking past
            Invoke, and the source chip inside it is the only part that gives up width. */}
        <div className="ml-auto flex items-center gap-[9px]">
          <IconButton
            onClick={onShowTypes}
            disabled={!hasMethod}
            style={{ opacity: hasMethod ? 1 : 0.35, flex: "none" }}
            title={
              hasMethod
                ? "View request/response message types"
                : "Select a method first"
            }
          >
            <Code size={15} />
          </IconButton>
          <Button
            variant="secondary"
            className="font-mono"
            style={{
              height: 32,
              padding: "0 10px",
              fontSize: 11,
              fontWeight: 400,
              gap: 6,
              cursor: "default",
              minWidth: 0,
            }}
            title={
              schemaSource
                ? schemaSource.live
                  ? `Schema resolved from this reflection source: ${schemaSource.id}`
                  : `Schema resolved from this descriptor source — the target below is where requests go: ${schemaSource.id}`
                : "No definition source defines this service"
            }
            disabled
          >
            {schemaSource?.live === false ? (
              <FileArchive size={14} style={{ color: "var(--v-text-2)" }} />
            ) : (
              <PlugsConnected
                size={14}
                style={{
                  color: schemaSource ? "var(--v-ok)" : "var(--v-text-muted)",
                }}
              />
            )}
            <span
              style={{
                minWidth: 0,
                overflow: "hidden",
                textOverflow: "ellipsis",
                whiteSpace: "nowrap",
              }}
            >
              {schemaSource ? middleEllipsis(schemaSource.id, 34) : "no source"}
            </span>
          </Button>
          <Button
            variant="solid"
            style={{ flex: "none" }}
            onClick={onInvoke}
            disabled={invoking || !canInvoke}
            title={
              !canInvoke
                ? "Set a target or add a reflection source to invoke"
                : "Invoke"
            }
          >
            <Play weight="fill" size={13} />
            {invoking ? "Invoking…" : "Invoke"}
          </Button>
        </div>
      </div>

      <TargetBar
        reflection={reflection}
        override={targetOverride}
        onChange={onTargetChange}
      />

      <MethodPickerModal
        open={pickerOpen}
        services={services}
        onClose={() => setPickerOpen(false)}
        onSelect={onPick}
      />
    </div>
  );
}
