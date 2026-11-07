// Dragging a palette tile or a field over the sheet. dropIndex is pure; createFormDrag owns the
// shield, the insertion line and the cancel paths (Escape, blur, pointercancel, lost capture).
// The keyboard path for every drag is the palette's Enter/click and Alt+Up/Down (keymap.js).
import { h } from "@hm/builder/dom";

/** The index to insert at for pointer height y: before the first field whose midpoint is below y, else the end. */
export function dropIndex(y, rects) {
  const i = rects.findIndex((r) => y < r.top + r.height / 2);
  return i < 0 ? rects.length : i;
}

/**
 * start(e, spec) begins a drag; onDrop(index, spec) runs when it is released over the sheet. index counts the
 * sheet's fields as they are now (a moved field's own slot included), spec is {id} or {type}.
 */
export function createFormDrag({ sheet, rowRects, onDrop, shield = h("div", { class: "hm-fd-shield", hidden: true, "aria-hidden": "true" }) }) {
  const line = h("div", { class: "hm-fd-drop", "aria-hidden": "true" });
  if (!shield.isConnected) document.body.append(shield);
  let drag = null;

  function move(e) {
    if (!drag) return;
    const r = sheet.getBoundingClientRect();
    const over = e.clientX >= r.left && e.clientX <= r.right && e.clientY >= r.top && e.clientY <= r.bottom;
    drag.index = over ? dropIndex(e.clientY, rowRects()) : null;
    if (drag.index === null) return line.remove();
    const at = sheet.querySelectorAll(":scope > [data-id]")[drag.index] ?? null;
    sheet.insertBefore(line, at);
  }

  function end(commit) {
    if (!drag) return;
    const { index, spec } = drag;
    drag = null;
    shield.hidden = true;
    line.remove();
    document.body.classList.remove("hm-fd-dragging");
    shield.removeEventListener("pointermove", move);
    shield.removeEventListener("pointerup", up);
    shield.removeEventListener("pointercancel", cancel);
    shield.removeEventListener("lostpointercapture", cancel);
    document.removeEventListener("keydown", key, true);
    window.removeEventListener("blur", cancel);
    if (commit && index !== null) onDrop(index, spec);
  }
  const up = () => end(true);
  const cancel = () => end(false);
  const key = (e) => {
    if (e.key !== "Escape") return;
    e.preventDefault();
    e.stopPropagation();
    end(false);
  };

  return {
    get active() {
      return !!drag;
    },
    start(e, spec) {
      if (drag) return;
      drag = { spec, index: null };
      shield.hidden = false;
      document.body.classList.add("hm-fd-dragging");
      shield.setPointerCapture?.(e.pointerId);
      shield.addEventListener("pointermove", move);
      shield.addEventListener("pointerup", up);
      shield.addEventListener("pointercancel", cancel);
      shield.addEventListener("lostpointercapture", cancel);
      document.addEventListener("keydown", key, true);
      window.addEventListener("blur", cancel);
      move(e);
    },
    cancel,
  };
}
