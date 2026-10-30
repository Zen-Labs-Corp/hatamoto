// The navigator: the page as an ARIA tree (role=tree > treeitem, aria-level/-expanded/
// -selected/-setsize/-posinset) with one roving tab stop. Rows are a flat list, so the
// pure helpers below (rowsOf, step, moveOp) work on plain data and are node-tested.
// Every change goes through the screen's callbacks as a store command.
import { h } from "@hm/builder/dom";
import { locate, siblings, find } from "@hm/builder/store";
import { BP_NAME, BPS, hiddenAt } from "@hm/builder/bp";
import { isCollapsed } from "@hm/builder/selinfo";
import { refLabel } from "@hm/builder/reuseops";
import { makeKindNames, SEP } from "@hm/builder/kindnames";

const plain = makeKindNames();
// The names screen.js registers once, so a bare label(node) (the Inspector header) reads exactly like the tree and the announcements.
const shared = { kindName: plain, sections: [] };
export function useLabels({ kindName = plain, sections = [] } = {}) {
  Object.assign(shared, { kindName, sections });
}

/** A theme section's type as a plain name: "card_columns" is "Card columns". */
const typeName = (type) => (type ? type.charAt(0).toUpperCase() + type.slice(1).replace(/[_-]+/g, " ").trim() : "Section");

/** The row text: the name given to it (navlabel), else the kind's plain name (kindName), plus the first words of its text when it has some ("Heading · Build pages"). A ref reads "Block: <name>" (blocks: [{id,name}]); a component its section's title (sections: [{type,title}]). */
export function label(n, blocks, kindName = shared.kindName, sections = shared.sections) {
  const own = n.p?.navlabel; // the name the person gave it (any kind)
  if (typeof own === "string" && own.trim()) return own.trim();
  if (n.t === "ref") return refLabel(n, blocks);
  const p = n.p ?? {};
  if (n.t === "component") return sections.find((x) => x.type === p.type)?.title || typeName(String(p.type ?? ""));
  if (n.t === "container") return p.layout === "grid" ? "Grid" : p.tag === "section" ? "Section" : "Container";
  const named = [p.text, p.title, p.label, p.alt, p.caption].find((v) => typeof v === "string" && v.trim());
  const text = named ? named.trim().slice(0, 40) : "";
  const base = text && text.toLowerCase() !== kindName(n.t).toLowerCase() ? `${kindName(n.t)}${SEP}${text}` : kindName(n.t); // "Heading", not "Heading · Heading"
  return n.b && Object.keys(n.b).length ? `${base} (bound)` : base;
}

/** The row's accessible text: its label (a collapsed section's: sectionName), then the preset note when it has one. */
export function rowName(node, note = "", blocks, kindName, sections) {
  const text = isCollapsed(node) ? sectionName(node, blocks, kindName, sections) : label(node, blocks, kindName, sections);
  return note ? `${text}, ${note}` : text;
}

/** A collapsed section's row name: the name given to the container, else the component's (its navlabel, else the section title, else its type name). */
export function sectionName(container, blocks, kindName = shared.kindName, sections = shared.sections) {
  const own = container.p?.navlabel;
  if (typeof own === "string" && own.trim()) return own.trim();
  return label(container.c[0], blocks, kindName, sections);
}

/**
 * The visible rows in order: {id, node, level, pos, size, parentId, kids, expanded, match, section, collapsed, index}.
 * A container(tag=section) wrapping one component is ONE row (id = the container, collapsed = the component); the component's
 * children are the rows below it at level 2 with parentId = the container. section: a top-level row; index: its 1-based place
 * among the top-level rows, whatever the filter hides (null below the top).
 * A non-empty filter keeps the rows whose name (nameOf) has every word, plus their ancestors, all expanded (collapsed is ignored);
 * a collapsed row matches on the container's name or the component's.
 */
