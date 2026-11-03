// The Style tab's Figma-style groups: the existing style groups (style.js GROUPS) regrouped into Layout, Spacing, Fill,
// Typography, Corners & border and Effects. Pure data and rules, no DOM and no import of style.js, so it is node-tested.
// A lazy chunk. Run: node --test scripts/admin-qa/unit/builder-inspgroups.test.mjs

import { groupDot, isLength } from "@hm/builder/controls";

/** The Figma groups in display order; `from` names the style groups each one takes (every old group lands in exactly one). */
const FIGMA = [
  { name: "Layout", from: ["Layout", "In its parent", "Size"] },
  { name: "Spacing", from: ["Spacing"] },
  { name: "Fill", from: ["Background"] },
  { name: "Typography", from: ["Typography"] },
  { name: "Corners & border", from: ["Border"] },
  { name: "Effects", from: ["Effects", "Position", "Transform", "Filter", "Transition", "Visibility"] },
];

/** The kinds whose content is text: their Typography group comes first and starts open. */
const TEXT_KINDS = new Set(["heading", "text", "button"]);
export const isTextKind = (node) => TEXT_KINDS.has(node?.t);

/** The keys each group shows by default, in display order; the rest of the group goes behind its "More options" toggle. */
export const PRIMARY = {
  Layout: ["maxw", "ai", "gap", "minh"],
  Spacing: [],
  Fill: ["bg"],
  Typography: ["fs", "fw", "lh", "ls", "ta", "color"],
  "Corners & border": ["radius", "bw"],
  Effects: ["shadow"],
};
/** A text kind shows only the top and bottom margin of Spacing by default (the box model is for sections). */
const TEXT_SPACING = ["mt", "mb"];

/** The width presets (Full | Wide | Text) and the `maxw` value each one writes (existing key, no new token). */
export const WIDTH_PRESETS = { full: "full", wide: "container", text: "45rem" };
/** The preset a `maxw` value is (full, wide, text), or "" when it is none of them. */
export const widthOf = (maxw) => Object.entries(WIDTH_PRESETS).find(([, v]) => v === maxw)?.[0] ?? "";

/** The keys of a group that are shown by default for node: a section's Layout rows, a text block's margins; none for other Layouts (they are all More). */
function primaryKeys(name, node) {
  if (name === "Layout" && node?.t !== "container") return [];
  if (name === "Spacing") return isTextKind(node) ? TEXT_SPACING : [];
  return PRIMARY[name] ?? [];
}

/**
 * Regroups groupsFor's output ([{name, entries}]): [{name, entries, primary, more, sub}] with every entry exactly once.
 * `entries` is the whole group (for the set-here dot); `sub` is [{name, entries}] per style group it took, in order: the UI
 * draws a sub-heading for a sub whose name differs from the group's. `primary` (in PRIMARY order) and `more` (in
 * group order) split `entries`. Empty groups are dropped.
 * For a text kind (node) Typography is the first group; every other kind keeps the order above.
 */
export function figmaGroups(groups, node = null) {
  const by = new Map(groups.map((g) => [g.name, g]));
  const order = isTextKind(node) ? [...FIGMA.filter((f) => f.name === "Typography"), ...FIGMA.filter((f) => f.name !== "Typography")] : FIGMA;
  return order.map((f) => {
    const sub = f.from.map((n) => by.get(n)).filter(Boolean).map((g) => ({ name: g.name, entries: g.entries }));
    const entries = sub.flatMap((s) => s.entries);
    const keys = primaryKeys(f.name, node);
    const primary = keys.map((k) => entries.find((e) => e.key === k)).filter(Boolean);
    return { name: f.name, entries, primary, more: entries.filter((e) => !primary.includes(e)), sub };
  }).filter((g) => g.entries.length);
}

/** Whether any entry of a group has a value set here (breakpoint + state). own(key) -> value | null. */
export const groupHasValue = (entries, own) => groupDot(entries, own);

/** Whether any entry of a group's More list has a value set here: the disclosure then starts open. */
export const moreHasValue = (g, own) => groupDot(g.more, own);

/** A group starts open when it has a value set here; Layout and Spacing also start open on a container (a section), Typography on a text kind. */
export const initialOpen = (name, hasValue, node) => hasValue || (node?.t === "container" && (name === "Layout" || name === "Spacing")) || (name === "Typography" && isTextKind(node));

/** Short field labels for the scrub handles; the full name stays the accessible name. */
export const SHORT = { w: "W", maxw: "W", h: "H", minh: "H", gap: "Gap", fs: "Sz", fw: "Wt", lh: "LH", ls: "LS", radius: "R", bw: "B", mt: "↑", mb: "↓" };

/** Whether the label of entry scrubs: a length or a numeric lit key, except the box model sides (they scrub on their own number). */
const SIDES = new Set(["pt", "pr", "pb", "pl", "mt", "mr", "mb", "ml"]);
export const canScrub = (entry) => isLength(entry) && !SIDES.has(entry.key);
