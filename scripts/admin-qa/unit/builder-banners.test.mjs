// The conflict and recovery banners: plain copy, three conflict actions, one banner per kind, recovery buttons.
// Run: node --test scripts/admin-qa/unit/builder-banners.test.mjs
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
  constructor(tag) { this.tag = tag; this.dataset = {}; this.attrs = {}; this.kids = []; this.on = {}; this.textContent = ""; this.removed = false; }
  setAttribute(k, v) { this.attrs[k] = String(v); }
  getAttribute(k) { return this.attrs[k] ?? null; }
  append(...k) { for (const n of k) if (typeof n === "object") n.parent = this; this.kids.push(...k); }
  replaceChildren(...k) { this.kids = k; }
  addEventListener(t, f) { (this.on[t] ??= []).push(f); }
  click() { for (const f of this.on.click ?? []) f({}); }
  remove() { this.removed = true; this.parent?.kids.splice(this.parent.kids.indexOf(this), 1); }
  querySelector(sel) { const k = sel.match(/data-banner="(\w+)"/)[1]; return this.kids.find((n) => n.dataset.banner === k) ?? null; }
  contains(n) { return n === this || this.kids.some((k) => typeof k === "object" && k.contains(n)); }
  get parentElement() { return this.parent ?? null; }
  focus() { document.activeElement = this; }
  walk(out = []) { for (const k of this.kids) if (typeof k === "object") (out.push(k), k.walk(out)); return out; }
}
globalThis.document = { createElement: (t) => new El(t), createElementNS: (_n, t) => new El(t), querySelector: () => null };
const store = (init = {}) => ({ data: { ...init }, getItem(k) { return this.data[k] ?? null; }, setItem(k, v) { this.data[k] = v; } });
const B = await import(new URL("builder/banners.js", root));
const labels = (n) => n.walk().filter((x) => x.tag === "button").map((x) => x.textContent || x.walk().map((y) => y.textContent).join(""));

test("conflict shows plain copy, Keep my version, Load their version and a Versions link", () => {
  const banner = new El("div");
  const saved = [];
  B.conflict(banner, { resolve: (c) => saved.push(c) }, { versionsUrl: "/admin/entities/1/versions" });
  const node = banner.kids[0];
  assert.equal(node.attrs.role, "alert");
  assert.equal(node.dataset.banner, "conflict");
  assert.match(node.walk()[0].textContent, /^Someone else saved this page while you were editing\. Your changes are still here and are not saved yet\.$/);
  assert.deepEqual(labels(node), ["Keep my version", "Load their version"]);
  const link = node.walk().find((x) => x.tag === "a");
  assert.equal(link.attrs.href, "/admin/entities/1/versions");
  assert.equal(link.attrs.target, "_blank");
  assert.equal(link.attrs.rel, "noopener");
  node.walk().find((x) => x.tag === "button").click();
  assert.deepEqual(saved, ["overwrite"]);
});

test("a second conflict replaces the first; the link is left out without a Versions url", () => {
  const banner = new El("div");
  B.conflict(banner, {}, {});
  B.conflict(banner, {}, {});
  assert.equal(banner.kids.length, 1);
  assert.equal(banner.kids[0].walk().some((x) => x.tag === "a"), false);
});

test("recovery sits beside a conflict (different kinds); Restore applies, Discard drops the copy", () => {
  const banner = new El("div");
  B.conflict(banner, {}, {});
  let restored = 0, discarded = 0;
  const saver = { discardLocal: () => discarded++ };
  B.recovery(banner, saver, { action: "restore" }, () => (restored++, true));
  assert.equal(banner.kids.length, 2);
  const rec = banner.kids[1];
  assert.equal(rec.attrs.role, "status");
  assert.deepEqual(labels(rec), ["Restore my unsaved changes", "Discard them"]);
  rec.walk().find((x) => x.tag === "button").click();
  assert.equal(restored, 1);
  assert.equal(rec.removed, true);
  assert.equal(banner.kids.length, 1, "the conflict is still there");
});

test("a stale copy offers only Discard; an unreadable copy is dropped", () => {
  const banner = new El("div");
  B.recovery(banner, { discardLocal() {} }, { action: "stale" }, () => true);
  assert.deepEqual(labels(banner.kids[0]), ["Discard them"]);
  const b2 = new El("div");
  let discarded = 0;
  B.recovery(b2, { discardLocal: () => discarded++ }, { action: "restore" }, () => false);
  b2.kids[0].walk().find((x) => x.tag === "button").click();
  assert.equal(discarded, 1);
});

test("a note is one banner with a Dismiss button that removes it", () => {
  const banner = new El("div");
  B.note(banner, "The slug is taken.");
  B.note(banner, "The slug is taken again.");
  assert.equal(banner.kids.length, 1, "one note at a time");
  const node = banner.kids[0];
  assert.equal(node.dataset.banner, "note");
  assert.deepEqual(labels(node), ["Dismiss"]);
  node.walk().find((x) => x.tag === "button").click();
  assert.equal(banner.kids.length, 0);
});

test("Dismiss puts focus back where it was, or on the canvas region, never on body", () => {
  const body = new El("body");
  document.body = body;
  const canvas = new El("section");
  const banner = new El("div");
  canvas.append(banner);
  const field = new El("input");
  document.activeElement = field;
  B.note(banner, "Saved, not published.");
  let dismiss = banner.kids[0].walk().find((x) => x.tag === "button");
  dismiss.focus();
  dismiss.click();
  assert.equal(banner.kids.length, 0);
  assert.equal(document.activeElement, field, "the element focused before the note");
  document.activeElement = body;
  B.note(banner, "Saved, not published.");
  dismiss = banner.kids[0].walk().find((x) => x.tag === "button");
  dismiss.focus();
  dismiss.click();
  assert.equal(document.activeElement, canvas, "no earlier focus: the canvas region");
  assert.equal(canvas.attrs.tabindex, "-1");
  document.activeElement = field;
  B.note(banner, "x");
  banner.kids[0].walk().find((x) => x.tag === "button").click(); // focus was not on the note: left alone
  assert.equal(document.activeElement, field);
});

test("Load their version lets go of the local copy and the unsaved prompt before it reloads", () => {
  const order = [];
  globalThis.location = { reload: () => order.push("reload") };
  const banner = new El("div");
  B.conflict(banner, { leave: () => order.push("leave") }, {});
  banner.kids[0].walk().filter((x) => x.tag === "button")[1].click();
  assert.deepEqual(order, ["leave", "reload"]);
  delete globalThis.location;
});