export function rowsOf(doc, collapsed = new Set(), filter = "", nameOf = (n) => label(n)) {
  const words = filter.toLowerCase().split(/\s+/).filter(Boolean);
  const named = (n, name) => words.every((w) => name.toLowerCase().includes(w));
  const hit = (n) => words.length > 0 && (named(n, nameOf(n)) || (isCollapsed(n) && named(n, nameOf(n.c[0]))));
  const kidsOf = (n) => (isCollapsed(n) ? (n.c[0].c ?? []) : (n.c ?? []));
  const keep = new Set();
  const mark = (ns, trail) =>
    ns.forEach((n) => {
      const t = [...trail, n.id];
      if (hit(n)) t.forEach((i) => keep.add(i));
      mark(kidsOf(n), t);
    });
  if (words.length) mark(doc.nodes, []);
  const shown = (ns) => (words.length ? ns.filter((n) => keep.has(n.id)) : ns);
  const out = [];
  const walk = (all, level, parentId) => {
    const ns = shown(all);
    ns.forEach((n) => {
      const sub = isCollapsed(n) ? n.c[0] : null;
      const below = sub ? (sub.c ?? []) : (n.c ?? []);
      const kids = shown(below).length;
      const open = kids > 0 && (words.length > 0 || !collapsed.has(n.id));
      const top = level === 1;
      out.push({ id: n.id, node: n, level, pos: ns.indexOf(n) + 1, size: ns.length, parentId, kids, expanded: open, match: hit(n), section: top, collapsed: sub, index: top ? doc.nodes.indexOf(n) + 1 : null });
      if (open) walk(below, level + 1, n.id);
    });
  };
  walk(doc.nodes, 1, "");
  return out;
}

/** Every id that has children: the collapsed set after "Collapse all". */
export function collapseAll(doc) {
  const out = new Set();
  const walk = (ns) =>
    ns.forEach((n) => {
      const below = isCollapsed(n) ? (n.c[0].c ?? []) : (n.c ?? []);
      if (below.length) (out.add(n.id), walk(below));
    });
  walk(doc.nodes);
  return out;
}

/** Whether every parent is collapsed (and there is one): the only time the toolbar button offers "Expand all". */
export function allCollapsed(doc, collapsed) {
  const ids = collapseAll(doc);
  return ids.size > 0 && [...ids].every((id) => collapsed.has(id));
}

const pluralBlocks = (n) => `${n} block${n === 1 ? "" : "s"}`;

/** A section row's meta line: "N blocks · <background>", or "Hidden on <Devices>" in place of the background ("Hidden" when every device). The count is omitted when there are no blocks, the background when none is set. "" for other rows. */
export function metaOf(row) {
  if (!row.section) return "";
  const node = row.node;
  const count = (row.collapsed ? (row.collapsed.c ?? []) : (node.c ?? [])).length;
  const off = BPS.filter((bp) => hiddenAt(node, bp));
  const bg = node.s?.base?.bg;
  const tail = off.length === BPS.length ? "Hidden" : off.length ? `Hidden on ${off.map((bp) => BP_NAME[bp]).join(", ")}` : typeof bg === "string" && bg.trim() ? bg.trim() : "";
  return [count ? pluralBlocks(count) : "", tail].filter(Boolean).join(" · ");
}

/** What a section row's thumbnail shows: the container's base background (a hex or a token name as written; null when none) and its text alignment. */
export function thumbOf(container) {
  const base = container.s?.base ?? {};
  const bg = typeof base.bg === "string" && base.bg.trim() ? base.bg.trim() : null;
  return { bg, align: ["left", "center", "right"].includes(base.ta) ? base.ta : "left" };
}

/**
 * What a navigation key does on row `id`: {select}, {expand}, {collapse} or null.
 * action: prev next first last collapse expand.
 */
