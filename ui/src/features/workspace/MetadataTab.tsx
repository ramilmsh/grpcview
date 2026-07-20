import { Plus, Trash } from "@/components/ui/icons";
import { Button } from "@/components/ui/Button";
import type { MetadataRow } from "@/lib/format";

// MetadataTab edits gRPC request metadata (draft_metadata Struct). Ported from
// MetadataEditor (plan §7) with the design's per-row enable checkbox added.
// KNOWN LIMITATION (preserved): list-valued headers collapse to a comma-joined
// string on save, so lists don't round-trip (plan §7/§11).
export function MetadataTab({
  rows,
  onChange,
}: {
  rows: MetadataRow[];
  onChange: (rows: MetadataRow[]) => void;
}) {
  const update = (i: number, patch: Partial<MetadataRow>) => {
    const next = rows.slice();
    next[i] = { ...next[i], ...patch };
    onChange(next);
  };
  const remove = (i: number) => onChange(rows.filter((_, j) => j !== i));
  const add = () => onChange([...rows, { key: "", value: "", enabled: true }]);

  return (
    <div style={{ flex: 1, overflow: "auto", padding: "12px 14px" }}>
      <p className="text-muted" style={{ fontSize: 12, marginBottom: 10 }}>
        Sent as gRPC request metadata. Use a <code>-bin</code> suffix and a base64
        value for binary keys.
      </p>

      {rows.length === 0 ? (
        <div className="text-muted" style={{ fontSize: 13, padding: "12px 0" }}>
          No metadata.
        </div>
      ) : (
        <div
          style={{
            display: "grid",
            gridTemplateColumns: "20px 1fr 1.4fr 28px",
            gap: "8px 10px",
            alignItems: "center",
            fontFamily: "var(--mono)",
            fontSize: 13,
          }}
        >
          {rows.map((row, i) => (
            // eslint-disable-next-line react/no-array-index-key
            <FragmentRow key={i} row={row} i={i} update={update} remove={remove} />
          ))}
        </div>
      )}

      <Button variant="ghost" style={{ marginTop: 12, fontSize: 13, gap: 6 }} onClick={add}>
        <Plus size={14} /> Add metadata
      </Button>
    </div>
  );
}

function FragmentRow({
  row,
  i,
  update,
  remove,
}: {
  row: MetadataRow;
  i: number;
  update: (i: number, patch: Partial<MetadataRow>) => void;
  remove: (i: number) => void;
}) {
  const dim = row.enabled ? 1 : 0.5;
  return (
    <>
      <input
        type="checkbox"
        checked={row.enabled}
        style={{ accentColor: "var(--color-accent)" }}
        onChange={(e) => update(i, { enabled: e.target.checked })}
      />
      <input
        className="bare"
        style={{ opacity: dim }}
        placeholder="key"
        value={row.key}
        onChange={(e) => update(i, { key: e.target.value })}
      />
      <input
        className="bare"
        style={{ opacity: dim }}
        placeholder="value"
        value={row.value}
        onChange={(e) => update(i, { value: e.target.value })}
      />
      <button className="iconbtn" title="Remove" onClick={() => remove(i)}>
        <Trash size={14} />
      </button>
    </>
  );
}
