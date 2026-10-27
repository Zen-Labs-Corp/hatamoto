// Maps committed builder ops to canvas bridge messages (v2). Pure: planCommit takes
// the doc before and after a commit plus the ops and returns what the canvas needs;
// every message is built through toBridge so protocol.js stays the one place that
// knows the shapes. The caller (sync) posts entries by `when`:
//   "now"      immediately (no server round trip needed)
//   "fragment" after the server rendered `render[frag]`; run finish() to fill it in
//   "confirm"  after the server confirmed the commit (finish() fills the css rules)
// `reload: true` means the commit cannot be mapped; stage a fresh load instead.
import { toBridge } from "@hm/canvas/protocol";
import { applyOp, find, hasParentHover, locate, siblings, v1Shaped } from "@hm/builder/store";

const TEXT_KINDS = new Set(["heading", "text", "button"]);
const INLINE_KEYS = new Set(["text", "html"]); // the canvas edits html on heading/text only (store.canEditText)

// outer: the element that stands for the node in its parent (replace/remove/move);
// inner: the element that carries its styles and children.
function outerRef(doc, id) {
  const l = locate(doc, id);
  if (!l.parent && v1Shaped(l.node)) return { sel: "section", key: l.node.c[0].id };
  // the engine marks a top-level native container as a plain node; only components carry a section marker
  if (l.node.t === "component") return { sel: "section", key: id };
  return { sel: "node", key: id };
}
export const innerRef = (doc, id) => ({ sel: find(doc, id).t === "component" ? "section" : "node", key: id });

// Where the node now at (parentId, index) goes, in terms of its neighbours.
function placeRef(doc, parentId, index, selfId) {
  const sib = siblings(doc, parentId).filter((x) => x.id !== selfId);
  if (index < sib.length) return { before: outerRef(doc, sib[index].id) };
  if (sib.length) return { after: outerRef(doc, sib[sib.length - 1].id) };
  return { parent: parentId ? innerRef(doc, parentId) : { sel: "main", key: "" } };
}

// The nearest loop strictly above id (null when none). A node under a loop only
// renders correctly with its item scope, so edits there re-render the loop.
export function loopAbove(doc, id) {
  let host = null;
  const walk = (ns, loop) => {
    for (const n of ns) {
      if (n.id === id) return (host = loop), true;
      if (n.c?.length && walk(n.c, n.t === "loop" ? n.id : loop)) return true;
    }
    return false;
  };
  walk(doc.nodes, null);
  return host;
}
// the loop that renders a child placed in parentId: the parent itself or the loop above it
const loopFor = (doc, parentId) => (!parentId ? null : find(doc, parentId)?.t === "loop" ? parentId : loopAbove(doc, parentId));

const topState = (doc) => new Map(doc.nodes.map((n) => [n.id, { v1: v1Shaped(n), outer: outerRef(doc, n.id) }]));

/**
 * planCommit(before, after, ops, {inline, decls}) -> {messages, render, css, reload}
 * messages: [{when, frag?, msg}]; render: node ids the server must render (in order);
 * css: node ids whose rules to re-send after confirm.
 * inline: a text prop change already in the frame DOM (inline typing); no message.
 * decls(node, bp, key, value): the setVar declarations for a style preview (host.js
 * owns the StyleSpec table); null makes the commit unmappable, [] means no
 * visible preview (a breakpoint other than the device's): only the css confirm.
 */
