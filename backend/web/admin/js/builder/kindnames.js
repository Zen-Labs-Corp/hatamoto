// One label map for element kinds, shared by the navigator, canvas tag, palette and announcements.
// Pure and tiny: it is on the first-paint path.
const FIX = { accorditem: "Accordion item", tabpanel: "Tab panel", loopempty: "Empty state", richtext: "Rich text" };

/** kinds = boot.kinds [{kind, label}]. Returns kindName(kind) -> the kind's own label, else a plain title-cased name. */
export function makeKindNames(kinds = []) {
  const m = new Map(kinds.filter((k) => k.label).map((k) => [k.kind, k.label]));
  return (kind) => m.get(kind) ?? FIX[kind] ?? String(kind ?? "").replace(/^./, (c) => c.toUpperCase());
}

/** The row name of a synced block reference. */
export const refName = (name) => `Block: ${name}`;

/** Separates an element's name from its text in a row. */
export const SEP = " · ";
