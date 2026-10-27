// Pending-changes bar of the robots policy form (hm/aia-policy-start/-end).
// The form works without this: every purpose posts as purpose.<id> = allow|deny
// and the bar's Save is a plain submit button. This only adds the live part: it
// compares each Allow / Block pair with the value the page was saved with
// (data-aia-saved on the fieldset), shows how many differ, marks the changed
// purposes "Unsaved change", and lets Discard put them back. The pure pieces are
// exported for the unit test; the DOM part runs only when a form is on the page.
//
// The choice also drives what the rest of the page says about it, as the
// prototype's refreshAgents did, from the radios (unsaved choices count, so
// Discard puts it all back). Server-rendered hooks, each carrying a purpose id:
//   [data-aia-blocked="training"]   shown only while that purpose is blocked ("Blocked by policy")
//   [data-aia-allowed="training"]   shown only while it is allowed
//   [data-aia-ignorer="user_fetch"] one per agent that ignores robots.txt and fetched pages;
//                                   those on a blocked purpose are counted into
//   [data-aia-ignoring-count]       the number shown by the attention and compliance cards
import { fetchJSON, forget } from "@hm/aia/load";

// Not @hm/adminpath: the unit-test resolver maps only @hm/aia/* (aia-policy.test.mjs), so @hm/adminpath would not resolve.
// Keep this expression the same as adminpath.js.
const adminBase = () => (globalThis.document?.querySelector?.('meta[name="admin-base"]')?.content || "/admin").replace(/\/+$/, "");
const robots = () => adminBase() + "/analytics/data/robots.json";

/** How many of `sets` ([{saved, current}]) differ from what was saved. */
export const pendingCount = (sets) => sets.filter((s) => s.saved !== s.current).length;

/** The bar's sentence for n pending changes. */
export const pendingText = (n) => (n === 1 ? "1 policy change is not saved yet." : n + " policy changes are not saved yet.");

/** The ids of the purposes whose current choice is Block, from [{purpose, current}]. */
export const blockedSet = (sets) => new Set(sets.filter((s) => s.current === "deny").map((s) => s.purpose));

/**
 * Show or hide what the page says about each purpose and update the count of
 * agents ignoring robots.txt while blocked. `root` is any node with
 * querySelectorAll; `blocked` is a Set of purpose ids. Returns that count.
 */
export function applyPolicy(root, blocked) {
  for (const el of root.querySelectorAll("[data-aia-blocked]")) el.hidden = !blocked.has(el.dataset.aiaBlocked);
  for (const el of root.querySelectorAll("[data-aia-allowed]")) el.hidden = blocked.has(el.dataset.aiaAllowed);
  let n = 0;
  for (const el of root.querySelectorAll("[data-aia-ignorer]")) if (blocked.has(el.dataset.aiaIgnorer)) n++;
  for (const el of root.querySelectorAll("[data-aia-ignoring-count]")) el.textContent = String(n);
  return n;
}

const IDLE_TEXT = "Save applies every choice here to robots.txt.";

export function enhance(form) {
  const bar = form.querySelector("[data-aia-pending]");
  const count = form.querySelector("[data-aia-count]");
  const discard = form.querySelector("[data-aia-discard]");
  // The announcement goes to a region outside the bar: while the bar is hidden
  // it is not in the accessibility tree, and the first change would be silent.
  const live = form.querySelector("[data-aia-live]");
  if (!bar || !count) return;
  const sets = () => [...form.querySelectorAll("[data-aia-saved]")];
  const valueOf = (fs) => {
    const on = fs.querySelector("input:checked");
    return on ? on.value : fs.dataset.aiaSaved;
  };
  const dirtyOf = (fs) => valueOf(fs) !== fs.dataset.aiaSaved;

  const purposeOf = (fs) => fs.querySelector("input").name.replace(/^purpose\./, "");
  const refresh = () => {
    const choices = sets().map((fs) => ({ purpose: purposeOf(fs), saved: fs.dataset.aiaSaved, current: valueOf(fs) }));
    const n = pendingCount(choices);
    applyPolicy(document, blockedSet(choices));
    for (const fs of sets()) {
      const badge = fs.parentElement.querySelector("[data-aia-unsaved]");
      if (badge) badge.hidden = !dirtyOf(fs);
    }
    bar.hidden = n === 0;
    if (discard) discard.hidden = n === 0;
    count.textContent = n === 0 ? IDLE_TEXT : pendingText(n);
    if (live) live.textContent = n === 0 ? "" : pendingText(n);
  };

  form.addEventListener("change", refresh);
  if (discard)
    discard.addEventListener("click", () => {
      const first = sets().find(dirtyOf);
      for (const fs of sets()) {
        const back = fs.querySelector(`input[value="${fs.dataset.aiaSaved}"]`);
        if (back) back.checked = true;
      }
      refresh();
      // The focused control may have just been reset: land on the first one that changed.
      const target = first && first.querySelector("input:checked");
      if (target) target.focus();
    });

  // A page restored from the back/forward cache keeps old radios and an old baseline:
  // ask the server for the truth and re-baseline "saved" against it (the radios are left alone).
  globalThis.addEventListener("pageshow", async (e) => {
    if (!e.persisted) return;
    forget(robots());
    try {
      const body = await fetchJSON(robots());
      for (const p of body.purposes) {
        const fs = form.querySelector(`input[name="purpose.${p.purpose}"]`)?.closest("[data-aia-saved]");
        if (!fs) continue;
        fs.dataset.aiaSaved = p.action; // the radios keep what the person chose
      }
    } catch {
      /* keep what the page shows; Save still posts every choice */
    }
    refresh();
  });
  refresh();
}

if (typeof document !== "undefined") {
  for (const form of document.querySelectorAll("form[data-aia-policy]")) enhance(form);
}
