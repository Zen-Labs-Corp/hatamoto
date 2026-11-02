// The inline SEO view of the entity and term lists: a field saves when it loses focus after a change.
// Without JavaScript each row is a form with its own Save button; here the button hides and the
// row's hm/savestate shows the outcome. Delegated on the document so an htmx swap keeps working.
import { applyResponse, rowDirty, rowValues, stateText, viewRedirect, viewToRemember } from "@hm/seobulklogic";
import { safeStorage } from "@hm/shelllogic";

const NAMES = ["title", "description", "keyword", "noindex"];

const fieldsOf = (form) => {
  const out = {};
  for (const n of NAMES) {
    const el = form.elements.namedItem(n);
    if (el) out[n] = el;
  }
  return out;
};
const origOf = (form) => {
  const out = {};
  for (const n of NAMES) {
    const el = form.elements.namedItem(`orig_${n}`);
    if (el) out[n] = el.value;
  }
  return out;
};
const paint = (form, state, text) => {
  const s = form.querySelector(".hm-savestate");
  if (!s) return;
  s.dataset.state = state;
  const t = s.querySelector(".hm-savestate__text");
  if (t) t.textContent = text ?? stateText(state);
};
const paintCount = (form, name, c) => {
  const p = document.getElementById(`${form.id}-${name}-count`);
  if (!p || !c) return;
  p.className = `hm-be__count hm-be__count--${c.status}`;
  const spans = p.querySelectorAll("span");
  const max = spans[0]?.textContent.split("/")[1]?.trim() ?? "";
  if (spans[0]) spans[0].textContent = `${c.len} / ${max}`;
  if (spans[1]) spans[1].textContent = c.msg ?? "";
};

/** Wires the list. Returns what a test needs. */
export function mountSEOBulk(doc = document) {
  const pending = new Set();
  const save = async (form) => {
    if (pending.has(form)) return;
    const fields = fieldsOf(form);
    if (!rowDirty(rowValues(fields), origOf(form))) return;
    pending.add(form);
    paint(form, "saving");
    let res = { state: "error", text: stateText("error"), orig: null, refresh: [] };
    let json = null;
    try {
      const r = await fetch(form.getAttribute("action"), { method: "POST", body: new URLSearchParams(new FormData(form)), headers: { Accept: "application/json" } });
      json = await r.json().catch(() => null);
      res = applyResponse(r.status, json);
    } catch {
      /* network failure: the error state stays */
    }
    pending.delete(form);
    if (res.orig) {
      for (const [n, v] of Object.entries(res.orig)) {
        const o = form.elements.namedItem(`orig_${n}`);
        if (o) o.value = v;
      }
    }
    for (const n of res.refresh) {
      const f = fields[n];
      if (f?.type === "checkbox") f.checked = json.values[n] === true;
      else if (f) f.value = json.values[n] ?? "";
    }
    for (const [n, c] of Object.entries(json?.counters ?? {})) paintCount(form, n, c);
    paint(form, res.state, res.text);
    // an edit made while the save was in flight is still unsaved
    if (res.state === "saved" && rowDirty(rowValues(fieldsOf(form)), origOf(form))) paint(form, "unsaved");
  };
  const rowForm = (el) => (el?.form?.querySelector?.("[data-hm-seobulk-save]") ? el.form : null);

  doc.addEventListener("input", (e) => {
    const form = rowForm(e.target);
    if (form && e.target.type !== "checkbox") paint(form, rowDirty(rowValues(fieldsOf(form)), origOf(form)) ? "unsaved" : "idle");
  });
  // change fires on blur after an edit and on a switch toggle; input never saves
  doc.addEventListener("change", (e) => {
    const form = rowForm(e.target);
    if (form) save(form);
  });
  doc.addEventListener("submit", (e) => {
    const form = e.target;
    if (!form.querySelector?.("[data-hm-seobulk-save]")) return;
    e.preventDefault();
    save(form);
  });
  // a swap must not drop a row that is saving
  doc.addEventListener("htmx:beforeSwap", (e) => {
    if (doc.querySelector('.hm-savestate[data-state="saving"]')) e.preventDefault();
  });
  for (const b of doc.querySelectorAll("[data-hm-seobulk-save]")) b.hidden = true;
  return { save };
}

const win = globalThis;
if (win.document && !win.document.documentElement.dataset.hmSeobulk && win.document.querySelector("[data-hm-seobulk-save]")) {
  win.document.documentElement.dataset.hmSeobulk = "1";
  mountSEOBulk();
}
// the module is only declared by the entity and term lists, so every load here is such a list
if (win.document && win.location) {
  const store = safeStorage("local");
  const key = `hm-seo-view:${win.location.pathname}`;
  const to = viewRedirect(win.location.search, store?.getItem(key));
  if (to) win.location.replace(to);
  else store?.setItem(key, viewToRemember(win.location.search));
}
