// The Pages tab: one link row per page, the current one aria-current, a filter, and the empty notes.
// Run: node --test scripts/admin-qa/unit/builder-pagestab.test.mjs
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
  constructor(tag) { this.tag = tag; this.attrs = {}; this.kids = []; this.on = {}; this._text = ""; this.value = ""; }
  setAttribute(k, v) { this.attrs[k] = String(v); }
  removeAttribute(k) { delete this.attrs[k]; }
  getAttribute(k) { return this.attrs[k] ?? null; }
  set textContent(v) { this._text = v; this.kids = []; }
  get textContent() { return this._text + this.kids.map((k) => (typeof k === "string" ? k : k.textContent)).join(""); }
  append(...k) { this.kids.push(...k); }
  replaceChildren(...k) { this.kids = k; this._text = ""; }
  addEventListener(t, f) { (this.on[t] ??= []).push(f); }
  walk(out = []) { for (const k of this.kids) if (typeof k === "object") (out.push(k), k.walk(out)); return out; }
}
globalThis.document = { createElement: (t) => new El(t) };
const { mountPages, filterPages } = await import(new URL("builder/pagestab.js", root));

const pages = [
  { id: "a", title: "Home", path: "/", editHref: "/admin/entities/a/edit", status: "published", current: true },
  { id: "b", title: "Pricing", path: "/pricing", editHref: "/admin/entities/b/edit", status: "published" },
  { id: "c", title: "Next launch", path: "", editHref: "/admin/entities/c/edit", status: "draft" },
];
const links = (host) => host.walk().filter((n) => n.tag === "a");

test("one link per page, the current page is aria-current, a draft says Draft", () => {
  const host = new El("div");
  mountPages(host, { pages });
  const a = links(host);
  assert.deepEqual(a.map((n) => n.attrs.href), pages.map((p) => p.editHref));
  assert.deepEqual(a.map((n) => n.attrs["aria-current"] ?? null), ["page", null, null]);
  assert.match(a[2].textContent, /Next launch.*Draft/);
});

test("the filter matches title and path; no match and no pages say so", () => {
  assert.deepEqual(filterPages(pages, "PRIC").map((p) => p.id), ["b"]);
  assert.deepEqual(filterPages(pages, "/").map((p) => p.id), ["a", "b"]);
  assert.equal(filterPages(pages, "  ").length, 3);
  const host = new El("div");
  const ui = mountPages(host, { pages });
  const input = host.walk().find((n) => n.tag === "input");
  input.value = "zzz";
  input.on.input[0]();
  assert.equal(links(host).length, 0);
  assert.equal(ui.el.walk().find((n) => n.tag === "p").textContent, "No pages match.");
  const empty = new El("div");
  mountPages(empty, { pages: [] });
  assert.equal(empty.walk().find((n) => n.tag === "p").textContent, "No pages yet.");
});

test("a site whose only page is the current one says there are no other pages", () => {
  const host = new El("div");
  mountPages(host, { pages: [pages[0]] });
  assert.equal(links(host).length, 1);
  const note = host.walk().find((n) => n.tag === "p");
  assert.equal(note.textContent, "No other pages yet.");
  assert.equal(note.attrs.hidden, undefined);
  const many = new El("div");
  mountPages(many, { pages });
  assert.equal(many.walk().find((n) => n.tag === "p").textContent, "");
});

test("the filter field sits in the same search wrapper (icon + inset) as the Layers search", () => {
  const host = new El("div");
  mountPages(host, { pages });
  const wrap = host.walk().find((n) => n.attrs.class === "hm-bs__search");
  assert.ok(wrap, "no .hm-bs__search wrapper");
  assert.ok(wrap.walk().some((n) => "data-hm-bs-searchico" in n.attrs), "no search icon");
  assert.ok(wrap.walk().some((n) => n.tag === "input"));
});
