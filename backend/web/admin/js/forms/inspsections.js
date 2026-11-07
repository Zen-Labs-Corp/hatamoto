// The inspector's section bodies (a lazy chunk, loaded when a field is first selected). sectionsFor
// is pure: which sections a field type gets. createSections builds them. DOM via h() only.
import { h, actButton, switchRow, warnNote } from "@hm/builder/dom";
import { TYPES, isLayout, DEFAULT_FILE_MB } from "@hm/forms/types";
import { nameFrom, uniqueName, labelOps } from "@hm/forms/ops";
import { icon } from "@hm/forms/palette";

const INPUTS = ["text", "email", "phone", "textarea", "number", "url"];
const CHOICES = ["select", "radio", "checkbox"];
const PLACEHOLDER = [...INPUTS, "select"];
const OPS = [["filled", "is filled"], ["empty", "is empty"], ["is", "is"], ["is_not", "is not"], ["contains", "contains"]];
const SOURCES = [["static", "A fixed value"], ["param", "A web address parameter"], ["page", "The page address"], ["referrer", "The referrer"]];

/** The sections a field of this type shows, in order: content, choices, layout, rules, advanced. */
export function sectionsFor(field) {
  const t = field.type;
  if (isLayout(t)) return ["content"];
  if (t === "hidden") return ["content", "advanced"];
  const out = ["content"];
  if (CHOICES.includes(t)) out.push("choices");
  out.push("layout", "rules");
  if (INPUTS.includes(t) || t === "file") out.push("advanced");
  return out;
}

/** An icon-only button cloned from the server-rendered template[data-hm-bs-iconact], its icon swapped for `name`. */
function iconButton(name, label, props = {}) {
  const b = document.querySelector("template[data-hm-bs-iconact]").content.firstElementChild.cloneNode(true);
  b.querySelector(".hm-icon").replaceWith(icon(name).firstElementChild);
  b.setAttribute("aria-label", label);
  b.setAttribute("title", label);
  for (const [k, v] of Object.entries(props)) if (v === true) b.setAttribute(k, "");
  return b;
}

let uid = 0;
const row = (label, control, { id, help } = {}) =>
  h("div", { class: "hm-field" }, h("label", { class: "hm-field__label", for: id, text: label }), control, help ? h("p", { class: "hm-field__help", text: help }) : null);
const select = (id, pairs, value) => {
  const s = h("select", { class: "hm-field__control", id }, ...pairs.map(([v, t]) => h("option", { value: v, text: t })));
  s.value = value;
  return s;
};

