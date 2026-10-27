// Breakpoint names and the pure style helpers the first-paint shell needs (navigator, screen,
// inspector). The Style tab body itself is a lazy chunk (builder/style).

export const BP_NAME = { base: "Desktop", md: "Tablet", sm: "Phone" };
// Mirrors breakpointMaxWidth in internal/builder/style.go (a Go test pins it).
export const BREAKPOINT_MAX = { md: 1024, sm: 640 };
export const DEVICE_LABEL = { ...BP_NAME, md: `Tablet ≤${BREAKPOINT_MAX.md}`, sm: `Phone ≤${BREAKPOINT_MAX.sm}` };
export const BPS = ["base", "md", "sm"];
const STATE_NAME = { hover: "Hover", focus: "Focus", active: "Pressed", "parent-hover": "Parent hover" };
/** "Tablet" for md, "Hover, Tablet" for md:hover. */
export const atLabel = (at) => {
  const [b, st] = at.split(":");
  return st ? `${STATE_NAME[st]}, ${BP_NAME[b]}` : BP_NAME[b];
};
const WIDER = { base: [], md: ["base"], sm: ["md", "base"] };

export const NAMES = {
  dir: "Direction", ai: "Align items", jc: "Justify", wrap: "Wrap", cols: "Columns", rows: "Rows", gap: "Gap", span: "Column span", as: "Align self",
  pt: "Padding top", pb: "Padding bottom", pl: "Padding left", pr: "Padding right", mt: "Margin top", mb: "Margin bottom",
  maxw: "Max width", w: "Width", minh: "Min height", bg: "Background", color: "Text colour", radius: "Corners", fs: "Font size",
  ta: "Text align", hide: "Hide on",
};

/** The value set at exactly bp, or null. */
export const ownValue = (node, bp, key) => node.s?.[bp]?.[key] ?? null;

/** The nearest wider breakpoint's value for key: {value, bp} or null. */
export function inheritedValue(node, bp, key) {
  for (const b of WIDER[bp] ?? []) {
    const v = node.s?.[b]?.[key];
    if (v !== undefined && v !== null) return { value: v, bp: b };
  }
  return null;
}

/**
 * Whether node is not shown at bp. Walks desktop-first: within one width hide wins, else
 * display re-shows (or "none" hides); a width with neither keeps what the wider one gave.
 */
export function hiddenAt(node, bp) {
  let hidden = false;
  for (const b of BPS.slice(0, BPS.indexOf(bp) + 1)) {
    const s = node.s?.[b];
    if (s?.hide) hidden = true;
    else if (s?.display) hidden = s.display === "none";
  }
  return hidden;
}

/**
 * The display value node has when shown at bp: the nearest wider display the author set (not none), else
 * the kind's own (kindDisplay, from the server's kinds table; a container follows its layout prop).
 */
export function natural(node, bp, kindDisplay) {
  for (const b of WIDER[bp] ?? []) {
    const d = node.s?.[b]?.display;
    if (d && d !== "none") return d;
  }
  if (node.t === "container") return node.p?.layout === "grid" ? "grid" : "flex";
  return kindDisplay ?? (node.t === "button" ? "inline-block" : "block");
}
