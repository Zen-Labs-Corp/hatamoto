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

/**
 * Regroups groupsFor's output ([{name, entries}]): [{name, entries, sub}] with every entry exactly once.
 * `entries` is the whole group (for the set-here dot); `sub` is [{name, entries}] per style group it took, in order: the UI
 * draws a sub-heading for a sub whose name differs from the group's. Empty groups are dropped.
 */
export function figmaGroups(groups) {
  const by = new Map(groups.map((g) => [g.name, g]));
  return FIGMA.map((f) => {
    const sub = f.from.map((n) => by.get(n)).filter(Boolean).map((g) => ({ name: g.name, entries: g.entries }));
    return { name: f.name, entries: sub.flatMap((s) => s.entries), sub };
  }).filter((g) => g.entries.length);
}

/** Whether any entry of a group has a value set here (breakpoint + state). own(key) -> value | null. */
export const groupHasValue = (entries, own) => groupDot(entries, own);

/** A group starts open when it has a value set here; Layout and Spacing also start open on a container (a section). */
export const initialOpen = (name, hasValue, node) => hasValue || (node?.t === "container" && (name === "Layout" || name === "Spacing"));

/** Short field labels for the scrub handles; the full name stays the accessible name. */
export const SHORT = { w: "W", h: "H", gap: "Gap", fs: "Sz", lh: "LH", ls: "LS", fw: "Wt" };

/** Whether the label of entry scrubs: a length or a numeric lit key, except the box model sides (they scrub on their own number). */
const SIDES = new Set(["pt", "pr", "pb", "pl", "mt", "mr", "mb", "ml"]);
export const canScrub = (entry) => isLength(entry) && !SIDES.has(entry.key);
