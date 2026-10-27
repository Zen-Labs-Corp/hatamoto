// Drag and drop over the sandboxed canvas. The frame shares no DOM with us, so the
// drop target is computed from the node rects the bridge reports (document space) and
// the doc structure: dropTarget is pure and node-tested; createDrag owns the pointer
// shield (a fixed full-viewport layer that exists only during a drag) and the drop
// indicator drawn inside the canvas stage. No SortableJS here, no frame access.
import { applyOp, holdsChildren, v1Shaped } from "@hm/builder/store";
import { h } from "@hm/builder/dom";

const THRESHOLD = 4; // px of pointer travel before a press becomes a drag
const EDGE = 8; // px (on screen) of a container's border that means "next to it", not "inside it"

/** Client coordinates -> document coordinates of the frame (box: the stage's client rect). */
export const toDoc = (clientX, clientY, box, scale) => ({ x: (clientX - box.left) / (scale || 1), y: (clientY - box.top) / (scale || 1) });

const ids = (n) => [n.id, ...(n.c ?? []).flatMap(ids)];

/** Indexes the doc and the reported rects once per drag. */
export function prepare(doc, nodes) {
  const info = new Map(); // id -> {node, parentId}
  const walk = (ns, parentId) =>
    ns.forEach((n) => {
      info.set(n.id, { node: n, parentId });
      if (n.c) walk(n.c, n.id);
    });
  walk(doc.nodes, "");
  return { doc, info, rects: new Map(nodes.map((n) => [n.id, n])) };
}

/**
 * dropTarget(pt, prep, drag, {scale, main}) -> {parent, index, op, box, line} | null
 * pt: document-space point. drag: {id} (move that node) or {node} (insert it).
 * parent/index are what the op uses (index counts the siblings without the dragged node).
 * line: the indicator rect (a zero-width or zero-height line, or the whole box when
 * box is true). A placement the store would refuse (into itself, depth, node limit) is
 * never returned: the next candidate is tried instead.
 */
export function dropTarget(pt, prep, drag, { scale = 1, main = null } = {}) {
  const { doc, info, rects } = prep;
  const moving = drag.id ? info.get(drag.id)?.node : null;
  const skip = new Set(moving ? ids(moving) : []);
  const edge = EDGE / (scale || 1);
  const hit = (r) => pt.x >= r.x && pt.x <= r.x + r.w && pt.y >= r.y && pt.y <= r.y + r.h;
  const kids = (parentId) => (parentId ? (info.get(parentId)?.node.c ?? []) : doc.nodes).filter((n) => n.id !== drag.id);
  const dirOf = (parentId) => (parentId ? (rects.get(parentId)?.dir ?? "column") : "column");
  // a v1-shaped top-level container is shown as its component's section
  const rectOf = (n, parentId) => rects.get(!parentId && v1Shaped(n) ? n.c[0].id : n.id);

  const indexAt = (parentId) => {
    const list = kids(parentId);
    const column = dirOf(parentId) === "column";
    for (let i = 0; i < list.length; i++) {
      const r = rectOf(list[i], parentId);
      if (!r) continue;
      const before = column ? pt.y < r.y + r.h / 2 : pt.y < r.y || (pt.y <= r.y + r.h && pt.x < r.x + r.w / 2);
      if (before) return i;
    }
    return list.length;
  };

  const indicator = (parentId, index) => {
    const list = kids(parentId);
    const room = parentId ? rects.get(parentId) : (main ?? { x: 0, y: 0, w: 1280, h: 80 });
    const ref = list.length ? rectOf(list[Math.min(index, list.length - 1)], parentId) : null;
    if (!ref) return { box: true, line: { x: room.x, y: room.y, w: room.w, h: room.h ?? 80 } };
    const after = index >= list.length;
    return dirOf(parentId) === "column"
      ? { box: false, line: { x: ref.x, y: after ? ref.y + ref.h : ref.y, w: ref.w, h: 0 } }
      : { box: false, line: { x: after ? ref.x + ref.w : ref.x, y: ref.y, w: 0, h: ref.h } };
  };

  const place = (parent, index) => {
    const op = drag.id ? { op: "move", id: drag.id, parent, index } : { op: "insert", parent, index, node: drag.node };
    try {
      applyOp(doc, op);
    } catch {
      return null;
    }
    return { parent, index, op, ...indicator(parent, index) };
  };

  const containers = () =>
    [...rects.values()]
      .filter((n) => n.c === 1 && hit(n) && holdsChildren(info.get(n.id)?.node.t) && !skip.has(n.id))
      .sort((a, b) => a.w * a.h - b.w * b.h);

  for (const c of containers()) {
    const { parentId } = info.get(c.id);
    const along = dirOf(parentId) === "column" ? [pt.y - c.y, c.h] : [pt.x - c.x, c.w];
    const zone = Math.min(edge, along[1] / 4);
    const at = kids(parentId).findIndex((n) => n.id === c.id);
    const next = along[0] <= zone ? place(parentId, at) : along[1] - along[0] <= zone ? place(parentId, at + 1) : null;
    const t = next ?? place(c.id, indexAt(c.id));
    if (t) return t;
  }
  return place("", indexAt(""));
}

