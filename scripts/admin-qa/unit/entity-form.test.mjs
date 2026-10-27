// The entity form's save indicator and parent chip. Run: node --test scripts/admin-qa/unit/entity-form.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";

const { selectedLabel, mountEntityForm } = await import(new URL("../../../backend/web/admin/js/entityform.js", import.meta.url));

function rig() {
  const state = { dataset: { state: "saved" } };
  const text = { textContent: "Saved 1 Mar 2026" };
  state.querySelector = () => text;
  const chip = { textContent: "No parent (top level)" };
  const on = {};
  const form = {
    addEventListener: (t, f) => (on[t] = f),
    querySelector: (sel) => (sel.includes("savestate") ? state : sel === "[data-hm-parent-chip] .hm-badge" ? chip : null),
  };
  mountEntityForm(form);
  return { state, text, chip, on };
}
const select = (label) => ({ id: "parent", name: "parent", selectedOptions: [{ textContent: ` ${label} ` }] });

test("an edit flips the indicator to Unsaved changes, a submit to Saving", () => {
  const r = rig();
  r.on.input({ target: { name: "name" } });
  assert.deepEqual([r.state.dataset.state, r.text.textContent], ["unsaved", "Unsaved changes"]);
  r.on.submit({});
  assert.deepEqual([r.state.dataset.state, r.text.textContent], ["saving", "Saving…"]);
});

test("searching for a parent is not an edit", () => {
  const r = rig();
  r.on.input({ target: { name: "parent_q" } });
  assert.equal(r.state.dataset.state, "saved");
});

test("picking a parent updates the chip", () => {
  const r = rig();
  r.on.change({ target: select("Services") });
  assert.equal(r.chip.textContent, "Services");
  assert.equal(selectedLabel(null), "");
});