export function createSections({ field, fields, commit, errors = {}, announce = () => {} }) {
  const id = field.id;
  const dom = `fd${++uid}`;
  const label = field.label || TYPES[field.type]?.label || "field";
  const send = (ops, o = {}) => commit(ops, { label: `Edit ${label}`, ...o });
  const set = (key, value, o) => send([{ op: "setField", id, key, value: value === "" || value === false ? null : value }], o);
  const typing = (key) => ({ coalesce: `fd:${id}:${key}` });

  /** A text control that commits on every keystroke as one coalesced undo step. */
  function text(key, name, { tag = "input", help, get = (f) => f[key] ?? "", put } = {}) {
    const el = h(tag, { class: "hm-field__control", id: `${dom}-${key}`, "data-fd-key": key, ...(tag === "input" ? { type: "text" } : { rows: "3" }) });
    el.value = get(field);
    el.addEventListener("input", () => (put ? put(el.value) : set(key, el.value, typing(key))));
    return row(name, el, { id: el.id, help });
  }
  const section = (name, ...kids) => h("section", { class: "hm-fd-section", "aria-label": name }, h("h3", { class: "hm-fd-group", text: name }), ...kids);

  function content() {
    const kids = [];
    const err = errors[id];
    if (!isLayout(field.type)) {
      kids.push(text("label", "Label", { put: (v) => send(labelOps({ fields }, id, v), typing("label")) }));
      kids.push(text("name", "Name", { help: "Used as the answer's name in emails and exports.", put: (v) => send([{ op: "setField", id, key: "name", value: v }, { op: "setField", id, key: "name_locked", value: true }], typing("name")) }));
      if (field.name_locked) {
        kids.push(
          warnNote("The name no longer follows the label."),
          actButton("Follow the label again", { type: "button", onclick: () => send([{ op: "setField", id, key: "name_locked", value: null }, { op: "setField", id, key: "name", value: uniqueName(nameFrom(field.label || TYPES[field.type].label), fields.filter((x) => x.id !== id)) }]) }),
        );
      }
    }
    if (PLACEHOLDER.includes(field.type)) kids.push(text("placeholder", "Placeholder"));
    if (field.type === "heading") kids.push(text("text", "Heading"));
    if (field.type === "paragraph") kids.push(text("text", "Text", { tag: "textarea" }));
    if (field.type === "consent") kids.push(text("link", "Link to the terms", { help: "A path like /privacy or a full address." }));
    if (field.type === "divider") kids.push(h("p", { class: "hm-muted", text: "A line between sections. Nothing to set." }));
    if (!isLayout(field.type) && field.type !== "hidden") {
      kids.push(text("help_text", "Help text"));
      const sw = switchRow(`${dom}-req`, "Required");
      sw.input.checked = !!field.required;
      sw.input.addEventListener("change", () => send([{ op: "setField", id, key: "required", value: sw.input.checked }]));
      kids.push(sw.label);
    }
    if (err) kids.push(h("p", { class: "hm-field__error", role: "alert", text: err }));
    return section("Content", ...kids);
  }

  function choices() {
    const opts = field.options ?? [];
    const put = (next, o) => send([{ op: "setField", id, key: "options", value: next }], o);
    const rows = opts.map((o, i) => {
      const lab = h("input", { class: "hm-field__control", type: "text", "aria-label": `Option ${i + 1} label`, value: o.label });
      const val = h("input", { class: "hm-field__control hm-fd-choice__value", type: "text", "aria-label": `Option ${i + 1} value`, value: o.value });
      lab.value = o.label;
      val.value = o.value;
      lab.addEventListener("input", () => put(opts.map((x, k) => (k === i ? { ...x, label: lab.value } : x)), { coalesce: `fd:${id}:opt${i}l` }));
      val.addEventListener("input", () => put(opts.map((x, k) => (k === i ? { ...x, value: val.value } : x)), { coalesce: `fd:${id}:opt${i}v` }));
      const swap = (j) => {
        const n = [...opts];
        [n[i], n[j]] = [n[j], n[i]];
        put(n);
      };
      const first = h("input", { class: "hm-field__box", type: "radio", name: `${dom}-def`, "aria-label": `Option ${i + 1} is selected at first`, title: "Selected at first (click again to clear)", checked: field.default_value === o.value });
      first.checked = field.default_value === o.value;
      first.addEventListener("click", () => set("default_value", field.default_value === o.value ? "" : o.value));
      const up = iconButton("arrow-up", `Move option ${i + 1} up`, { disabled: i === 0 });
      const down = iconButton("arrow-down", `Move option ${i + 1} down`, { disabled: i === opts.length - 1 });
      const del = iconButton("x", `Remove option ${i + 1}`, { disabled: opts.length <= 1 });
      up.addEventListener("click", () => swap(i - 1));
      down.addEventListener("click", () => swap(i + 1));
      del.addEventListener("click", () => put(opts.filter((_, k) => k !== i)));
      return h("div", { class: "hm-fd-choice", role: "group", "aria-label": `Option ${i + 1}` }, field.type === "checkbox" ? null : first, lab, val, up, down, del);
    });
    const n = opts.length + 1;
    const add = actButton("Add an option", { type: "button", onclick: () => put([...opts, { label: `Option ${n}`, value: `option_${n}` }]) });
    const paste = h("textarea", { class: "hm-field__control", rows: "4", id: `${dom}-paste`, hidden: true, "aria-label": "One option per line" });
    const apply = actButton("Use this list", { type: "button", hidden: true, onclick: () => {
      const lines = paste.value.split("\n").map((s) => s.trim()).filter(Boolean);
      if (!lines.length) return announce("Type one option per line.");
      const seen = new Set();
      put(lines.map((l) => {
        let v = nameFrom(l);
        for (let k = 2; seen.has(v); k++) v = `${nameFrom(l).slice(0, 56)}_${k}`;
        seen.add(v);
        return { label: l, value: v };
      }));
    } });
    const toggle = actButton("Paste a list", { type: "button", "aria-expanded": "false", onclick: () => {
      paste.hidden = apply.hidden = !paste.hidden;
      toggle.setAttribute("aria-expanded", String(!paste.hidden));
      if (!paste.hidden) paste.focus();
    } });
    const kids = [...rows, h("div", { class: "hm-fd-section__row" }, add, toggle), paste, apply];
    if (field.type !== "checkbox") kids.push(h("p", { class: "hm-field__help", text: "The round button marks the option selected at first. Click it again to clear." }));
    return section("Choices", ...kids);
  }

  function layout() {
    const w = select(`${dom}-width`, [["", "Full"], ["half", "Half"], ["third", "Third"]], field.width && field.width !== "full" ? field.width : "");
    w.addEventListener("change", () => set("width", w.value));
    return section("Layout", row("Width", w, { id: w.id }));
  }

  function rules() {
    const sources = fields.filter((f) => f.id !== id && !isLayout(f.type) && f.type !== "hidden" && f.type !== "file");
    const l = field.logic;
    const src = select(`${dom}-src`, [["", "Always"], ...sources.map((f) => [f.id, f.label || f.name])], l && sources.some((f) => f.id === l.field_id) ? l.field_id : "");
    const op = select(`${dom}-op`, OPS, l?.op ?? "filled");
    const val = h("input", { class: "hm-field__control", type: "text", id: `${dom}-lv`, "aria-label": "Value", value: l?.value ?? "" });
    val.value = l?.value ?? "";
    op.hidden = !src.value;
    val.hidden = !src.value || op.value === "filled" || op.value === "empty";
    const push = (o) => {
      const logic = src.value ? { field_id: src.value, op: op.value, ...(op.value === "filled" || op.value === "empty" || !val.value ? {} : { value: val.value }) } : null;
      send([{ op: "setField", id, key: "logic", value: logic }], o);
    };
    src.addEventListener("change", () => push());
    op.addEventListener("change", () => push());
    val.addEventListener("input", () => push(typing("logic")));
    const mail = switchRow(`${dom}-mail`, "Include in the notification email");
    mail.input.checked = field.in_email !== false;
    mail.input.addEventListener("change", () => send([{ op: "setField", id, key: "in_email", value: mail.input.checked ? null : false }]));
    return section("Rules", row("Show when", src, { id: src.id }), op, val, mail.label);
  }

  function advanced() {
    const v = field.validation ?? {};
    const num = (key, name) => {
      const el = h("input", { class: "hm-field__control", type: "number", min: "0", id: `${dom}-${key}`, value: v[key] ?? "" });
      el.value = v[key] ?? "";
      el.addEventListener("input", () => {
        const next = { ...v };
        if (el.value === "") delete next[key];
        else next[key] = Math.max(0, Math.trunc(Number(el.value)));
        set("validation", Object.keys(next).length ? next : null, typing(`v.${key}`));
      });
      return row(name, el, { id: el.id });
    };
    const str = (key, name) => {
      const el = h("input", { class: "hm-field__control", type: "text", id: `${dom}-${key}`, value: v[key] ?? "" });
      el.value = v[key] ?? "";
      el.addEventListener("input", () => {
        const next = { ...v };
        if (el.value === "") delete next[key];
        else next[key] = el.value;
        set("validation", Object.keys(next).length ? next : null, typing(`v.${key}`));
      });
      return row(name, el, { id: el.id });
    };
    // Allowed files: comma-separated extensions, stored as the validation's accept list.
    const fileAccept = () => {
      const el = h("input", { class: "hm-field__control", type: "text", id: `${dom}-accept`, value: (v.accept ?? []).join(", ") });
      el.value = (v.accept ?? []).join(", ");
      el.addEventListener("input", () => {
        const next = { ...v };
        const list = el.value.split(",").map((s) => s.trim().replace(/^\./, "").toLowerCase()).filter(Boolean);
        if (list.length) next.accept = list;
        else delete next.accept;
        set("validation", Object.keys(next).length ? next : null, typing("v.accept"));
      });
      return row("Allowed files", el, { id: el.id, help: "File types, separated by commas: pdf, png, docx." });
    };
    const fileMax = () => {
      const el = h("input", { class: "hm-field__control", type: "number", min: "1", max: "25", id: `${dom}-max_mb`, value: v.max_mb ?? DEFAULT_FILE_MB });
      el.value = v.max_mb ?? DEFAULT_FILE_MB;
      el.addEventListener("input", () => {
        const next = { ...v };
        if (el.value === "") delete next.max_mb;
        else next.max_mb = Math.min(25, Math.max(1, Math.trunc(Number(el.value))));
        set("validation", Object.keys(next).length ? next : null, typing("v.max_mb"));
      });
      return row("Largest file (MB)", el, { id: el.id, help: "Up to 25." });
    };
    if (field.type === "file") return section("Advanced", fileAccept(), fileMax());
    const kids = [];
    if (field.type === "number") kids.push(num("min", "Smallest number"), num("max", "Largest number"));
    else kids.push(num("min_length", "Shortest answer"), num("max_length", "Longest answer"));
    kids.push(str("pattern", "Must match (pattern)"), str("message", "Message when it does not match"));
    if (field.type === "email") {
      const sw = switchRow(`${dom}-free`, "Refuse free email providers");
      sw.input.checked = !!field.block_free_email;
      sw.input.addEventListener("change", () => set("block_free_email", sw.input.checked));
      kids.push(sw.label);
    }
    return section("Advanced", ...kids);
  }

  function hidden() {
    const hs = field.hidden ?? { source: "static", value: "" };
    const src = select(`${dom}-hsrc`, SOURCES, hs.source);
    const val = h("input", { class: "hm-field__control", type: "text", id: `${dom}-hval`, value: (hs.source === "param" ? hs.param : hs.value) ?? "" });
    val.value = (hs.source === "param" ? hs.param : hs.value) ?? "";
    const push = (o) => {
      const s = src.value;
      set("hidden", s === "param" ? { source: s, param: val.value } : s === "static" ? { source: s, value: val.value } : { source: s }, o);
    };
    src.addEventListener("change", () => push());
    val.addEventListener("input", () => push(typing("hidden")));
    val.hidden = src.value === "page" || src.value === "referrer";
    return section("Advanced", row("Value comes from", src, { id: src.id }), val);
  }

  const build = { content, choices, layout, rules, advanced: field.type === "hidden" ? hidden : advanced };
  return sectionsFor(field).map((s) => build[s]());
}
