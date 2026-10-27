// The Ctrl/Cmd+K command palette (a lazy chunk): one search over Recent, Patterns, Elements, Blocks
// and a short list of Commands, as a centred dialog (combobox + listbox, aria-activedescendant).
// It works with the left pane hidden and on a phone. Run: node --test scripts/admin-qa/unit/builder-commandk.test.mjs
import { h } from "@hm/builder/dom";
import { filterItems, rank } from "@hm/builder/search";
import * as recent from "@hm/builder/recent";

const MAC = /Mac|iPhone|iPad/.test(globalThis.navigator?.platform ?? "");

/** "Ctrl Shift Z" as a Mac reads it: "⌘ ⇧ Z". */
export const keyHint = (keys, mac) => (mac ? keys.replace(/\bCtrl\b/g, "⌘").replace(/\bShift\b/g, "⇧").replace(/\bAlt\b/g, "⌥") : keys);

/** The commands, in the order they are listed; Site styles only for owners, Preview only when the page has a public address. */
export function commandsFor({ canStyle = false, preview = false, mac = MAC } = {}) {
  return [
    { id: "undo", name: "Undo", key: "Ctrl Z" },
    { id: "redo", name: "Redo", key: "Ctrl Shift Z" },
    { id: "device-base", name: "Desktop", hint: "Edit and preview at desktop width" },
    { id: "device-md", name: "Tablet", hint: "Edit and preview at tablet width" },
    { id: "device-sm", name: "Phone", hint: "Edit and preview at phone width" },
    { id: "togglenav", name: "Show or hide Navigator", key: "Ctrl Alt [" },
    { id: "toggleinspector", name: "Show or hide Inspector", key: "Ctrl Alt ]" },
    { id: "history", name: "History", key: "Ctrl H" },
    { id: "sheet", name: "Keyboard shortcuts", key: "?" },
    { id: "mkblock", name: "Save as block…" },
    { id: "savetpl", name: "Save as template…" },
    ...(canStyle ? [{ id: "sitestyles", name: "Site styles", hint: "Colours and corners for the whole site" }] : []),
    ...(preview ? [{ id: "preview", name: "Preview", hint: "Open the page in a new tab" }] : []),
  ].map((c) => ({ ...c, key: undefined, shortcut: c.key && keyHint(c.key, mac), hint: c.hint ?? "", kind: "command", group: "Commands", cmd: c.id }));
}

const SECTION = { Patterns: "Patterns", Blocks: "Blocks" };
const ORDER = ["Recent", "Patterns", "Elements", "Blocks", "Commands"];
export const SHORT = 8;

/**
 * [{name, rows}] sections for a query. items are palette items, commands from commandsFor, recents the stored keys.
 * With no query each section shows its first few; with one, everything that matches, best first.
 */
export function sectionsFor({ items, commands, recents = [], query = "" }) {
  const q = query.trim();
  const all = [...items, ...commands];
  const hit = rank(filterItems(all, q), q);
  const sectionOf = (i) => (i.group === "Commands" ? "Commands" : (SECTION[i.group] ?? "Elements"));
  const by = (name) => hit.filter((i) => sectionOf(i) === name);
  const last = q ? [] : recents.map((k) => all.find((i) => i.key === k)).filter(Boolean);
  // Without a query the sections keep their order; with one, the section of the best match comes first, so Enter takes it.
  const names = q ? [...new Set(hit.map(sectionOf))] : ORDER.slice(1);
  const out = [{ name: "Recent", rows: last }, ...names.map((name) => ({ name, rows: by(name) }))];
  return out.map((s) => ({ ...s, rows: q ? s.rows : s.rows.slice(0, SHORT) })).filter((s) => s.rows.length);
}

/**
 * createCommandK({getItems, canStyle, preview, user, storage, onInsert(item, {keepOpen}), onRun(id), announce}) -> {open}
 * Enter runs the active row and closes; Shift+Enter on an element or pattern inserts it and leaves the palette open.
 */
