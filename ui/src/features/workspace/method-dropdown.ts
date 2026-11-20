// The pure half of MethodDropdown (the header's combined method picker): fuzzy filtering
// over "Service/Method", grouping by service, the trigger's shortened service name, and
// which row starts active. Its own module so it is testable without React.
import { serviceFullName, type ServiceLike } from "./method-picker";

// Indices in `hay` matching `q` as a case-insensitive subsequence, greedy left to right;
// null when it doesn't match. An empty query matches with no hits.
export function fuzzyHits(hay: string, q: string): number[] | null {
  const h = hay.toLowerCase();
  const n = q.toLowerCase();
  const out: number[] = [];
  let j = 0;
  for (let i = 0; i < h.length && j < n.length; i++) {
    if (h[i] === n[j]) {
      out.push(i);
      j++;
    }
  }
  return j === n.length ? out : null;
}

export interface DropdownRow<S extends ServiceLike> {
  service: S;
  method: S["methods"][number];
  // Position in the flat list — what the keyboard moves over.
  index: number;
  // Matched character indices within the method name only.
  hits: number[];
}

export interface DropdownGroup<S extends ServiceLike> {
  full: string;
  service: S;
  rows: DropdownRow<S>[];
}

// Services in input order, each keeping only its matching methods; a service with none
// drops out. Matched on "Service/Method" (the short service name, not the package).
export function groupMethods<S extends ServiceLike>(
  services: readonly S[],
  query: string,
): { groups: DropdownGroup<S>[]; rows: DropdownRow<S>[] } {
  const q = query.trim();
  const groups: DropdownGroup<S>[] = [];
  const rows: DropdownRow<S>[] = [];
  for (const service of services) {
    const off = service.name.length + 1;
    const group: DropdownGroup<S> = {
      full: serviceFullName(service),
      service,
      rows: [],
    };
    for (const method of service.methods) {
      const hits = fuzzyHits(`${service.name}/${method.name}`, q);
      if (hits === null) continue;
      const row: DropdownRow<S> = {
        service,
        method,
        index: rows.length,
        hits: hits.filter((i) => i >= off).map((i) => i - off),
      };
      group.rows.push(row);
      rows.push(row);
    }
    if (group.rows.length > 0) groups.push(group);
  }
  return { groups, rows };
}

// Runs of hit / non-hit characters, for highlighting.
export function highlightSegments(
  text: string,
  hits: readonly number[],
): { text: string; hit: boolean }[] {
  const set = new Set(hits);
  const segs: { text: string; hit: boolean }[] = [];
  for (let i = 0; i < text.length; i++) {
    const hit = set.has(i);
    const last = segs[segs.length - 1];
    if (last && last.hit === hit) last.text += text[i];
    else segs.push({ text: text[i], hit });
  }
  return segs;
}

// The trigger's service label: the full name, or past `max` chars "…<lastPkg>.<Service>".
export function shortServiceName(full: string, max = 32): string {
  if (full.length <= max) return full;
  const parts = full.split(".");
  if (parts.length < 3) return full;
  return `…${parts.slice(-2).join(".")}`;
}

// The row active when the list (re)shapes: the current method on an empty query, else the
// first match. null when nothing is listed.
export function initialActive<S extends ServiceLike>(
  rows: readonly DropdownRow<S>[],
  query: string,
  current: { service: string; method: string } | null,
): number | null {
  if (rows.length === 0) return null;
  if (!query.trim() && current) {
    const i = rows.findIndex(
      (r) =>
        serviceFullName(r.service) === current.service &&
        r.method.name === current.method,
    );
    if (i !== -1) return i;
  }
  return 0;
}
