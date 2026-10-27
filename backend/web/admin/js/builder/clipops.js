// Node clipboard and bulk operations (a lazy chunk). This first half is pure: ops over a doc
// value and the clipboard envelope, tested under node. The second half (copy, cut, paste,
// wrap, select siblings) takes the screen's ctx and talks to the system clipboard and server.
import { locate, find, siblings, newId, freshCopy, unwrapOps, v1Shaped, moveSubject } from "@hm/builder/store";
import { insertOps } from "@hm/builder/reuseops";
import { canHold } from "@hm/builder/palette";

/** What goes on the system clipboard: {"hm":"nodes","v":2,"nodes":[...],"site":origin}. */
export const envelope = (nodes, site = "") => ({ hm: "nodes", v: 2, nodes, site });

/** The nodes of clipboard text, or null when it is not a non-empty nodes envelope. The server still validates. */
export function parseEnvelope(text) {
  let m;
  try {
    m = JSON.parse(text);
  } catch {
    return null;
  }
  return m && typeof m === "object" && m.hm === "nodes" && m.v === 2 && Array.isArray(m.nodes) && m.nodes.length > 0 ? m.nodes : null;
}

/** The ids of ids that exist, in document order, without any whose ancestor is also in ids. A v1 section selected through its component counts as the section. */
export function topmost(doc, ids) {
  const want = new Set(ids.map((id) => moveSubject(doc, id)));
  const out = [];
  const walk = (ns) => {
    for (const n of ns) {
      if (want.has(n.id)) out.push(n.id);
      else if (n.c) walk(n.c);
    }
  };
  walk(doc.nodes);
  return out;
}

/** {parentId, index, ids} when ids are adjacent siblings (any order given, doc order returned), else null. */
export function contiguous(doc, ids) {
  const locs = [...new Set(ids)].map((id) => locate(doc, id));
  if (locs.length === 0 || locs.some((l) => !l) || locs.some((l) => l.parentId !== locs[0].parentId)) return null;
  locs.sort((a, b) => a.index - b.index);
  if (locs.some((l, i) => l.index !== locs[0].index + i)) return null;
  return { parentId: locs[0].parentId, index: locs[0].index, ids: locs.map((l) => l.node.id) };
}

/** Insert an empty wrapper where the first of ids is and move all of ids into it, in order; null unless ids are contiguous siblings. */
export function wrapManyOps(doc, ids, kind = "container") {
  const run = contiguous(doc, ids);
  if (!run) return null;
  const wrapper = { id: newId(), t: kind };
  return [
    { op: "insert", parent: run.parentId, index: run.index, node: wrapper },
    ...run.ids.map((id, i) => ({ op: "move", id, parent: wrapper.id, index: i })),
  ];
}

/** Remove every selected node (a selected node's descendants go with it). */
export function deleteManyOps(doc, ids) {
  return topmost(doc, ids).map((id) => ({ op: "remove", id }));
}

/** A fresh-id copy of each selected node right after it. Later nodes first, so earlier indexes stay valid. */
export function duplicateManyOps(doc, ids) {
  return topmost(doc, ids)
    .reverse()
    .map((id) => {
      const l = locate(doc, id);
      return { op: "insert", parent: l.parentId, index: l.index + 1, node: freshCopy(l.node) };
    });
}

/** Deep copies of the selected nodes in document order, for the clipboard. */
export function copyNodes(doc, ids) {
  return topmost(doc, ids).map((id) => structuredClone(find(doc, id)));
}

/**
 * The selection after a click on id. Plain click: just id. Ctrl/Cmd toggles id. Shift selects the
 * siblings between the anchor and id (adding to the selection with Ctrl); an anchor under another
 * parent makes it an ordinary click. primary is the element the inspector shows (the last picked).
 * Returns {selection: Set, primary, anchor}.
 */
