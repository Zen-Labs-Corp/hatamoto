// Where a preset-provided value comes from (a lazy chunk, pure): the Style tab's source label says
// "Preset card" for a key the node does not own but its preset gives. The cascade is design.js's own
// (a value at a breakpoint also holds at every narrower one), never a copy of it.
import { cascade } from "@hm/builder/design";

/** The preset a node names, from the boot presets ([{name, label, s}]), or null. */
export const presetOf = (node, presets) => (node?.p?.preset ? ((presets ?? []).find((p) => p.name === node.p.preset) ?? null) : null);

/** The value preset gives key in state (never another state's, never Normal's) at breakpoint bp, or null. */
export const presetStateValueAt = (preset, bp, state, key) => (preset?.s && state ? cascade(preset.s, `${bp}:${state}`, key) : null);

/**
 * The value preset gives key at breakpoint bp and state ("" for Normal), or null. A state reads its own values
 * first, then the Normal ones at the same width. (The Style tab asks the two apart: the server puts the node's
 * own Normal value between them, see controls.rowState.)
 */
export function presetValueAt(preset, bp, state, key) {
  if (!preset?.s) return null;
  return presetStateValueAt(preset, bp, state, key) ?? cascade(preset.s, bp, key);
}