export function planCommit(before, after, ops, { inline = false, decls = () => [] } = {}) {
  const plan = { messages: [], render: [], css: [], reload: false };
  const reload = () => ({ messages: [], render: [], css: [], reload: true });
  // A frame drawn from v1 sections has no builder base sheet or widget CSS; the first native node needs a full render.
  if (before.nodes.length && before.nodes.every(v1Shaped) && after.nodes.some((n) => !v1Shaped(n) && n.t !== "component")) return reload();
  const fresh = new Set(); // ids inserted by this commit: their fragment already shows the final state
  const flipped = new Set(); // top-level ids already re-rendered whole

  const want = (id) => {
    let i = plan.render.indexOf(id);
    if (i < 0) i = plan.render.push(id) - 1;
    return i;
  };
  const patched = new Set(); // loops already re-rendered by this commit
  const patchLoop = (doc, id) => {
    if (patched.has(id)) return;
    patched.add(id);
    push("fragment", toBridge.patch({ ...outerRef(doc, id), html: "" }), want(id));
  };
  const push = (when, msg, frag) => plan.messages.push(frag === undefined ? { when, msg } : { when, frag, msg });
  const confirmCSS = (doc, id) => {
    push("confirm", toBridge.css({ ...innerRef(doc, id), css: { [id]: "" } }));
    if (!plan.css.includes(id)) plan.css.push(id);
  };

  let cur = before;
  for (const op of ops) {
    let r;
    try {
      r = applyOp(cur, op);
    } catch {
      return reload();
    }
    const next = r.doc;
    const subject = op.op === "insert" ? op.node.id : op.id;
    const inCommit = (id) => fresh.has(id);

    // top-level nodes whose v1 shape flips are patched whole
    const tb = topState(cur);
    const retop = [];
    for (const n of next.nodes) {
      const b = tb.get(n.id);
      if (b && b.v1 !== v1Shaped(n)) retop.push({ id: n.id, outer: b.outer });
    }
    const topBefore = op.op === "insert" ? null : locate(cur, subject) && cur.nodes[locate(cur, subject).path[0]].id;
    const covered = retop.some((t) => t.id === topBefore || (locate(next, subject) && next.nodes[locate(next, subject).path[0]].id === t.id));
    for (const t of retop) {
      if (flipped.has(t.id)) continue;
      flipped.add(t.id);
      push("fragment", toBridge.patch({ ...t.outer, html: "" }), want(t.id));
    }

    // an op on something this commit inserted is already in that fragment, unless it relocates it
    if (op.op === "move" || op.op === "remove") {
      if (inCommit(op.id) || (op.op === "move" && inCommit(op.parent ?? ""))) return reload();
    } else if (op.op !== "insert" && inCommit(op.id)) {
      cur = next;
      continue;
    }
    if (op.op === "insert" && inCommit(op.parent ?? "")) {
      cur = next;
      continue;
    }

    // ops under a loop re-render the loop (all items), never the lone node
    const oldH = op.op === "insert" ? null : loopAbove(cur, op.id);
    const newH = op.op === "insert" || op.op === "move" ? loopFor(next, op.parent ?? "") : op.op === "remove" ? null : oldH;
    // inline typing under a loop is already in item 1's DOM: the loop is re-rendered once when the edit ends
    if (inline && op.op === "setProp" && INLINE_KEYS.has(op.key) && oldH && TEXT_KINDS.has(find(next, op.id).t)) {
      // no message
    } else if (!covered && op.op !== "setStyle" && (oldH || newH)) {
      if (op.op === "move" && !oldH) push("now", toBridge.remove(outerRef(cur, op.id))); // leaves the plain tree first
      for (const h of new Set([oldH, newH])) if (h) patchLoop(next, h);
      if (op.op === "move" && !newH) push("fragment", toBridge.insert({ html: "", ...placeRef(next, op.parent ?? "", op.index, op.id) }), want(op.id));
    } else if (!covered) {
      switch (op.op) {
        case "setStyle": {
          const d = decls(find(next, op.id), op.bp, op.key, op.value);
          if (!d) return reload();
          if (d.length) push("now", toBridge.setVar({ ...innerRef(next, op.id), decls: d })); // none: nothing to preview at this width
          confirmCSS(next, op.id);
          break;
        }
        case "setBind":
        case "setProp": {
          const n = find(next, op.id);
          if (op.op === "setProp" && ((inline && INLINE_KEYS.has(op.key) && TEXT_KINDS.has(n.t)) || op.key === "navlabel")) break; // navlabel is never rendered
          push("fragment", toBridge.patch({ ...outerRef(next, op.id), html: "" }), want(op.id));
          break;
        }
        case "remove":
          push("now", toBridge.remove(outerRef(cur, op.id)));
          break;
        case "insert":
          push("fragment", toBridge.insert({ html: "", ...placeRef(next, op.parent ?? "", op.index, op.node.id) }), want(op.node.id));
          break;
        case "move": {
          // top-level <-> nested changes the wrapper element, so it is re-rendered
          const crossesTop = (topBefore === op.id) !== !op.parent;
          const place = placeRef(next, op.parent ?? "", op.index, op.id);
          if (crossesTop) {
            push("now", toBridge.remove(outerRef(cur, op.id)));
            push("fragment", toBridge.insert({ html: "", ...place }), want(op.id));
          } else {
            push("now", toBridge.move({ ...outerRef(cur, op.id), ...place }));
            if (hasParentHover(find(next, op.id).s)) confirmCSS(next, op.id);
          }
          break;
        }
        default:
          return reload();
      }
    }
    if (op.op === "insert") {
      const walk = (n) => { fresh.add(n.id); (n.c ?? []).forEach(walk); };
      walk(op.node);
    }
    cur = next;
  }

  // fragments are rendered from the final doc: every id must exist there, and the replay must land on it
  if (plan.render.some((id) => !find(after, id)) || JSON.stringify(cur) !== JSON.stringify(after)) return reload();
  return plan;
}

/**
 * Completes a "fragment" or "confirm" entry: the fragment {html, css} from the server
 * for the former, the {id: rule} map for the latter. Returns a message for validToBridge.
 */
export function finish(entry, { frags = [], css = {} } = {}) {
  const m = entry.msg;
  if (entry.when === "confirm") return toBridge.css({ ...m, css: Object.fromEntries(Object.keys(m.css).map((id) => [id, css[id] ?? ""])) });
  if (entry.when !== "fragment") return m;
  const f = frags[entry.frag];
  return toBridge[m.type]({ ...m, html: f.html, css: f.css });
}

/**
 * A top-level insert or move is placed against its neighbour in the doc, but a
 * neighbour that renders nothing (a blank content section) is not in the frame.
 * order: the top-level keys the server rendered; skip: keys not in the frame yet
 * (what this commit inserts). Returns msg anchored on the nearest present
 * top-level neighbour instead, or on <main> when there is none.
 */
export function reanchor(doc, order, msg, skip = new Set()) {
  if (msg.type !== "insert" && msg.type !== "move") return msg;
  const side = msg.before ? "before" : msg.after ? "after" : null;
  if (!side) return msg;
  const top = doc.nodes.map((n) => outerRef(doc, n.id));
  const at = top.findIndex((t) => t.sel === msg[side].sel && t.key === msg[side].key);
  const present = new Set(order);
  if (at < 0 || present.has(msg[side].key)) return msg;
  const ok = (t) => present.has(t.key) && !skip.has(t.key) && t.key !== msg.key;
  const fwd = top.slice(at + 1).find(ok);
  const back = top.slice(0, at).reverse().find(ok);
  const { before, after, parent, ...rest } = msg;
  const place = side === "before" ? (fwd ? { before: fwd } : back ? { after: back } : null) : back ? { after: back } : fwd ? { before: fwd } : null;
  return { ...rest, ...(place ?? { parent: { sel: "main", key: "" } }) };
}