export function pickSelection(doc, selection, anchor, id, { shift = false, ctrl = false } = {}) {
  const at = locate(doc, id);
  if (!at) return { selection: new Set(), primary: null, anchor: null };
  const from = shift && anchor ? locate(doc, anchor) : null;
  if (from && from.parentId === at.parentId) {
    const [a, b] = [from.index, at.index].sort((x, y) => x - y);
    const range = siblings(doc, at.parentId)
      .slice(a, b + 1)
      .map((n) => n.id);
    return { selection: new Set(ctrl ? [...selection, ...range] : range), primary: id, anchor };
  }
  if (ctrl) {
    const next = new Set(selection);
    if (next.delete(id)) {
      const primary = [...next].pop() ?? null;
      return { selection: next, primary, anchor: primary };
    }
    return { selection: next.add(id), primary: id, anchor: id };
  }
  return { selection: new Set([id]), primary: id, anchor: id };
}

// The bulk actions of the screen: ctx is {doc, run(ops, opts) -> bool, select(id), selectAll(ids), announce(msg)}.

/** Removes the selection in one history step and selects the nearest survivor. */
export function removeMany(ctx, ids, name = "Delete") {
  const ops = deleteManyOps(ctx.doc, ids);
  const first = locate(ctx.doc, ops[0]?.id);
  if (!first) return;
  const gone = new Set(ops.map((o) => o.id));
  const sib = siblings(ctx.doc, first.parentId);
  const keep = (n) => !gone.has(n.id);
  const next = sib.slice(first.index + 1).find(keep) ?? sib.slice(0, first.index).reverse().find(keep) ?? first.parent;
  if (!ctx.run(ops, { label: name === "Cut" ? `Cut ${plural(ops.length)}` : name })) return;
  ctx.select(next ? next.id : null);
  ctx.announce(`${name === "Cut" ? "Cut" : "Deleted"} ${plural(ops.length)}.`);
}

/** Duplicates the selection in one history step and selects the copies. */
export function duplicateMany(ctx, ids) {
  const ops = duplicateManyOps(ctx.doc, ids);
  if (!ctx.run(ops, { label: "Duplicate" })) return;
  const fresh = ops.map((o) => o.node.id).reverse(); // document order
  ctx.selectAll(fresh);
  ctx.announce(`Duplicated ${fresh.length} elements`);
}

// ---- clipboard, wrap, siblings ---------------------------------------------------------
// ctx also has {refuse(msg) (a visible message, announce when absent), csrf, url (the paste endpoint), kinds, selected}. The system clipboard may be
// denied (permissions, insecure origin), so the last copy is kept in memory too.

let held = null; // the last copied nodes
let heldOnSystem = false; // whether that copy reached the system clipboard (if not, an older system envelope must not win)

const refuse = (ctx, msg) => (ctx.refuse ?? ctx.announce)(msg);
const plural = (n) => `${n} ${n === 1 ? "element" : "elements"}`;

/** Puts the selection on the system clipboard as an envelope, and keeps it in memory. */
export async function copy(ctx, ids) {
  const nodes = copyNodes(ctx.doc, ids);
  if (!nodes.length) return false;
  held = nodes;
  heldOnSystem = false;
  try {
    await navigator.clipboard.writeText(JSON.stringify(envelope(nodes, location.origin)));
    heldOnSystem = true;
  } catch {
    ctx.announce(`Copied ${plural(nodes.length)}; the system clipboard is not available, so only this page can paste them`);
    return true;
  }
  ctx.announce(`Copied ${plural(nodes.length)}`);
  return true;
}

export async function cut(ctx, ids) {
  if (await copy(ctx, ids)) removeMany(ctx, ids, "Cut");
}

/** Where pasted nodes go: inside the selected container, else right after the selection (top level when nothing is selected). */
export function pastePoint(doc, selectedId, kinds, inside) {
  const at = selectedId ? locate(doc, moveSubject(doc, selectedId)) : null;
  if (!at) return { parent: "", index: doc.nodes.length };
  if (inside) return canHold(kinds, at.node.t) && !v1Shaped(at.node) ? { parent: at.node.id, index: (at.node.c ?? []).length } : null;
  return { parent: at.parentId, index: at.index + 1 };
}

