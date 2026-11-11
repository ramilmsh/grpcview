import { X } from "@/components/ui/icons";
import type { Service, Method } from "@grpcview/v1/workspace_pb";
import { IconButton } from "@/components/ui/Button";
import { Backdrop } from "@/components/ui/Backdrop";
import { MethodPicker } from "./MethodPicker";

// MethodPicker in a modal, for repointing an existing request (MethodHeader). Picking
// closes it. Unmounted while closed, so every open starts with an empty query.
export function MethodPickerModal({
  open,
  services,
  onClose,
  onSelect,
}: {
  open: boolean;
  services: Service[];
  onClose: () => void;
  onSelect: (service: Service, method: Method) => void;
}) {
  if (!open) return null;

  return (
    <Backdrop onClose={onClose}>
      <div
        className="flex flex-col"
        style={{
          width: "min(640px, 100%)",
          height: 520,
          background: "var(--color-surface)",
          borderRadius: "var(--radius-lg)",
          boxShadow: "var(--shadow-lg)",
          overflow: "hidden",
        }}
        onClick={(e) => e.stopPropagation()}
        role="dialog"
        aria-modal="true"
      >
        <div
          className="flex items-center"
          style={{
            padding: "12px 14px",
            borderBottom: "1px solid var(--line)",
            gap: 8,
          }}
        >
          <span className="dialog-title" style={{ flex: 1, fontSize: 17 }}>
            Select method
          </span>
          <IconButton onClick={onClose} title="Close">
            <X size={18} />
          </IconButton>
        </div>

        <MethodPicker
          services={services}
          autoFocus
          onPick={(service, method) => {
            onSelect(service, method);
            onClose();
          }}
        />
      </div>
    </Backdrop>
  );
}
