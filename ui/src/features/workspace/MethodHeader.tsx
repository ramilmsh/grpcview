import { useState } from "react";
import { CaretDown, Play, PlugsConnected } from "@/components/ui/icons";
import type { Service, Method, Server, Request } from "@grpcview/v1/workspace_pb";
import { Button } from "@/components/ui/Button";
import { MethodKindTag } from "@/components/ui/Tag";
import { serviceName } from "@/lib/format";
import { MethodPickerModal } from "./MethodPickerModal";
import { TargetBar } from "./TargetBar";

// MethodHeader: request name (read-only — rename unsupported, plan §11), the
// service/method selector (opens the picker; persists via UpdateRequest), the
// resolving-source chip (read-only), and Invoke. The first reflection source is
// both the invoke target and the enable/disable signal.
export function MethodHeader({
  request,
  services,
  reflection,
  invoking,
  onChangeMethod,
  onInvoke,
}: {
  request: Request;
  services: Service[];
  reflection: Server | null;
  invoking: boolean;
  onChangeMethod: (service: string, method: string) => void;
  onInvoke: () => void;
}) {
  const [pickerOpen, setPickerOpen] = useState(false);
  const canInvoke = !!reflection;

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
        <span
          className="font-heading"
          style={{
            fontSize: 15,
            fontWeight: 600,
            color: "var(--color-text)",
            maxWidth: 240,
            overflow: "hidden",
            textOverflow: "ellipsis",
            whiteSpace: "nowrap",
          }}
          title="Rename is unsupported in Phase 1"
        >
          {request.name}
        </span>
        <MethodKindTag kind="u" />

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
          {/* source resolution chip (read-only in Phase 1) */}
          <Button
            variant="secondary"
            className="font-mono"
            style={{ padding: "4px 9px", fontSize: 11, gap: 6, cursor: "default" }}
            title="Schema/target resolved from this definition source"
            disabled
          >
            <PlugsConnected
              size={14}
              style={{ color: reflection ? "var(--ok)" : "var(--color-neutral-600)" }}
            />
            {reflection ? `reflection:${reflection.host}` : "no source"}
          </Button>
          <Button
            variant="primary"
            style={{ padding: "6px 16px", fontSize: 14, gap: 7 }}
            onClick={onInvoke}
            disabled={invoking || !canInvoke}
            title={!canInvoke ? "Add a reflection source to invoke" : "Invoke"}
          >
            <Play weight="fill" size={13} />
            {invoking ? "Invoking…" : "Invoke"}
          </Button>
        </div>
      </div>

      <TargetBar target={reflection} />

      <MethodPickerModal
        open={pickerOpen}
        services={services}
        onClose={() => setPickerOpen(false)}
        onSelect={onPick}
      />
    </div>
  );
}
