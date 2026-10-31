// The Fill group's theme colour pills.
// Run: node --test scripts/admin-qa/unit/builder-swatches.test.mjs
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
    Object.assign(this, { tag, attrs: {}, kids: [], on: {}, textContent: "", props: {} });
    this.style = { setProperty: (k, v) => (this.props[k] = v) };
  }
  setAttribute(k, v) { this.attrs[k] = String(v); }
  getAttribute(k) { return this.attrs[k] ?? null; }
  append(...k) { this.kids.push(...k); }
  addEventListener(t, f) { (this.on[t] ??= []).push(f); }
  click() { (this.on.click ?? []).forEach((f) => f()); }
  walk(f) { f(this); this.kids.forEach((k) => k.walk(f)); }
}
globalThis.document = { createElement: (t) => new El(t) };

const { swatchRow } = await import(new URL("builder/swatches.js", root));
const find = (root, pred) => { const r = []; root.walk((n) => pred(n) && r.push(n)); return r; };
const palette = { colors: [{ name: "primary", label: "Brand", hex: "#112233", css: "var(--hm-primary)" }, { name: "bg", label: "Page", hex: "", css: "var(--hm-bg)" }, { name: "accent", hex: "#c8102e" }] };

test("one pill per palette colour, in a labelled group, painted through the custom property", () => {
  const { el } = swatchRow({ name: "Background", palette, apply() {} });
  assert.equal(el.attrs.role, "group");
  assert.equal(el.attrs["aria-label"], "Background theme colours");
  const pills = find(el, (n) => n.tag === "button");
  assert.equal(pills.length, 3);
  assert.deepEqual(pills.map((b) => b.kids[1].textContent), ["Brand", "Page", "accent"]);
  assert.deepEqual(pills.map((b) => b.kids[0].props["--hm-bs-sw"]), ["#112233", "var(--hm-bg)", "#c8102e"], "hex first, the css reference when there is none");
  assert.equal(find(el, (n) => "style" in n.attrs).length, 0, "no style attribute");
  assert.ok(pills.every((b) => b.attrs.type === "button"));
});

test("a click applies the token name, never the hex", () => {
  const got = [];
  const { el } = swatchRow({ name: "Background", palette, apply: (v) => got.push(v) });
  find(el, (n) => n.tag === "button").forEach((b) => b.click());
  assert.deepEqual(got, ["primary", "bg", "accent"]);
});

test("sync marks the pill of the value set here and none for a custom or unset value", () => {
  const { el, sync } = swatchRow({ name: "Background", palette, current: "bg", apply() {} });
  const pressed = () => find(el, (n) => n.tag === "button").map((b) => b.attrs["aria-pressed"]);
  assert.deepEqual(pressed(), ["false", "true", "false"]);
  sync("accent");
  assert.deepEqual(pressed(), ["false", "false", "true"]);
  sync("#abcdef");
  assert.deepEqual(pressed(), ["false", "false", "false"]);
  sync(null);
  assert.deepEqual(pressed(), ["false", "false", "false"]);
});

test("an empty palette renders no pills and the group is hidden", () => {
  for (const p of [{ colors: [] }, null, {}]) {
    const { el, sync } = swatchRow({ name: "Background", palette: p, apply() {} });
    assert.equal(find(el, (n) => n.tag === "button").length, 0);
    assert.equal(el.attrs.hidden, "");
    sync("bg");
  }
});
