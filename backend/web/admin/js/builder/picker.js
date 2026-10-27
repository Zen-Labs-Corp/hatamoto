// The "+" structure picker (a lazy chunk): a popover of section layouts to start a row from.
// LAYOUTS are the pure part: each make() returns one fresh-id top-level node. Equal splits use
// the columns widget (it stacks on small screens by itself); the uneven ones and the grid use a
// container in grid layout with a grid-template-columns value (gtc) or a column count (cols).
import { h, focusKept } from "@hm/builder/dom";
import { newId } from "@hm/builder/store";

const cell = () => ({ id: newId(), t: "container" });
const cells = (n) => Array.from({ length: n }, cell);
const container = () => cell();
const columns = (n) => ({ id: newId(), t: "columns", p: { count: n }, c: cells(n) });
const grid = (key, value, n) => ({ id: newId(), t: "container", p: { layout: "grid" }, s: { base: { [key]: value } }, c: cells(n) });

/** pv is the preview's grid-template-columns; n the number of cells it shows. */
export const LAYOUTS = [
  { id: "1", name: "1 column", pv: "1fr", n: 1, make: container },
  { id: "2", name: "2 columns", pv: "1fr 1fr", n: 2, make: () => columns(2) },
  { id: "3", name: "3 columns", pv: "1fr 1fr 1fr", n: 3, make: () => columns(3) },
  { id: "4", name: "4 columns", pv: "1fr 1fr 1fr 1fr", n: 4, make: () => columns(4) },
  { id: "1-2", name: "33 / 67", pv: "1fr 2fr", n: 2, make: () => grid("gtc", "1fr 2fr", 2) },
  { id: "2-1", name: "67 / 33", pv: "2fr 1fr", n: 2, make: () => grid("gtc", "2fr 1fr", 2) },
  { id: "2x2", name: "Grid 2×2", pv: "1fr 1fr", n: 4, make: () => grid("cols", "2", 4) },
];

/** The rows under the layouts: they leave the layouts for the Add tab, at the same position. */
export const MORE = [
  { mode: "element", label: "Choose an element…" },
  { mode: "pattern", label: "Insert a ready-made section…" },
];

/**
 * Opens the picker under `anchor` (the "+" button). onPick(layout) adds that layout; onChoose({mode}) is one of the MORE rows.
 * Focus returns to the anchor (or restore() when the anchor is gone) on close; Esc and an outside click close it.
 */
export function openPicker({ anchor, onPick, onChoose, restore }) {
  let pending = null;
  const choose = (fn) => () => {
    pending = fn;
    m.hidePopover();
  };
  const items = [
    ...LAYOUTS.map((l) =>
      h("button", { type: "button", class: "hm-bs-picker__item", onclick: choose(() => onPick(l)) }, h("span", { class: "hm-bs-picker__pv", "aria-hidden": "true", "data-pv": l.pv }, ...Array.from({ length: l.n }, () => h("i"))), h("span", { text: l.name })),
    ),
    ...MORE.map((r) => h("button", { type: "button", class: "hm-bs-picker__item hm-bs-picker__more", text: r.label, onclick: choose(() => onChoose(r)) })),
  ];
  for (const pv of items.map((el) => el.querySelector("[data-pv]"))) pv?.style.setProperty("grid-template-columns", pv.dataset.pv);
  const m = h("div", { class: "hm-bs-picker", popover: "auto", role: "dialog", "aria-label": "Add a section" }, h("div", { role: "group", "aria-label": "Layouts", class: "hm-bs-picker__list" }, ...items));
  m.addEventListener("keydown", (e) => {
    const i = items.indexOf(document.activeElement);
    const to = { ArrowRight: i + 1, ArrowDown: i + 1, ArrowLeft: i - 1, ArrowUp: i - 1, Home: 0, End: items.length - 1 }[e.key];
    if (to !== undefined) {
      e.preventDefault();
      items[(to + items.length) % items.length].focus();
    } else if (e.key === "Tab") m.hidePopover();
  });
  const keptFocus = focusKept(m);
  m.addEventListener("toggle", (e) => {
    if (e.newState !== "closed") return;
    m.remove();
    if (keptFocus()) {
      if (anchor?.isConnected) anchor.focus();
      else restore?.();
    }
    pending?.();
  });
  document.body.append(m);
  m.showPopover();
  const r = anchor?.getBoundingClientRect();
  const vw = document.documentElement.clientWidth;
  m.style.left = `${Math.max(8, Math.min(r?.left ?? 8, vw - m.offsetWidth - 8))}px`;
  m.style.top = `${Math.max(8, Math.min((r?.bottom ?? 8) + 4, innerHeight - m.offsetHeight - 8))}px`;
  items[0].focus();
}