/**
 * createDrag({canvas, getDoc, scroller, onDrop, announce}) -> {start(event, spec), active}
 * spec: {id} or {node}. onDrop(target, spec) runs the commit. Escape, pointercancel,
 * lost capture and window blur cancel; the shield never outlives the drag.
 */
export function createDrag({ canvas, getDoc, scroller, onDrop }) {
  const shield = h("div", { class: "hm-bs-shield", hidden: true, "aria-hidden": "true" });
  const mark = h("div", { class: "hm-bs-drop", hidden: true, "aria-hidden": "true" });
  document.body.append(shield);
  canvas.stage.append(mark);
  let drag = null;

  function paint(t) {
    if (!t) {
      mark.hidden = true;
      return;
    }
    const s = canvas.scale;
    const { x, y, w, h: ht } = t.line;
    mark.classList.toggle("hm-bs-drop--box", t.box);
    mark.classList.toggle("hm-bs-drop--v", !t.box && w === 0);
    mark.style.setProperty("left", `${x * s}px`);
    mark.style.setProperty("top", `${y * s}px`);
    mark.style.setProperty("width", `${t.box || w > 0 ? Math.max(3, w * s) : 3}px`);
    mark.style.setProperty("height", `${t.box || ht > 0 ? Math.max(3, ht * s) : 3}px`);
    mark.hidden = false;
  }

  function move(e) {
    if (!drag) return;
    const box = canvas.stage.getBoundingClientRect();
    const pt = toDoc(e.clientX, e.clientY, box, canvas.scale);
    drag.last = e;
    const main = { x: 0, y: canvas.layout.header.height, w: box.width / (canvas.scale || 1), h: 80 };
    drag.target = hit(box, e) ? dropTarget(pt, drag.prep, drag.spec, { scale: canvas.scale, main }) : null;
    paint(drag.target);
  }
  const hit = (box, e) => e.clientX >= box.left && e.clientX <= box.right && e.clientY >= box.top && e.clientY <= box.bottom;

  function end(commit) {
    if (!drag) return;
    const { target, spec } = drag;
    drag = null;
    shield.hidden = true;
    mark.hidden = true;
    document.body.classList.remove("hm-bs-dragging");
    shield.removeEventListener("pointermove", move);
    shield.removeEventListener("pointerup", up);
    shield.removeEventListener("pointercancel", cancel);
    shield.removeEventListener("lostpointercapture", cancel);
    shield.removeEventListener("wheel", wheel);
    document.removeEventListener("keydown", key, true);
    window.removeEventListener("blur", cancel);
    if (commit && target) onDrop(target, spec);
  }
  const up = () => end(true);
  const cancel = () => end(false);
  const key = (e) => {
    if (e.key !== "Escape") return;
    e.preventDefault();
    e.stopPropagation();
    end(false);
  };
  // The shield sits above the pane, so the wheel is forwarded to it.
  const wheel = (e) => {
    e.preventDefault();
    scroller?.scrollBy({ top: e.deltaY, left: e.deltaX });
    move(e);
  };

  return {
    get active() {
      return !!drag;
    },
    start(e, spec) {
      if (drag) return;
      drag = { spec, prep: prepare(getDoc(), canvas.nodes), target: null };
      shield.hidden = false;
      document.body.classList.add("hm-bs-dragging");
      shield.setPointerCapture?.(e.pointerId);
      shield.addEventListener("pointermove", move);
      shield.addEventListener("pointerup", up);
      shield.addEventListener("pointercancel", cancel);
      shield.addEventListener("lostpointercapture", cancel);
      shield.addEventListener("wheel", wheel, { passive: false });
      document.addEventListener("keydown", key, true);
      window.addEventListener("blur", cancel);
      move(e);
    },
    cancel,
  };
}

/**
 * Turns a press-and-move on el into a drag start. pick(event) returns a function
 * that makes the drag spec ({id} or {node}), or null when the press is not on a source.
 * Touch is left alone (it scrolls); touch users have the keyboard commands and click.
 */
export function dragSource(el, { pick, start }) {
  let down = null;
  el.addEventListener("pointerdown", (e) => {
    const make = e.button === 0 && e.pointerType !== "touch" ? pick(e) : null;
    down = make ? { x: e.clientX, y: e.clientY, id: e.pointerId, make } : null;
  });
  el.addEventListener("pointermove", (e) => {
    if (!down || e.pointerId !== down.id) return;
    if (e.buttons === 0) down = null;
    else if (Math.hypot(e.clientX - down.x, e.clientY - down.y) > THRESHOLD) {
      const d = down;
      down = null;
      start(e, d.make());
    }
  });
  for (const ev of ["pointerup", "pointercancel"]) el.addEventListener(ev, () => (down = null));
}
