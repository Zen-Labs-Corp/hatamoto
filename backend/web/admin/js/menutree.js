// Menu editor. The tree is a flat <ol> of <li data-hm-item data-depth>; the
// form fields are named i.N.* and the server reads them in N order. This script
// applies the structural buttons (up, down, indent, outdent, duplicate, move to,
// remove) in the page, with the same pure operations the server runs
// (@hm/menulogic), and keeps undo and redo, the unsaved state and the list of
// items that need attention. Without it the same buttons post to the server,
// which applies the edit and re-renders. Dragging lives in @hm/menudrag.
import { renumberOp, renumberRef } from "@hm/listlogic";
import { duplicateBlock, indentBlock, moveBlock, moveToParent, outdentBlock, removeBlock, blockEnd } from "@hm/menulogic";
import { init as initAdd } from "@hm/menuadd";
import { init as initDrag } from "@hm/menudrag";
import { init as initKeys } from "@hm/menukeys";
import { init as initSearchSelect } from "@hm/searchselect";

const DEFAULT_MAX_DEPTH = 5;
const OP_RE = /^(up|down|indent|outdent|dup|moveto|remove)\.(\d+)$/;
const UNTITLED = "Untitled item";

// The wording is the server's (menus.go), so a client and a server edit read alike.
const DONE = {
  up: "Item moved up. It is not saved yet.",
  down: "Item moved down. It is not saved yet.",
  indent: "Item indented. It is not saved yet.",
  outdent: "Item moved out. It is not saved yet.",
  dup: "Item duplicated. It is not saved yet.",
  moveto: "Item moved. It is not saved yet.",
  remove: "Item removed with its sub-items. It is not saved yet.",
  add: "Item added. It is not saved yet.",
};
const limitNotice = (max) => `A menu holds at most ${max} items.`;
const REFUSED = {
  up: "This item is already first.",
  down: "This item is already last.",
  indent: "This item cannot be indented: it needs an item above it to go under.",
  outdent: "This item is already at the top level.",
  chooseTarget: "Choose where to move this item, then use Move to again.",
  underSelf: "An item cannot move under itself.",
  tooDeep: "This item has too many levels under it to move there.",
};

function items(tree) {
  return [...tree.querySelectorAll(":scope > [data-hm-item]")];
}
const depthOf = (li) => Number(li.dataset.depth || 0);
const field = (li, suffix) => li.querySelector(`[name$=".${suffix}"]`);

function setDepth(li, d) {
  li.dataset.depth = String(d);
  li.querySelector("[data-hm-depth]").value = String(d);
}

const maxDepthOf = (tree) => Number(tree.closest("form")?.dataset.maxDepth) || DEFAULT_MAX_DEPTH;

// model is the tree as rows for @hm/menulogic; each row keeps its <li>.
const model = (tree) => items(tree).map((li) => ({ depth: depthOf(li), li }));

// cloneItem copies an item for Duplicate. The copy gets no HTML id (an id is
// unique on the page) and its search boxes are wired again.
function cloneItem(li) {
  const copy = li.cloneNode(true);
  const id = field(copy, "id");
  if (id) id.value = "";
  copy.removeAttribute("hidden");
  copy.removeAttribute("data-hm-selected");
  copy.removeAttribute("data-hm-collapsed");
  copy.querySelectorAll("[data-hm-searchselect]").forEach((box) => box.removeAttribute("data-hm-ready"));
  return copy;
}

// place writes rows back into the tree. A row whose <li> was already placed is
// a copy (Duplicate) and gets its own element.
function place(tree, rows) {
  const seen = new Set();
  const lis = rows.map((r) => {
    const li = seen.has(r.li) ? cloneItem(r.li) : r.li;
    seen.add(li);
    li.dataset.depth = String(r.depth);
    return li;
  });
  // Moving nodes drops focus; the control that had it gets it back.
  const focused = tree.contains(document.activeElement) ? document.activeElement : null;
  tree.replaceChildren(...lis);
  lis.forEach((li) => initSearchSelect(li));
  if (focused?.isConnected) focused.focus();
}

