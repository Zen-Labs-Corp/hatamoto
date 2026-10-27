// Robots policy pending bar, the DOM-free part. Node cannot resolve the @hm
// import map, so a resolver hook maps @hm/aia/* onto the source files.
// Run: node --test scripts/admin-qa/unit/aia-policy.test.mjs
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
const { pendingCount, pendingText } = await import(new URL("aia/policy.js", root));

test("imports in Node with no document", () => {
  assert.equal(typeof document, "undefined");
});

test("pendingCount counts the purposes that differ from the saved value", () => {
  assert.equal(pendingCount([]), 0);
  assert.equal(pendingCount([{ saved: "allow", current: "allow" }, { saved: "deny", current: "deny" }]), 0);
  assert.equal(pendingCount([{ saved: "allow", current: "deny" }, { saved: "deny", current: "deny" }, { saved: "deny", current: "allow" }]), 2);
});

test("pendingText is singular for one change and plural otherwise", () => {
  assert.equal(pendingText(1), "1 policy change is not saved yet.");
  assert.equal(pendingText(3), "3 policy changes are not saved yet.");
});

const { blockedSet, applyPolicy } = await import(new URL("aia/policy.js", root));

// A node with just what applyPolicy touches.
const node = (attr, value) => ({ dataset: { [attr]: value }, hidden: false, textContent: "" });
const page = (nodes) => ({ querySelectorAll: (sel) => nodes[sel] || [] });

test("blockedSet is the purposes whose current choice is deny", () => {
  const b = blockedSet([{ purpose: "training", current: "deny" }, { purpose: "seo", current: "allow" }, { purpose: "user_fetch", current: "deny" }]);
  assert.deepEqual([...b].sort(), ["training", "user_fetch"]);
});

test("applyPolicy shows the Blocked by policy badges of blocked purposes only and counts ignorers", () => {
  const rows = { gpt: node("aiaBlocked", "training"), claude: node("aiaBlocked", "training"), chat: node("aiaBlocked", "user_fetch"), ok: node("aiaAllowed", "training") };
  const ig = [node("aiaIgnorer", "user_fetch"), node("aiaIgnorer", "user_fetch"), node("aiaIgnorer", "other_ai")];
  const count = { dataset: {}, textContent: "" };
  const root = page({ "[data-aia-blocked]": [rows.gpt, rows.claude, rows.chat], "[data-aia-allowed]": [rows.ok], "[data-aia-ignorer]": ig, "[data-aia-ignoring-count]": [count] });

  assert.equal(applyPolicy(root, new Set(["user_fetch"])), 2);
  assert.deepEqual([rows.gpt.hidden, rows.claude.hidden, rows.chat.hidden, rows.ok.hidden], [true, true, false, false]);
  assert.equal(count.textContent, "2");

  // blocking Training too: its rows show the badge, its "Allowed" mark goes
  applyPolicy(root, new Set(["user_fetch", "training"]));
  assert.deepEqual([rows.gpt.hidden, rows.claude.hidden, rows.chat.hidden, rows.ok.hidden], [false, false, false, true]);

  // Discard back to nothing blocked: every badge hidden, count 0
  assert.equal(applyPolicy(root, new Set()), 0);
  assert.deepEqual([rows.gpt.hidden, rows.claude.hidden, rows.chat.hidden, rows.ok.hidden], [true, true, true, false]);
  assert.equal(count.textContent, "0");
});

const { enhance } = await import(new URL("aia/policy.js", root));

test("the first unsaved change is announced through a live region outside the hidden bar", () => {
  // The bar starts hidden (display: none), where a live region is not in the
  // accessibility tree, so the announcement goes to [data-aia-live], which sits
  // outside the bar and stays rendered.
  const bar = { hidden: true };
  const count = { textContent: "" };
  const live = { textContent: "" };
  const discard = { hidden: true, addEventListener() {} };
  const input = { name: "purpose.training", value: "deny", checked: true };
  const unsaved = { hidden: true };
  const fs = {
    dataset: { aiaSaved: "allow" },
    querySelector: (sel) => (sel === "input" || sel === "input:checked" ? input : null),
    parentElement: { querySelector: () => unsaved },
  };
  const nodes = { "[data-aia-pending]": bar, "[data-aia-count]": count, "[data-aia-live]": live, "[data-aia-discard]": discard };
  let onChange;
  const form = {
    querySelector: (sel) => nodes[sel] || null,
    querySelectorAll: (sel) => (sel === "[data-aia-saved]" ? [fs] : []),
    addEventListener: (type, fn) => type === "change" && (onChange = fn),
  };
  globalThis.document = page({});
  globalThis.addEventListener = () => {};
  try {
    enhance(form);
    onChange();
  } finally {
    delete globalThis.document;
    delete globalThis.addEventListener;
  }
  assert.equal(bar.hidden, false);
  assert.equal(count.textContent, "1 policy change is not saved yet.");
  assert.equal(live.textContent, "1 policy change is not saved yet.");
});
