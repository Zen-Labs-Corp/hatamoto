// The phone settings drawer: focus mode, server-side field errors, Escape and focus return.
// Node has no DOM, so the elements are small stubs. Run: node --test scripts/admin-qa/unit/blog-drawer.test.mjs
import { register } from "node:module";
import { test } from "node:test";
import assert from "node:assert/strict";

register(
  "data:text/javascript," +
    encodeURIComponent(`export async function resolve(s, c, next) {
      if (s === "@hm/dates") return { url: ${JSON.stringify(new URL("../../../backend/web/admin/js/dates.js", import.meta.url).href)}, shortCircuit: true };
      if (s.startsWith("@hm/")) return { url: "data:text/javascript,export const createAutosave=()=>({}),createRecovery=()=>({}),recoveryAction=()=>({});", shortCircuit: true };
      return next(s, c);
    }`),
);
const { mountSettingsDrawer } = await import(new URL("../../../backend/web/admin/js/blogeditor.js", import.meta.url));

function rig({ matches = true, invalid = false } = {}) {
  const log = [];
  const handlers = {};
  const doc = {
    addEventListener: (t, f) => ((handlers[t] ??= []).push(f)),
    querySelector: () => null,
    getElementById: (id) => (id === "slug" ? slugField : null),
  };
  const panel = { hidden: true, id: "p-seo" };
  const seoTab = { click: () => { panel.hidden = false; log.push("show seo tab"); } };
  const slugField = { focus: () => log.push("focus slug"), closest: () => panel };
  const attrs = new Set();
  const closeBtn = { focus: () => log.push("focus close"), addEventListener: (t, f) => (closeBtn.on = f) };
  const side = {
    classList: { toggle: () => {} },
    hasAttribute: (n) => attrs.has(n),
    setAttribute: (n) => attrs.add(n),
    removeAttribute: (n) => attrs.delete(n),
    querySelector: (sel) => (sel.includes("aria-invalid") ? (invalid ? {} : null) : sel === "[data-hm-be-settings-close]" ? closeBtn : sel.includes("aria-controls") ? seoTab : null),
    contains: (el) => el === slugField,
  };
  const opener = { isConnected: true, focus: () => log.push("focus opener"), closest: () => null, addEventListener: (t, f) => (opener.on = f) };
  const main = { inert: false };
  const bar = { inert: false };
  const narrow = { matches, addEventListener() {} };
  let focus = true;
  mountSettingsDrawer({ doc, side, openers: [opener], behind: [main, bar], narrow, fallbackFocus: opener, leaveFocus: () => { if (focus) { focus = false; log.push("leave focus mode"); } } });
  const fire = (t, e) => handlers[t].forEach((f) => f(e));
  return { log, attrs, main, bar, opener, closeBtn, fire, doc };
}

test("opening leaves focus mode first, makes the page inert and Close returns focus", () => {
  const r = rig();
  r.opener.on();
  assert.deepEqual(r.log.slice(0, 2), ["leave focus mode", "focus close"]);
  assert.ok(r.attrs.has("data-open"));
  assert.equal(r.main.inert && r.bar.inert, true);
  r.closeBtn.on();
  assert.equal(r.attrs.has("data-open"), false);
  assert.equal(r.main.inert || r.bar.inert, false);
  assert.equal(r.log.at(-1), "focus opener");
});

test("Escape closes the drawer", () => {
  const r = rig();
  r.opener.on();
  r.fire("keydown", { key: "Escape" });
  assert.equal(r.attrs.has("data-open"), false);
});

test("a drawer holding a rejected field opens on load, a wide screen does not", () => {
  assert.ok(rig({ invalid: true }).attrs.has("data-open"));
  assert.equal(rig({ invalid: true, matches: false }).attrs.has("data-open"), false);
  assert.equal(rig().attrs.has("data-open"), false);
});

test("a summary link to a field inside the closed drawer opens it and focuses the field", () => {
  const r = rig();
  let prevented = false;
  r.fire("click", { preventDefault: () => (prevented = true), target: { closest: () => ({ getAttribute: () => "#slug" }) } });
  assert.ok(prevented && r.attrs.has("data-open"));
  assert.deepEqual(r.log.slice(-2), ["show seo tab", "focus slug"]); // the field's tab opens before it is focused
});

test("⋯ menu stand-in items are hidden at the widths where they do not apply, so arrow keys skip them", async () => {
  const { showMenuItemsByWidth } = await import(new URL("../../../backend/web/admin/js/blogeditor.js", import.meta.url));
  const narrow = [{ hidden: true }];
  const drawer = [{ hidden: true }];
  const qs = [];
  const mm = (q) => {
    const m = { q, matches: false, on: () => {}, addEventListener: (t, f) => (m.on = f) };
    qs.push(m);
    return m;
  };
  showMenuItemsByWidth({ querySelectorAll: (sel) => (sel === ".hm-be__menu-narrow" ? narrow : drawer) }, mm);
  assert.equal(narrow[0].hidden && drawer[0].hidden, true); // desktop: both hidden
  assert.deepEqual(qs.map((m) => m.q), ["(max-width: 45rem)", "(max-width: 45rem)"]);
  qs.forEach((m) => ((m.matches = false), m.on()));
  assert.equal(drawer[0].hidden, true); // tablet: the bar has Settings, so ⋯ does not repeat it
  qs.forEach((m) => ((m.matches = true), m.on()));
  assert.equal(narrow[0].hidden, false);
  assert.equal(drawer[0].hidden, false); // phone: the bar button is gone, ⋯ carries Settings
});