export function step(rows, id, action) {
  const i = rows.findIndex((r) => r.id === id);
  if (i < 0) return rows[0] ? { select: rows[0].id } : null;
  const r = rows[i];
  switch (action) {
    case "prev":
      return i > 0 ? { select: rows[i - 1].id } : null;
    case "next":
      return i < rows.length - 1 ? { select: rows[i + 1].id } : null;
    case "first":
      return { select: rows[0].id };
    case "last":
      return { select: rows[rows.length - 1].id };
    case "collapse":
      if (r.expanded) return { collapse: id };
      return r.parentId ? { select: r.parentId } : null;
    case "expand":
      if (r.kids && !r.expanded) return { expand: id };
      return r.expanded ? { select: rows[i + 1].id } : null;
    default:
      return null;
  }
}

/** The move op for Alt+arrows on id (moveup movedown outdent indent), or null when there is nowhere to go. */
export function moveOp(doc, id, action, kinds) {
  const at = locate(doc, id);
  if (!at) return null;
  const sib = siblings(doc, at.parentId);
  switch (action) {
    case "moveup":
      return at.index > 0 ? { op: "move", id, parent: at.parentId, index: at.index - 1 } : null;
    case "movedown":
      return at.index < sib.length - 1 ? { op: "move", id, parent: at.parentId, index: at.index + 1 } : null;
    case "outdent": {
      if (!at.parent) return null;
      const up = locate(doc, at.parentId);
      return { op: "move", id, parent: up.parentId, index: up.index + 1 };
    }
    case "indent": {
      const prev = sib[at.index - 1];
      return prev && (kinds ? kinds.find((k) => k.kind === prev.t)?.children === true : prev.t === "container") ? { op: "move", id, parent: prev.id, index: (prev.c ?? []).length } : null;
    }
    default:
      return null;
  }
}

/** The "Hidden" pill cloned from the server-rendered template, with its visually hidden " on <device>" suffix; plain text when the template is missing. */
export function hiddenBadge(tpl, device) {
  const suffix = h("span", { class: "hm-visually-hidden", text: ` on ${BP_NAME[device]}` });
  const pill = tpl?.content.firstElementChild?.cloneNode(true);
  if (!pill) return h("span", {}, "Hidden", suffix);
  pill.append(suffix);
  return pill;
}

/**
 * mountNavigator(root, {getDoc, getSelected, onSelect, getDevice, onToggleHide}) -> {draw, reveal, act, focus}
 * getSelection() lists every selected id (the primary one is getSelected()); onSelect(id, {shift, ctrl}) reports the modifiers of a click.
 * kindName(kind) is the plain name of a kind (kindnames.js); getSections() lists the theme sections for component rows.
 * bar holds the toolbar outside the tree: the Filter input, Collapse all and Rename (enabled with a selection); announce(text) reports the match count.
 * getBlocks() is the synced block list for a ref row's name. getNote(node) is the preset note appended to a row's text ("preset card, differs"; no nested control).
 * onMenu(id, x, y) opens the actions menu at a page point (right-click on a row); rect(id) is a row's box.
 * onAdd(button) opens the layout picker from the empty page's button.
 * onRename(id, value) saves a name typed in a row (double-click or F2; value "" removes it).
 * getDevice() is the active breakpoint: rows hidden at it carry a badge. The h key on a row calls onToggleHide(id).
 * draw() repaints on the next frame (keeping focus when it was in the tree; draw(true) moves focus to the selected row);
 * act(action) runs a navigation key and returns whether it was one.
 */
