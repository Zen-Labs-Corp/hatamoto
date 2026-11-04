// The "/" popover (a lazy chunk): section cards at the selection. Picking one inserts it after the selected section.
// A Popover API panel like picker.js: Esc and an outside click close it, focus returns to restore() unless it moved on.
import { h, focusKept } from "@hm/builder/dom";
import { sectionCard } from "@hm/builder/sectioncard";
import { shapeOf, startersFirst } from "@hm/builder/patterns";

/**
 * openSlashPopover({anchorRect, items, onPick, restore}): items are the section items of the palette. anchorRect is the
 * selection's box in the viewport (or null: the popover sits near the top left). onPick(item) runs after the panel closed.
 */
export function openSlashPopover({ anchorRect, items, onPick, restore }) {
  let pending = null;
  const shapeFor = (i) => shapeOf(i.type);
  const cards = startersFirst(items).map((i) => sectionCard(i, shapeFor, h));
  const byKey = new Map(items.map((i) => [i.key, i]));
  const buttons = () => cards.map((c) => c.firstElementChild);
  const m = h("div", { class: "hm-bs-slashpop", popover: "auto", role: "dialog", "aria-label": "Insert a section" }, h("p", { class: "hm-bs-slashpop__t", text: "Insert section" }), h("ul", { class: "hm-bs-pal__list hm-bs-slashpop__list" }, ...cards));
  m.addEventListener("click", (e) => {
    const item = byKey.get(e.target.closest?.("[data-pal]")?.dataset.pal);
    if (!item) return;
    pending = () => onPick(item);
    m.hidePopover();
  });
  m.addEventListener("keydown", (e) => {
    const bs = buttons();
    const i = bs.indexOf(document.activeElement);
    const to = { ArrowRight: i + 1, ArrowDown: i + 1, ArrowLeft: i - 1, ArrowUp: i - 1, Home: 0, End: bs.length - 1 }[e.key];
    if (to !== undefined) {
      e.preventDefault();
      bs[(to + bs.length) % bs.length].focus();
    } else if (e.key === "Tab") m.hidePopover();
  });
  const keptFocus = focusKept(m);
  m.addEventListener("toggle", (e) => {
    if (e.newState !== "closed") return;
    m.remove();
    if (keptFocus()) restore?.();
    pending?.();
  });
  document.body.append(m);
  m.showPopover();
  const vw = document.documentElement.clientWidth;
  const cx = anchorRect ? anchorRect.left + anchorRect.width / 2 : 8 + m.offsetWidth / 2;
  m.style.left = `${Math.max(8, Math.min(cx - m.offsetWidth / 2, vw - m.offsetWidth - 8))}px`;
  m.style.top = `${Math.max(8, Math.min(anchorRect ? anchorRect.bottom + 8 : 8, innerHeight - Math.min(m.offsetHeight, 360) - 8))}px`;
  buttons()[0]?.focus();
}
