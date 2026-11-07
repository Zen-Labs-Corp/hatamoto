// The designer's palette: a search box and the field types in groups, as buttons (click or Enter
// adds the field; a press-and-move starts a drag), two to a row.
import { h, chip } from "@hm/builder/dom";
import { dragSource } from "@hm/builder/dragsource";
import { filterItems } from "@hm/builder/search";
import { TYPES, GROUP_ORDER } from "@hm/forms/types";

const GROUP_NAMES = { fields: "Fields", choices: "Choices", layout: "Layout", advanced: "Advanced" };

/** An icon cloned from the server-rendered template[data-hm-bs-ico] (a bare placeholder when it is missing). */
export const icon = (name) => document.querySelector?.("template[data-hm-bs-ico]")?.content?.querySelector(`[data-ico="${name}"]`)?.cloneNode(true) ?? h("span", { "data-ico": name });

/** The palette items for the boot types: {key, name, group, icon, hint, fresh}, only types the designer knows, in the designer's order. */
export function paletteItems(types) {
  return types
    .filter((t) => TYPES[t.type])
    .map((t) => ({ key: t.type, name: TYPES[t.type].label, group: TYPES[t.type].group, icon: TYPES[t.type].icon, hint: TYPES[t.type].hint, fresh: !!TYPES[t.type].fresh }))
    .sort((a, b) => GROUP_ORDER.indexOf(a.group) - GROUP_ORDER.indexOf(b.group) || Object.keys(TYPES).indexOf(a.key) - Object.keys(TYPES).indexOf(b.key));
}

export function mountPalette(root, { types, onAdd, onDragStart }) {
  const items = paletteItems(types);
  const input = h("input", { type: "search", class: "hm-fd-search__input", autocomplete: "off", placeholder: "Find a field type", "aria-label": "Find a field type" });
  const search = h("label", { class: "hm-fd-search" }, h("span", { class: "hm-fd-search__icon", "aria-hidden": "true" }, icon("search")), input);
  const list = h("div", { class: "hm-fd-palette__list" });
  const tile = (it) => {
    const b = h("button", { type: "button", class: "hm-fd-tile", title: it.hint, "data-type": it.key }, icon(it.icon), h("span", { text: it.name }), it.fresh ? chip("New", { class: "hm-fd-tile__new" }) : null);
    b.addEventListener("click", () => onAdd(it.key));
    dragSource(b, { pick: () => () => ({ type: it.key }), start: (e, spec) => onDragStart?.(spec.type, e) });
    return b;
  };
  const section = (name, its) => h("section", { class: "hm-fd-section", "aria-label": name }, h("h3", { class: "hm-fd-group", text: name }), h("div", { class: "hm-fd-palette__tiles" }, ...its.map(tile)));

  function paint() {
    const shown = filterItems(items, input.value.trim());
    const groups = GROUP_ORDER.map((g) => [g, shown.filter((i) => i.group === g)]).filter(([, its]) => its.length);
    list.replaceChildren(...groups.map(([g, its]) => section(GROUP_NAMES[g], its)), ...(shown.length ? [] : [h("p", { class: "hm-muted", role: "status", text: "No field matches." })]));
  }
  input.addEventListener("input", paint);
  root.replaceChildren(search, list);
  paint();
  return { paint };
}