export function mountNavigator(root, { getDoc, getSelected, getSelection = () => [], onSelect, getDevice = () => "base", onRename = () => {}, onMenu = () => {}, onAdd = () => {}, onToggleHide = () => {}, getNote = () => "", getBlocks = () => [], kindName = plain, getSections = () => [], bar = null, announce = () => {} }) {
  const collapsed = new Set();
  const badgeTpl = root.closest("[data-hm-builder]")?.querySelector("template[data-hm-bs-hidden-badge]"); // the server-rendered pill
  if (!badgeTpl) console.warn("builder navigator: template[data-hm-bs-hidden-badge] not found inside [data-hm-builder]; Hidden rows lose the pill look");
  let raf = 0;
  let wantFocus = false;
  let force = false; // draw(true): take focus whatever has it now
  let renaming = null; // the row shown as an input; paint never rebuilds a live one
  let field = null;
  let filter = "";
  const nameOf = (n) => (isCollapsed(n) ? sectionName(n, getBlocks(), kindName, getSections()) : label(n, getBlocks(), kindName, getSections()));
  const rowsNow = () => rowsOf(getDoc(), collapsed, filter, nameOf);
  const filterEl = bar?.querySelector("[data-hm-bs-filter]");
  const collapseBtn = bar?.querySelector("[data-hm-bs-collapse]");
  const renameBtn = bar?.querySelector("[data-hm-bs-rename]");

  function paint() {
    raf = 0;
    if (field?.isConnected) return;
    const doc = getDoc();
    const sel = getSelected();
    const members = new Set(getSelection());
    const rows = rowsNow();
    if (renameBtn) renameBtn.disabled = !sel;
    if (collapseBtn) collapseBtn.querySelector("span").textContent = allCollapsed(doc, collapsed) ? "Expand all" : "Collapse all";
    const active = document.activeElement;
    const hadFocus = root.contains(active) || force || (wantFocus && (!active || active === document.body));
    force = false;
    wantFocus = false;
    if (!rows.length && filter) return root.replaceChildren(h("p", { class: "hm-muted", text: "No elements match." }));
    if (!rows.length) {
      const add = h("button", { type: "button", class: "hm-btn hm-btn--sm", text: "Add a section" });
      add.addEventListener("click", () => onAdd(add));
      root.replaceChildren(h("p", { class: "hm-muted", text: "Nothing here yet. Use Add element or press / to insert something." }), add);
      return;
    }
    if (renaming && !rows.some((r) => r.id === renaming)) renaming = field = null;
    const stop = rows.some((r) => r.id === sel) ? sel : rows[0].id;
    const device = getDevice();
    const items = rows.map((r) => {
      const hidden = hiddenAt(r.node, device);
      const li = h("li", {
        class: "hm-bs-row",
        role: "treeitem",
        tabindex: r.id === stop ? "0" : "-1",
        "data-id": r.id,
        "aria-level": String(r.level),
        "aria-setsize": String(r.size),
        "aria-posinset": String(r.pos),
        "aria-selected": String(r.id === sel || members.has(r.id)),
        "aria-expanded": r.kids ? String(r.expanded) : false,
        text: rowName(r.node, getNote(r.node), getBlocks(), kindName, getSections()),
      });
      if (r.kids) li.prepend(h("span", { "data-caret": "", "aria-hidden": "true" }));
      if (r.id === renaming) li.replaceChildren((field = renameField(r.node)));
      else if (hidden) li.append(" ", hiddenBadge(badgeTpl, device)); // part of the row's name, so no aria-label can disagree with the visible text
      // indent: level steps, plus the chevron's width on rows that have none
      li.style.setProperty("padding-inline-start", `calc(var(--space-2) + ${r.level - 1} * var(--space-4)${r.kids ? "" : " + 1em + var(--space-1)"})`);
      return li;
    });
    root.replaceChildren(h("ul", { class: "hm-bs-tree", role: "tree", "aria-multiselectable": "true", "aria-label": "Navigator" }, ...items));
    if (field) field.focus(), field.select();
    else if (hadFocus) focus();
  }

  /** The input that replaces a row's text: Enter saves, Esc cancels, leaving saves; the keymap ignores keys typed in it. */
  function renameField(node) {
    const input = h("input", { class: "hm-bs-rename", type: "text", maxlength: "80", "aria-label": "Name", value: node.p?.navlabel ?? "" });
    input.addEventListener("keydown", (e) => {
      if (e.key === "Enter") finish(true, true);
      else if (e.key === "Escape") finish(false, true);
    });
    input.addEventListener("blur", () => finish(true, false));
    return input;
  }

  function finish(save, refocus) {
    if (!renaming) return;
    const id = renaming;
    const value = field.value.trim();
    renaming = field = null;
    force = refocus;
    paint();
    if (save && value !== (find(getDoc(), id)?.p?.navlabel ?? "")) onRename(id, value);
  }

  function rename(id) {
    if (!id || !find(getDoc(), id)) return;
    renaming = id;
    field = null;
    paint();
    // An input that never got focus (its pane is hidden) would block every later paint: give it up.
    setTimeout(() => {
      if (field && document.activeElement !== field) cancelRename();
    }, 0);
  }

  function cancelRename() {
    renaming = field = null;
    paint();
  }

  const draw = (focus = false) => {
    if (focus === true) force = true;
    if (!raf) raf = requestAnimationFrame(paint);
  };
  function focus() {
    (root.querySelector('[tabindex="0"]') ?? null)?.focus();
  }

  /** Expands every ancestor of id so its row is visible. */
  function reveal(id) {
    let at = locate(getDoc(), id);
    while (at && at.parent) {
      collapsed.delete(at.parentId);
      at = locate(getDoc(), at.parentId);
    }
  }

  function act(action) {
    const sel = getSelected();
    const rows = rowsNow();
    if (action === "rename") return rename(sel), true;
    if (!["prev", "next", "first", "last", "collapse", "expand"].includes(action)) return false;
    const r = step(rows, sel, action);
    if (!r) return true;
    wantFocus = true;
    if (r.collapse) collapsed.add(r.collapse);
    else if (r.expand) collapsed.delete(r.expand);
    if (r.select) onSelect(r.select);
    else paint();
    return true;
  }

  function toggle(id) {
    if (!collapsed.delete(id)) collapsed.add(id);
    paint();
  }

  let said = 0;
  filterEl?.addEventListener("input", () => {
    filter = filterEl.value;
    paint();
    clearTimeout(said);
    said = setTimeout(() => filter.trim() && announce(`${rowsNow().filter((r) => r.match).length} matches`), 400);
  });
  filterEl?.addEventListener("keydown", (e) => {
    if (e.key === "Escape" && filter) (filterEl.value = filter = ""), paint(), e.stopPropagation();
  });
  collapseBtn?.addEventListener("click", () => {
    if (allCollapsed(getDoc(), collapsed)) collapsed.clear();
    else collapseAll(getDoc()).forEach((id) => collapsed.add(id));
    paint();
  });

  root.addEventListener("click", (e) => {
    const row = e.target.closest?.("[data-id]");
    if (!row) return;
    wantFocus = true;
    if (e.target.closest("[data-caret]")) return toggle(row.dataset.id);
    // pick: a plain click or tap. Modifier clicks (multi-select) and the second click of a double-click are not a pick, and neither are keys.
    onSelect(row.dataset.id, { shift: e.shiftKey, ctrl: e.ctrlKey || e.metaKey, pick: !(e.shiftKey || e.ctrlKey || e.metaKey || e.altKey) && !(e.detail > 1) });
  });
  root.addEventListener("keydown", (e) => {
    const row = e.target.closest?.("[data-id]");
    if (!row || e.target !== row || e.key !== "h" || e.ctrlKey || e.metaKey || e.altKey || e.shiftKey) return;
    e.preventDefault();
    onToggleHide(row.dataset.id);
  });
  root.addEventListener("contextmenu", (e) => {
    const row = e.target.closest?.("[data-id]");
    if (!row || e.target.closest("input")) return;
    e.preventDefault();
    onMenu(row.dataset.id, e.clientX, e.clientY);
  });
  root.addEventListener("dblclick", (e) => {
    const row = e.target.closest?.("[data-id]");
    if (row && !e.target.closest("[data-caret], input")) rename(row.dataset.id);
  });

  return { draw, reveal, act, focus, rename, rect: (id) => [...root.querySelectorAll("[data-id]")].find((r) => r.dataset.id === id)?.getBoundingClientRect() };
}