/** Pastes clipboard nodes (system clipboard, else memory) through the server's validation and fresh ids. */
export async function paste(ctx, _ids, { inside = false } = {}) {
  let nodes = null;
  if (!held || heldOnSystem) {
    try {
      nodes = parseEnvelope(await navigator.clipboard.readText());
    } catch {
      // denied or empty: fall back to memory
    }
  }
  nodes ??= held;
  if (!nodes) return refuse(ctx, "Nothing to paste. Copy an element first");
  const at = pastePoint(ctx.doc, ctx.selected, ctx.kinds, inside);
  if (!at) return refuse(ctx, "This element cannot hold others");
  let res;
  try {
    const r = await fetch(ctx.url, { method: "POST", credentials: "same-origin", headers: { "Content-Type": "application/json", "X-CSRF-Token": ctx.csrf() }, body: JSON.stringify(envelope(nodes, location.origin)) });
    const body = await r.json().catch(() => ({}));
    if (!r.ok) throw new Error(body.error || "That did not work. Try again.");
    res = body;
  } catch (err) {
    return refuse(ctx, err instanceof TypeError ? "The server could not be reached. Try again." : err.message);
  }
  const ops = res.nodes.map((node, k) => ({ op: "insert", parent: at.parent, index: at.index + k, node }));
  if (!ctx.run(ops, { label: `Paste ${plural(res.nodes.length)}${inside ? " inside" : ""}` })) return;
  ctx.selectAll(res.nodes.map((n) => n.id));
  ctx.announce(`Pasted ${plural(res.nodes.length)}${res.missingBlocks?.length ? `; ${res.missingBlocks.length === 1 ? "a synced block does" : res.missingBlocks.length + " synced blocks do"} not exist on this site` : ""}`);
}

const topV1 = (doc, id) => {
  const l = locate(doc, moveSubject(doc, id));
  return !!l && !l.parent && v1Shaped(l.node); // a legacy section: the frame treats it as the section itself
};

// A reference or component is one opaque unit: wrapping it would hide it from its own editor.
const OPAQUE = new Set(["ref", "component"]);

/** Whether ids can be wrapped: adjacent siblings, none a legacy section (or its internals), a reference or a component. */
export const canWrap = (doc, ids) => !!contiguous(doc, ids) && !ids.some((id) => topV1(doc, id) || OPAQUE.has(find(doc, id)?.t));

/** Whether the single selected id is a container with elements in it that can be unwrapped. */
export function canUnwrap(doc, ids) {
  const n = ids.length === 1 && find(doc, moveSubject(doc, ids[0]));
  return !!n && n.t === "container" && !!n.c?.length && !topV1(doc, n.id);
}

/** Wraps the selection in a new container (it must be adjacent siblings). */
export function wrap(ctx, ids) {
  const ops = canWrap(ctx.doc, ids) && wrapManyOps(ctx.doc, ids);
  if (!ops) return refuse(ctx, "Select elements side by side in the same container to wrap them");
  if (ctx.run(ops, { label: "Wrap in Container" })) ctx.select(ops[0].node.id);
}

/** Replaces a selected container by its children. */
export function unwrap(ctx, ids) {
  if (!canUnwrap(ctx.doc, ids)) return refuse(ctx, "Select a container with elements in it to unwrap it");
  const n = find(ctx.doc, moveSubject(ctx.doc, ids[0]));
  const kids = n.c.map((c) => c.id);
  if (ctx.run(unwrapOps(ctx.doc, n.id), { label: "Unwrap Container" })) ctx.selectAll(kids);
}

/** Selects every sibling of the selected element. */
export function selectSiblings(ctx, ids) {
  const at = locate(ctx.doc, ids[ids.length - 1]);
  if (at) ctx.selectAll(siblings(ctx.doc, at.parentId).map((n) => n.id));
}