export function createCommandK({ getItems, canStyle = false, preview = false, user = "", storage, onInsert, onRun, announce = () => {} }) {
  const commands = commandsFor({ canStyle, preview });
  let recents = [];
  let rows = []; // the flat list of what is shown, in order
  let active = 0;
  const input = h("input", { type: "text", class: "hm-field__control", id: "hm-bs-ck-q", role: "combobox", "aria-expanded": "true", "aria-controls": "hm-bs-ck-list", "aria-autocomplete": "list", "aria-label": "Search elements, patterns and commands", placeholder: "Search elements, patterns and commands…", autocomplete: "off" });
  const list = h("ul", { class: "hm-bs-cmdk__list", id: "hm-bs-ck-list", role: "listbox", "aria-label": "Results" });
  const dlg = h(
    "dialog",
    { class: "hm-dialog hm-bs-cmdk", id: "hm-bs-cmdk", "aria-labelledby": "hm-bs-ck-t" },
    h("div", { class: "hm-dialog__panel" }, h("h2", { class: "hm-visually-hidden", id: "hm-bs-ck-t", text: "Command palette" }), input, list, h("p", { class: "hm-muted hm-bs-cmdk__tip", text: "Enter runs. Shift+Enter adds and keeps this open. Esc closes." })),
  );

  function paint(say = false) {
    const sections = sectionsFor({ items: getItems(), commands, recents, query: input.value });
    rows = sections.flatMap((s) => s.rows.map((r) => ({ ...r, section: s.name })));
    active = Math.min(active, Math.max(0, rows.length - 1));
    let n = 0;
    list.replaceChildren(
      ...sections.flatMap((s) => [
        h("li", { role: "presentation", class: "hm-bs-cmdk__g", text: s.name }),
        ...s.rows.map((r) => {
          const i = n++;
          return h("li", { role: "option", id: `hm-bs-ck-${i}`, class: "hm-bs-cmdk__o", "aria-selected": String(i === active), "data-i": String(i), onclick: () => run(i, false) }, h("span", { text: r.name }), (r.shortcut || r.hint) && h("span", { class: "hm-muted hm-bs-cmdk__h", text: r.shortcut || r.hint }));
        }),
      ]),
    );
    if (!rows.length) list.append(h("li", { role: "presentation", class: "hm-muted", text: "Nothing matches." }));
    mark();
    if (say) announce(`${rows.length} ${rows.length === 1 ? "result" : "results"}`);
  }

  function mark() {
    const opts = Array.from(list.querySelectorAll("[role=option]"));
    opts.forEach((o, i) => o.setAttribute("aria-selected", String(i === active)));
    if (rows.length) input.setAttribute("aria-activedescendant", `hm-bs-ck-${active}`);
    else input.removeAttribute("aria-activedescendant");
    opts[active]?.scrollIntoView?.({ block: "nearest" });
  }

  function run(i, keepOpen) {
    const row = rows[i];
    if (!row) return;
    if (row.kind === "command") {
      dlg.close();
      return onRun(row.cmd);
    }
    recents = recent.use(user, row.key, storage);
    if (!keepOpen) dlg.close();
    onInsert(row, { keepOpen });
    if (keepOpen) paint();
  }

  input.addEventListener("input", () => ((active = 0), paint(true)));
  input.addEventListener("keydown", (e) => {
    const n = rows.length;
    if (e.key === "ArrowDown" || e.key === "ArrowUp") active = n ? (active + (e.key === "ArrowDown" ? 1 : -1) + n) % n : 0;
    else if (e.key === "Enter") run(active, !!e.shiftKey && rows[active]?.kind !== "command");
    else return;
    e.preventDefault();
    if (e.key !== "Enter") mark();
  });
  document.body.append(dlg);

  return {
    open() {
      if (dlg.open) return;
      recents = recent.load(user, storage);
      input.value = "";
      active = 0;
      paint();
      dlg.showModal();
      input.focus();
    },
    el: dlg,
  };
}
