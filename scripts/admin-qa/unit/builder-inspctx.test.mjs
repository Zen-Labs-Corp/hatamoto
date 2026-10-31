// The Style tab's context line: breakpoint pill, State dropdown, override note.
// Run: node --test scripts/admin-qa/unit/builder-inspctx.test.mjs
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

class El {
  constructor(tag) {
    Object.assign(this, { tag, attrs: {}, kids: [], on: {}, textContent: "", value: "" });
  }
  setAttribute(k, v) { this.attrs[k] = String(v); }
  getAttribute(k) { return this.attrs[k] ?? null; }
  append(...k) { this.kids.push(...k); }
  addEventListener(t, f) { (this.on[t] ??= []).push(f); }
  walk(f) { f(this); this.kids.forEach((k) => k.walk(f)); }
}
globalThis.document = { createElement: (t) => new El(t) };

const { overrideNote, stateChoices, contextLine, STATE_CHOICES } = await import(new URL("builder/inspctx.js", root));
const find = (root, pred) => { const r = []; root.walk((n) => pred(n) && r.push(n)); return r; };

test("override note per device", () => {
  assert.equal(overrideNote("base"), "Applies to every screen size unless Tablet or Phone override it.");
  assert.equal(overrideNote("md"), "Values set here override Desktop on screens ≤1024px.");
  assert.equal(overrideNote("sm"), "Values set here override Tablet on screens ≤640px.");
});

test("override note names the state when one is chosen", () => {
  assert.match(overrideNote("md", "hover"), /≤1024px\. Applies only in the Hover state\.$/);
  assert.match(overrideNote("base", "active"), /Pressed state\.$/);
});

test("Parent hover is offered only for a parent that can take it", () => {
  const ids = (parent) => stateChoices(parent).map(([id]) => id);
  assert.deepEqual(ids({ t: "container" }), STATE_CHOICES.map(([id]) => id));
  assert.equal(ids({ t: "ref" }).includes("parent-hover"), false);
  assert.equal(ids({ t: "component" }).includes("parent-hover"), false);
  assert.equal(ids(null).includes("parent-hover"), false);
});

test("the line holds a pill, a labelled State select and the note it is described by", () => {
  const line = contextLine({ device: "md", state: "focus", choices: stateChoices({ t: "container" }), onState() {} });
  assert.equal(find(line, (n) => n.attrs.class === "hm-bs-ctx__bp")[0].textContent, "Tablet");
  const [select] = find(line, (n) => n.tag === "select");
  assert.equal(select.attrs["aria-label"], "State");
  assert.equal(select.value, "focus");
  const [note] = find(line, (n) => n.attrs.class === "hm-bs-ctx__note");
  assert.equal(select.attrs["aria-describedby"], note.attrs.id);
  assert.equal(note.textContent, overrideNote("md", "focus"));
  assert.deepEqual(find(select, (n) => n.tag === "option").map((o) => o.textContent), ["Normal", "Hover", "Focus", "Pressed", "Parent hover"]);
  assert.equal(find(line, (n) => "style" in n.attrs).length, 0, "no style attribute");
});

test("changing the select calls onState with the state key, Normal is the empty key", () => {
  const got = [];
  const line = contextLine({ device: "base", onState: (v) => got.push(v) });
  const [select] = find(line, (n) => n.tag === "select");
  select.value = "hover";
  select.on.change.forEach((f) => f());
  select.value = "";
  select.on.change.forEach((f) => f());
  assert.deepEqual(got, ["hover", ""]);
});
