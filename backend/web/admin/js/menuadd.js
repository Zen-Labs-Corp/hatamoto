// Menu editor "Add" popover. The server keeps the form working without scripts
// (a select and an Add item button); with scripts the popover offers pages to
// tick (several at once), an address, a heading and an archive. Rows are added
// to the tree in the page, as one undo step, through api.addRows from
// @hm/menutree, from a blank item the server renders into a template. Nothing
// here posts: the server still checks the saved tree.
import { init as initSearchSelect } from "@hm/searchselect";

const clone = (name) => document.querySelector(`template[data-hm-tpl="${name}"]`)?.content.firstElementChild.cloneNode(true);

export function init(form, api) {
  if (form.dataset.hmAdd) return;
  const pop = clone("add");
  if (!pop) return;
  form.dataset.hmAdd = "1";
  // The panel sits outside the form: its fields are not part of what a save posts.
  form.after(pop);
  initSearchSelect(pop);
  document.querySelectorAll("[data-hm-add-open]").forEach((b) => { b.hidden = false; });

  const tabs = [...pop.querySelectorAll('[role="tab"]')];
  const panels = tabs.map((t) => pop.querySelector(`#${t.getAttribute("aria-controls")}`));
  const search = pop.querySelector("[data-hm-add-search]");
  const list = pop.querySelector("[data-hm-add-list]");
  const picked = pop.querySelector("[data-hm-add-picked]");
  const status = pop.querySelector("[data-hm-add-status]");
  const confirmPages = pop.querySelector('[data-hm-add-confirm="pages"]');
  let after = -1; // the row the "+" action belongs to, or -1 for the end
  let rowOpener = null; // the row's "+" that opened the panel: it places the panel and gets focus back
  const chosen = new Map(); // page id to title, kept while the search changes
  let timer = 0;
  let seq = 0;

  function showTab(tab, focus) {
    tabs.forEach((t, i) => {
      const on = t === tab;
      t.setAttribute("aria-selected", String(on));
      t.tabIndex = on ? 0 : -1;
      panels[i].hidden = !on;
    });
    if (focus) tab.focus();
  }
  tabs.forEach((t, i) => {
    t.addEventListener("click", () => showTab(t, false));
    t.addEventListener("keydown", (evt) => {
      const step = evt.key === "ArrowRight" ? 1 : evt.key === "ArrowLeft" ? -1 : 0;
      const to = evt.key === "Home" ? 0 : evt.key === "End" ? tabs.length - 1 : step ? (i + step + tabs.length) % tabs.length : -1;
      if (to < 0) return;
      evt.preventDefault();
      showTab(tabs[to], true);
    });
  });

  // Pages: a listbox where several options can be selected.
  const options = () => [...list.querySelectorAll('[role="option"]')];
  function paintPicked() {
    picked.replaceChildren(...[...chosen.values()].map((title) => {
      const chip = clone("chip");
      chip.textContent = title;
      return chip;
    }));
    status.textContent = chosen.size === 0 ? "" : chosen.size === 1 ? "1 page selected" : `${chosen.size} pages selected`;
    confirmPages.disabled = chosen.size === 0;
    options().forEach((o) => o.setAttribute("aria-selected", String(chosen.has(o.dataset.value))));
  }
  function toggle(opt) {
    if (chosen.has(opt.dataset.value)) chosen.delete(opt.dataset.value);
    else chosen.set(opt.dataset.value, opt.dataset.label);
    paintPicked();
  }
  function mark(i) {
    const opts = options();
    opts.forEach((o, k) => o.toggleAttribute("data-active", k === i));
    if (opts[i]) {
      search.setAttribute("aria-activedescendant", opts[i].id);
      opts[i].scrollIntoView({ block: "nearest" });
    } else {
      search.removeAttribute("aria-activedescendant");
    }
  }
  function show(results) {
    list.replaceChildren(...results.map((r, k) => {
      const opt = clone("add-option");
      opt.id = `add-opt-${k}`;
      opt.dataset.value = r.value;
      opt.dataset.label = r.label;
      opt.setAttribute("aria-selected", String(chosen.has(r.value)));
      opt.querySelector("[data-hm-add-name]").textContent = r.label;
      opt.querySelector("[data-hm-add-hint]").textContent = r.hint ?? "";
      opt.addEventListener("mousedown", (evt) => evt.preventDefault()); // keep focus in the search box
      opt.addEventListener("click", () => toggle(opt));
      return opt;
    }));
    mark(-1);
    if (results.length === 0) status.textContent = "No results";
    else paintPicked();
  }
  async function find() {
    const mine = ++seq;
    try {
      const res = await fetch(`${search.dataset.src}?q=${encodeURIComponent(search.value.trim())}`, { headers: { Accept: "application/json" }, credentials: "same-origin" });
      if (!res.ok) throw new Error(String(res.status));
      const results = await res.json();
      if (mine === seq) show(results);
    } catch {
      if (mine === seq) status.textContent = "Could not load pages";
    }
  }
  search.addEventListener("input", () => {
    clearTimeout(timer);
    timer = setTimeout(find, 200);
  });
  search.addEventListener("keydown", (evt) => {
    const opts = options();
    const cur = opts.findIndex((o) => o.hasAttribute("data-active"));
    if (evt.key === "ArrowDown") { evt.preventDefault(); mark(Math.min(cur + 1, opts.length - 1)); }
    else if (evt.key === "ArrowUp") { evt.preventDefault(); mark(Math.max(cur - 1, 0)); }
    else if (evt.key === "Enter") {
      evt.preventDefault();
      if (cur >= 0) toggle(opts[cur]);
      else if (chosen.size) confirmPages.click();
    }
  });

  // Confirm: build the specs, add them in one step and put the keyboard on the first new row.
  function done(specs) {
    const rows = api.addRows(specs, after);
    if (rows.length === 0) return;
    pop.hidePopover();
    api.focusRow(rows[0]);
    rows[0].focus();
  }
  const text = (sel) => pop.querySelector(sel).value.trim();
  function needs(input) {
    input.setAttribute("aria-invalid", "true");
    input.focus();
  }
  pop.addEventListener("input", (evt) => evt.target.removeAttribute?.("aria-invalid"));
  pop.addEventListener("click", (evt) => {
    const kind = evt.target.closest("[data-hm-add-confirm]")?.dataset.hmAddConfirm;
    if (!kind) return;
    if (kind === "pages") {
      done([...chosen].map(([entity, name]) => ({ type: "entity", entity, name, label: name })));
    } else if (kind === "address") {
      const label = pop.querySelector('[data-hm-add-label="address"]');
      const url = pop.querySelector("[data-hm-add-url]");
      if (!label.value.trim()) return needs(label);
      if (!url.value.trim()) return needs(url);
      done([{ type: "custom", label: label.value.trim(), url: url.value.trim() }]);
    } else if (kind === "heading") {
      const label = pop.querySelector('[data-hm-add-label="heading"]');
      if (!label.value.trim()) return needs(label);
      done([{ type: "none", label: label.value.trim() }]);
    } else if (kind === "archive") {
      const box = pop.querySelector("#add-panel-archive [data-hm-ss-input]");
      const value = pop.querySelector("#add-panel-archive [data-hm-ss-value]").value;
      const name = box.value.trim();
      if (!name) return needs(box);
      done([{ type: "archive", term: value, name, label: name }]);
    }
  });

  // Opening: the header Add button adds at the end, a row's "+" adds after that row.
  document.addEventListener("click", (evt) => {
    const opener = evt.target.closest("[data-hm-add-open], [data-hm-add-after]");
    if (!opener) return;
    const row = opener.closest("[data-hm-item]");
    after = row ? api.items().indexOf(row) : -1;
    rowOpener = row ? opener : null;
    if (row && !pop.matches(":popover-open")) pop.showPopover();
  }, true);
  // placeAt puts the panel under a row's "+", as the shared popover code does for the header button.
  function placeAt(el) {
    const r = el.getBoundingClientRect();
    pop.style.top = `${Math.max(8, Math.min(r.bottom + 4, innerHeight - pop.offsetHeight - 8))}px`;
    pop.style.left = `${Math.max(8, Math.min(r.left, document.documentElement.clientWidth - pop.offsetWidth - 8))}px`;
  }
  pop.addEventListener("toggle", (evt) => {
    if (rowOpener) {
      // The shared code answered for the header button: undo that for a row's "+".
      document.querySelector('[data-hm-add-open][popovertarget="menu-add"]')?.setAttribute("aria-expanded", "false");
      if (evt.newState === "open") placeAt(rowOpener);
    }
    if (evt.newState !== "open") {
      const back = rowOpener;
      rowOpener = null;
      if (back?.isConnected && (pop.contains(document.activeElement) || document.activeElement === document.body)) back.focus();
      // A fresh start next time.
      chosen.clear();
      pop.querySelectorAll("input:not([type=hidden])").forEach((i) => { i.value = ""; i.removeAttribute("aria-invalid"); });
      pop.querySelectorAll("[data-hm-ss-value], [data-hm-ss-prev]").forEach((i) => { i.value = ""; });
      paintPicked();
      return;
    }
    showTab(tabs[0], false);
    find(); // a reopened panel starts from the results of an empty search
    search.focus();
  });
  paintPicked();
}
