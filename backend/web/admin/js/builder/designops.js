// Design ops (a lazy chunk): the op builders that edit a node's preset and the copy / paste
// style actions. drift, the cascade and the clipboard itself stay in design.js (first paint).
// Pure: each is a list of the store's existing ops, so one commit is one undo step.
import { find } from "@hm/builder/store";
import { BPS, cascade, clipboard, drift, entries, get } from "@hm/builder/design";

const setStyle = (id, bp, key, value) => ({ op: "setStyle", id, bp, key, value });
const setPreset = (id, value) => ({ op: "setProp", id, key: "preset", value });

export const applyOps = (node, name) => (node.p?.preset === name ? [] : [setPreset(node.id, name || null)]);

/** Clear own values until nothing differs from the preset: one click, one undo step. */
export function resetOps(node, preset) {
  const ops = [];
  const s = structuredClone(node.s ?? {});
  for (let pass = 0; pass < BPS.length; pass++) {
    const d = drift({ ...node, s }, [{ name: node.p?.preset, ...preset }], null);
    if (!d.overrides.length) break;
    for (const { bp, key } of d.overrides) {
      ops.push(setStyle(node.id, bp, key, null));
      delete s[bp]?.[key];
    }
  }
  return ops;
}

/** Copy the preset's values the node does not own (at that breakpoint or a wider one) onto the node, then drop the reference. */
export function detachOps(node, preset) {
  const ops = entries(preset?.s).filter(({ bp, key }) => cascade(node.s, bp, key) === null).map(({ bp, key, value }) => setStyle(node.id, bp, key, value));
  return [...ops, setPreset(node.id, null)];
}

/** After "save as preset" the node references it and drops the own values the preset now holds. */
export const savePresetOps = (node, name, preset) => [setPreset(node.id, name), ...entries(node.s).filter(({ bp, key, value }) => get(preset?.s, bp, key) === value).map(({ bp, key }) => setStyle(node.id, bp, key, null))];

/** The POST body of a new preset: a copy of the node's own style map. */
export function presetBodyFrom(node) {
  const s = {};
  for (const { bp, key, value } of entries(node.s)) if (typeof value === "string" && !bp.endsWith(":parent-hover")) (s[bp] ??= {})[key] = value; // presets have no parent context
  return { s };
}

// ---- copy / paste style ----------------------------------------------------------

/** Remember the node's whole style map and preset (a deep copy). */
export function copyStyle(node) {
  clipboard.set({ id: node.id, s: structuredClone(node.s ?? {}), preset: node.p?.preset ?? null });
}

/** Ops that make target look like the copied node, replacing its style map and preset; [] when nothing to do.
 * parent-hover values are skipped unless the target can take them (opts.parentHover): the server refuses the whole batch otherwise. */
export function pasteOps(target, doc, { parentHover = false } = {}) {
  const held = clipboard.get();
  if (!held || held.id === target.id || (doc && !find(doc, held.id))) return [];
  const ops = [];
  const skip = (bp) => !parentHover && bp.endsWith(":parent-hover");
  for (const { bp, key } of entries(target.s)) if (get(held.s, bp, key) === null) ops.push(setStyle(target.id, bp, key, null));
  for (const { bp, key, value } of entries(held.s)) if (!skip(bp) && get(target.s, bp, key) !== value) ops.push(setStyle(target.id, bp, key, value));
  if (target.t !== "component" && (target.p?.preset ?? null) !== held.preset) ops.push(setPreset(target.id, held.preset)); // components take no preset
  return ops;
}
