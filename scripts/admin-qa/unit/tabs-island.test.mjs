// The tabs island wires tab and panel by id but keeps an id the author gave the panel (its anchor).
// Run: node --test scripts/admin-qa/unit/tabs-island.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const src = readFileSync(new URL("../../../backend/internal/render/widgets/tabs/island.js", import.meta.url), "utf8");

function node(extra = {}) {
  return { attrs: {}, id: "", setAttribute(k, v) { this.attrs[k] = v; }, append() {}, prepend() {}, classList: { add() {} }, addEventListener() {}, ...extra };
}
function run(ids) {
  const panels = ids.map((id) => node({ id, matches: () => true, querySelector: () => ({ textContent: "T" }) }));
  const box = node({ children: panels });
  const created = [];
  const document = {
    querySelectorAll: () => [box],
    createElement: () => { const e = node(); created.push(e); return e; },
  };
  new Function("document", src.replace(/^export .*$/gm, ""))(document);
  return { panels, tabs: created.filter((e) => e.role === "tab") };
}

test("a panel without an id gets a generated one that its tab controls", () => {
  const { panels, tabs } = run(["", ""]);
  assert.equal(panels[0].id, tabs[0].id + "p");
  assert.equal(tabs[1].attrs["aria-controls"], panels[1].id);
});

test("a panel with an author's anchor keeps it and the tab controls that id", () => {
  const { panels, tabs } = run(["pricing", ""]);
  assert.equal(panels[0].id, "pricing");
  assert.equal(tabs[0].attrs["aria-controls"], "pricing");
  assert.equal(panels[0].attrs["aria-labelledby"], tabs[0].id);
  assert.notEqual(tabs[0].id, "pricing");
});
