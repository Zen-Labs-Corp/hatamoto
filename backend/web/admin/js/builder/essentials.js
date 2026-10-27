// The Style tab's Essentials: the five or six controls most edits need, per kind (a lazy chunk,
// pure). Each key is the same control the groups under "More style options" would show; a key
// listed here is hidden there, so a value is edited in one place only. "spacing" stands for the
// box model: the margin ring and the padding ring in one control (mt..ml, pt..pr), the only place a margin is set.
export const ESSENTIALS = {
  heading: ["fs", "fw", "color", "ta", "spacing"],
  button: ["bg", "color", "radius", "spacing", "fs", "w"],
  container: ["dir", "gap", "spacing", "bg", "radius", "w"],
  image: ["w", "h", "fit", "radius", "spacing"],
  _: ["spacing", "bg", "radius", "w"],
};

/** The labels an essential control wears when the table's name is not the plain one. */
export const ESSENTIAL_NAMES = { dir: "Layout direction", ta: "Alignment", fit: "Image fit" };

export const PADDING = ["pt", "pb", "pl", "pr"];
export const MARGIN = ["mt", "mb", "ml", "mr"];
export const SPACING = [...MARGIN, ...PADDING];

/** The essential keys for node that the table offers (the box model needs the four padding sides; the margin ring shows when the table has its four too), in display order. */
export function essentialsFor(node, table) {
  const have = new Set(table.map((e) => e.key));
  return (ESSENTIALS[node.t] ?? ESSENTIALS._).filter((k) => (k === "spacing" ? PADDING.every((p) => have.has(p)) : have.has(k))).filter((k) => !(k === "dir" && node.p?.layout === "grid"));
}

/** Every style key the essentials cover: the groups below leave these out. */
export const coveredKeys = (keys) => keys.flatMap((k) => (k === "spacing" ? SPACING : [k]));
