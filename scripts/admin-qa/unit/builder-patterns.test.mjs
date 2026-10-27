// Pattern thumbnails: shapes by section type, boxes from a template tree, and the fetch cap.
// Run: node --test scripts/admin-qa/unit/builder-patterns.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";

const P = await import(new URL("../../../backend/web/admin/js/builder/patterns.js", import.meta.url));

test("shapeOf picks a schematic by section type and falls back to a generic one", () => {
  assert.notDeepEqual(P.shapeOf("hero"), P.shapeOf("faq"));
  assert.deepEqual(P.shapeOf("features_3"), P.shapeOf("features"));
  assert.deepEqual(P.shapeOf("pricing-table"), P.shapeOf("pricing"));
  assert.deepEqual(P.shapeOf("zzz"), P.shapeOf(undefined));
  for (const t of ["hero", "faq", "cta", "gallery", "contact", "testimonials", "zzz"]) {
    for (const [x, y, w, h] of P.shapeOf(t)) assert.ok(x >= 0 && y >= 0 && x + w <= P.W && y + h <= P.H, `${t} box inside the frame`);
  }
});

test("shapeOfTree: top-level nodes stack, columns sit side by side, depth stops at 3", () => {
  const tree = [
    { t: "container", c: [{ t: "columns", c: [{ t: "text" }, { t: "image" }] }] },
    { t: "heading" },
  ];
  const boxes = P.shapeOfTree(tree);
  assert.equal(boxes.length, 3);
  const [a, b, c] = boxes;
  assert.ok(b[0] > a[0] && b[1] === a[1], "two columns side by side");
  assert.ok(c[1] > a[1], "the second top-level node is below");
  const deep = { t: "container", c: [{ t: "container", c: [{ t: "container", c: [{ t: "container", c: [{ t: "text" }] }] }] }] };
  assert.equal(P.shapeOfTree([deep]).length, 1);
  assert.deepEqual(P.shapeOfTree([]), P.shapeOf(""));
});

test("thumb draws aria-hidden SVG rects with createElementNS", () => {
  const made = [];
  globalThis.document = {
    createElementNS: (ns, tag) => {
      const e = { ns, tag, attrs: {}, kids: [], setAttribute(k, v) { this.attrs[k] = v; }, append(...k) { this.kids.push(...k); } };
      made.push(e);
      return e;
    },
  };
  const svg = P.thumb([[1, 2, 3, 4], [5, 6, 7, 8]]);
  assert.equal(svg.attrs["aria-hidden"], "true");
  assert.equal(svg.ns, "http://www.w3.org/2000/svg");
  assert.equal(svg.kids.length, 2);
  assert.equal(svg.kids[0].attrs.width, "3");
});

test("templateShapes asks for at most 12 uncached templates, caches them, and a failure stays generic", async () => {
  const items = Array.from({ length: 20 }, (_, i) => ({ key: `tpl:t${i}` }));
  const asked = [];
  const fetch = async (url) => {
    asked.push(url);
    if (url.endsWith("/t3")) throw new Error("offline");
    return { ok: !url.endsWith("/t4"), json: async () => ({ nodes: [{ t: "heading" }, { t: "text" }] }) };
  };
  const cache = new Map();
  const n = await P.templateShapes(items, { template: "/admin/x/template" }, cache, { fetch });
  assert.equal(n, 12);
  assert.equal(asked.length, 12);
  assert.equal(asked[0], "/admin/x/template/t0");
  assert.equal(cache.get("tpl:t0").length, 2);
  assert.deepEqual(cache.get("tpl:t3"), P.shapeOf(""), "failed: generic");
  assert.deepEqual(cache.get("tpl:t4"), P.shapeOf(""), "not ok: generic");
  assert.equal(await P.templateShapes(items.slice(0, 12), { template: "/admin/x/template" }, cache, { fetch }), 0, "nothing asked twice");
});

test("the thumbnail cap is per session: repeated paints never fetch more than 12 in total", async () => {
  const items = Array.from({ length: 60 }, (_, i) => ({ key: `tpl:t${i}` }));
  let asked = 0;
  const fetch = async () => (asked++, { ok: true, json: async () => ({ nodes: [] }) });
  const cache = new Map();
  for (let paint = 0; paint < 5; paint++) await P.templateShapes(items, { template: "/x" }, cache, { fetch });
  assert.equal(asked, 12);
});

test("startersFirst puts hero, features, cta and faq-like sections first; saved templates go after the theme's", () => {
  const items = [["content", "Content"], ["clients", "Client logos"], ["cta_band", "CTA"], ["faq", "FAQ"], ["hero", "Hero"], ["features_3", "Features"]].map(([type, name]) => ({ key: `component:${type}`, kind: "component", type, name }));
  const tpl = { key: "tpl:1", kind: "template", name: "My pricing" };
  assert.deepEqual(P.startersFirst([tpl, ...items]).map((i) => i.name), ["Hero", "Features", "CTA", "FAQ", "Content", "Client logos", "My pricing"]);
});

test("patternRow is one short row until all patterns are asked for", () => {
  const items = Array.from({ length: 14 }, (_, i) => ({ key: `component:p${i}`, kind: "component", type: `p${i}`, name: `P${i}` }));
  const short = P.patternRow(items);
  assert.deepEqual([short.shown.length, short.total, short.more], [4, 14, true]);
  assert.equal(P.patternRow(items, true).shown.length, 14);
  assert.deepEqual(P.patternRow(items.slice(0, 3)).more, false, "nothing to expand");
});
