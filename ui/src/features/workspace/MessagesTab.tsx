import { Plus, Trash } from "@/components/ui/icons";
import { Tag } from "@/components/ui/Tag";
import { Button } from "@/components/ui/Button";

// MessagesTab is the multi-message compose surface for client-streaming / bidi
// requests (plan §5). The browser transport can't interleave, so the user
// composes the whole request list up-front and Invoke sends it all at once, then
// responses stream back. messages[0] is the persisted primary (mirrors the
// request's single draft_body); messages[1..] are ephemeral compose extras. The
// list is fully controlled — every edit/add/remove funnels through onChange.
export function MessagesTab({
  messages,
  onChange,
  inputTypeName,
}: {
  messages: string[];
  onChange: (next: string[]) => void;
  inputTypeName?: string;
}) {
  const setAt = (i: number, v: string) =>
    onChange(messages.map((m, idx) => (idx === i ? v : m)));
  const add = () => onChange([...messages, "{}"]);
  const removeAt = (i: number) => {
    if (messages.length <= 1) return; // always keep the primary
    onChange(messages.filter((_, idx) => idx !== i));
  };

  return (
    <div className="flex flex-col" style={{ flex: 1, minHeight: 0 }}>
      <div
        className="flex flex-col"
        style={{ flex: 1, minHeight: 0, overflow: "auto", padding: 12, gap: 10 }}
      >
        {messages.map((body, i) => (
          <div
            key={i}
            style={{
              flex: "none", // don't shrink; shrinking + overflow:hidden would clip the card
              border: "1px solid var(--line)",
              borderRadius: 8,
              background: "var(--panel-2)",
              overflow: "hidden",
            }}
          >
            <div
              className="flex items-center gap-[8px]"
              style={{ padding: "6px 10px", borderBottom: "1px solid var(--line)" }}
            >
              <Tag variant="accent">#{i + 1}</Tag>
              {i === 0 && (
                <span className="text-muted" style={{ fontSize: 11 }}>
                  primary · saved
                </span>
              )}
              {messages.length > 1 && (
                <button
                  className="rowbtn danger ml-auto"
                  title="Remove message"
                  onClick={() => removeAt(i)}
                >
                  <Trash size={13} />
                </button>
              )}
            </div>
            <textarea
              className="font-mono"
              spellCheck={false}
              value={body}
              onChange={(e) => setAt(i, e.target.value)}
              style={{
                width: "100%",
                minHeight: 110,
                resize: "vertical",
                display: "block",
                background: "transparent",
                color: "var(--color-text)",
                border: "none",
                outline: "none",
                padding: "10px 12px",
                fontSize: 12.5,
                lineHeight: 1.5,
              }}
            />
          </div>
        ))}
        <div>
          <Button onClick={add} style={{ padding: "5px 11px", fontSize: 12, gap: 6 }}>
            <Plus size={13} /> Add message
          </Button>
        </div>
      </div>
      <div
        className="flex items-center gap-[10px] font-mono"
        style={{
          flex: "none",
          padding: "6px 12px",
          borderTop: "1px solid var(--line)",
          fontSize: 11,
          color: "var(--color-neutral-600)",
        }}
      >
        <span>
          {messages.length} {messages.length === 1 ? "message" : "messages"} ·{" "}
          {inputTypeName ?? "message"}
        </span>
        <span className="ml-auto">sent together, then responses stream back</span>
      </div>
    </div>
  );
}
