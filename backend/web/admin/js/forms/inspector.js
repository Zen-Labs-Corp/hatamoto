// The designer's inspector. Nothing selected: the Form check and the shortcut list. A field
// selected: its type (switchable within its family) and the section bodies, a lazy chunk loaded
// with a literal import() the first time a field is selected. No draw while a text control in the
// panel has focus (own typing never rebuilds the input being typed in); other inputs are synced.
import { h } from "@hm/builder/dom";
import { lazy } from "@hm/builder/lazy";
import { TYPES, compatible } from "@hm/forms/types";
import { formCheck } from "@hm/forms/check";
import { KEYS, keyCap } from "@hm/forms/shortcuts";
import { icon } from "@hm/forms/palette";

const sectionsChunk = lazy(() => import("@hm/forms/inspsections"));

export function mountInspector(root, { getDoc, getSelected, commit, getErrors = () => ({}), announce = () => {} }) {
  let gen = 0; // a later draw makes an earlier, slower section load drop its result
  let shown = null;

  const isTyping = () => {
    const a = document.activeElement;
    return !!a && root.contains(a) && (a.tagName === "TEXTAREA" || (a.tagName === "INPUT" && a.type !== "checkbox" && a.type !== "radio"));
  };

  function idle(doc) {
    const check = formCheck(doc);
    const item = (c) =>
      h("li", { class: "hm-fd-check", "data-ok": String(c.ok) }, h("span", { class: "hm-fd-check__ico", "aria-hidden": "true" }, icon(c.ok ? "check" : "alert-circle")), h("span", { class: "hm-visually-hidden", text: c.ok ? "Done: " : "To do: " }), h("span", { text: c.ok ? c.label : c.hint }));
    const row = ([combos, what]) => h("tr", {}, h("td", {}, ...combos.map((c) => h("kbd", { text: keyCap(c) }))), h("td", { text: what }));
    return [
      h("div", { class: "hm-fd-empty-note" }, h("span", { class: "hm-fd-empty-note__ico", "aria-hidden": "true" }, icon("file-text")), h("b", { text: "Nothing selected" }), h("p", { class: "hm-muted", text: "Click a field on the form to edit it, or drag a new one in from Add a field." })),
      h("section", { class: "hm-fd-section", "aria-label": "Form check" }, h("h3", { class: "hm-fd-group", text: "Form check" }), h("ul", { class: "hm-fd-checks" }, ...check.map(item))),
      h("section", { class: "hm-fd-section", "aria-label": "Shortcuts" }, h("h3", { class: "hm-fd-group", text: "Shortcuts" }), h("table", { class: "hm-fd-keys" }, h("tbody", {}, ...KEYS.map(row)))),
    ];
  }

  function header(f) {
    const types = compatible(f.type);
    const sw = h("select", { class: "hm-field__control", "aria-label": "Field type", disabled: types.length < 2 }, ...types.map((t) => h("option", { value: t, text: TYPES[t].label })));
    sw.value = f.type;
    sw.addEventListener("change", () => commit([{ op: "replaceField", id: f.id, field: { ...f, type: sw.value } }], { label: "Change type" }));
    return h("div", { class: "hm-fd-type" }, h("span", { class: "hm-fd-tile__ico", "aria-hidden": "true" }, icon(TYPES[f.type]?.icon ?? "pen")), sw);
  }

  function draw() {
    const doc = getDoc();
    const f = doc.fields.find((x) => x.id === getSelected());
    if (f && f.id === shown && isTyping()) {
      for (const el of root.querySelectorAll("[data-fd-key]")) if (el !== document.activeElement) el.value = f[el.dataset.fdKey] ?? "";
      return;
    }
    const mine = ++gen;
    // A rebuild of the same field keeps keyboard focus on the same control (by its position).
    const ctl = () => [...root.querySelectorAll("input, select, textarea, button")];
    const at = f && f.id === shown && root.contains(document.activeElement) ? ctl().indexOf(document.activeElement) : -1;
    shown = f?.id ?? null;
    if (!f) return root.replaceChildren(...idle(doc));
    const slot = h("div", {});
    const busy = setTimeout(() => !slot.firstChild && slot.append(h("p", { class: "hm-muted", role: "status", text: "Loading…" })), 150);
    root.replaceChildren(header(f), slot);
    sectionsChunk()
      .then((m) => {
        clearTimeout(busy);
        if (mine === gen) {
          slot.replaceChildren(...m.createSections({ field: f, fields: doc.fields, commit, errors: getErrors() ?? {}, announce }));
          if (at >= 0) ctl()[at]?.focus();
        }
      })
      .catch(() => {
        clearTimeout(busy);
        if (mine === gen) slot.replaceChildren(h("p", { class: "hm-muted", role: "alert", text: "The settings could not be loaded. Reload the page to try again." }));
      });
  }

  return {
    draw,
    focus: () => root.querySelector("input, select, textarea")?.focus(),
  };
}
