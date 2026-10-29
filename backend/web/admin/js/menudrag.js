// Menu editor drag and drop, with pointer events so mouse, pen and touch work
// alike. Press the grip of an item and move: the pointer's height picks the
// gap, its sideways travel the level, and a line with a label ("inside About",
// "top level") shows where the item and its sub-items would land. Releasing
// moves them as one undo step; Escape or a cancelled pointer leaves the tree
// alone. The places come from @hm/menulogic (dropTargets), which keeps every
// level valid.
import { blockEnd, dropTargets } from "@hm/menulogic";

const FALLBACK_INDENT = 32;

// indentOf is how far one level moves an item sideways, read from the page.
function indentOf(rows) {
  const deep = rows.find((r) => r.depth > 0);
  const px = deep ? parseFloat(getComputedStyle(deep.li).marginLeft) / deep.depth : 0;
  return px > 0 ? px : FALLBACK_INDENT;
}

// init wires drag on the tree. api: rows() the current rows, place(rows) writes
// rows back, commit(before) records the undo step, maxDepth(), done(message).
export function init(tree, api) {
  const template = document.querySelector('template[data-hm-tpl="dropline"]');
  if (!template) return;
  let drag = null;

  function finish() {
    if (!drag) return;
    const { handle, line, rows, from, end } = drag;
    handle.removeEventListener("pointermove", onMove);
    handle.removeEventListener("pointerup", onUp);
    handle.removeEventListener("pointercancel", onCancel);
    document.removeEventListener("keydown", onKey, true);
    if (handle.hasPointerCapture?.(drag.pointerId)) handle.releasePointerCapture(drag.pointerId);
    for (let k = from; k < end; k++) rows[k].li.removeAttribute("data-hm-dragging");
    line.remove();
    drag = null;
  }

  function onMove(evt) {
    if (!drag || evt.pointerId !== drag.pointerId) return;
    const { rows, from, end, line } = drag;
    const pointerDepth = rows[from].depth + Math.round((evt.clientX - drag.startX) / drag.indent);
    const slots = dropTargets(rows, from, pointerDepth, api.maxDepth());
    drag.slot = null;
    line.hidden = true;
    if (slots.length === 0) return;
    const rest = rows.filter((_, k) => k < from || k >= end).map((r) => r.li);
    const gap = rest.filter((li) => {
      const box = li.getBoundingClientRect();
      return evt.clientY > box.top + box.height / 2;
    }).length;
    const slot = slots.reduce((best, s) => (Math.abs(s.index - gap) < Math.abs(best.index - gap) ? s : best), slots[0]);
    drag.slot = slot;
    const box = tree.getBoundingClientRect();
    const before = rest[slot.index]?.getBoundingClientRect();
    const after = rest[slot.index - 1]?.getBoundingClientRect();
    const y = before ? before.top : after ? after.bottom : box.top;
    const x = box.left + slot.depth * drag.indent;
    line.style.top = `${Math.round(y - (before ? 4 : -4))}px`;
    line.style.left = `${Math.round(x)}px`;
    line.style.width = `${Math.max(0, Math.round(box.right - x))}px`;
    const parent = slot.label.kind === "inside" ? rows[slot.label.index].li.querySelector(".hm-menutree__title")?.textContent : "";
    line.querySelector("[data-hm-droplabel]").textContent = slot.label.kind === "inside" ? `inside ${parent}` : "top level";
    line.hidden = false;
  }

  function onUp(evt) {
    if (!drag || evt.pointerId !== drag.pointerId) return;
    const { rows, from, end, slot } = drag;
    finish();
    if (!slot) return;
    const delta = slot.depth - rows[from].depth;
    if (slot.index === from && delta === 0) return;
    const rest = rows.filter((_, k) => k < from || k >= end);
    const moved = rows.slice(from, end).map((r) => ({ ...r, depth: r.depth + delta }));
    api.place([...rest.slice(0, slot.index), ...moved, ...rest.slice(slot.index)]);
    api.commit(rows);
    api.done("Item moved. It is not saved yet.");
  }

  function onCancel(evt) {
    if (drag && evt.pointerId === drag.pointerId) finish();
  }

  function onKey(evt) {
    if (evt.key !== "Escape") return;
    evt.preventDefault();
    evt.stopPropagation();
    finish();
  }

  tree.addEventListener("pointerdown", (evt) => {
    const handle = evt.target.closest("[data-hm-handle]");
    if (!handle || drag || (evt.pointerType === "mouse" && evt.button !== 0)) return;
    const rows = api.rows();
    const from = rows.findIndex((r) => r.li === handle.closest("[data-hm-item]"));
    if (from < 0) return;
    evt.preventDefault();
    handle.setPointerCapture(evt.pointerId);
    const end = blockEnd(rows, from);
    const line = template.content.firstElementChild.cloneNode(true);
    line.hidden = true;
    document.body.append(line);
    for (let k = from; k < end; k++) rows[k].li.setAttribute("data-hm-dragging", "");
    drag = { handle, pointerId: evt.pointerId, rows, from, end, line, startX: evt.clientX, indent: indentOf(rows), slot: null };
    handle.addEventListener("pointermove", onMove);
    handle.addEventListener("pointerup", onUp);
    handle.addEventListener("pointercancel", onCancel);
    document.addEventListener("keydown", onKey, true);
  });
}
