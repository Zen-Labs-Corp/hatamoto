// The Content and Advanced tab bodies of the inspector: native prop fields, the Bind
// control, the rich text prop and the component form. A lazy chunk (inspector.js loads
// it on first Content/Advanced draw) so first paint carries only the tab shell.
import { h, actButton, warnNote, switchRow } from "@hm/builder/dom";
import { find, unchanged } from "@hm/builder/store";
import { bindOps, unbindOps, sourcesFor, chipText, inLoop } from "@hm/builder/bindops";
import { seedNode } from "@hm/builder/palette";
import { plainLabel } from "@hm/builder/inspector";

export const PROP_NAMES = { text: "Text", level: "Level", href: "Link", src: "Image address", url: "Video address", poster: "Poster image", count: "Number of columns", slug: "Form", single: "Open one item at a time", ordered: "Numbered", alt: "Alt text", layout: "Layout", tag: "HTML tag", hidden: "Disabled (not rendered)", decorative: "Decorative (no alt text needed)" };
const title = (s) => s.charAt(0).toUpperCase() + s.slice(1);
export const propName = (k) => plainLabel(PROP_NAMES[k] ?? title(k));
// Props edited on the Advanced tab, or not by a plain control at all.
const ADVANCED = new Set(["hidden", "tag", "v1"]);
const SKIP = new Set(["type", "content", "extra", "target", "rel"]); // target/rel: the element settings chunk
const INLINE = new Set(["heading", "text"]); // edited with the inline editor
const ITEMS = new Set(["tabs", "accordion", "gallery", "list"]); // the Items panel (items.js) edits their entries
const plainEnum = (v) => title(String(v).replaceAll("-", " ")); // icon ids as names: "alert-circle" -> "Alert circle" (no preview: the client has no icon set)

// The order the Content tab lists a kind's props in: the first ones in this order, any other after them (as the server lists them).
const ORDER = { heading: ["text", "level"], button: ["text", "href"], image: ["src", "alt", "caption", "href", "lightbox"] };
export const orderProps = (node, props) => {
  const first = ORDER[node.t];
  return first ? [...first.map((k) => props.find((p) => p.key === k)).filter(Boolean), ...props.filter((p) => !first.includes(p.key))] : props;
};

/** Which control a prop gets: the server's widget hint first, then its type; "src" props take a library picker. */
export function controlOf(p) {
  if (p.type === "boolean") return "check";
  if (p.enum?.length) return "select";
  if (p.type === "integer") return "number";
  const w = p.widget ?? "";
  if (w === "richtext" || w === "media" || w === "textarea" || w === "lines" || w === "code") return w;
  if (w.startsWith("datalist:")) return "datalist";
  return p.key === "src" ? "media" : "text";
}

/** Whether an image without alt text and not marked decorative needs the "Add alt text" warning. */
export const needsAlt = (node) => node.t === "image" && node.p?.decorative !== true && !(typeof node.p?.alt === "string" && node.p.alt.trim());

let uid = 0;

/** The bound-data chip: the server-rendered mono badge cloned from its template, with the source text. */
function boundChip(root, id, text) {
  const chip = root.closest("[data-hm-builder]")?.querySelector("template[data-hm-bs-badge]")?.content.firstElementChild?.cloneNode(true);
  if (!chip) return h("span", { id, text });
  chip.id = id;
  chip.textContent = text;
  return chip;
}

/**
 * createContent(ctx) -> {propField, contentPanel, advancedPanel}
 * ctx: {api, root, commit, draw, kindInfo, gen() (the draw generation), pushRich(ctl), setFields(f)}
 */
