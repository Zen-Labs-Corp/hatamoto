// The Class row under the inspector header (a lazy chunk): apply a class, the drift list with
// reset, detach, and "Save as class...". Every edit is one batch from designops.js. The stored
// key stays `preset`; "class" is the word people see.
import { h, actButton, warnNote } from "@hm/builder/dom";
import { NAMES, atLabel } from "@hm/builder/bp";
import { drift } from "@hm/builder/design";
import { applyOps, resetOps, detachOps, savePresetOps, presetBodyFrom } from "@hm/builder/designops";

const styleName = (k) => NAMES[k] ?? k.charAt(0).toUpperCase() + k.slice(1);

/** The class select's options: None, every preset, and the one the node names when it no longer exists. */
export function presetOptions(presets, current) {
  const out = [["", "None"], ...presets.map((p) => [p.name, p.label || p.name])];
  if (current && !presets.some((p) => p.name === current)) out.push([current, `${current} (removed)`]);
  return out;
}

/** The drift list rows: [{bp, key, text}] for each own value that differs from the preset. */
export const driftRows = (d) => d.overrides.map(({ bp, key, own, preset, at }) => ({ bp, key, text: `${styleName(key)} (${atLabel(bp)}): ${own}, class has ${preset}${at && at !== bp ? ` on ${atLabel(at)}` : ""}` }));

/** The Class row: "Class: card v  Save as class...", with the drift list under it when styles differ. Commits are one batch each and redraw the panel, then put focus back. */
export function presetPanel(node, current, { api, commit, draw, root, id, ready = () => Promise.resolve() }) {
  const design = api.design;
  const act = (ops, focus, name) => {
    if (!ops.length || !commit(ops, { label: name })) return false;
    draw();
    // The redrawn panel fills its Preset section once the lazy chunks are in: focus the named control then, never <body>.
    ready().then(() => (root.querySelector(`[data-focus="${focus}"]`) ?? root.querySelector('[role="tab"][tabindex="0"]'))?.focus());
    return true;
  };
  const select = h("select", { id, class: "hm-field__control", "data-focus": "select", "aria-describedby": `${id}-d` });
  for (const [v, text] of presetOptions(design.presets, node.p?.preset)) select.append(h("option", { value: v, text }));
  select.value = node.p?.preset ?? "";
  select.addEventListener("change", () => act(applyOps(current(), select.value), "select", "Apply class"));
  const info = h("div", { id: `${id}-d`, class: "hm-bs-drift" });
  const btn = (text, focus, run, aria) => actButton(text, { "data-focus": focus, ...(aria ? { "aria-label": aria } : {}), onclick: run });
  function paint() {
    const n = current();
    if (!n) return;
    const d = drift(n, design.presets, api.styleTable(), { tokens: design.tokens });
    const preset = design.presets.find((p) => p.name === n.p?.preset);
    const kids = [];
    if (d.state === "missing") {
      kids.push(h("p", { class: "hm-muted", text: "This class was removed, so its styles no longer apply." }), btn("Detach", "detach", () => act(detachOps(n, null), "select", "Detach class")));
    } else if (preset) {
      if (d.overrides.length) {
        kids.push(warnNote("Differs from class:"));
        kids.push(
          h(
            "ul",
            { class: "hm-bs-driftlist" },
            ...driftRows(d).map((r) => h("li", {}, r.text, " ", btn("Reset", `reset-${r.bp}-${r.key}`, () => act(resetOps(current(), preset).filter((o) => o.key === r.key), "select", "Reset to class"), `Reset ${styleName(r.key).toLowerCase()} to the class on all screen sizes`))),
          ),
        );
        kids.push(btn("Reset to class", "resetall", () => act(resetOps(n, preset), "select", "Reset to class")));
      }
      kids.push(btn("Detach to custom", "detach", () => act(detachOps(n, preset), "select", "Detach class")));
    }
    if (design.tokens && d.literals.length) kids.push(h("p", { class: "hm-muted", text: `Custom values: ${[...new Set(d.literals.map((l) => styleName(l.key)))].join(", ")}.` }));
    info.replaceChildren(...kids);
  }
  paint();
  const row = h("div", { class: "hm-bs-classrow" }, h("label", { class: "hm-field__label", for: id, text: "Class" }), select);
  const el = h("div", { class: "hm-bs-class" }, row, info);
  if (design.canCreate) {
    const form = saveForm(api, node, current, act, id);
    const open = actButton("Save as class…", { "aria-expanded": "false", "aria-controls": `${id}-f` });
    form.id = `${id}-f`;
    form.hidden = true;
    open.addEventListener("click", () => {
      form.hidden = !form.hidden;
      open.setAttribute("aria-expanded", String(!form.hidden));
      if (!form.hidden) form.querySelector("input")?.focus();
    });
    row.append(open);
    el.append(form);
  }
  return { el, paint };
}

/** "Save as class": the node's own styles become a new preset (created on the server, never updated from here). */
function saveForm(api, node, current, act, id) {
  const input = h("input", { id: `${id}-n`, class: "hm-field__control", type: "text", autocomplete: "off", spellcheck: "false", maxlength: "32", "aria-describedby": `${id}-m` });
  const msg = h("span", { class: "hm-bs-err", id: `${id}-m`, role: "status", hidden: true });
  const go = actButton("Save class");
  go.addEventListener("click", async () => {
    const n = current();
    const name = input.value.trim().toLowerCase();
    const fail = (text) => {
      msg.textContent = text;
      msg.hidden = !text;
      if (text) input.setAttribute("aria-invalid", "true");
      else input.removeAttribute("aria-invalid");
    };
    if (!n || go.disabled) return;
    if (!name) return fail("Give the class a name (lowercase letters, digits, hyphens).");
    go.disabled = true;
    try {
      const label = name.charAt(0).toUpperCase() + name.slice(1).replaceAll("-", " ");
      const res = await fetch(api.urls.presets, { method: "POST", credentials: "same-origin", headers: { "Content-Type": "application/json", "X-CSRF-Token": api.csrf() }, body: JSON.stringify({ name, label, ...presetBodyFrom(n) }) });
      const data = await res.json().catch(() => ({}));
      if (res.status !== 201) return fail(data.error || "The class could not be saved.");
      fail("");
      const fresh = current();
      api.design.presets.push({ name: data.name, label: data.label, s: data.s });
      api.design.presets.sort((a, b) => (a.name < b.name ? -1 : 1));
      if (fresh && act(savePresetOps(fresh, data.name, data), "select", "Save as class")) api.reload();
    } catch {
      fail("The class could not be saved. Check your connection and try again.");
    } finally {
      go.disabled = false;
    }
  });
  return h("div", { class: "hm-field hm-bs-field hm-bs-classform" }, h("label", { class: "hm-field__label", for: `${id}-n`, text: "Name for the new class" }), input, go, msg);
}

