// The Add tab body (a lazy chunk, mounted the first time the tab opens): a search field and the
// palette items as two-column tiles. The panel is inline (a modal would block dragging onto the
// canvas); tiles are buttons, so Enter/Space insert, and a press-and-move starts a drag (drag.js).
// Recent (the last inserts, per user) and the Patterns thumbnails come from the lazy recent.js and patterns.js.
import { h } from "@hm/builder/dom";
import { GROUP_ORDER, itemsOf, makeNode } from "@hm/builder/palette";
import { filterItems, rank } from "@hm/builder/search";
import * as recent from "@hm/builder/recent";
import { patternRow, shapeOf, templateShapes, thumb } from "@hm/builder/patterns";
import { actButton } from "@hm/builder/dom";

/**
 * mountPalette(root, {kinds, sections, onInsert(item, {keepOpen, keyboard}), announce, slot, extra, user, urls, storage}) -> {open, reset, pick, el, repaint, used}
 * open() focuses the search field; pick/el feed dragSource (drag.js). extra() lists the
 * template and block items (kind "template"|"block"), read on every paint so new ones show.
 * A template needs a fetch, so only blocks drag; both insert on click. Enter inserts the first
 * tile shown; Shift+Enter does too and leaves the search focused for the next one.
 */
export function mountPalette(root, { kinds, sections, onInsert, announce = () => {}, slot = false, extra = () => [], user = "", urls = null, storage }) {
  const base = itemsOf(kinds, sections, { slot });
  const all = () => [...base, ...extra()];
  let allPatterns = false; // "Show all patterns": with no search the Patterns group is one short row
  const shapes = new Map(); // template key -> shape, filled as the tiles are seen
  const shapeFor = (i) => (i.kind === "template" ? (shapes.get(i.key) ?? shapeOf("")) : shapeOf(i.type));
  const search = h("input", { type: "search", class: "hm-field__control", id: "hm-bs-pal-q", placeholder: "Search…", autocomplete: "off", "aria-controls": "hm-bs-pal-list" });
  const list = h("div", { id: "hm-bs-pal-list" });
  // One button for the life of the tab: a repaint moves it into the new list but never makes another, so it keeps its aria-expanded and gets focus back.
  const toggle = actButton("", { type: "button", "aria-expanded": "false", onclick: () => ((allPatterns = !allPatterns), paint()) });
  const toggleLabel = (text) => ((toggle.querySelector?.("span") ?? toggle).textContent = text);
  const byKey = (k) => all().find((i) => i.key === k);
  const used = (key) => {
    if (!key) return;
    recent.use(user, key, storage);
    paint(); // the Recent row
  };
  const insert = (item, o) => {
    if (!item) return;
    onInsert(item, o);
    used(item.key);
    // The insert asks the Navigator to focus the new row on the next frame; a hidden tab cannot take focus, so a keyboard insert shows it.
    if (o.keyboard && !o.keepOpen) root.closest("[data-hm-builder]")?.querySelector('[data-hm-bs-ltab="nav"]')?.click();
  };
  root.append(h("div", { class: "hm-field hm-bs-field" }, h("label", { class: "hm-field__label", for: "hm-bs-pal-q", text: "Search elements and patterns" }), search), list);

  const tile = (i, group) => {
    const pat = group === "Patterns";
    return h("li", {}, h("button", { type: "button", class: `hm-bs-pal__item${pat ? " hm-bs-pal__item--pat" : ""}`, "data-pal": i.key, title: `${i.name}${i.hint ? `: ${i.hint}` : ""}. Click or Enter to add, or drag onto the page.` }, pat && thumb(shapeFor(i)), h("span", { class: "hm-bs-pal__n", text: i.name })));
  };

  function paint(say = false) {
    const shown = rank(filterItems(all(), search.value), search.value);
    const order = (g) => (GROUP_ORDER.includes(g) ? GROUP_ORDER.indexOf(g) : GROUP_ORDER.length);
    const seen = [...new Set(shown.map((i) => i.group))];
    const groups = search.value.trim() ? seen : seen.sort((a, b) => order(a) - order(b)); // a search keeps the group of the best match first, so Enter inserts that
    const rows = new Map(groups.map((g) => [g, shown.filter((i) => i.group === g)]));
    if (!search.value.trim()) {
      const last = recent.load(user, storage).map(byKey).filter(Boolean);
      if (last.length) (groups.unshift("Recent"), rows.set("Recent", last));
    }
    const searching = !!search.value.trim();
    const hadFocus = document.activeElement === toggle;
    toggleLabel(allPatterns ? "Show fewer patterns" : `Show all patterns (${patternRow(rows.get("Patterns") ?? [], true).total})`);
    toggle.setAttribute("aria-expanded", String(allPatterns));
    const pats = patternRow(rows.get("Patterns") ?? [], allPatterns || searching);
    list.replaceChildren(
      ...groups.map((g) => {
        const tiles = g === "Patterns" && !searching ? pats.shown : rows.get(g);
        const more = g === "Patterns" && !searching && pats.more && toggle;
        return h("section", { class: "hm-bs-pal__g", "aria-label": g }, h("h3", { class: "hm-bs-pal__t", text: g }), h("ul", { class: "hm-bs-pal__list" }, ...tiles.map((i) => tile(i, g))), more);
      }),
    );
    if (hadFocus) toggle.focus(); // replaceChildren detached the focused button
    if (!shown.length) list.append(h("p", { class: "hm-muted", text: "Nothing matches." }));
    if (say) announce(`${shown.length} ${shown.length === 1 ? "match" : "matches"}`);
    const want = urls?.template ? (searching ? shown : pats.shown).filter((i) => i.kind === "template") : [];
    if (want.length) templateShapes(want, urls, shapes).then((n) => n && paint());
  }

  list.addEventListener("click", (e) => {
    const b = e.target.closest?.("[data-pal]");
    if (b) insert(byKey(b.dataset.pal), { keepOpen: false, keyboard: e.detail === 0 }); // detail 0: Enter or Space on a tile, not a pointer
  });
  const buttons = () => Array.from(list.querySelectorAll("[data-pal]"));
  search.addEventListener("input", () => paint(true));
  search.addEventListener("keydown", (e) => {
    if (e.key === "ArrowDown") buttons()[0]?.focus();
    else if (e.key === "Enter") {
      const first = buttons()[0] && byKey(buttons()[0].dataset.pal);
      if (first) insert(first, { keepOpen: e.shiftKey, keyboard: true });
    } else return;
    e.preventDefault();
  });
  list.addEventListener("keydown", (e) => {
    const bs = buttons();
    const i = bs.indexOf(document.activeElement);
    const to = { ArrowDown: i + 1, ArrowUp: i - 1, Home: 0, End: bs.length - 1 }[e.key];
    if (to === undefined || i < 0) return;
    e.preventDefault();
    if (to < 0) search.focus();
    else bs[Math.min(to, bs.length - 1)].focus();
  });
  paint();

  return {
    open() {
      search.focus();
      search.select();
    },
    /** Shows every pattern and focuses the first (the "Insert a ready-made section" row of the "+" picker). */
    showPatterns() {
      allPatterns = true;
      search.value = "";
      paint();
      const first = list.querySelector(".hm-bs-pal__item--pat") ?? search;
      first.scrollIntoView?.({ block: "nearest" });
      first.focus();
    },
    /** Clears the filter (after an insert) so the full list is back for the next one. */
    reset() {
      search.value = "";
      paint(); // also the Recent row, which Ctrl+K inserts change
    },
    /** The press-and-move source: the item under the pointer, as a spec factory for dragSource. */
    pick: (e) => {
      const b = e.target.closest?.("[data-pal]");
      const item = b && byKey(b.dataset.pal);
      return item && item.kind !== "template" ? () => ({ node: makeNode(item, kinds), key: item.key }) : null;
    },
    repaint: () => paint(),
    used,
    el: list,
  };
}
