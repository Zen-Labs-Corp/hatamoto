// Page boot and tracker, with just enough fake DOM. Node cannot resolve the @hm
// import map, so a resolver hook maps @hm/aia/* onto the source files.
// Run: node --test scripts/admin-qa/unit/aia-page.test.mjs
import { register } from "node:module";
import { test } from "node:test";
import assert from "node:assert/strict";

const root = new URL("../../../backend/web/admin/js/", import.meta.url).href;
register(
  "data:text/javascript," +
    encodeURIComponent(`export async function resolve(s, c, next) {
      if (s.startsWith("@hm/aia/")) return { url: ${JSON.stringify(root)} + s.slice(4) + ".js", shortCircuit: true };
      return next(s, c);
    }`),
);

const fakeNode = (extra = {}) => ({ hidden: false, style: {}, dataset: {}, setAttribute() {}, removeAttribute() {}, replaceChildren() {}, append() {}, ...extra });

test("a failed chart request leaves the server-rendered data table and its button alone", async () => {
  const table = fakeNode();
  const showData = fakeNode();
  const plot = fakeNode();
  const parts = { "[data-aia-plot]": plot, "[data-aia-data]": table, "[data-aia-sd]": showData };
  const fig = fakeNode({
    dataset: { hmAiaChart: "time", src: "/admin/analytics/data/overview.json", chart: "fetches" },
    querySelector: (sel) => parts[sel] || null,
    contains: () => false,
  });
  globalThis.document = {
    activeElement: null,
    createElement: () => fakeNode(),
    querySelector: () => null,
    querySelectorAll: (sel) => (sel === "[data-hm-aia-chart]" ? [fig] : []),
    addEventListener() {},
  };
  globalThis.fetch = () => Promise.reject(new Error("offline"));
  try {
    await import(new URL("aia/page.js", root));
    await new Promise((r) => setTimeout(r, 20));
  } finally {
    delete globalThis.document;
    delete globalThis.fetch;
  }
  assert.equal(table.hidden, false, "data table hidden after a failed fetch");
  assert.equal(showData.hidden, false, "Show data button hidden after a failed fetch");
});

test("the tracker reads a block's label once: its aria-label, not also a live region", async () => {
  const { enhanceTracker } = await import(new URL("aia/tracker.js", root));
  let focus;
  const block = { setAttribute() {}, getAttribute: () => "Sep 15: 3 fetches", addEventListener: (t, fn) => t === "focus" && (focus = fn) };
  const appended = [];
  const tracker = { querySelectorAll: () => [block], querySelector: () => null, append: (n) => appended.push(n), addEventListener() {} };
  assert.doesNotThrow(() => {
    enhanceTracker(tracker);
    focus();
  });
  assert.equal(appended.length, 0, "no live region is added");
});
