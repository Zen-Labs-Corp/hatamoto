// Menu editor. The tree is a flat <ol> of <li data-hm-item data-depth>; the
// form fields are named i.N.* and the server reads them in N order. This script
// upgrades the buttons (up, down, indent) and adds drag and drop with
// SortableJS; without it the same buttons post to the server, which applies the
// edit and re-renders. Outdent, Remove, Duplicate and Move to always post to
// the server, so they act the same with and without this script.
import { renumberOp, renumberRef } from "@hm/listlogic";

const MAX_DEPTH = 5;

function items(tree) {
  return [...tree.querySelectorAll(":scope > [data-hm-item]")];
}
const depthOf = (li) => Number(li.dataset.depth || 0);

function setDepth(li, d) {
  li.dataset.depth = String(d);
  li.querySelector("[data-hm-depth]").value = String(d);
}

// block is an item and the deeper items that follow it.
function block(tree, li) {
  const all = items(tree);
  const start = all.indexOf(li);
  const out = [li];
  for (let k = start + 1; k < all.length && depthOf(all[k]) > depthOf(li); k++) out.push(all[k]);
  return out;
}

// renumber rewrites every i.N / i-N / op value to match the DOM order, keeps
// depths valid and refreshes the visible and spoken item labels.
function renumber(tree) {
  let prev = -1;
  items(tree).forEach((li, i) => {
    const d = Math.max(0, Math.min(depthOf(li), prev + 1, MAX_DEPTH));
    setDepth(li, d);
    prev = d;
    li.querySelectorAll("*").forEach((el) => {
      for (const a of ["name", "id", "for", "aria-describedby", "aria-controls", "aria-activedescendant", "popovertarget"]) {
        const v = el.getAttribute(a);
        if (v === null) continue;
        const nv = renumberRef(v, "i", i);
        if (nv !== v) el.setAttribute(a, nv);
      }
      if (el.getAttribute("name") === "_op") el.value = renumberOp(el.value, i);
    });
    li.querySelectorAll(".hm-visually-hidden").forEach((el) => {
      if (/^ item \d+/.test(el.textContent)) el.textContent = el.textContent.replace(/item \d+/, `item ${i + 1}`);
    });
    const lvl = li.querySelector("[data-hm-level]");
    if (lvl) lvl.textContent = `Item ${i + 1}, level ${d + 1}`;
  });
  refreshOps(tree);
  syncMoveTo(tree);
}

// syncMoveTo rebuilds the "Move to" destinations from the tree as it is now:
// their values are row numbers, which a reorder changes. The chosen
// destination stays chosen, if it is still a top-level item.
function syncMoveTo(tree) {
  const sel = document.getElementById("moveto-target");
  if (!sel) return;
  const chosen = sel.selectedOptions[0]?.hmLi;
  const keep = [...sel.options].filter((o) => !o.hmLi && !/^\d+$/.test(o.value));
  sel.replaceChildren(...keep);
  items(tree).forEach((li, i) => {
    if (depthOf(li) !== 0) return;
    const o = new Option(`Under ${li.querySelector('[name$=".label"]')?.value.trim() || "Untitled item"}`, String(i));
    o.hmLi = li;
    o.selected = li === chosen;
    sel.append(o);
  });
}

// refreshOps disables the buttons that cannot act: indent under nothing,
// outdent at the top level. The server renders the same state on load.
function refreshOps(tree) {
  const all = items(tree);
  all.forEach((li, i) => {
    const d = depthOf(li);
    const indent = li.querySelector('[data-hm-op="indent"]');
    const outdent = li.querySelector('[name="_op"][value^="outdent."]');
    if (indent) indent.disabled = i === 0 || d > depthOf(all[i - 1]) || d >= MAX_DEPTH;
    if (outdent) outdent.disabled = d === 0;
  });
}

function move(tree, li, dir) {
  const d = depthOf(li);
  const mine = block(tree, li);
  const all = items(tree);
  if (dir < 0) {
    for (let k = all.indexOf(li) - 1; k >= 0; k--) {
      if (depthOf(all[k]) < d) return false;
      if (depthOf(all[k]) === d) {
        all[k].before(...mine);
        return true;
      }
    }
    return false;
  }
  const next = all[all.indexOf(li) + mine.length];
  if (!next || depthOf(next) !== d) return false;
  const theirs = block(tree, next);
  theirs[theirs.length - 1].after(...mine);
  return true;
}

function shift(tree, li, delta) {
  const d = depthOf(li);
  const all = items(tree);
  const idx = all.indexOf(li);
  if (delta > 0 && (idx === 0 || d > depthOf(all[idx - 1]) || d >= MAX_DEPTH)) return false;
  if (delta < 0 && d === 0) return false;
  block(tree, li).forEach((el) => setDepth(el, depthOf(el) + delta));
  return true;
}

function announce(msg) {
  document.dispatchEvent(new CustomEvent("hm:toast", { detail: { kind: "info", message: msg, timeout: 2500 } }));
}

function init(root) {
  root.querySelectorAll("[data-hm-menutree]").forEach((tree) => {
    if (tree.dataset.hmReady) return;
    tree.setAttribute("data-hm-ready", "1");
    tree.querySelectorAll("[data-hm-handle]").forEach((h) => { h.hidden = false; });

    // A closed <details> hides its invalid field and no browser opens it, so
    // validation would fail silently. Open them before the browser validates.
    tree.closest("form")?.addEventListener("click", (evt) => {
      const b = evt.target.closest("button[type=submit]");
      if (!b || b.formNoValidate) return;
      tree.querySelectorAll("details:not([open])").forEach((d) => {
        if (d.querySelector(":invalid")) d.open = true;
      });
    }, true);

    tree.addEventListener("click", (evt) => {
      const btn = evt.target.closest("[data-hm-op]");
      if (!btn) return;
      const li = btn.closest("[data-hm-item]");
      const op = btn.dataset.hmOp;
      btn.closest("[popover]")?.hidePopover?.();
      evt.preventDefault();
      const ok = op === "up" ? move(tree, li, -1) : op === "down" ? move(tree, li, 1) : shift(tree, li, 1);
      renumber(tree);
      // The moved item keeps focus: on the same button, or on its menu when that button lives in the menu.
      (li.querySelector(`.hm-menutree__ops > [data-hm-op="${op}"]`) ?? li.querySelector("[popovertarget]"))?.focus();
      if (!ok) announce("That item cannot move there.");
    });

    tree.addEventListener("input", (evt) => {
      if (evt.target.matches('[name$=".label"]')) syncMoveTo(tree);
    });

    const target = document.getElementById("moveto-target");
    if (target) {
      const all = items(tree);
      [...target.options].forEach((o) => { if (/^\d+$/.test(o.value)) o.hmLi = all[Number(o.value)]; });
    }

    tree.addEventListener("change", (evt) => {
      if (evt.target.matches("[data-hm-type]")) evt.target.closest("[data-hm-item]").dataset.type = evt.target.value;
    });

    if (window.Sortable) {
      let carried = [];
      window.Sortable.create(tree, {
        draggable: "[data-hm-item]",
        handle: "[data-hm-handle]",
        animation: 120,
        onStart: (evt) => {
          // The sub-items travel with the item.
          carried = block(tree, evt.item).slice(1);
        },
        onEnd: (evt) => {
          carried.reverse().forEach((el) => evt.item.after(el));
          carried = [];
          renumber(tree);
        },
      });
    }
  });
}

document.addEventListener("DOMContentLoaded", () => init(document));
document.addEventListener("htmx:afterSettle", (evt) => init(evt.target));
export { renumber, init };