// renumber rewrites every i.N / i-N / op value to match the DOM order, keeps
// depths valid and refreshes the visible and spoken item labels.
function renumber(tree) {
  let prev = -1;
  const max = maxDepthOf(tree);
  const all = items(tree);
  all.forEach((li, i) => {
    const d = Math.max(0, Math.min(depthOf(li), prev + 1, max));
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
  // Duplicate and Remove take the sub-items along: say so exactly when there are some.
  all.forEach((li, i) => {
    const suffix = i + 1 < all.length && depthOf(all[i + 1]) > depthOf(li) ? " and its sub-items" : "";
    li.querySelectorAll('[name="_op"][value^="dup."] .hm-visually-hidden, [name="_op"][value^="remove."] .hm-visually-hidden').forEach((el) => {
      el.textContent = ` item ${i + 1}${suffix}`;
    });
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
    const o = new Option(`Under ${li.querySelector('[name$=".label"]')?.value.trim() || UNTITLED}`, String(i));
    o.hmLi = li;
    o.selected = li === chosen;
    sel.append(o);
  });
}

// refreshOps disables the buttons that cannot act: indent under nothing,
// outdent at the top level. The server renders the same state on load.
function refreshOps(tree) {
  const all = items(tree);
  const max = maxDepthOf(tree);
  all.forEach((li, i) => {
    const d = depthOf(li);
    const indent = li.querySelector('[data-hm-op="indent"]');
    const outdent = li.querySelector('[name="_op"][value^="outdent."]');
    if (indent) indent.disabled = i === 0 || d > depthOf(all[i - 1]) || d >= max;
    if (outdent) outdent.disabled = d === 0;
  });
}

// problemOf is "err" for an item that cannot be saved as it is, "warn" for one
// that saves but renders as plain text, "" otherwise. It mirrors the server.
function problemOf(li) {
  if (!field(li, "label")?.value.trim()) return "err";
  // A message the server sent stays until the field it names is edited (see clearServerError).
  if (li.querySelector('[aria-invalid="true"]')) return "err";
  const type = li.dataset.type;
  const filled = (name) => Boolean(field(li, name)?.value.trim() || field(li, `${name}_q`)?.value.trim());
  if (type === "entity" && !filled("entity")) return "warn";
  if (type === "archive" && !filled("term")) return "warn";
  if (type === "custom" && !filled("url")) return "warn";
  return "";
}

// clearServerError drops the server's message for the field that was just edited:
// its red mark and its text. The item counts as fixed once none are left.
function clearServerError(target) {
  const wrap = target.closest?.(".hm-field");
  if (!wrap) return;
  wrap.querySelectorAll('[aria-invalid="true"]').forEach((el) => el.removeAttribute("aria-invalid"));
  wrap.classList.remove("hm-field--invalid");
  wrap.querySelectorAll(".hm-field__error").forEach((el) => el.remove());
}

// inspectorField is the inspector control for a row field named by a suffix
// (label, url, entity, style ...), opening the Advanced section when it is in there.
function inspectorField(suffix) {
  const root = document.querySelector("[data-hm-inspector]");
  const ctl = root?.querySelector(`[data-hm-ins="${suffix}"]`)
    ?? root?.querySelector(`[data-hm-ins-target="${suffix === "term" ? "archive" : suffix}"] [data-hm-ss-input]`)
    ?? root?.querySelector(`[data-hm-ins-group="${suffix}"] [tabindex="0"], [data-hm-ins-group="${suffix}"] input:checked`);
  const more = ctl?.closest("details");
  if (more) more.open = true;
  return ctl ?? root?.querySelector('[data-hm-ins="label"]');
}

function targetSummary(li) {
  const label = field(li, "label")?.value.trim() ?? "";
  const type = li.dataset.type;
  if (type === "none") return "No link";
  const kind = type === "entity" ? "Page or post" : type === "archive" ? "Archive" : "Link";
  const name = (type === "entity" ? field(li, "entity_q")?.value : type === "archive" ? field(li, "term_q")?.value : field(li, "url")?.value)?.trim() ?? "";
  return name && name !== label ? `${kind} · ${name}` : kind;
}

// safeAddress mirrors the server's check of a custom address (safeMenuURL in menus.go).
function safeAddress(u) {
  if (u.length > 500 || /[\s\\<>"]/.test(u) || u.startsWith("//")) return false;
  return /^[/#?]/.test(u) || /^https?:\/\/[^/?#]+/i.test(u) || /^(mailto|tel):\S+/i.test(u);
}

const chipText = (key) => document.querySelector('template[data-hm-tpl="chiptext"]')?.content.querySelector(`[data-key="${key}"]`)?.textContent ?? "";

// optionText is what a theme preset field shows for its chosen value ("" for none).
function optionText(li, suffix) {
  const f = field(li, suffix);
  if (!f?.value) return "";
  return f.selectedOptions?.[0]?.textContent.trim() || f.value;
}

// chipsFor lists the short facts shown after a row's title: its link state, the
// theme presets it uses and its CSS classes. The words come from the page's templates.
function chipsFor(li) {
  const type = li.dataset.type;
  const filled = (name) => Boolean(field(li, name)?.value.trim() || field(li, `${name}_q`)?.value.trim());
  const list = [];
  if (type === "none") list.push(chipText("nolink"));
  else if (type === "entity" && !filled("entity")) list.push(chipText("nopage"));
  else if (type === "custom" && filled("url") && !safeAddress(field(li, "url").value.trim())) list.push(chipText("badaddress"));
  list.push(optionText(li, "badge"), optionText(li, "style"), optionText(li, "color"));
  list.push(...(field(li, "class")?.value.split(/\s+/) ?? []));
  return list.filter(Boolean);
}

// paintChips keeps the kind icon and the chips of a row current. A chip is the
// page's own badge template; only its text is set here.
function paintChips(li) {
  const kind = li.querySelector("[data-hm-kind-slot]");
  if (kind && kind.dataset.kind !== li.dataset.type) {
    kind.dataset.kind = li.dataset.type;
    const icon = document.querySelector(`template[data-hm-tpl="kinds"]`)?.content.querySelector(`[data-hm-kind="${li.dataset.type}"]`);
    if (icon) kind.replaceChildren(...icon.cloneNode(true).childNodes);
    // The server's icon gives way to the one from the template.
    li.querySelector(":scope > .hm-menutree__head > .hm-icon")?.remove();
  }
  const slot = li.querySelector("[data-hm-chips]");
  if (!slot) return;
  const list = chipsFor(li);
  const key = list.join("\u0001");
  if (slot.dataset.chips === key) return;
  slot.dataset.chips = key;
  slot.replaceChildren(...list.map((text) => {
    const chip = clone("chip");
    chip.textContent = text;
    return chip;
  }));
}

// refreshRow brings the row's title, summary, accessible names and warning in
// line with its fields, as the server renders them.
function refreshRow(li) {
  const label = field(li, "label")?.value.trim() ?? "";
  const title = li.querySelector(".hm-menutree__title");
  if (title) title.textContent = label || UNTITLED;
  const summary = li.querySelector(".hm-menutree__target-summary");
  if (summary) summary.textContent = targetSummary(li);
  const name = `Actions for ${label || UNTITLED}`;
  li.querySelector("[data-hm-add-after]")?.setAttribute("aria-label", `Add an item after ${label || UNTITLED}`);
  li.querySelector('[popovertarget][aria-haspopup="menu"]')?.setAttribute("aria-label", name);
  li.querySelector(".hm-menu")?.setAttribute("aria-label", name);
  const warn = problemOf(li) === "warn";
  const mark = li.querySelector("[data-hm-warn]");
  if (mark) mark.hidden = !warn;
  const note = li.querySelector(".hm-menutree__fields > .hm-callout");
  if (note && !warn) note.hidden = true;
  paintChips(li);
}

function announce(msg) {
  document.dispatchEvent(new CustomEvent("hm:toast", { detail: { kind: "info", message: msg, timeout: 2500 } }));
}

const clone = (name) => document.querySelector(`template[data-hm-tpl="${name}"]`)?.content.firstElementChild.cloneNode(true);

// newRow copies the page's blank item and fills it from spec. The script's own
// switches are turned on as they are for the rows the server rendered.
function newRow(spec, depth) {
  const li = clone("newitem");
  if (!li) return {};
  li.querySelectorAll("[data-hm-editor-only]").forEach((el) => { el.hidden = false; });
  li.querySelectorAll("[data-hm-handle]").forEach((el) => { el.hidden = false; });
  const type = spec.type ?? "entity";
  li.dataset.type = type;
  li.querySelectorAll("[data-hm-type]").forEach((r) => { r.checked = r.value === type; });
  const set = (suffix, value) => { const f = field(li, suffix); if (f && value) f.value = value; };
  // A script skips maxlength, and the server refuses a label over 100 characters; the full title stays in the search text.
  set("label", spec.label && [...spec.label].slice(0, 100).join(""));
  set("url", spec.url);
  // A typed name without a pick is kept too: the server resolves it on save, as for a search box.
  const key = type === "entity" ? "entity" : type === "archive" ? "term" : "";
  if (key) {
    set(key, spec[key]);
    set(`${key}_q`, spec.name);
    set(`${key}_prev`, spec.name);
  }
  return { depth, li };
}

function init(root) {
  root.querySelectorAll("[data-hm-menutree]").forEach((tree) => {
    if (tree.dataset.hmReady) return;
    tree.setAttribute("data-hm-ready", "1");
    const form = tree.closest("form");
    // The save bar sits outside the form once the layout has placed it.
    const ui = {
      state: document.querySelector("[data-hm-state]"),
      issues: document.querySelector("[data-hm-issues]"),
      undo: document.querySelector("[data-hm-undo-all]"),
      redo: document.querySelector("[data-hm-redo]"),
      discard: document.querySelector("[data-hm-discard]"),
      notice: form.querySelector("[data-hm-notice]"),
    };
    let keys = null; // set once the keyboard module is wired, after the first refresh
    const undoStack = [];
    const redoStack = [];
    let edited = form.hasAttribute("data-hm-dirty"); // a server edit or a failed save is unsaved work
    let leaving = false;
    const issueAt = { err: -1, warn: -1 };

    document.querySelectorAll("[data-hm-editor-only]").forEach((el) => { el.hidden = false; });
    // The layout rules in menueditor.css apply only from here: without this script the form stays as the server renders it.
    form.setAttribute("data-hm-js", "");
    // The row fields are hidden while the inspector edits them, and a hidden required field would
    // stop the browser's own check without a word. The count of items to fix and the server decide.
    form.noValidate = true;
    document.getElementById("menu-details")?.setAttribute("data-hm-inline", "");
    form.querySelector(":scope > .hm-savebar")?.setAttribute("data-hm-bar", "");
    tree.querySelectorAll("[data-hm-handle]").forEach((h) => { h.hidden = false; });

    const dirty = () => edited || undoStack.length > 0;

    function refreshBar() {
      const bar = ui.state?.querySelector("[data-hm-savestate]");
      if (bar) {
        bar.dataset.state = dirty() ? "unsaved" : "saved";
        bar.querySelector(".hm-savestate__text").textContent = dirty() ? "Unsaved changes" : "All changes saved";
      }
      if (ui.undo) ui.undo.disabled = undoStack.length === 0;
      if (ui.redo) ui.redo.disabled = redoStack.length === 0;
      if (ui.discard) ui.discard.disabled = !dirty();
    }

    function refreshIssues() {
      const all = items(tree);
      all.forEach(refreshRow);
      if (!ui.issues) return;
      const counts = { err: 0, warn: 0 };
      all.forEach((li) => { const p = problemOf(li); if (p) counts[p]++; });
      // Rebuilding only on a change keeps a chip alive between mouse down and click.
      const key = `${counts.err},${counts.warn}`;
      if (ui.issues.dataset.counts === key) return;
      ui.issues.dataset.counts = key;
      const chips = [];
      for (const [kind, tpl] of [["err", "problem-error"], ["warn", "problem"]]) {
        if (!counts[kind]) continue;
        const chip = clone(tpl);
        const text = chip.querySelector(".hm-badge").lastChild;
        text.textContent = `${counts[kind]} ${text.textContent}`;
        chips.push(chip);
      }
      ui.issues.replaceChildren(...chips);
    }

    const refresh = () => { renumber(tree); refreshIssues(); refreshBar(); keys?.refresh(); };

    function showNotice(message) {
      if (!ui.notice) return;
      ui.notice.replaceChildren();
      if (!message) return;
      const note = clone("notice");
      if (!note) return;
      note.querySelector(".hm-callout__body").textContent = message;
      ui.notice.append(note);
    }

    // commit records the tree as it was before an edit, so Undo can restore it.
    let batch = null; // while set, edits are one undo step: {before}
    function commit(before) {
      if (batch) {
        batch.before ??= before;
        return;
      }
      undoStack.push(before);
      redoStack.length = 0;
      refresh();
    }

    function restore(from, to, message) {
      const snap = from.pop();
      if (!snap) return;
      to.push(model(tree));
      place(tree, snap);
      showNotice("");
      refresh();
      // A row that held focus may be gone, and the button that was clicked may
      // have just disabled itself: keep the keyboard user in the tree.
      const at = document.activeElement;
      if (!at || at === document.body || at.disabled || !at.isConnected) keys?.focusCursor();
      announce(message);
    }
    const undo = () => restore(undoStack, redoStack, "Undone.");
    const redo = () => restore(redoStack, undoStack, "Redone.");

    // runOp applies one structural button on the tree. It returns the refusal
    // text, or "" when the edit was applied.
    function runOp(name, i) {
      const rows = model(tree);
      const max = maxDepthOf(tree);
      const maxItems = Number(form.dataset.maxItems) || Infinity;
      let next = null;
      switch (name) {
        case "up": next = moveBlock(rows, i, -1); if (!next) return REFUSED.up; break;
        case "down": next = moveBlock(rows, i, 1); if (!next) return REFUSED.down; break;
        case "indent": next = indentBlock(rows, i, max); if (!next) return REFUSED.indent; break;
        case "outdent": next = outdentBlock(rows, i); if (!next) return REFUSED.outdent; break;
        case "remove": next = removeBlock(rows, i)?.rows; break;
        case "dup":
          if (rows.length + blockEnd(rows, i) - i > maxItems) return limitNotice(maxItems);
          next = duplicateBlock(rows, i);
          break;
        case "moveto": {
          const v = document.getElementById("moveto-target")?.value ?? "";
          const parent = v === "top" ? null : /^\d+$/.test(v) ? Number(v) : -1;
          if (parent !== null && !(parent >= 0 && parent < rows.length && rows[parent].depth === 0)) return REFUSED.chooseTarget;
          if (parent !== null && parent >= i && parent < blockEnd(rows, i)) return REFUSED.underSelf;
          next = moveToParent(rows, i, parent, max);
          if (!next) return REFUSED.tooDeep;
          break;
        }
        default: return "";
      }
      if (!next) return "";
      const before = rows;
      place(tree, next);
      commit(before);
      return "";
    }

    // applyOp runs one structural edit on an item, shows the refusal or the
    // done message, and returns the refusal text ("" when it was applied).
    function applyOp(name, li) {
      const refused = runOp(name, items(tree).indexOf(li));
      showNotice(refused);
      if (!refused) announce(DONE[name]);
      return refused;
    }

    // addRows puts new rows into the tree as one undo step: after the block of
    // the item at index after, or at the end. Each row is a copy of the page's
    // blank item with the fields of its spec ({type, label, entity, term, url, name}) filled in.
    // It returns the new rows, or none when the menu is full.
    function addRows(specs, after = -1) {
      const maxItems = Number(form.dataset.maxItems) || Infinity;
      const rows = model(tree);
      if (rows.length + specs.length > maxItems) {
        showNotice(limitNotice(maxItems));
        return [];
      }
      const at = after >= 0 && after < rows.length ? blockEnd(rows, after) : rows.length;
      const depth = at > 0 && after >= 0 ? rows[after].depth : 0;
      const fresh = specs.map((spec) => newRow(spec, depth));
      if (fresh.some((r) => !r.li)) return [];
      const next = [...rows.slice(0, at), ...fresh, ...rows.slice(at)];
      tree.hidden = false;
      tree.parentElement.querySelector(":scope > .hm-empty")?.setAttribute("hidden", "");
      place(tree, next);
      commit(rows);
      showNotice("");
      announce(fresh.length === 1 ? DONE.add : `${fresh.length} items added. They are not saved yet.`);
      return fresh.map((r) => r.li);
    }
    document.addEventListener("hm:op", (evt) => opMany(evt.detail.op, evt.detail.items));

    // opMany applies one edit to several items as a single undo step.
    function opMany(name, lis) {
      const all = items(tree);
      const order = lis.filter((li) => all.includes(li)).sort((a, b) => all.indexOf(a) - all.indexOf(b));
      if (name !== "indent" && name !== "up") order.reverse(); // the rest go bottom first, so earlier edits do not move later items
      batch = {};
      let refused = "";
      try {
        order.forEach((li) => {
          if (items(tree).includes(li)) refused = runOp(name, items(tree).indexOf(li)) || refused;
        });
      } finally {
        const done = batch;
        batch = null;
        if (done.before) commit(done.before);
      }
      showNotice(refused);
      if (!refused) announce(DONE[name]);
      return refused;
    }
    form.addEventListener("click", (evt) => {
      const btn = evt.target.closest('button[name="_op"]');
      const m = btn && tree.contains(btn) ? OP_RE.exec(btn.value) : null;
      if (!m) return;
      const li = btn.closest("[data-hm-item]");
      const name = m[1];
      const at = items(tree).indexOf(li);
      btn.closest("[popover]")?.hidePopover?.();
      evt.preventDefault();
      applyOp(name, li);
      const now = items(tree);
      const target = name === "remove" ? (now[at] ?? now[at - 1]) : li;
      // The moved item keeps focus: on the same button, or on its menu when that button lives in the menu.
      const focus = name === "up" || name === "down" ? target?.querySelector(`.hm-menutree__ops > [data-hm-op="${name}"]`) : null;
      (focus ?? target?.querySelector('[popovertarget][aria-haspopup="menu"]'))?.focus();
    });

    // A closed <details> hides its invalid field and no browser opens it, so
    // validation would fail silently. Open them before the browser validates.
    form.addEventListener("click", (evt) => {
      const b = evt.target.closest("button[type=submit]");
      if (!b || b.formNoValidate) return;
      tree.querySelectorAll("details:not([open])").forEach((d) => {
        if (d.querySelector(":invalid")) d.open = true;
      });
    }, true);

    const edit = (evt) => {
      if (evt.target.matches("[data-hm-type]")) evt.target.closest("[data-hm-item]").dataset.type = evt.target.value;
      if (evt.target.matches('[name$=".label"]')) syncMoveTo(tree);
      edited = true;
      refreshIssues();
      refreshBar();
    };
    // Capture: the inspector reads the row on the same event, after the stale message is gone.
    tree.addEventListener("input", (evt) => clearServerError(evt.target), true);
    tree.addEventListener("change", (evt) => clearServerError(evt.target), true);
    tree.addEventListener("hm:picked", (evt) => clearServerError(evt.target), true);
    tree.addEventListener("input", edit);
    tree.addEventListener("change", edit);
    tree.addEventListener("hm:picked", edit);

    ui.undo?.addEventListener("click", undo);
    ui.redo?.addEventListener("click", redo);
    ui.discard?.addEventListener("click", () => {
      leaving = true;
      window.location.assign(window.location.pathname + window.location.search);
    });
    ui.issues?.addEventListener("click", (evt) => {
      const chip = evt.target.closest("[data-hm-issue]");
      if (!chip) return;
      const kind = chip.dataset.hmIssue;
      const list = items(tree).filter((li) => problemOf(li) === kind);
      if (!list.length) return;
      issueAt[kind] = (issueAt[kind] + 1) % list.length;
      const li = list[issueAt[kind]];
      const inspector = document.querySelector("[data-hm-inspector] [data-hm-ins]");
      let target;
      if (inspector) {
        // The inspector edits the row, so select it there and take the keyboard to the field to fix.
        document.dispatchEvent(new CustomEvent("hm:select", { detail: { items: [li] } }));
        target = kind === "err" ? document.querySelector('[data-hm-inspector] [data-hm-ins="label"]') : document.querySelector(`[data-hm-inspector] [data-hm-ins-target="${li.dataset.type}"] input:not([type=hidden])`);
      } else {
        const details = li.querySelector(".hm-menutree__edit");
        if (details) details.open = true;
        target = kind === "err" ? field(li, "label") : li.querySelector(`.hm-menutree__t-${li.dataset.type} input:not([type=hidden])`);
      }
      li.scrollIntoView({ block: "center" });
      target?.focus();
    });

    // The server's error summary links to fields the inspector hides: select the row and
    // take the keyboard to the field in the inspector. The rows are matched by the ids
    // the page came with, which a later reorder renumbers.
    const summaryRows = new Map();
    document.querySelectorAll('.hm-formerrors a[href^="#i-"]').forEach((a) => {
      const id = a.getAttribute("href").slice(1);
      const m = /^i-(\d+)-(\w+)$/.exec(id);
      const li = m && items(tree)[Number(m[1])];
      if (li) summaryRows.set(a, { li, suffix: m[2] });
    });
    const select = (li) => document.dispatchEvent(new CustomEvent("hm:select", { detail: { items: [li] } }));
    document.addEventListener("click", (evt) => {
      const hit = summaryRows.get(evt.target.closest?.('.hm-formerrors a[href^="#i-"]'));
      if (!hit || !document.querySelector("[data-hm-inspector] [data-hm-ins]")) return;
      evt.preventDefault();
      select(hit.li);
      hit.li.scrollIntoView({ block: "center" });
      inspectorField(hit.suffix)?.focus();
    });
    // After a failed save the first item with a message is the one the inspector shows.
    const firstBad = items(tree).find((li) => li.querySelector(".hm-field__error"));
    if (firstBad) setTimeout(() => select(firstBad), 0);

    // Undo and Redo on the keyboard, except where a text field has its own.
    document.addEventListener("keydown", (evt) => {
      if (!(evt.metaKey || evt.ctrlKey) || evt.altKey) return;
      const key = evt.key.toLowerCase();
      if (key !== "z" && key !== "y") return;
      if (evt.target.closest?.("input, textarea, select, [contenteditable]")) return;
      evt.preventDefault();
      if (key === "y" || evt.shiftKey) redo();
      else undo();
    });

    // Add posts to the server, which refuses at the limit in the same words; say it here first.
    form.addEventListener("submit", (evt) => {
      const max = Number(form.dataset.maxItems) || Infinity;
      if (evt.submitter?.value === "add" && items(tree).length >= max) {
        evt.preventDefault();
        showNotice(limitNotice(max));
        return;
      }
      leaving = true;
    });
    window.addEventListener("beforeunload", (evt) => {
      if (!leaving && form.isConnected && dirty()) {
        evt.preventDefault();
        evt.returnValue = "";
      }
    });

    const target = document.getElementById("moveto-target");
    if (target) {
      const all = items(tree);
      [...target.options].forEach((o) => { if (/^\d+$/.test(o.value)) o.hmLi = all[Number(o.value)]; });
    }

    keys = initKeys(tree, {
      form,
      items: () => items(tree),
      apply: applyOp,
      many: opMany,
    });

    initAdd(form, {
      addRows,
      items: () => items(tree),
      focusRow: (li) => document.dispatchEvent(new CustomEvent("hm:select", { detail: { items: [li] } })),
    });

    initDrag(tree, {
      rows: () => model(tree),
      place: (rows) => place(tree, rows),
      commit,
      maxDepth: () => maxDepthOf(tree),
      done: (message) => {
        showNotice("");
        announce(message);
      },
    });

    refresh();
  });
}

document.addEventListener("DOMContentLoaded", () => init(document));
document.addEventListener("htmx:afterSettle", (evt) => init(evt.target));
export { renumber, init };
