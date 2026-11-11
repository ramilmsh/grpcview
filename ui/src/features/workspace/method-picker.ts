// The pure half of MethodPicker: what each pane lists for a query, and how the keyboard
// moves between and within the panes. Its own module so it is testable without React.
import type { MethodKind } from "@/components/ui/Tag";

// Structural, so tests can pass plain objects; the real Service/Method satisfy it.
export interface ServiceLike {
  name: string;
  package: string;
  methods: readonly { name: string }[];
}

const has = (s: string, q: string): boolean => s.toLowerCase().includes(q);

const norm = (query: string): string => query.trim().toLowerCase();

// Same as format.ts's serviceName, structurally typed.
export const serviceFullName = (s: {
  name: string;
  package: string;
}): string => (s.package ? `${s.package}.${s.name}` : s.name);

const serviceHit = (s: ServiceLike, q: string): boolean =>
  has(s.name, q) || has(s.package, q);

// A service stays when its name or package matches, or any of its methods does.
export function filterServices<S extends ServiceLike>(
  services: readonly S[],
  query: string,
): S[] {
  const q = norm(query);
  if (!q) return [...services];
  return services.filter(
    (s) => serviceHit(s, q) || s.methods.some((m) => has(m.name, q)),
  );
}

// A service that matched on its own name or package keeps every method; one that only
// stayed for its methods shows just those.
export function filterMethods<S extends ServiceLike>(
  service: S,
  query: string,
): S["methods"][number][] {
  const q = norm(query);
  if (!q || serviceHit(service, q)) return [...service.methods];
  return service.methods.filter((m) => has(m.name, q));
}

export const KIND_NAME: Record<MethodKind, string> = {
  u: "Unary",
  ss: "Server stream",
  cs: "Client stream",
  bd: "Bidi stream",
};

export type PickerPane = "services" | "methods";

// Arrow-key movement over `count` rows. null = nothing highlighted yet: Down lands on the
// first row, Up on the last.
export function moveIndex(
  index: number | null,
  count: number,
  delta: 1 | -1,
): number | null {
  if (count === 0) return null;
  if (index === null) return delta === 1 ? 0 : count - 1;
  return Math.min(count - 1, Math.max(0, index + delta));
}
