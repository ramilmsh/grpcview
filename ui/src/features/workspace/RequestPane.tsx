import type { BodyLanguage } from "@grpcview/v1/workspace_pb";
import { ArrowsSplit, BracketsCurly } from "@/components/ui/icons";
import { Subtab } from "@/components/ui/Subtab";
import { Tag, type MethodKind } from "@/components/ui/Tag";
import { useUIStore } from "@/lib/ui-store";
import type { MetadataRow } from "@/lib/format";
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
  schema,
  kind,
  body,
  onBodyChange,
  messages,
  onMessagesChange,
  metadataRows,
  onMetadataChange,
  middleware,
  onMiddlewareChange,
  currentMethod,
  currentKey,
  inputTypeName,
  bodyLanguage,
  descriptorSet,
  inputPackage,
  inputFile,
  onBodyLanguageChange,
}: {
  schema?: object;
  kind: MethodKind;
  body: string;
  onBodyChange: (v: string) => void;
  messages: string[];
  onMessagesChange: (next: string[]) => void;
  metadataRows: MetadataRow[];
  onMetadataChange: (rows: MetadataRow[]) => void;
  middleware: string[];
  onMiddlewareChange: (next: string[]) => void;
  currentMethod: { service: string; method: string };
  currentKey: string;
  inputTypeName?: string;
  bodyLanguage: BodyLanguage;
  // T2 typed-body inputs, passed through to MessageTab → Editor (used only in TS mode).
  descriptorSet?: Uint8Array;
  inputPackage?: string;
  inputFile?: string;
  onBodyLanguageChange: (next: BodyLanguage) => void;
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
            schema={schema}
            body={body}
            onChange={onBodyChange}
            currentMethod={currentMethod}
            currentKey={currentKey}
            inputTypeName={inputTypeName}
            bodyLanguage={bodyLanguage}
            descriptorSet={descriptorSet}
            inputPackage={inputPackage}
            inputFile={inputFile}
            onBodyLanguageChange={onBodyLanguageChange}
          />
        )
      ) : subtab === "metadata" ? (
        <MetadataTab rows={metadataRows} onChange={onMetadataChange} />
      ) : (
        <MiddlewareTab middleware={middleware} onChange={onMiddlewareChange} />
      )}
    </div>
  );
}
