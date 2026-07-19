import React from "react";
import { Plus, Trash2 } from "lucide-react";
import { JsonObject } from "@bufbuild/protobuf";

// A single metadata (header) entry. Rows are the editor's working
// representation: they keep insertion order and allow a half-typed empty key,
// neither of which a plain object preserves.
export interface MetadataRow {
  key: string;
  value: string;
}

// rowsToObject drops rows with a blank key and produces the JsonObject that maps
// onto the request's google.protobuf.Struct metadata field.
export const rowsToObject = (rows: MetadataRow[]): JsonObject => {
  const obj: JsonObject = {};
  for (const { key, value } of rows) {
    const k = key.trim();
    if (k) obj[k] = value;
  }
  return obj;
};

// metadataValueToString renders a metadata value for display: list values
// (multi-valued metadata) are comma-joined; scalars are stringified.
export const metadataValueToString = (value: unknown): string =>
  Array.isArray(value)
    ? value.map((v) => String(v)).join(", ")
    : String(value ?? "");

// objectToRows expands persisted metadata back into editable rows. List values
// (multi-valued metadata) are shown comma-joined.
export const objectToRows = (obj?: JsonObject): MetadataRow[] => {
  if (!obj) return [];
  return Object.entries(obj).map(([key, value]) => ({
    key,
    value: metadataValueToString(value),
  }));
};

interface MetadataEditorProps {
  rows: MetadataRow[];
  onChange: (rows: MetadataRow[]) => void;
}

export const MetadataEditor: React.FC<MetadataEditorProps> = ({
  rows,
  onChange,
}) => {
  const update = (index: number, patch: Partial<MetadataRow>) => {
    const next = rows.slice();
    next[index] = { ...next[index], ...patch };
    onChange(next);
  };
  const remove = (index: number) =>
    onChange(rows.filter((_, i) => i !== index));
  const add = () => onChange([...rows, { key: "", value: "" }]);

  return (
    <div className="p-3 h-full overflow-y-auto">
      <div className="text-xs text-gray-500 mb-2">
        Sent as gRPC request metadata. Use a <code>-bin</code> suffix and a
        base64 value for binary keys.
      </div>

      {rows.length === 0 ? (
        <div className="text-sm text-gray-400 py-4 text-center">
          No metadata.
        </div>
      ) : (
        <div className="space-y-1">
          {rows.map((row, index) => (
            <div key={index} className="flex gap-2 items-center">
              <input
                className="border rounded px-2 py-1 text-sm w-1/3 outline-none focus:border-purple-600 font-mono"
                placeholder="key"
                value={row.key}
                onChange={(e) => update(index, { key: e.target.value })}
              />
              <input
                className="border rounded px-2 py-1 text-sm flex-grow outline-none focus:border-purple-600 font-mono"
                placeholder="value"
                value={row.value}
                onChange={(e) => update(index, { value: e.target.value })}
              />
              <button
                onClick={() => remove(index)}
                className="text-gray-400 hover:text-red-600 p-1"
                title="Remove"
              >
                <Trash2 size={16} />
              </button>
            </div>
          ))}
        </div>
      )}

      <button
        onClick={add}
        className="mt-2 flex items-center gap-1 text-purple-600 hover:bg-purple-50 px-2 py-1 rounded text-sm font-medium"
      >
        <Plus size={16} /> Add metadata
      </button>
    </div>
  );
};