export function createContent(ctx) {
  const { api, root, commit, draw, kindInfo, gen, pushRich, setFields } = ctx;

  const setProp = (id, key, value, opts = {}) => commit([{ op: "setProp", id, key, value }], { label: `Change ${propName(key)}`, ...opts });

  function propField(node, p) {
    const field = baseField(node, p);
    field.dataset.prop = p.key;
    if (p.key === "text" && INLINE.has(node.t) && !chipText(node, "text")) inlineField(node, field);
    return p.bind && api.bindSources?.length ? bindField(node, p, field) : field;
  }

  // a lazy chunk over the plain field, which stays if it does not load
  function inlineField(node, field) {
    const mine = gen();
    import("@hm/builder/inlinefield")
      .then((m) => m.upgrade(field, node, { commit, label: "Text", announce: api.announce, current: () => find(api.getDoc(), node.id) ?? node, live: () => mine === gen() && field.isConnected }))
      .then((ctl) => ctl && pushRich(ctl))
      .catch(console.error);
  }

  /** The Bind control of a bindable prop: a "Bind to" select, or a chip with Unbind (the control is disabled while bound). */
  function bindField(node, p, field) {
    const key = p.key;
    const bound = chipText(node, key);
    const ctl = field.querySelector(".hm-field__control");
    const id = `hm-bs-b${++uid}`;
    const again = (focus) => {
      draw();
      root.querySelector(`[data-focus="${focus}"]`)?.focus();
    };
    const tail = h("div", { class: "hm-bs-bind" });
    if (bound) {
      ctl.disabled = true;
      ctl.setAttribute("aria-describedby", `${id}-c`);
      tail.append(
        boundChip(root, `${id}-c`, bound),
        actButton("Unbind", { "data-focus": "bind", "aria-label": `Unbind ${propName(key).toLowerCase()}`, onclick: () => commit(unbindOps(node.id, key), { label: `Unbind ${propName(key)}` }) && (api.announce(`${propName(key)} unbound`), again("bind")) }),
      );
    } else {
      // collapsed: the "Use data" toggle opens the source picker under the field and closes it again
      const open = actButton("Use data", { class: "hm-btn--ghost hm-bs-bindtoggle", "data-focus": "bind", "aria-expanded": "false", "aria-label": `Use data for ${propName(key).toLowerCase()}` });
      let form = null;
      const build = () => {
        const rows = sourcesFor(api.bindSources, p.bind, inLoop(api.getDoc(), node.id));
        const sel = h("select", { id, class: "hm-field__control", "data-focus": "bind-source" });
        sel.append(h("option", { value: "", text: "Literal value" }), ...rows.map((r) => h("option", { value: r.source, text: `${r.group}: ${r.label}` })));
        const path = h("input", { type: "text", class: "hm-field__control", autocomplete: "off", spellcheck: "false", hidden: true, "aria-label": "Path after the dot" });
        const done = (src) => {
          if (commit(bindOps(node.id, key, src), { label: `Bind ${propName(key)}` })) api.announce(`${propName(key)} bound to ${src}`);
          again("bind");
        };
        sel.addEventListener("change", () => {
          const pre = sel.value.endsWith(">") ? sel.value.slice(0, sel.value.lastIndexOf("<")) : "";
          path.hidden = !pre;
          if (pre) path.focus();
          else if (sel.value) done(sel.value);
        });
        path.addEventListener("keydown", (e) => {
          if (e.key !== "Enter") return;
          e.preventDefault();
          if (path.value.trim()) done(sel.value.slice(0, sel.value.lastIndexOf("<")) + path.value.trim());
        });
        return h("div", { class: "hm-bs-bind__form" }, h("label", { class: "hm-field__label", for: id, text: "Bind to" }), sel, path);
      };
      open.addEventListener("click", () => {
        if (form) form.hidden = !form.hidden;
        else tail.append((form = build()));
        open.setAttribute("aria-expanded", String(!form.hidden));
        if (!form.hidden) form.querySelector("select")?.focus();
      });
      tail.append(open);
    }
    field.append(tail);
    return field;
  }

  function baseField(node, p) {
    const id = `hm-bs-p${++uid}`;
    const cur = node.p?.[p.key];
    const name = propName(p.key);
    const err = h("span", { class: "hm-bs-err", id: `${id}-e`, hidden: true, text: "A value is required." });
    let el;
    const extra = [];
    const kind = controlOf(p);
    if (kind === "richtext") return richField(node, p, id, name);
    if (kind === "check") {
      const sw = switchRow(id, name);
      el = sw.input;
      el.checked = cur === true;
      el.addEventListener("change", () => setProp(node.id, p.key, el.checked ? true : null));
      return h("div", { class: "hm-field hm-bs-field hm-bs-field--check" }, sw.label);
    }
    const apply = (raw, opts) => {
      const empty = raw === "";
      if (empty && p.required) {
        el.setAttribute("aria-invalid", "true");
        err.hidden = false;
        return;
      }
      el.removeAttribute("aria-invalid");
      err.hidden = true;
      const value = empty ? null : p.type === "integer" ? Number(raw) : raw;
      const cur = () => find(api.getDoc(), node.id)?.p ?? {};
      if (unchanged(api.getDoc(), node.id, p.key, value)) return; // the store's value now, not the one drawn
      // the plain field of a heading/text (until the editor loads, or when it never does) must also drop the html, which wins at render
      if (p.key === "text" && INLINE.has(node.t)) commit([{ op: "setProp", id: node.id, key: "text", value }, { op: "setProp", id: node.id, key: "html", value: null }], { label: "Change Text", ...opts });
      else if (p.key === "tag" && node.t === "container" && (value === null || value === "div") && cur().aria && !cur().href && !find(api.getDoc(), node.id)?.b?.href) {
        // a plain div takes no accessible name: drop the one it had in the same step
        commit([{ op: "setProp", id: node.id, key: "tag", value }, { op: "setProp", id: node.id, key: "aria", value: null }], { label: "Change HTML tag", ...opts });
      } else setProp(node.id, p.key, value, opts);
    };
    if (node.t === "columns" && p.key === "count") {
      // the count and the child containers change together (columnsops.js): empty ones are added or removed, never content
      el = h("input", { id, type: "number", class: "hm-field__control", min: String(p.min ?? 2), max: String(p.max ?? 6), step: "1", "aria-describedby": `${id}-e` });
      el.value = String(node.c?.length ?? cur ?? 2);
      el.addEventListener("change", () => {
        const n = Math.min(p.max ?? 6, Math.max(p.min ?? 2, Math.round(Number(el.value)) || 2));
        import("@hm/builder/columnsops")
          .then(({ columnsOps }) => {
            const r = columnsOps(find(api.getDoc(), node.id) ?? node, n);
            err.hidden = !r.refuse;
            err.textContent = r.refuse ?? "";
            if (r.refuse) return void (el.value = String(node.c?.length ?? n), el.setAttribute("aria-invalid", "true"));
            el.removeAttribute("aria-invalid");
            if (r.ops.length && commit(r.ops, { label: "Change Number of columns" })) (api.announce(`${n} columns`), draw());
          })
          .catch(console.error);
      });
      err.setAttribute("role", "alert");
      return h("div", { class: "hm-field hm-bs-field" }, h("label", { class: "hm-field__label", for: id, text: name }), el, err);
    }
    if (kind === "datalist" && p.widget === "datalist:forms") {
      // a select of the site's forms (the stored slug is unchanged)
      const forms = api.options?.forms ?? [];
      el = h("select", { id, class: "hm-field__control", "aria-describedby": `${id}-e` });
      el.append(h("option", { value: "", text: "Choose a form" }), ...(cur && !forms.includes(cur) ? [cur, ...forms] : forms).map((v) => h("option", { value: v, text: v })));
      el.value = cur ?? "";
      el.addEventListener("change", () => apply(el.value));
      return h("div", { class: "hm-field hm-bs-field" }, h("label", { class: "hm-field__label", for: id, text: name }), el, err);
    }
    if (node.t === "heading" && p.key === "level") {
      // H1 to H6 as one segmented group (one click each, the current level pressed); the schema gives min and max, not a list
      const lid = `${id}-l`;
      const levels = p.enum?.length ? p.enum : Array.from({ length: (p.max ?? 6) - (p.min ?? 1) + 1 }, (_, i) => (p.min ?? 1) + i);
      const btns = levels.map((v) => {
        const b = h("button", { type: "button", class: "hm-bs-opt", "aria-pressed": String(String(v) === String(cur ?? levels[0])), text: `H${v}` });
        b.addEventListener("click", () => {
          for (const o of btns) o.setAttribute("aria-pressed", String(o === b));
          apply(String(v));
        });
        return b;
      });
      el = h("div", { class: "hm-bs-opts", role: "group", "aria-labelledby": lid }, ...btns);
      return h("div", { class: "hm-field hm-bs-field" }, h("span", { class: "hm-field__label", id: lid, text: name }), el, err);
    }
    if (kind === "select") {
      el = h("select", { id, class: "hm-field__control", "aria-describedby": `${id}-e` });
      if (!p.required) el.append(h("option", { value: "", text: "Not set" }));
      const plain = p.key === "icon" || (node.t === "icon" && p.key === "name");
      for (const v of p.enum) el.append(h("option", { value: String(v), text: plain ? plainEnum(v) : String(v) }));
      el.value = cur === undefined ? (p.required ? String(p.enum[0]) : "") : String(cur);
      el.addEventListener("change", () => apply(el.value));
    } else if (kind === "number") {
      el = h("input", { id, type: "number", class: "hm-field__control", min: p.min ?? false, max: p.max ?? false, step: "1", "aria-describedby": `${id}-e` });
      el.value = cur ?? "";
      el.addEventListener("change", () => apply(el.value.trim()));
    } else {
      const area = kind === "textarea" || kind === "lines" || kind === "code" || (node.t === "text" && p.key === "text");
      el = h(area ? "textarea" : "input", { id, class: "hm-field__control", "aria-describedby": `${id}-e`, ...(area ? { rows: "4" } : { type: "text" }) });
      if (kind === "code") {
        el.setAttribute("spellcheck", "false");
        el.classList.add("hm-bs-code");
      }
      if (p.maxLength) el.setAttribute("maxlength", String(p.maxLength));
      if (kind === "datalist") {
        const opts = api.options?.[p.widget.slice("datalist:".length)] ?? [];
        el.setAttribute("list", `${id}-l`);
        el.setAttribute("autocomplete", "off");
        extra.push(h("datalist", { id: `${id}-l` }, ...opts.map((o) => h("option", { value: o }))));
      }
      if (kind === "media") extra.push(actButton("Choose from library", { "data-hm-media-picker": id }));
      if (kind === "lines") {
        el.setAttribute("aria-describedby", `${id}-e ${id}-h`);
        extra.push(h("span", { class: "hm-muted", id: `${id}-h`, text: "One item per line." }));
      }
      el.value = cur ?? "";
      el.addEventListener("input", () => apply(el.value, { coalesce: true, label: `Change ${name}` }));
    }
    return h("div", { class: "hm-field hm-bs-field" }, h("label", { class: "hm-field__label", for: id, text: name }), el, ...extra, err);
  }

  /** The rich text prop: the Tiptap kit on a textarea; changes coalesce into one undo step. */
  function richField(node, p, id, name) {
    const area = h("textarea", { id, class: "hm-field__control", rows: "6" });
    area.value = node.p?.[p.key] ?? "";
    const holder = h("div", { class: "hm-field hm-bs-field" }, h("label", { class: "hm-field__label", for: id, text: name }), area);
    const mine = gen();
    import("@hm/editor/kit")
      .then((kit) => {
        if (mine !== gen() || !area.isConnected) return;
        const ctl = kit.mountRichText(area, {
          label: name,
          onChange: (html) => {
            const value = html === "" ? null : html;
            if (!unchanged(api.getDoc(), node.id, p.key, value)) setProp(node.id, p.key, value, { coalesce: true, label: `Change ${name}` });
          },
        });
        pushRich(ctl);
      })
      .catch((err) => console.error("hatamoto: the rich text editor could not be loaded; the plain field still works", err));
    area.addEventListener("input", () => setProp(node.id, p.key, area.value === "" ? null : area.value, { coalesce: true, label: `Change ${name}` }));
    return holder;
  }

  function contentPanel(node) {
    if (node.t === "ref") {
      // The ref panel is a lazy chunk (reuse.js); the slot is filled unless the panel was redrawn meanwhile.
      const slot = h("div", { class: "hm-bs-content" });
      const mine = gen();
      import("@hm/builder/reuse")
        .then(({ refPanel }) => {
          if (mine === gen() && slot.isConnected) slot.replaceWith(refPanel(node, { reuse: api.reuse, getDoc: api.getDoc, commit, select: api.select, announce: api.announce }));
        })
        .catch((err) => slot.replaceChildren(h("p", { class: "hm-muted", role: "alert", text: `The block panel did not load. Reload the page. ${err.message}` })));
      return slot;
    }
    const panel = h("div", { class: "hm-bs-content" });
    const props = (kindInfo(node.t)?.props ?? []).filter((p) => !ADVANCED.has(p.key) && !SKIP.has(p.key) && p.type !== "object" && !(p.key === "html" && INLINE.has(node.t)) && !(node.t === "list" && p.key === "items"));
    for (const p of orderProps(node, props)) panel.append(propField(node, p));
    if (ITEMS.has(node.t))
      import("@hm/builder/items")
        .then((m) => {
          const items = panel.isConnected && m.itemsPanel(find(api.getDoc(), node.id) ?? node, { api, commit, draw });
          if (items) panel.prepend(items);
        })
        .catch(console.error);
    if (node.t !== "component") import("@hm/builder/elementsettings").then((m) => m.contentExtras(node, panel, { api, commit })).catch(console.error);
    if (node.t === "loop" && !(node.c ?? []).some((c) => c.t === "loopempty")) {
      const add = seedNode({ t: "loopempty", c: [{ t: "text", p: { text: "Nothing here yet." } }] });
      panel.append(actButton("Add empty state", { onclick: () => commit([{ op: "insert", parent: node.id, index: (node.c ?? []).length, node: add }], { label: "Add empty state" }) && (api.announce("Empty state added"), draw()) }));
    }
    if (node.t === "image") {
      const warn = warnNote("Add alt text, or mark the image as decorative.", { role: "status" });
      const sync = () => {
        const now = find(api.getDoc(), node.id);
        warn.hidden = !now || !needsAlt(now);
      };
      sync();
      panel.append(warn);
      panel.addEventListener("input", sync);
      panel.addEventListener("change", sync);
    }
    if (node.t === "component") {
      const type = node.p?.type;
      if (!api.sections.some((s) => s.type === type)) {
        panel.append(h("p", { class: "hm-muted", text: `The section type “${type ?? ""}” is not in this theme. Its content is kept as it is; you can still move, style or remove it.` }));
      } else {
        const holder = h("div", { class: "hm-bs-fieldsholder" });
        panel.append(holder);
        const id = node.id;
        // fields.js is a lazy chunk (prefetched when idle); a redraw before it arrives cancels the mount.
        const mine = gen();
        const wait = h("p", { class: "hm-muted", role: "status", text: "Loading the section form…" });
        holder.append(wait);
        let real = null;
        setFields({ destroy: () => real?.destroy() });
        import("@hm/builder/fields")
          .then(({ mountFields }) => {
            if (mine !== gen() || !holder.isConnected) return;
            wait.remove();
            real = mountFields(holder, {
              node: id,
              type,
              getContent: () => find(api.getDoc(), id)?.p?.content,
              urls: api.urls,
              csrf: api.csrf,
              onContent: (content) => {
                const now = find(api.getDoc(), id);
                if (!now || JSON.stringify(now.p?.content ?? {}) === JSON.stringify(content)) return;
                commit([{ op: "setProp", id, key: "content", value: content }], { coalesce: true, label: "Change section content" });
              },
            });
          })
          .catch((err) => wait.replaceWith(h("p", { class: "hm-muted", role: "alert", text: `The section form did not load. Reload the page. ${err.message}` })));
      }
    } else if (!props.length && !ITEMS.has(node.t)) {
      panel.append(h("p", { class: "hm-muted", text: "This element has no content settings." }));
    }
    return panel;
  }

  function advancedPanel(node) {
    const panel = h("div", { class: "hm-bs-advanced" });
    const idf = `hm-bs-id${++uid}`;
    const idEl = h("input", { id: idf, class: "hm-field__control", type: "text", readonly: true });
    idEl.value = node.id;
    panel.append(h("div", { class: "hm-field hm-bs-field" }, h("label", { class: "hm-field__label", for: idf, text: "Element ID (read only)" }), idEl));
    for (const p of kindInfo(node.t)?.props ?? []) if (p.key === "hidden" || (p.key === "tag" && p.enum)) panel.append(propField(node, { ...p }));
    import("@hm/builder/elementsettings").then((m) => m.advancedExtras(node, panel, { api, commit })).catch(console.error);
    return panel;
  }

  return { propField, contentPanel, advancedPanel };
}
