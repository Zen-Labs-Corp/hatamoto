// Reuse helpers: pure op builders and list/label logic for templates and synced
// blocks (refs). Every ops array runs through one commit, so each action is one
// undo step. No DOM, no fetch; server ids are opaque strings, never selectors.
// Run: node --test scripts/admin-qa/unit/builder-reuse.test.mjs
import { locate, newId, v1Shaped } from "@hm/builder/store";
import { refName } from "@hm/builder/kindnames";
import { insertPoint } from "@hm/builder/palette";

/** A ref node pointing at a block. */
export const refNode = (blockId, id = newId()) => ({ id, t: "ref", p: { block: blockId } });

const must = (doc, id) => {
  const loc = locate(doc, id);
  if (!loc) throw new Error("Element not found.");
  return loc;
};

export const insertAll = (parent, index, nodes) => nodes.map((node, k) => ({ op: "insert", parent, index: index + k, node }));

/** Replace a node by a ref to blockId, in the same place. */
export function makeBlockOps(doc, nodeId, blockId) {
  const loc = must(doc, nodeId);
  return [{ op: "remove", id: nodeId }, ...insertAll(loc.parentId, loc.index, [refNode(blockId)])];
}

/** Replace a ref by nodes (already fresh ids), in order, in the same place. */
export function detachOps(doc, refId, nodes) {
  const loc = must(doc, refId);
  return [{ op: "remove", id: refId }, ...insertAll(loc.parentId, loc.index, nodes)];
}

/** Insert nodes at the palette's keyboard insert point, in order. */
export function insertOps(doc, selectedId, nodes, kinds) {
  const at = insertPoint(doc, selectedId, kinds);
  return insertAll(at.parent, at.index, nodes);
}

/** Palette items for boot `reuse` ({templates:[{id,name,hint?}], blocks:[...]}); the Add tab and Ctrl+K filter them (search.js). */
export function reuseItems(reuse) {
  const items = [
    ...(reuse?.templates ?? []).map((t) => ({ key: `tpl:${t.id}`, name: t.name, hint: t.hint ?? "", group: "Patterns", kind: "template" })),
    ...(reuse?.blocks ?? []).map((b) => ({ key: `blk:${b.id}`, name: b.name, hint: b.hint ?? "", group: "Blocks", kind: "block" })),
  ];
  return items.map(({ kind, ...i }) => i);
}

/** Navigator/inspector label of a ref node; blocks is [{id,name}]. */
export function refLabel(node, blocks) {
  const b = (blocks ?? []).find((x) => x.id === node?.p?.block);
  return refName(b ? b.name : "(missing)");
}

const hasKind = (n, t) => n.t === t || (n.c ?? []).some((c) => hasKind(c, t));

/** Whether the node can become a synced block: not a ref, not part of a v1-shaped section, no slot inside. */
export function canMakeBlock(doc, nodeId, kinds) {
  const loc = nodeId ? locate(doc, nodeId) : null;
  if (!loc || loc.node.t === "ref" || hasKind(loc.node, "slot")) return false;
  if (loc.parent && loc.depth === 2 && v1Shaped(loc.parent)) return false;
  return !kinds || kinds.some((k) => k.kind === loc.node.t) || loc.node.t === "component";
}

/** What a save-as-template request sends: the selected node, or the whole page. */
export const templateSource = (doc, selectedId) => (selectedId && locate(doc, selectedId) ? { nodeId: selectedId } : { page: true });
