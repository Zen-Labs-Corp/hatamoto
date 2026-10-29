// Menu editor keyboard, selection and view. Each row is a stop of its own
// (roving tabindex): the arrow keys move between rows, Alt with an arrow moves
// or indents the row, Enter or F2 renames it, Delete removes it, Ctrl or Cmd
// with D duplicates it and with S saves. Click selects a row; Ctrl, Cmd or
// Shift click selects several. The filter and the collapse buttons only change
// what is shown, never the tree. Every edit goes through the editor's own
// operations (api.apply, api.many), so it can be undone like a click.
//
// The selection is announced to the inspector as a "hm:select" event on the
// document with detail.items (the <li> rows); the same event from another
// module (the preview) selects those rows.
const FIELD = "input, textarea, select, [contenteditable]";
const CLICKABLE = "button, a, input, select, textarea, summary, label, [popover], [data-hm-handle]";

// ops maps a key to the structural operation it runs on the row.
const ALT_OPS = { ArrowUp: "up", ArrowDown: "down", ArrowRight: "indent", ArrowLeft: "outdent" };

export function init(tree, api) {
  const depthOf = (li) => Number(li.dataset.depth || 0);
  const label = (li) => li.querySelector('[name$=".label"]')?.value.trim() ?? "";
  const clone = (name) => document.querySelector(`template[data-hm-tpl="${name}"]`)?.content.firstElementChild.cloneNode(true);

  let selected = [];
  let cursor = null; // the row that holds the tab stop and anchors a range
  let query = "";
  const collapsed = new Set();
  let emitting = false;

  const rows = () => api.items();
  const visible = () => rows().filter((li) => !li.hidden);

  function emit() {
    emitting = true;
    document.dispatchEvent(new CustomEvent("hm:select", { detail: { items: [...selected] } }));
    emitting = false;
  }

  function paint() {
    const all = rows();
    selected = selected.filter((li) => all.includes(li));
    if (!all.includes(cursor)) cursor = selected[0] ?? all[0] ?? null;
    all.forEach((li, i) => {
      // A row is named by what it shows (title, where it links, chips), not by every button and field it holds.
      const parts = [[".hm-menutree__title", "name"], [".hm-menutree__target-summary", "target"], ["[data-hm-chips]", "chips"], ["[data-hm-warn]", "warn"]]
        .map(([sel, key]) => {
          const el = li.querySelector(sel);
          if (el) el.id = `i-${i}-${key}`;
          return el?.id;
        })
        .filter(Boolean);
      li.setAttribute("aria-labelledby", parts.join(" "));
      li.setAttribute("role", "treeitem");
      li.setAttribute("aria-selected", String(selected.includes(li)));
      li.setAttribute("aria-level", String(depthOf(li) + 1));
      li.toggleAttribute("data-hm-selected", selected.includes(li));
      li.toggleAttribute("data-hm-collapsed", collapsed.has(li));
      li.tabIndex = li === cursor ? 0 : -1;
    });
  }

  function select(list, primary, announce = true) {
    selected = list;
    cursor = primary ?? list[list.length - 1] ?? cursor;
    paint();
    if (announce) emit();
  }

  // view hides rows under a collapsed parent and rows the filter does not match
  // (a match keeps its parents shown, so it stays in context).
  function view() {
    const all = rows();
    const q = query.trim().toLowerCase();
    const shown = new Set(all);
    if (q) {
      shown.clear();
      all.forEach((li, i) => {
        if (!(`${label(li)} ${li.querySelector(".hm-menutree__target-summary")?.textContent ?? ""}`.toLowerCase().includes(q))) return;
        shown.add(li);
        for (let k = i - 1, d = depthOf(li); k >= 0 && d > 0; k--) {
          if (depthOf(all[k]) < d) {
            shown.add(all[k]);
            d = depthOf(all[k]);
          }
        }
      });
    } else {
      let hideBelow = Infinity;
      all.forEach((li) => {
        const d = depthOf(li);
        if (d <= hideBelow) hideBelow = Infinity;
        if (d > hideBelow) shown.delete(li);
        else if (collapsed.has(li)) hideBelow = d;
      });
    }
    all.forEach((li, i) => {
      li.hidden = !shown.has(li);
      const parent = i + 1 < all.length && depthOf(all[i + 1]) > depthOf(li);
      if (parent) li.setAttribute("aria-expanded", String(!collapsed.has(li)));
      else li.removeAttribute("aria-expanded");
      const toggle = li.querySelector("[data-hm-collapse]");
      if (toggle) {
        toggle.hidden = !parent;
        toggle.setAttribute("aria-expanded", String(!collapsed.has(li)));
        toggle.setAttribute("aria-label", `${collapsed.has(li) ? "Show" : "Hide"} sub-items of ${label(li) || "this item"}`);
      }
    });
    if (cursor?.hidden) cursor = visible()[0] ?? cursor;
    const count = document.querySelector("[data-hm-count]");
    if (count) count.textContent = `${all.length} ${all.length === 1 ? count.dataset.one : count.dataset.other}`;
    paint();
  }

  function refresh() {
    [...collapsed].forEach((li) => { if (!rows().includes(li)) collapsed.delete(li); });
    const had = selected.length;
    view();
    if (selected.length !== had) emit(); // a selected row was removed
  }

  // targets is what a bulk key acts on: the selection, or the row the key was pressed on.
  const targets = (li) => (selected.includes(li) && selected.length > 1 ? [...selected] : [li]);

  function focusRow(li) {
    if (!li) return;
    select([li], li);
    li.focus();
  }

  function move(li, step) {
    const list = visible();
    focusRow(list[list.indexOf(li) + step]);
  }

  function rename(li) {
    const inspector = document.querySelector('[data-hm-inspector] [data-hm-ins="label"]');
    if (inspector) {
      inspector.focus();
      return;
    }
    const details = li.querySelector(".hm-menutree__edit");
    if (details) details.open = true;
    li.querySelector('[name$=".label"]')?.focus();
  }

  function save() {
    api.form.requestSubmit(api.form.querySelector('button[name="_op"][value="save"]'));
  }

  function shortcuts() {
    const dlg = document.getElementById("menu-shortcuts");
    if (dlg && !dlg.open) dlg.showModal();
  }

  function keydown(evt) {
    const mod = evt.metaKey || evt.ctrlKey;
    const inField = evt.target.closest?.(FIELD);
    if (mod && !evt.altKey && evt.key.toLowerCase() === "s") {
      evt.preventDefault();
      save();
      return;
    }
    // Enter in a field of the tree, the inspector or the filter confirms the field.
    // It must not reach the form's default button, which saves the whole menu.
    if (evt.key === "Enter" && !mod && !evt.altKey && evt.target.matches?.("input:not([type=checkbox], [type=radio], [type=button], [type=submit])") && evt.target.closest("[data-hm-menutree], [data-hm-inspector], [data-hm-filter]")) {
      evt.preventDefault();
      if (evt.target.matches('[data-hm-inspector] [data-hm-ins="label"]')) cursor?.focus();
      return;
    }
    if (inField || evt.target.closest?.("dialog, [popover]:popover-open")) return;
    const li = evt.target.closest?.("[data-hm-item]");
    if (!li || !tree.contains(li)) {
      if (evt.key === "?" && !mod) {
        evt.preventDefault();
        shortcuts();
      }
      return;
    }
    const onRow = evt.target === li;
    if (evt.altKey && !mod && ALT_OPS[evt.key]) {
      evt.preventDefault();
      const op = ALT_OPS[evt.key];
      if (selected.includes(li) && selected.length > 1) api.many(op, selected);
      else api.apply(op, li);
      li.focus();
    } else if (evt.altKey && evt.code === "KeyN") {
      evt.preventDefault();
      api.form.requestSubmit(api.form.querySelector('button[name="_op"][value="add"]'));
    } else if (mod && !evt.altKey && evt.key.toLowerCase() === "d") {
      evt.preventDefault();
      api.many("dup", targets(li));
    } else if (onRow && evt.key === "ArrowDown") {
      evt.preventDefault();
      move(li, 1);
    } else if (onRow && evt.key === "ArrowUp") {
      evt.preventDefault();
      move(li, -1);
    } else if (onRow && evt.key === "Home") {
      evt.preventDefault();
      focusRow(visible()[0]);
    } else if (onRow && evt.key === "End") {
      evt.preventDefault();
      focusRow(visible().at(-1));
    } else if (onRow && (evt.key === "Enter" || evt.key === "F2")) {
      evt.preventDefault();
      rename(li);
    } else if (onRow && !mod && evt.key === "Delete") {
      evt.preventDefault();
      const list = visible();
      const at = list.indexOf(li);
      api.many("remove", targets(li));
      const now = visible();
      focusRow(now.find((el) => list.indexOf(el) > at) ?? now.at(-1));
    } else if (onRow && evt.key === "?") {
      evt.preventDefault();
      shortcuts();
    } else if (onRow && evt.key === "ArrowRight" && collapsed.has(li)) {
      evt.preventDefault();
      collapsed.delete(li);
      view();
    } else if (onRow && evt.key === "ArrowLeft" && !evt.altKey && !collapsed.has(li) && li.querySelector("[data-hm-collapse]:not([hidden])")) {
      evt.preventDefault();
      collapsed.add(li);
      view();
    }
  }
  document.addEventListener("keydown", keydown);

  // Click selects; Ctrl or Cmd adds or removes one row, Shift takes the range.
  tree.addEventListener("click", (evt) => {
    const toggle = evt.target.closest("[data-hm-collapse]");
    if (toggle) {
      const li = toggle.closest("[data-hm-item]");
      if (collapsed.has(li)) collapsed.delete(li);
      else collapsed.add(li);
      view();
      return;
    }
    const li = evt.target.closest("[data-hm-item]");
    if (!li) return;
    if (evt.target.closest(CLICKABLE)) {
      // Using a control in a row selects the row, unless it is part of a selection.
      if (!selected.includes(li) && !evt.shiftKey && !evt.metaKey && !evt.ctrlKey) select([li], li);
      return;
    }
    if (evt.shiftKey && cursor) {
      const list = visible();
      const [a, b] = [list.indexOf(cursor), list.indexOf(li)].sort((x, y) => x - y);
      select(list.slice(a, b + 1), li);
    } else if (evt.metaKey || evt.ctrlKey) {
      select(selected.includes(li) ? selected.filter((x) => x !== li) : [...selected, li], li);
    } else {
      select([li], li);
    }
    li.focus();
  });
  // Shift click would also select text.
  tree.addEventListener("mousedown", (evt) => {
    if (evt.shiftKey && !evt.target.closest(FIELD)) evt.preventDefault();
  });
  // Focus by keyboard selects the row; with the pointer the click decides (it may add to a selection).
  let pressing = false;
  tree.addEventListener("pointerdown", () => { pressing = true; }, true);
  document.addEventListener("pointerup", () => setTimeout(() => { pressing = false; }));
  tree.addEventListener("focusin", (evt) => {
    const li = evt.target.closest("[data-hm-item]");
    if (!li || selected.includes(li) || pressing) return;
    select([li], li);
  });

  document.addEventListener("hm:select", (evt) => {
    if (emitting) return;
    const list = (evt.detail?.items ?? []).filter((li) => rows().includes(li));
    select(list, list.at(-1), false);
  });

  // The rows are a tree for assistive technology: the server's list stays a plain
  // list without this script. A row is named by its content, led by its title.
  tree.setAttribute("role", "tree");
  tree.setAttribute("aria-label", "Menu items");
  tree.setAttribute("aria-multiselectable", "true");

  // The view controls come from the server and stay hidden until this script runs.
  const tools = clone("treetools");
  if (tools) {
    tree.before(tools);
    const filter = tools.querySelector("[data-hm-filter]");
    filter?.addEventListener("input", () => {
      query = filter.value;
      view();
    });
    tools.querySelector("[data-hm-collapse-all]")?.addEventListener("click", () => {
      rows().forEach((li, i, all) => { if (i + 1 < all.length && depthOf(all[i + 1]) > depthOf(li)) collapsed.add(li); });
      view();
    });
    tools.querySelector("[data-hm-expand-all]")?.addEventListener("click", () => {
      collapsed.clear();
      view();
    });
  }
  const foot = clone("treefoot");
  if (foot) tree.after(foot);

  select([], rows()[0], false);
  view();
  return { refresh, focusCursor: () => cursor?.focus(), selection: () => [...selected] };
}
