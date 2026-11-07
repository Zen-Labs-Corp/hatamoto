// "Try it" (a lazy chunk): the form as a visitor meets it, read-write and offline. Logic shows and
// hides fields as answers change; Submit runs the same validation the server does (validate.js) and
// shows its messages inline, announced through the live region. Nothing is sent anywhere.
import { h, actButton } from "@hm/builder/dom";
import { validate, visible } from "@hm/forms/validate";
import { fileHint } from "@hm/forms/types";

const WIDTH = { half: "hm-fd-field--half", third: "hm-fd-field--third" };
const isLayout = (f) => f.type === "heading" || f.type === "paragraph" || f.type === "divider";
const TEXT_TYPE = { email: "email", phone: "tel", url: "url", date: "date", number: "text" };

export function mountPreview(root, { getDoc, freeEmail = [], announce = () => {} }) {
  let seq = 0;

  /** A field's control, and how to read its answer from the DOM. */
  function control(f, id) {
    const opts = f.options ?? [];
    switch (f.type) {
      case "textarea": {
        const el = h("textarea", { class: "hm-field__control", id, rows: "4", placeholder: f.placeholder });
        return { el, read: () => el.value };
      }
      case "select": {
        const el = h("select", { class: "hm-field__control", id }, h("option", { value: "", text: f.placeholder || "Choose…" }), ...opts.map((o) => h("option", { value: o.value, text: o.label })));
        el.value = f.default_value ?? "";
        return { el, read: () => el.value };
      }
      case "radio":
      case "checkbox": {
        if (f.type === "checkbox" && opts.length === 0) {
          const input = h("input", { class: "hm-field__box", type: "checkbox", id });
          return { el: h("label", { class: "hm-field__check", for: id }, input, h("span", { text: f.label })), read: () => input.checked };
        }
        const inputs = opts.map((o, i) => h("input", { class: "hm-field__box", type: f.type, name: `${id}-g`, id: `${id}-${i}`, value: o.value, checked: f.type === "radio" && o.value === f.default_value }));
        const el = h("div", { class: "hm-field__group", role: f.type === "radio" ? "radiogroup" : "group", "aria-labelledby": `${id}-l` }, ...opts.map((o, i) => h("label", { class: "hm-field__check", for: `${id}-${i}` }, inputs[i], h("span", { text: o.label }))));
        return { el, read: () => (f.type === "radio" ? (inputs.find((i) => i.checked)?.value ?? "") : inputs.filter((i) => i.checked).map((i) => i.value)) };
      }
      case "consent": {
        const input = h("input", { class: "hm-field__box", type: "checkbox", id });
        return { el: h("label", { class: "hm-field__check", for: id }, input, h("span", { text: f.label || "I agree" })), read: () => input.checked };
      }
      case "file": {
        // nothing is sent from the preview: a switched-off tile, and no answer to read
        const el = h("div", { class: "hm-fd-field__ctl", id }, h("b", { text: "Choose a file" }), " or drag it here", h("br", {}), fileHint(f));
        return { el, read: () => "" };
      }
      default: {
        const el = h("input", { class: "hm-field__control", id, type: TEXT_TYPE[f.type] ?? "text", placeholder: f.placeholder });
        return { el, read: () => el.value };
      }
    }
  }

  function draw() {
    const doc = getDoc();
    const fields = doc.fields ?? [];
    const base = `fdp${++seq}`;
    const rows = [];
    const reads = new Map(); // name -> read()
    const errorEls = new Map(); // name -> <p>
    for (const f of fields) {
      if (f.type === "hidden") continue;
      const wrap = h("div", { class: `hm-fd-field ${WIDTH[f.width] ?? ""}`.trim(), "data-id": f.id });
      if (f.type === "heading") wrap.append(h("div", { class: "hm-fd-field__label", role: "heading", "aria-level": "3", text: f.text || "" }));
      else if (f.type === "paragraph") wrap.append(h("p", { class: "hm-muted", text: f.text || "" }));
      else if (f.type === "divider") wrap.append(h("hr", {}));
      else {
        const id = `${base}-${f.id}`;
        const c = control(f, id);
        const own = f.type === "consent" || (f.type === "checkbox" && !(f.options ?? []).length);
        if (!own) wrap.append(h("label", { class: "hm-field__label", id: `${id}-l`, for: c.el.tagName === "DIV" ? null : id, text: f.label + (f.required ? " *" : "") }));
        wrap.append(c.el);
        if (f.help_text) wrap.append(h("p", { class: "hm-field__help", text: f.help_text }));
        const err = h("p", { class: "hm-field__error", id: `${id}-e`, hidden: true });
        wrap.append(err);
        if (f.name) {
          reads.set(f.name, c.read);
          errorEls.set(f.name, [err, c.el]);
        }
      }
      rows.push([f, wrap]);
    }
    const answers = () => Object.fromEntries([...reads].map(([n, read]) => [n, read()]));
    const sync = () => {
      const shown = visible(fields, answers());
      for (const [f, wrap] of rows) wrap.hidden = !shown[f.id];
    };
    const note = h("p", { class: "hm-muted", role: "status" });
    const form = h("form", { class: "hm-fd__card hm-fd__sheet", novalidate: true, "aria-label": "Try the form" }, ...rows.map(([, w]) => w), h("div", { class: "hm-fd-field" }, actButton("Submit", { type: "submit" }), note));
    form.addEventListener("input", sync);
    form.addEventListener("change", sync);
    form.addEventListener("submit", (e) => {
      e.preventDefault();
      const r = validate(fields, answers(), { page_url: "", referrer: "" }, freeEmail);
      for (const [name, [err, el]] of errorEls) {
        const msg = r.errors[name] ?? "";
        err.textContent = msg;
        err.hidden = !msg;
        if (msg) (el.setAttribute("aria-invalid", "true"), el.setAttribute("aria-describedby", err.id));
        else (el.removeAttribute("aria-invalid"), el.removeAttribute("aria-describedby"));
      }
      const msgs = Object.values(r.errors);
      const say = msgs.length ? `${msgs.length === 1 ? "1 problem" : `${msgs.length} problems`}: ${msgs[0]}` : "Looks good. Nothing was sent: this is only a preview.";
      note.textContent = say;
      announce(say);
      if (msgs.length) form.querySelector("[aria-invalid]")?.focus?.();
    });
    root.replaceChildren(form);
    sync();
  }

  return { draw };
}
