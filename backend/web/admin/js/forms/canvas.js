// The designer's canvas: the form as a visitor sees it, one block per field, as a listbox
// (role=option, roving tabindex; the screen's keymap moves the selection). The label is a plain
// text input that commits on Enter or blur and reverts on Escape. DOM is built with h() only.
import { h, chip, actButton } from "@hm/builder/dom";
import { TYPES, isLayout, fileHint } from "@hm/forms/types";
import { visible } from "@hm/forms/validate";

const OP_TEXT = { filled: "is filled", empty: "is empty", is: "is", is_not: "is not", contains: "contains" };

/** "Shows when Email is filled" / `Shows when Plan is "Pro"`: the rule of a field in words, "" when it has none. */
export function ruleNote(f, fields = []) {
  const l = f.logic;
  if (!l) return "";
  const src = fields.find((x) => x.id === l.field_id);
  const name = src ? src.label || src.name || "another field" : "a removed field";
  const needs = l.op === "filled" || l.op === "empty";
  return `Shows when ${name} ${OP_TEXT[l.op] ?? l.op}${needs ? "" : ` "${l.value ?? ""}"`}`;
}

/** "Short text · required", plus the rule note when the field has one. */
export function fieldSummary(f, fields = []) {
  return [TYPES[f.type]?.label ?? f.type, f.required && !isLayout(f.type) ? "required" : "", ruleNote(f, fields)].filter(Boolean).join(" · ");
}

const WIDTH = { half: "hm-fd-field--half", third: "hm-fd-field--third" };
const ACTIONS = [
  ["move-up", "Move up"],
  ["move-down", "Move down"],
  ["duplicate", "Duplicate"],
  ["delete", "Delete"],
];

const choice = (type, label, checked) => h("label", { class: "hm-field__check" }, h("input", { class: "hm-field__box", type, disabled: true, checked, tabindex: "-1" }), h("span", { text: label }));

/** True for the shapes drawn as one box beside the field's own label: consent, and a checkbox with no options. */
const singleBox = (f) => f.type === "consent" || (f.type === "checkbox" && !(f.options ?? []).length);

/**
 * What decides a row's redraw: everything but the label and name (the label input is patched, not rebuilt:
 * a label edit renames the field too, and a rebuild on blur would swallow the click that caused the blur),
 * except where the label is the text beside the box.
 */
export function rowSig(f, fields = [], error = "") {
  const label = singleBox(f) ? f.label : undefined;
  return JSON.stringify([{ ...f, label, name: undefined }, error, fields.map((x) => (x.id === f.logic?.field_id ? x.label || x.name : 0))]);
}

function control(f) {
  switch (f.type) {
    case "heading":
      return h("div", { class: "hm-fd-field__label", role: "heading", "aria-level": "3", text: f.text || "Heading" });
    case "paragraph":
      return h("p", { class: "hm-muted", text: f.text || "Add a few lines of text." });
    case "divider":
      return h("hr", {});
    case "textarea":
      return h("div", { class: "hm-fd-field__ctl", text: f.placeholder || "" });
    case "select":
      return h("div", { class: "hm-fd-field__ctl", text: f.placeholder || "Choose…" });
    case "radio":
    case "checkbox":
      // the public widget's own markup, switched off: real radios and boxes, the default ticked
      if (singleBox(f)) return h("div", { class: "hm-fd-field__opts" }, choice("checkbox", f.label ?? "", false));
      return h("div", { class: "hm-fd-field__opts hm-field__group" }, ...(f.options ?? []).map((o) => choice(f.type, o.label, f.type === "radio" && o.value === f.default_value)));
    case "consent":
      return h("div", { class: "hm-fd-field__opts" }, choice("checkbox", f.label ?? "", false));
    case "file":
      // the public tile, switched off: nothing here takes a file
      return h("div", { class: "hm-fd-field__ctl" }, h("b", { text: "Choose a file" }), " or drag it here", h("br", {}), fileHint(f));
    case "hidden":
      return h("div", { class: "hm-muted", text: "Hidden value: not shown to visitors" });
    default:
      return h("div", { class: "hm-fd-field__ctl", text: f.placeholder || "" });
  }
}

