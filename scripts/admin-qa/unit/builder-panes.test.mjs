// The builder panes' pure limits, clamp and storage logic. dom.js is only touched when
// mountPanes runs, so the module imports DOM-free.
// Run: node --test scripts/admin-qa/unit/builder-panes.test.mjs
import { register } from "node:module";
import { test } from "node:test";
import assert from "node:assert/strict";

const root = new URL("../../../backend/web/admin/js/", import.meta.url).href;
register(
  "data:text/javascript," +
    encodeURIComponent(`export async function resolve(s, c, next) {
      if (s.startsWith("@hm/")) return { url: ${JSON.stringify(root)} + s.slice(4) + ".js", shortCircuit: true };
      return next(s, c);
    }`),
);
const { LIMITS, CANVAS_MIN, SPLIT, defaults, limits, clampPanes, loadPanes, savePanes } = await import(new URL("../../../backend/web/admin/js/builder/panes.js", import.meta.url));

const store = (init = {}) => ({ m: { ...init }, getItem(k) { return k in this.m ? this.m[k] : null; }, setItem(k, v) { this.m[k] = v; } });
const canvasOf = (s, total) => total - (s.navOpen ? s.nav + SPLIT : 0) - (s.inspOpen ? s.insp + SPLIT : 0);

test("defaults sit inside the limits", () => {
  const d = defaults(16);
  assert.ok(d.nav >= LIMITS.nav.min * 16 && d.nav <= LIMITS.nav.max * 16);
  assert.ok(d.insp >= LIMITS.insp.min * 16 && d.insp <= LIMITS.insp.max * 16);
  assert.ok(d.navOpen && d.inspOpen);
});

test("limits: rem bounds, and the canvas keeps its minimum", () => {
  const d = defaults(16);
  assert.deepEqual(limits("nav", d, 1920, 16), { min: 192, max: 640 });
  assert.deepEqual(limits("insp", d, 1920, 16), { min: 256, max: 768 });
  const l = limits("insp", d, 1280, 16);
  assert.equal(l.max, 1280 - CANVAS_MIN - 2 * SPLIT - d.nav);
  // a closed neighbour frees its room
  assert.ok(limits("insp", { ...d, navOpen: false }, 1280, 16).max > l.max);
  // never below min, however narrow
  assert.equal(limits("nav", d, 500, 16).max, 192);
});

test("clampPanes: bounds, canvas floor, the Navigator pane yields first", () => {
  for (const total of [960, 1100, 1280, 1920, 3000]) {
    const s = clampPanes({ nav: 9999, insp: 9999, navOpen: true, inspOpen: true }, total, 16);
    assert.ok(s.nav >= 192 && s.nav <= 640 && s.insp >= 256 && s.insp <= 768, String(total));
    assert.ok(canvasOf(s, total) >= CANVAS_MIN, `${total}: ${canvasOf(s, total)}`);
  }
  const small = clampPanes({ nav: 1, insp: 1, navOpen: true, inspOpen: true }, 1920, 16);
  assert.deepEqual([small.nav, small.insp], [192, 256]);
  const squeezed = clampPanes({ nav: 400, insp: 600, navOpen: true, inspOpen: true }, 1100, 16);
  assert.equal(squeezed.insp, 412);
  assert.equal(squeezed.nav, 192);
  assert.ok(canvasOf(squeezed, 1100) >= CANVAS_MIN);
  // a collapsed pane's width is left alone
  const c = clampPanes({ nav: 400, insp: 400, navOpen: false, inspOpen: true }, 1280, 16);
  assert.equal(c.nav, 400);
});

test("load and save round-trip per user", () => {
  const st = store();
  savePanes(st, "u1", { nav: 300.4, insp: 500, navOpen: false, inspOpen: true });
  assert.deepEqual(loadPanes(st, "u1", 16), { nav: 300, insp: 500, navOpen: false, inspOpen: true });
  assert.deepEqual(loadPanes(st, "u2", 16), defaults(16));
});

test("load tolerates bad storage", () => {
  const d = defaults(16);
  for (const raw of ["not json", "null", "[]", "5", '{"nav":"x","insp":-3,"navClosed":"yes"}', '{"nav":null}', '{"nav":1e9,"insp":99999}']) {
    assert.deepEqual(loadPanes(store({ "hm-builder-panes:": raw }), "", 16), d, raw);
  }
  const throwing = { getItem() { throw new Error("denied"); }, setItem() { throw new Error("full"); } };
  assert.deepEqual(loadPanes(throwing, "", 16), d);
  assert.doesNotThrow(() => savePanes(throwing, "", d));
  assert.deepEqual(loadPanes(undefined, "", 16), d);
});
