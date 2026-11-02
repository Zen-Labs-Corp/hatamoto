// The inline SEO view of the entity and term lists: a field saves when it loses focus after a change.
// Without JavaScript each row is a form with its own Save button; here the button hides and the
// row's hm/savestate shows the outcome. Delegated on the document so an htmx swap keeps working.
import { applyResponse, conflictText, rowDirty, rowValues, showsRetry, stateText, viewRedirect, viewToRemember } from "@hm/seobulklogic";
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
    if (el && !el.disabled) out[n] = el.value; // a held (conflicting) field is not part of the row's saves
  }
  return out;
};
const paint = (form, state, text) => {
  const s = form.querySelector(".hm-savestate");
  if (!s) return;
  s.dataset.state = state;
  const t = s.querySelector(".hm-savestate__text");
  if (t) {
    t.textContent = text ?? stateText(state);
    t.title = t.textContent; // the cell clips a long message
  }
  retryButton(form, state);
};
// The Save button stays hidden for the script's blur saves, except after a failure or a conflict: then
// it is the keyboard-reachable Retry (a conflict keeps the user's value to send again). A later edit of a field saves again on its own as well.
const retryButton = (form, state) => {
  const b = form.querySelector("[data-hm-seobulk-save]");
  if (!b || state === "unsaved") return;
  const show = showsRetry(state);
  const label = b.querySelector("span");
  if (label) label.textContent = show ? "Retry" : "Save";
  if (!show && !b.hidden && b.ownerDocument.activeElement === b) form.elements.namedItem("title")?.focus();
  b.hidden = !show;
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
  // field -> the other user's stored value, per row: held out of every post until Retry
  const held = new WeakMap();
  const heldOf = (form) => held.get(form) ?? {};
  const release = (form) => {
    for (const n of Object.keys(heldOf(form))) {
      const o = form.elements.namedItem(`orig_${n}`);
      if (o) o.disabled = false;
    }
    held.delete(form);
  };
  const paintHeld = (form) => paint(form, "conflict", conflictText(heldOf(form)));
  const queued = new Set();
  const save = async (form, retry = false) => {
    if (retry) release(form); // the explicit overwrite: orig is the stored value, so the user's value goes out
    if (pending.has(form)) {
      queued.add(form); // a change made while a save is in flight is saved once that one is done
      return;
    }
    const fields = fieldsOf(form);
    if (!rowDirty(rowValues(fields), origOf(form))) return;
    pending.add(form);
    const posted = rowValues(fields);
    paint(form, "saving");
    let res = { state: "error", text: stateText("error"), orig: null, refresh: [] };
    let json = null;
    try {
      const r = await fetch(form.getAttribute("action"), { method: "POST", body: new URLSearchParams(new FormData(form)), headers: { Accept: "application/json" } });
      json = await r.json().catch(() => null);
      res = applyResponse(r.status, json, posted, rowValues(fieldsOf(form)));
    } catch {
      /* network failure: the error state stays */
    }
    pending.delete(form);
    if (res.orig) {
      for (const [n, v] of Object.entries(res.orig)) {
        const o = form.elements.namedItem(`orig_${n}`);
        if (o && !o.disabled) o.value = v;
      }
    }
    for (const [n, v] of Object.entries(res.held ?? {})) {
      const o = form.elements.namedItem(`orig_${n}`);
      if (o) o.disabled = true;
      held.set(form, { ...heldOf(form), [n]: v });
    }
    for (const n of res.refresh) {
      if (n in heldOf(form)) continue; // the user's value stays until Retry
      const f = fields[n];
      if (f?.type === "checkbox") f.checked = res.orig[n] === "true";
      else if (f) f.value = res.orig[n] ?? "";
    }
    for (const [n, c] of Object.entries(json?.counters ?? {})) paintCount(form, n, c);
    paint(form, res.state, res.text);
    if (res.state !== "error" && Object.keys(heldOf(form)).length) paintHeld(form); // the notice stays until Retry
    if (queued.delete(form)) return save(form);
    // an edit made while the save was in flight is still unsaved
    if (res.state === "saved" && !Object.keys(heldOf(form)).length && rowDirty(rowValues(fieldsOf(form)), origOf(form))) paint(form, "unsaved");
  };
  const rowForm = (el) => (el?.form?.querySelector?.("[data-hm-seobulk-save]") ? el.form : null);

  doc.addEventListener("input", (e) => {
    const form = rowForm(e.target);
    if (form && Object.keys(heldOf(form)).length) {
      const n = e.target.name;
      if (n in heldOf(form) && e.target.value === heldOf(form)[n]) {
        // the user typed the other value back: nothing left to overwrite
        const rest = { ...heldOf(form) };
        delete rest[n];
        const o = form.elements.namedItem(`orig_${n}`);
        if (o) o.disabled = false;
        if (Object.keys(rest).length) held.set(form, rest);
        else held.delete(form);
        if (!Object.keys(rest).length) paint(form, "idle");
      }
      return; // typing keeps the conflict notice
    }
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
    save(form, true);
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
