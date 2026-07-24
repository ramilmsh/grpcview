import { ArrowsSplit, BracketsCurly } from "@/components/ui/icons";
import { Subtab } from "@/components/ui/Subtab";
import { Tag, type MethodKind } from "@/components/ui/Tag";
import { useUIStore } from "@/lib/ui-store";
import { MessageTab } from "./MessageTab";
import { MessagesTab } from "./MessagesTab";
import { MetadataTab } from "./MetadataTab";
import { MiddlewareTab } from "./MiddlewareTab";

// RequestPane holds the Message + Metadata + Middleware subtabs (plan §1.4/§S3).
// The message subtab is the single-body editor for unary / server-streaming and
// the multi-message compose list for client-streaming / bidi (plan §5). Middleware
// is the ordered pre-invoke chain attached to this request (server state — see
// MiddlewareTab). Auth/Options/Variants land with later phases.
export function RequestPane({
  kind,
  body,
  onBodyChange,
  messages,
  onMessagesChange,
  metadata,
  onMetadataChange,
  middleware,
  onMiddlewareChange,
  currentKey,
  inputTypeName,
  descriptorSet,
  inputPackage,
  inputFile,
  generators,
}: {
  kind: MethodKind;
  body: string;
  onBodyChange: (v: string) => void;
  messages: string[];
  onMessagesChange: (next: string[]) => void;
  metadata: string;
  onMetadataChange: (v: string) => void;
  middleware: string[];
  onMiddlewareChange: (next: string[]) => void;
  currentKey: string;
  inputTypeName?: string;
  // T2 typed-body inputs, passed through to MessageTab → Editor.
  descriptorSet?: Uint8Array;
  inputPackage?: string;
  inputFile?: string;
  // T3 composition: workspace generator names, forwarded to MessageTab → Editor.
  generators: string[];
}) {
  const subtab = useUIStore((s) => s.requestSubtab);
  const setSubtab = useUIStore((s) => s.setRequestSubtab);
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
        </Subtab>
        <Subtab active={subtab === "middleware"} onClick={() => setSubtab("middleware")}>
          <ArrowsSplit size={14} />
          Middleware
          {middleware.length > 0 && (
            <Tag variant="accent" className="ml-[2px]">
              {middleware.length}
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
            body={body}
            onChange={onBodyChange}
            currentKey={currentKey}
            inputTypeName={inputTypeName}
            descriptorSet={descriptorSet}
            inputPackage={inputPackage}
            inputFile={inputFile}
            generators={generators}
          />
        )
      ) : subtab === "metadata" ? (
        <MetadataTab
          metadata={metadata}
          onChange={onMetadataChange}
          currentKey={currentKey}
          generators={generators}
        />
      ) : (
        <MiddlewareTab middleware={middleware} onChange={onMiddlewareChange} />
      )}
    </div>
  );
}