export function mountCanvas(root, { getDoc, getSelected, getErrors = () => ({}), onSelect, onEdit, onAction = () => {}, announce = () => {} }) {
  // A listbox needs options: with no fields the sheet is a plain drop target holding the empty note.
  const empty = h("div", { class: "hm-fd-empty" }, h("b", { text: "Drag your first field here" }), "or pick one from Add a field.");
  const listbox = (on) => {
    if (on) {
      root.setAttribute("role", "listbox");
      root.setAttribute("aria-label", "Form fields");
    } else {
      root.removeAttribute("role");
      root.removeAttribute("aria-label");
    }
  };
  const nodes = new Map(); // id -> {el, sig}
  const said = new Map(); // id -> last announced error
  let quiet = false; // a focus the screen moves itself does not select
  root.addEventListener("focusin", (e) => {
    const id = quiet ? null : e.target.closest?.("[data-id]")?.dataset.id;
    if (id) onSelect(id);
  });

  function build(f, fields, error) {
    const el = h("div", { role: "option", "data-id": f.id, tabindex: "-1" });
    if (!isLayout(f.type)) {
      const input = h("input", { type: "text", class: "hm-fd-field__label hm-fd-field__edit", value: f.label, "aria-label": `Label of ${TYPES[f.type]?.label ?? f.type} field`, "aria-invalid": error ? "true" : false, "data-hm-fd-label": "", "data-label": f.label });
      input.value = f.label;
      let start = f.label;
      input.addEventListener("focus", () => (start = input.value));
      input.addEventListener("keydown", (e) => {
        if (e.key === "Enter") input.blur();
        else if (e.key === "Escape") {
          e.stopPropagation();
          input.value = start;
          input.blur();
        }
      });
      input.addEventListener("blur", () => {
        if (input.value !== input.dataset.label) onEdit(f.id, "label", input.value, { coalesce: false });
      });
      el.append(h("div", { class: "hm-fd-field__head" }, input, f.required ? h("span", { class: "hm-fd-field__req", "aria-hidden": "true", text: "*" }) : null));
    }
    el.append(control(f));
    const note = ruleNote(f, fields);
    if (note) el.append(chip(note));
    if (error) el.append(h("p", { class: "hm-field__error", id: `fd-err-${f.id}`, text: error }));
    el.append(h("div", { class: "hm-fd-field__actions", "data-hm-fd-actions": "" }, ...ACTIONS.map(([a, text]) => actButton(text, { type: "button", tabindex: "-1", "data-act": a, onclick: () => onAction(f.id, a) }))));
    el.addEventListener("click", () => onSelect(f.id));
    return el;
  }

  function draw() {
    const doc = getDoc();
    const fields = doc.fields ?? [];
    const errors = getErrors() ?? {};
    const shown = visible(fields, {});
    const sel = getSelected();
    const keep = new Set(fields.map((f) => f.id));
    listbox(fields.length > 0);
    if (fields.length) empty.remove();
    else if (root.firstElementChild !== empty || root.children.length > 1) root.replaceChildren(empty);
    for (const [id, n] of nodes)
      if (!keep.has(id)) {
        n.el.remove();
        nodes.delete(id);
        said.delete(id);
      }
    const first = fields.some((f) => f.id === sel) ? sel : fields[0]?.id;
    fields.forEach((f, i) => {
      const error = errors[f.id] ?? "";
      const sig = rowSig(f, fields, error);
      let n = nodes.get(f.id);
      if (!n || n.sig !== sig) {
        const el = build(f, fields, error);
        if (n) n.el.replaceWith(el);
        n = { el, sig };
        nodes.set(f.id, n);
      }
      const el = n.el;
      const input = el.querySelector("[data-hm-fd-label]");
      // Roving tabindex reaches the label too: only the roving row's label is a Tab stop, so Tab goes on
      // to the inspector without selecting (by focus) every row on the way.
      input?.setAttribute("tabindex", f.id === first ? "0" : "-1");
      if (input && input.dataset.label !== f.label) {
        input.value = f.label;
        input.dataset.label = f.label;
      }
      if (root.children[i] !== el) root.insertBefore(el, root.children[i] ?? null);
      el.className = ["hm-fd-field", WIDTH[f.width] ?? "", f.id === sel ? "hm-fd-field--selected" : "", f.logic && !shown[f.id] ? "hm-fd-field--hidden-by-rule" : "", error ? "hm-fd-field--invalid" : ""].filter(Boolean).join(" ");
      el.setAttribute("aria-selected", String(f.id === sel));
      el.setAttribute("tabindex", f.id === first ? "0" : "-1");
      el.setAttribute("aria-label", `${f.label || TYPES[f.type]?.label || f.type}: ${fieldSummary(f, fields)}`);
      el.querySelector("[data-hm-fd-actions]").hidden = f.id !== sel;
      if (error && said.get(f.id) !== error) announce(error);
      if (error) said.set(f.id, error);
      else said.delete(f.id);
    });
    while (fields.length && root.children.length > fields.length) root.lastElementChild.remove();
  }

  return {
    draw,
    focusField: (id, { quiet: q = false } = {}) => {
      quiet = q;
      nodes.get(id)?.el.focus();
      quiet = false;
    },
    contains: (el) => root.contains(el),
    rowRects: () => Array.from(root.querySelectorAll(":scope > [data-id]"), (el) => el.getBoundingClientRect()),
  };
}
