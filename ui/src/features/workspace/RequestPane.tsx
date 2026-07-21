import { BracketsCurly } from "@/components/ui/icons";
import { Subtab } from "@/components/ui/Subtab";
import { Tag, type MethodKind } from "@/components/ui/Tag";
import { useUIStore } from "@/lib/ui-store";
import type { MetadataRow } from "@/lib/format";
import { MessageTab } from "./MessageTab";
import { MessagesTab } from "./MessagesTab";
import { MetadataTab } from "./MetadataTab";

// RequestPane holds the Message + Metadata subtabs (plan §1.4). The message
// subtab is the single-body editor for unary / server-streaming and the
// multi-message compose list for client-streaming / bidi (plan §5). Auth/
// Middleware/Options/Variants land with later phases.
export function RequestPane({
  schema,
  kind,
  body,
  onBodyChange,
  messages,
  onMessagesChange,
  metadataRows,
  onMetadataChange,
  currentMethod,
  currentKey,
  inputTypeName,
}: {
  schema?: object;
  kind: MethodKind;
  body: string;
  onBodyChange: (v: string) => void;
  messages: string[];
  onMessagesChange: (next: string[]) => void;
  metadataRows: MetadataRow[];
  onMetadataChange: (rows: MetadataRow[]) => void;
  currentMethod: { service: string; method: string };
  currentKey: string;
  inputTypeName?: string;
}) {
  const subtab = useUIStore((s) => s.requestSubtab);
  const setSubtab = useUIStore((s) => s.setRequestSubtab);
  const enabledCount = metadataRows.filter((r) => r.enabled && r.key.trim()).length;
  const multi = kind === "cs" || kind === "bd";

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
          {multi ? "Messages" : "Message"}
          {multi && messages.length > 1 && (
            <Tag variant="neutral" className="ml-[2px]">
              {messages.length}
            </Tag>
          )}
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
        multi ? (
          <MessagesTab
            messages={messages}
            onChange={onMessagesChange}
            inputTypeName={inputTypeName}
          />
        ) : (
          <MessageTab
            schema={schema}
            body={body}
            onChange={onBodyChange}
            currentMethod={currentMethod}
            currentKey={currentKey}
            inputTypeName={inputTypeName}
          />
        )
      ) : (
        <MetadataTab rows={metadataRows} onChange={onMetadataChange} />
      )}
    </div>
  );
}
