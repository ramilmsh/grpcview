import { BracketsCurly } from "@/components/ui/icons";
import { Subtab } from "@/components/ui/Subtab";
import { Tag } from "@/components/ui/Tag";
import { useUIStore } from "@/lib/ui-store";
import type { MetadataRow } from "@/lib/format";
import { MessageTab } from "./MessageTab";
import { MetadataTab } from "./MetadataTab";

// RequestPane holds the Message + Metadata subtabs (plan §1.4). Auth/Middleware/
// Options/Variants land with later phases.
export function RequestPane({
  schema,
  body,
  onBodyChange,
  metadataRows,
  onMetadataChange,
  currentMethod,
  currentKey,
  inputTypeName,
}: {
  schema?: object;
  body: string;
  onBodyChange: (v: string) => void;
  metadataRows: MetadataRow[];
  onMetadataChange: (rows: MetadataRow[]) => void;
  currentMethod: { service: string; method: string };
  currentKey: string;
  inputTypeName?: string;
}) {
  const subtab = useUIStore((s) => s.requestSubtab);
  const setSubtab = useUIStore((s) => s.setRequestSubtab);
  const enabledCount = metadataRows.filter((r) => r.enabled && r.key.trim()).length;

  return (
    <div
      className="flex flex-col"
      style={{ width: "50%", flex: "none", borderRight: "1px solid var(--line)", minWidth: 0 }}
    >
      <div
        className="flex items-center"
        style={{
          flex: "none",
          padding: "0 6px",
          borderBottom: "1px solid var(--line)",
          background: "var(--color-bg)",
          overflowX: "auto",
        }}
      >
        <Subtab active={subtab === "message"} onClick={() => setSubtab("message")}>
          <BracketsCurly size={14} />
          Message
        </Subtab>
        <Subtab active={subtab === "metadata"} onClick={() => setSubtab("metadata")}>
          Metadata
          {enabledCount > 0 && (
            <Tag variant="neutral" className="ml-[2px]" >
              {enabledCount}
            </Tag>
          )}
        </Subtab>
      </div>

      {subtab === "message" ? (
        <MessageTab
          schema={schema}
          body={body}
          onChange={onBodyChange}
          currentMethod={currentMethod}
          currentKey={currentKey}
          inputTypeName={inputTypeName}
        />
      ) : (
        <MetadataTab rows={metadataRows} onChange={onMetadataChange} />
      )}
    </div>
  );
}
