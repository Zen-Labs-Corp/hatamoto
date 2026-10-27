// The Add tab's "Show all patterns" toggle keeps focus and says whether it is expanded.
// Run: node --test scripts/admin-qa/unit/builder-addtab.test.mjs
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
  constructor(tag) { this.tag = tag; this.attrs = {}; this.kids = []; this.on = {}; this.textContent = ""; this.dataset = {}; this.value = ""; }
  setAttribute(k, v) { this.attrs[k] = String(v); if (k.startsWith("data-")) this.dataset[k.slice(5).replace(/-(\w)/g, (_, c) => c.toUpperCase())] = String(v); }
  getAttribute(k) { return this.attrs[k] ?? null; }
  append(...k) { for (const n of k) if (typeof n === "object") n.parent = this; this.kids.push(...k); }
  replaceChildren(...k) { for (const o of this.kids) if (typeof o === "object" && o.contains?.(document.activeElement)) document.activeElement = document.body; this.kids = []; this.append(...k); }
  addEventListener(t, f) { (this.on[t] ??= []).push(f); }
  click() { for (const f of this.on.click ?? []) f({ target: this, detail: 0 }); }
  focus() { if (this.attached()) document.activeElement = this; }
  select() {}
  closest() { return null; }
  attached() { let n = this; while (n.parent) n = n.parent; return n === document.body || n.isRoot === true; }
  contains(n) { return n === this || this.kids.some((k) => typeof k === "object" && k.contains(n)); }
  walk(out = []) { for (const k of this.kids) if (typeof k === "object") (out.push(k), k.walk(out)); return out; }
  querySelectorAll(sel) { const a = sel.match(/^\[(\S+?)\]$/)[1]; return this.walk().filter((n) => a in n.attrs); }
  querySelector(sel) { return sel === "span" ? (this.walk().find((n) => n.tag === "span") ?? null) : (this.querySelectorAll(sel)[0] ?? null); }
}
const body = new El("body");
body.isRoot = true;
globalThis.document = { body, activeElement: body, createElement: (t) => new El(t), createElementNS: (_n, t) => new El(t), querySelector: () => null };
const { mountPalette } = await import(new URL("builder/addtab.js", root));

test("Show all patterns: the same button keeps focus, aria-expanded flips, and more tiles appear", () => {
  const kinds = [{ kind: "container", children: true }];
  const sections = ["hero", "features", "cta", "faq", "pricing", "quote"].map((t) => ({ type: t, title: t }));
  const host = new El("div");
  body.append(host);
  const p = mountPalette(host, { kinds, sections, onInsert() {}, storage: { getItem: () => null, setItem() {} } });
  const toggle = () => p.el.querySelector("[aria-expanded]");
  const first = toggle();
  assert.equal(first.getAttribute("aria-expanded"), "false");
  assert.equal(p.el.querySelectorAll("[data-pal]").filter((b) => b.dataset.pal.startsWith("component:")).length, 4);
  first.focus();
  assert.equal(document.activeElement, first);
  first.click();
  assert.equal(toggle(), first, "the button is not rebuilt");
  assert.equal(document.activeElement, first, "focus stays on the toggle");
  assert.equal(first.getAttribute("aria-expanded"), "true");
  assert.equal(p.el.querySelectorAll("[data-pal]").filter((b) => b.dataset.pal.startsWith("component:")).length, 6);
  first.click();
  assert.equal(document.activeElement, first);
  assert.equal(first.getAttribute("aria-expanded"), "false");
});
