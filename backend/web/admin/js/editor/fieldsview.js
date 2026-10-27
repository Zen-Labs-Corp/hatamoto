// The html section's Fields tab (canvas panel): labelled inputs derived from the
// html itself (htmlfields.js), no stored schema. Writes go to the textarea
// through the expect-guarded writers, so a stale field can never overwrite newer
// markup. The field *structure* is frozen while the view is open: each write only
// re-reads values by address (refreshHtmlFields), so a region the person is typing
// in is never remounted. The view is derived afresh when a value arrives that it
// did not write (kit.js remounts it on "hm:external") or when a write is rejected.
import { BLANK } from "@hm/editor/htmltext";
import { deriveHtmlFields, refreshHtmlFields, replaceElementInner, replaceTextAt, setAttr, setElementText } from "@hm/editor/htmlfields";
import { canRoundTripProse } from "@hm/editor/prose";
import { mountProseRegion } from "@hm/editor/kit";

let uid = 0;

function h(tag, props = {}, ...kids) {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(props)) {
    if (v === false || v === null || v === undefined) continue;
    if (k === "text") e.textContent = v;
    else e.setAttribute(k, v === true ? "" : v);
  }
  e.append(...kids);
  return e;
}

/** Every leaf field of a derivation, by key. */
export function flattenFields(fields, out = new Map()) {
  for (const f of fields) {
    if (f.kind === "group") f.items.forEach((it) => flattenFields(it.fields, out));
    else if (f.kind === "links") flattenFields(f.items, out);
    else out.set(f.key, f);
  }
  return out;
}

export function mountFieldsView(container, textarea) {
  const regions = [];
  let model = deriveHtmlFields(textarea.value, { canRoundTrip: canRoundTripProse });
  let leaves = flattenFields(model);
  const notice = h("p", { class: "hm-ed__error", role: "status" });
  notice.hidden = true;

  const say = (msg) => {
    notice.textContent = msg;
    notice.hidden = !msg;
  };

  /** Applies a writer result. Returns true when the document changed. */
  const commit = (r) => {
    if (!r.ok) {
      if (r.reason !== "blank") {
        say("That part of the page changed, so the fields were reloaded.");
        render();
      }
      return false;
    }
    say("");
    textarea.value = r.html;
    textarea.dispatchEvent(new Event("input", { bubbles: true }));
    model = refreshHtmlFields(model, r.html);
    leaves = flattenFields(model);
    return true;
  };

  const control = (f, labelText, value, write) => {
    const id = `hm-hf-${++uid}`;
    const input = h("input", { class: "hm-field__control", type: "text", id, value: "", autocomplete: "off" });
    input.value = value;
    if (f.readOnly) input.readOnly = true;
    input.addEventListener("input", () => {
      const live = leaves.get(f.key) ?? f;
      if (f.kind !== "attr" && BLANK.test(input.value)) {
        say("Can't be empty.");
        return;
      }
      commit(write(live, input.value));
    });
    input.addEventListener("blur", () => {
      const live = leaves.get(f.key) ?? f;
      const shown = f.kind === "elementText" ? (live.value ?? "") : live.value;
      if (input.value !== shown && f.kind !== "attr") input.value = shown;
      say("");
    });
    return h("div", { class: "hm-field" }, h("label", { class: "hm-field__label", for: id, text: labelText }), input);
  };

  const renderField = (f, parent) => {
    switch (f.kind) {
      case "text":
        parent.append(control(f, f.label, f.value, (live, v) => replaceTextAt(textarea.value, f.path, live.value, v)));
        break;
      case "attr":
        parent.append(control(f, f.label, f.value, (live, v) => setAttr(textarea.value, f.path, f.attr, live.value, v)));
        break;
      case "elementText":
        parent.append(control(f, f.label, f.value, (live, v) => setElementText(textarea.value, f.path, live.innerHtml, v)));
        break;
      case "prose": {
        const id = `hm-hf-${++uid}`;
        const box = h("div", { class: "hm-field" }, h("span", { class: "hm-field__label", id, text: f.label }));
        parent.append(box);
        const region = mountProseRegion(box, f.innerHtml, {
          inline: f.inline,
          label: f.label,
          onChange: (inner) => commit(replaceElementInner(textarea.value, f.path, (leaves.get(f.key) ?? f).innerHtml, inner)),
        });
        regions.push(region);
        break;
      }
      case "links": {
        const set = h("fieldset", { class: "hm-sf-object" }, h("legend", { class: "hm-field__label", text: `${f.label} (${f.items.length})` }));
        f.items.forEach((it) => renderField(it, set));
        parent.append(set);
        break;
      }
      case "group": {
        const wrapper = h("fieldset", { class: "hm-sf-object" }, h("legend", { class: "hm-field__label", text: `${f.label} (${f.count})` }));
        for (const it of f.items) {
          if (f.flat) {
            it.fields.forEach((x) => renderField(x, wrapper));
            continue;
          }
          const d = h("details", { class: "hm-ed__group" }, h("summary", { text: it.title }));
          const inner = h("div", { class: "hm-ed__fields" });
          it.fields.forEach((x) => renderField(x, inner));
          d.append(inner);
          wrapper.append(d);
        }
        parent.append(wrapper);
        break;
      }
    }
  };

  function render() {
    regions.splice(0).forEach((r) => r.destroy());
    model = deriveHtmlFields(textarea.value, { canRoundTrip: canRoundTripProse });
    leaves = flattenFields(model);
    const root = h("div", { class: "hm-ed__fields" }, notice);
    if (model.length === 0) root.append(h("p", { class: "hm-muted", text: "There is no editable text here. Use the Code tab to change the markup." }));
    // Top-level fields sharing a group label go under one heading.
    for (const f of model) renderField(f, root);
    container.replaceChildren(root);
  }
  render();
  return { destroy: () => regions.splice(0).forEach((r) => r.destroy()) };
}
