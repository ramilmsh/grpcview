import { useState } from "react";
import { Plus, X } from "@/components/ui/icons";
import { MessageTab } from "./MessageTab";

// Client-streaming / bidi compose surface: the same body editor as unary, one message at a
// time, with a strip to switch between messages and add more. The browser transport can't
// interleave, so the whole list is sent at once. messages[0] is persisted.
export function MessagesTab({
  messages,
  onChange,
  currentKey,
  inputTypeName,
  inputPackage,
  inputFile,
}: {
  messages: string[];
  onChange: (next: string[]) => void;
  currentKey: string;
  inputTypeName?: string;
  inputPackage?: string;
  inputFile?: string;
}) {
  const [selected, setSelected] = useState(0);
  const sel = Math.min(selected, messages.length - 1);

  const setAt = (i: number, v: string) =>
    onChange(messages.map((m, idx) => (idx === i ? v : m)));
  const add = () => {
    onChange([...messages, "{}"]);
    setSelected(messages.length);
  };
  const removeAt = (i: number) => {
    if (messages.length <= 1) return;
    onChange(messages.filter((_, idx) => idx !== i));
    if (sel >= i && sel > 0) setSelected(sel - 1);
  };

  return (
    <div className="flex flex-col" style={{ flex: 1, minHeight: 0 }}>
      <div
        role="tablist"
        aria-label="Messages"
        className="flex items-center gap-[4px]"
        style={{
          flex: "none",
          padding: "6px 8px",
          borderBottom: "1px solid var(--v-line)",
          overflowX: "auto",
        }}
      >
        {messages.map((_, i) => (
          <div
            key={i}
            className={i === sel ? "msgchip on" : "msgchip"}
            title={
              i === 0 ? "Primary message — saved with the request" : undefined
            }
          >
            <button
              type="button"
              role="tab"
              aria-selected={i === sel}
              className="msgchip-label"
              onClick={() => setSelected(i)}
            >
              #{i + 1}
            </button>
            {messages.length > 1 && (
              <button
                type="button"
                className="msgchip-x"
                aria-label={`Remove message ${i + 1}`}
                title="Remove message"
                onClick={() => removeAt(i)}
              >
                <X size={11} />
              </button>
            )}
          </div>
        ))}
        <button type="button" className="msgchip-add" onClick={add}>
          <Plus size={12} /> Add message
        </button>
        <span
          className="ml-auto font-mono"
          style={{
            flex: "none",
            fontSize: 11,
            color: "var(--v-text-muted)",
            paddingRight: 4,
          }}
        >
          sent together, then responses stream back
        </span>
      </div>
      <MessageTab
        body={messages[sel] ?? ""}
        onChange={(v) => setAt(sel, v)}
        // Per-message identity: switching messages swaps the editor buffer.
        currentKey={`${currentKey}#${sel}`}
        inputTypeName={inputTypeName}
        inputPackage={inputPackage}
        inputFile={inputFile}
      />
    </div>
  );
}
