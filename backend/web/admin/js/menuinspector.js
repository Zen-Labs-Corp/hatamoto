// Menu editor inspector: the settings of the selected item, or of several. The
// controls are a view on the item's own form fields (i.N.label and the rest):
// selecting reads them, typing writes them and fires the same events the
// fields would, so the editor sees one set of inputs and a save posts exactly
// what a no-JS save posts. The inspector has no fields of its own that post.
// The row selection comes from @hm/menukeys as a "hm:select" event.
import { init as initSearchSelect } from "@hm/searchselect";

// A class list is checked as the server checks it (menu.ValidateCSSClasses): at
// most 200 characters, no control characters, quotes, backticks or angle
// brackets. The server stays the authority.
const MAX_CLASSES = 200;
const BAD_CLASS_CHAR = /[\u0000-\u001f\u007f-\u009f<>"'`]/;

export function classProblem(text) {
  const joined = text.split(/\s+/).filter(Boolean).join(" ");
  if (joined.length > MAX_CLASSES) return `Use at most ${MAX_CLASSES} characters.`;
  if (BAD_CLASS_CHAR.test(joined)) return "Classes cannot contain quotes, angle brackets or control characters.";
  return "";
}

const tokens = (text) => text.split(/\s+/).filter(Boolean);
const field = (li, suffix) => li.querySelector(`[name$=".${suffix}"]`);
const TYPE_SELECT = ["badge", "icon", "show"];
// Style and colour are pickers: tiles and swatches that act as one radio group each.
const PICKERS = ["style", "color"];
const PICK_OPTION = 'input[type="radio"], [role="radio"]';
const pickValue = (o) => (o.matches("input") ? o.value : o.dataset.value);

// outputOf is the link as the site would write it, from the item's fields. It is
// shown as text, never parsed.
function outputOf(li) {
  const get = (k) => field(li, k)?.value.trim() ?? "";
  const type = li.dataset.type;
  const classes = [...tokens(get("class"))];
  for (const k of ["style", "color"]) {
    const sel = field(li, k);
    const c = sel?.selectedOptions?.[0]?.dataset.class;
    if (c) classes.push(c);
  }
  const attrs = [];
  if (classes.length) attrs.push(`class="${classes.join(" ")}"`);
  if (get("id")) attrs.push(`id="${get("id")}"`);
  if (type !== "none") {
    attrs.push(`href="${type === "custom" ? get("url") : "..."}"`);
    if (field(li, "newtab")?.checked) attrs.push('target="_blank"');
    const rel = [...new Set([...(field(li, "newtab")?.checked ? ["noopener"] : []), ...tokens(get("rel").replace(/,/g, " "))])];
    if (rel.length) attrs.push(`rel="${rel.join(" ")}"`);
  }
  if (get("title")) attrs.push(`title="${get("title")}"`);
  if (get("aria")) attrs.push(`aria-label="${get("aria")}"`);
  const tag = type === "none" ? "span" : "a";
  return `<${tag}${attrs.map((a) => ` ${a}`).join("")}>${get("label")}</${tag}>`;
}

function build(root) {
  const tpl = document.querySelector('template[data-hm-tpl="inspector"]');
  if (!tpl) return null;
  const body = tpl.content.firstElementChild.cloneNode(true);
  // Nothing in the inspector posts: the item's own fields do.
  body.querySelectorAll("[name]").forEach((el) => el.removeAttribute("name"));
  // The unnamed tiles are a group of their own: the arrow keys are handled in init.
  body.querySelectorAll('fieldset[data-hm-ins-group]').forEach((g) => g.setAttribute("role", "radiogroup"));
  // A swatch takes its colour from the theme through the CSSOM (an inline style attribute is blocked by the page's policy).
  body.querySelectorAll("[data-swatch]").forEach((b) => { if (b.dataset.swatch) b.style.setProperty("--swatch", b.dataset.swatch); });
  root.replaceChildren(body);
  initSearchSelect(body);
  return body;
}

function init(rootDoc) {
  rootDoc.querySelectorAll("[data-hm-inspector]").forEach((root) => {
    if (root.dataset.hmReady) return;
    const tree = document.querySelector("[data-hm-menutree]");
    const body = build(root);
    if (!body || !tree) return;
    root.setAttribute("data-hm-ready", "1");

    const q = (sel) => body.querySelector(sel);
    const all = (sel) => [...body.querySelectorAll(sel)];
    const ctl = (k) => q(`[data-hm-ins="${k}"]`);
    const part = {
      empty: q("[data-hm-ins-empty]"),
      summary: q("[data-hm-ins-summary]"),
      single: q("[data-hm-ins-single]"),
      appearance: q("[data-hm-ins-appearance]"),
      advanced: q("[data-hm-ins-advanced]"),
      bulk: q("[data-hm-ins-bulk]"),
      output: q("[data-hm-ins-output-wrap]"),
    };
    let items = [];
    let writing = false; // the inspector's own write is on its way to the fields

    const one = () => (items.length === 1 ? items[0] : null);

    // write sets one field of an item and tells the editor, as typing in it would.
    function write(li, suffix, value) {
      const f = field(li, suffix);
      if (!f) return;
      writing = true;
      if (f.type === "checkbox") f.checked = Boolean(value);
      else f.value = value;
      f.dispatchEvent(new Event(f.tagName === "SELECT" || f.type === "checkbox" ? "change" : "input", { bubbles: true }));
      writing = false;
      if (items.length === 1) q("[data-hm-ins-output]").textContent = outputOf(li);
    }

    // The preset lists belong to the theme and are in each item's own select:
    // mirror them, so a value the theme no longer has still shows.
    function readSelect(li, k) {
      const sel = ctl(k);
      const src = field(li, k);
      const wrap = q(`[data-hm-ins-wrap="${k}"]`);
      wrap.hidden = !src || src.tagName !== "SELECT";
      if (wrap.hidden) return;
      sel.replaceChildren(...[...src.options].map((o) => o.cloneNode(true)));
      sel.value = src.value;
    }

    // markPicker shows value as the chosen option of a picker; none (null) leaves nothing chosen.
    function markPicker(k, value) {
      const opts = [...(q(`[data-hm-ins-group="${k}"]`)?.querySelectorAll(PICK_OPTION) ?? [])];
      const at = opts.findIndex((o) => value !== null && pickValue(o) === value);
      opts.forEach((o, i) => {
        if (o.matches("input")) o.checked = i === at;
        else o.setAttribute("aria-checked", String(i === at));
        o.tabIndex = i === Math.max(at, 0) ? 0 : -1; // one tab stop, on the chosen option
      });
    }

    // readPicker shows what the selected items have: the shared value, or nothing chosen when they differ.
    function readPicker(k) {
      const wrap = q(`[data-hm-ins-wrap="${k}"]`);
      if (!wrap) return;
      const src = field(items[0], k);
      wrap.hidden = !src || src.tagName !== "SELECT";
      if (wrap.hidden) return;
      const values = new Set(items.map((li) => field(li, k)?.value ?? ""));
      markPicker(k, values.size === 1 ? [...values][0] : null);
    }

    function pick(k, value) {
      items.forEach((li) => write(li, k, value));
      markPicker(k, value);
    }

    function syncClassChips(text) {
      const have = new Set(tokens(text));
      all("[data-hm-ins-class]").forEach((b) => b.setAttribute("aria-pressed", String(have.has(b.dataset.hmInsClass))));
    }

    function readSearch(li, box, suffix) {
      const input = box.querySelector("[data-hm-ss-input]");
      const value = box.querySelector("[data-hm-ss-value]");
      const text = field(li, `${suffix}_q`)?.value ?? "";
      if (input.value !== text) input.value = text;
      value.value = field(li, suffix)?.value ?? "";
    }

    function read() {
      const li = one();
      if (li) {
        const label = ctl("label");
        const v = field(li, "label")?.value ?? "";
        if (label.value !== v) label.value = v;
        const type = li.dataset.type;
        all("[data-hm-ins-type]").forEach((b) => b.setAttribute("aria-pressed", String(b.dataset.hmInsType === type)));
        all("[data-hm-ins-target]").forEach((t) => { t.hidden = t.dataset.hmInsTarget !== type; });
        readSearch(li, q('[data-hm-ins-target="entity"] [data-hm-searchselect]'), "entity");
        readSearch(li, q('[data-hm-ins-target="archive"] [data-hm-searchselect]'), "term");
        const url = ctl("url");
        const u = field(li, "url")?.value ?? "";
        if (url.value !== u) url.value = u;
        // The page card: what the item points to, with a way to open it.
        const id = field(li, "entity")?.value ?? "";
        const card = q("[data-hm-ins-page]");
        card.hidden = type !== "entity" || !id;
        if (!card.hidden) {
          q("[data-hm-ins-page-name]").textContent = field(li, "entity_q")?.value ?? "";
          const link = q("[data-hm-ins-page-link]");
          link.href = `${link.dataset.base ??= link.getAttribute("href")}${id}`;
        }
        for (const k of ["rel", "id", "title", "aria"]) {
          const c = ctl(k);
          const val = field(li, k)?.value ?? "";
          if (c.value !== val) c.value = val;
        }
        q("[data-hm-ins-output]").textContent = outputOf(li);
        // A failed save leaves its messages in the row's own fields, which the inspector hides.
        const server = q("[data-hm-ins-server]");
        server.textContent = [...li.querySelectorAll(".hm-field__error")].map((e) => e.textContent.trim()).filter(Boolean).join(" ");
        server.hidden = !server.textContent;
      }
      // Shared by one item and several: the appearance controls start from the first item.
      const first = items[0];
      if (first) {
        const nt = ctl("newtab");
        nt.checked = items.every((it) => field(it, "newtab")?.checked);
        TYPE_SELECT.forEach((k) => readSelect(first, k));
        PICKERS.forEach(readPicker);
        const cls = ctl("class");
        const text = li ? field(li, "class")?.value ?? "" : "";
        if (cls.value !== text) cls.value = text;
        syncClassChips(text);
        q("[data-hm-ins-problem]").hidden = true;
      }
    }

    function show() {
      const n = items.length;
      part.empty.hidden = n > 0;
      part.summary.hidden = n < 2;
      part.summary.textContent = `${n} items selected. Changes apply to all of them.`;
      part.single.hidden = n !== 1;
      part.advanced.hidden = n !== 1;
      part.output.hidden = n !== 1;
      part.appearance.hidden = n === 0;
      part.bulk.hidden = n < 2;
      // With several items "CSS classes" adds to each one's classes.
      const cls = ctl("class");
      const help = document.getElementById("ins-class-help");
      if (help) help.textContent = n > 1 ? "Classes you enter are added to every selected item." : "Separate classes with spaces.";
      cls.value = "";
      if (n) read();
    }

    document.addEventListener("hm:select", (evt) => {
      items = (evt.detail?.items ?? []).filter((li) => li.isConnected);
      show();
    });

    // Any change to an item's fields (a row, a pick, an undo) refreshes the view.
    const reread = () => { if (!writing && items.length) read(); };
    tree.addEventListener("input", reread);
    tree.addEventListener("change", reread);
    tree.addEventListener("hm:picked", reread);

    body.addEventListener("input", (evt) => {
      const li = one();
      const t = evt.target;
      if (t.closest("[data-hm-searchselect]")) {
        // Typing un-picks: the text goes to the item, the picked value is cleared.
        const box = t.closest("[data-hm-searchselect]");
        const suffix = box.closest("[data-hm-ins-target]").dataset.hmInsTarget === "entity" ? "entity" : "term";
        if (li) {
          write(li, `${suffix}_q`, t.value);
          write(li, suffix, "");
        }
        return;
      }
      const k = t.dataset.hmIns;
      if (!k || k === "newtab" || TYPE_SELECT.includes(k)) return;
      if (k === "class") {
        const bad = classProblem(t.value);
        const problem = q("[data-hm-ins-problem]");
        problem.textContent = bad;
        problem.hidden = !bad;
        t.setAttribute("aria-invalid", String(Boolean(bad)));
        if (bad) return;
        if (items.length === 1) {
          write(li, "class", tokens(t.value).join(" "));
          syncClassChips(t.value);
        }
        return;
      }
      if (li) write(li, k, t.value);
    });

    body.addEventListener("change", (evt) => {
      const t = evt.target;
      if (t.matches?.('[data-hm-ins-group] input[type="radio"]')) {
        pick(t.closest("[data-hm-ins-group]").dataset.hmInsGroup, t.value);
        return;
      }
      const k = t.dataset?.hmIns;
      if (!k) return;
      if (k === "newtab") items.forEach((li) => write(li, "newtab", t.checked));
      else if (TYPE_SELECT.includes(k)) items.forEach((li) => write(li, k, t.value));
      else if (k === "class" && items.length > 1 && !classProblem(t.value)) {
        const add = tokens(t.value);
        items.forEach((li) => write(li, "class", [...new Set([...tokens(field(li, "class")?.value ?? ""), ...add])].join(" ")));
        t.value = "";
      }
    });

    // A pick: the value and the shown name go to the item as the item's own box would put them.
    body.addEventListener("hm:picked", (evt) => {
      const li = one();
      const box = evt.target.closest("[data-hm-searchselect]");
      if (!li || !box) return;
      const suffix = box.closest("[data-hm-ins-target]").dataset.hmInsTarget === "entity" ? "entity" : "term";
      const shown = box.querySelector("[data-hm-ss-input]").value;
      writing = true;
      field(li, suffix).value = box.querySelector("[data-hm-ss-value]").value;
      field(li, `${suffix}_q`).value = shown;
      field(li, `${suffix}_prev`).value = shown;
      writing = false;
      li.dispatchEvent(new CustomEvent("hm:picked", { bubbles: true }));
      read();
    });

    // Arrow keys move the choice, as in any radio group; Home and End go to the ends.
    body.addEventListener("keydown", (evt) => {
      const opt = evt.target.closest?.(PICK_OPTION);
      const group = opt?.closest("[data-hm-ins-group]");
      if (!group) return;
      const opts = [...group.querySelectorAll(PICK_OPTION)];
      const i = opts.indexOf(opt);
      const to = { ArrowRight: i + 1, ArrowDown: i + 1, ArrowLeft: i - 1, ArrowUp: i - 1, Home: 0, End: opts.length - 1 }[evt.key];
      if (to === undefined) return;
      evt.preventDefault();
      const next = opts[(to + opts.length) % opts.length];
      next.focus();
      next.click();
    });

    body.addEventListener("click", (evt) => {
      const swatch = evt.target.closest('[role="radio"]');
      if (swatch && swatch.closest("[data-hm-ins-group]")) {
        pick(swatch.closest("[data-hm-ins-group]").dataset.hmInsGroup, swatch.dataset.value);
        return;
      }
      const type = evt.target.closest("[data-hm-ins-type]");
      const chip = evt.target.closest("[data-hm-ins-class]");
      const op = evt.target.closest("[data-hm-ins-op]");
      const li = one();
      if (type && li) {
        const radio = li.querySelector(`[name$=".type"][value="${type.dataset.hmInsType}"]`);
        if (radio) {
          radio.checked = true;
          radio.dispatchEvent(new Event("change", { bubbles: true }));
        }
      } else if (chip) {
        const name = chip.dataset.hmInsClass;
        const on = chip.getAttribute("aria-pressed") !== "true";
        items.forEach((it) => {
          const have = tokens(field(it, "class")?.value ?? "");
          const next = on ? [...new Set([...have, name])] : have.filter((x) => x !== name);
          write(it, "class", next.join(" "));
        });
        if (li) {
          ctl("class").value = field(li, "class").value;
          syncClassChips(ctl("class").value);
        } else chip.setAttribute("aria-pressed", String(on));
      } else if (op) {
        document.dispatchEvent(new CustomEvent("hm:op", { detail: { op: op.dataset.hmInsOp, items: [...items] } }));
      }
    });

    show();
  });
}

document.addEventListener("DOMContentLoaded", () => init(document));
document.addEventListener("htmx:afterSettle", (evt) => init(evt.target));
export { init };
