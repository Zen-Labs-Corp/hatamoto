// Repeaters (schemaform): drag-to-reorder with SortableJS, and renumbering of
// the dotted field names ("content.faqs.2.question") after every move so the
// server reads the new order. Without JS the Move up/down buttons do the same.
const ATTRS = ["name", "id", "for", "aria-describedby", "aria-labelledby", "data-path", "data-hm-counter", "data-hm-media-picker"];

function esc(s) {
  return s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

function renumber(fieldset) {
  const path = fieldset.dataset.path;
  const dotted = new RegExp(`(${esc(path)}\\.)(\\d+)(?=\\.|$)`);
  const dashedPath = path.replaceAll(".", "-");
  const dashed = new RegExp(`(${esc(dashedPath)}-)(\\d+)(?=-|$)`);
  const list = fieldset.querySelector(":scope > .hm-repeater__list");
  [...list.children].forEach((li, i) => {
    li.querySelectorAll("*").forEach((el) => {
      for (const a of ATTRS) {
        const v = el.getAttribute(a);
        if (v === null) continue;
        let nv = v.replace(dotted, (_, p) => `${p}${i}`);
        if (a === "id" || a === "for" || a.startsWith("aria-") || a === "data-hm-counter" || a === "data-hm-media-picker") {
          nv = nv.replace(dashed, (_, p) => `${p}${i}`);
        }
        if (nv !== v) el.setAttribute(a, nv);
      }
      const name = el.getAttribute("name") ?? "";
      if (name === `_move.${path}`) el.value = el.value.replace(/^\d+/, String(i));
      if (name === `_remove.${path}`) el.value = String(i);
      const label = el.getAttribute("aria-label");
      if (label && /item \d+/.test(label)) el.setAttribute("aria-label", label.replace(/item \d+/, `item ${i + 1}`));
    });
  });
}

// The grip is a drag handle only: dragging by the whole summary fights with
// opening and closing the item. It is injected, so without JS (no drag) it
// does not exist.
const GRIP = '<svg class="hm-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false"><circle cx="9" cy="12" r="1"/><circle cx="9" cy="5" r="1"/><circle cx="9" cy="19" r="1"/><circle cx="15" cy="12" r="1"/><circle cx="15" cy="5" r="1"/><circle cx="15" cy="19" r="1"/></svg>';

function addGrips(list) {
  list.querySelectorAll(":scope > [data-hm-item] > details > .hm-repeater__summary").forEach((summary) => {
    if (summary.querySelector(":scope > .hm-repeater__grip")) return;
    const grip = document.createElement("span");
    grip.className = "hm-repeater__grip";
    grip.setAttribute("aria-hidden", "true");
    grip.innerHTML = GRIP;
    // A click on the grip must not open or close the item.
    grip.addEventListener("click", (evt) => evt.preventDefault());
    summary.prepend(grip);
  });
}

function init(root) {
  if (!window.Sortable) return;
  root.querySelectorAll("[data-hm-sortable]").forEach((list) => {
    if (list.dataset.hmSortableReady) return;
    list.dataset.hmSortableReady = "1";
    const fieldset = list.closest("[data-hm-repeater]");
    addGrips(list);
    window.Sortable.create(list, {
      draggable: "[data-hm-item]",
      handle: ".hm-repeater__grip",
      animation: 120,
      onEnd: () => renumber(fieldset),
    });
  });
}

document.addEventListener("DOMContentLoaded", () => init(document));
document.addEventListener("htmx:afterSettle", (evt) => init(evt.target));
export { renumber, init };
