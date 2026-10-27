// The palette's pure parts: what can be added (native kinds from the boot kinds table, theme
// section types as components), the filter and ranking, and where a keyboard insert goes.
// The Add tab that shows them is the lazy addtab.js.
import { locate, newId, v1Shaped } from "@hm/builder/store";
import { makeKindNames } from "@hm/builder/kindnames";

const title = (s) => s.charAt(0).toUpperCase() + s.slice(1);
const KIND_HINT = { slot: "Where the page content goes", container: "Groups other elements", heading: "A headline", text: "A paragraph", button: "A link styled as a button", image: "A picture", spacer: "Empty space", divider: "A horizontal line" };

// Palette groups in display order; a group the server names that is not here goes after these.
export const GROUP_ORDER = ["Recent", "Patterns", "Layout", "Elements", "Content", "Media", "Interactive", "Data", "Plugin widgets", "Blocks"];

/**
 * [{key, kind, type?, name, hint, group}] native kinds first, then the theme's section types; widget kinds carry their own label, hint and group.
 * Hidden kinds (ref, slot) are not offered, except the Content slot when slot is true (a single or archive layout part).
 */
export function itemsOf(kinds, sections, { slot = false } = {}) {
  const kindName = makeKindNames(kinds);
  const native = kinds
    .filter((k) => k.kind !== "component" && (!k.hidden || (slot && k.kind === "slot")))
    .map((k) => ({ key: k.kind, kind: k.kind, name: k.label || (k.kind === "slot" ? "Content slot" : kindName(k.kind)), hint: k.hint || (KIND_HINT[k.kind] ?? ""), group: k.group || (k.kind === "slot" ? "Layout" : "Elements") }));
  const comps = sections.map((s) => ({ key: `component:${s.type}`, kind: "component", type: s.type, name: s.title || title(s.type), hint: "Theme section", group: "Patterns" }));
  return [...native, ...comps];
}

/** A new node for an item: the kind's default props and seeded children (fresh ids, one subtree, so one undo step), or a component of the section type. */
export function makeNode(item, kinds) {
  if (item.kind === "block") return { id: newId(), t: "ref", p: { block: item.key.slice(4) } }; // a synced block: key is "blk:<id>"
  if (item.kind === "component") return { id: newId(), t: "component", p: { type: item.type } };
  const k = kinds.find((x) => x.kind === item.kind);
  const def = k?.default;
  const node = { id: newId(), t: item.kind, ...(def && Object.keys(def).length ? { p: structuredClone(def) } : {}) };
  if (k?.seed?.length) node.c = k.seed.map(seedNode);
  return node;
}

/** A node from a seed entry {t, p, b, c}: fresh ids all the way down, so a seeded subtree is one undo step. */
export function seedNode(s) {
  const node = { id: newId(), t: s.t };
  if (s.p && Object.keys(s.p).length) node.p = structuredClone(s.p);
  if (s.b && Object.keys(s.b).length) node.b = { ...s.b };
  if (s.c?.length) node.c = s.c.map(seedNode);
  return node;
}

/** Whether nodes of kind t take children: the server's kinds table (without one, only containers). */
export const canHold = (kinds, t) => (kinds ? kinds.find((k) => k.kind === t)?.children === true : t === "container");

/** Where a keyboard insert goes: inside a selected element that takes children (at its end), else after the selection, else at the end of the page. */
export function insertPoint(doc, selectedId, kinds) {
  const at = selectedId ? locate(doc, selectedId) : null;
  if (!at) return { parent: "", index: doc.nodes.length };
  if (canHold(kinds, at.node.t) && !(at.depth === 1 && v1Shaped(at.node))) return { parent: at.node.id, index: (at.node.c ?? []).length };
  return { parent: at.parentId, index: at.index + 1 };
}

/**
 * A "+" point from the canvas (frame keys) as a document position {parent, index}, or null when the sibling or the container
 * it names is not in the doc (the layout lags an undo or a delete): the caller refuses instead of inserting at the top.
 * A v1 section's key names the component inside its top-level container.
 */
export function docPos(doc, pt) {
  const ref = (key) => {
    const l = locate(doc, key);
    const up = l?.parent && locate(doc, l.parent.id);
    return up && up.depth === 1 && v1Shaped(up.node) ? up : l;
  };
  const key = pt.after ?? pt.before;
  if (key) {
    const sib = ref(key);
    return sib ? { parent: sib.parentId, index: sib.index + (pt.after ? 1 : 0) } : null;
  }
  if (!pt.parent) return { parent: "", index: 0 };
  const box = ref(pt.parent);
  return box ? { parent: box.node.id, index: 0 } : null;
}

/** Whether pos ({parent, index}) still names a place in doc: the container exists and the index is not past its children. */
export function posValid(doc, pos) {
  if (!pos) return false;
  const at = pos.parent ? locate(doc, pos.parent) : null;
  const kids = pos.parent ? at && (at.node.c ?? []) : doc.nodes; // an empty container has no `c` yet
  return !!kids && pos.index >= 0 && pos.index <= kids.length;
}

/** The history label of a layout added at pos: a section at the page level, else the element's own name. */
export const addLabel = (pos, name) => (pos.parent ? `Add ${name}` : `Add section: ${name}`);
