// Palette: hidden kinds, the Content slot of a part, template and block groups, the
// reuse screen's pure parts. Run: node --test scripts/admin-qa/unit/builder-palette.test.mjs
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
const P = await import(new URL("../../../backend/web/admin/js/builder/palette.js", import.meta.url));
const S = await import(new URL("../../../backend/web/admin/js/builder/search.js", import.meta.url));
const U = await import(new URL("../../../backend/web/admin/js/builder/reusecore.js", import.meta.url));

const kinds = [
  { kind: "container", children: true },
  { kind: "heading", default: { text: "Heading", level: 2 } },
  { kind: "component", children: false },
  { kind: "ref", hidden: true },
  { kind: "slot", hidden: true },
];
const sections = [{ type: "hero", title: "Hero" }];
const BLK = "11111111-1111-4111-8111-111111111111";
const reuse = { available: true, role: "page", slot: "", templates: [{ id: "t1", name: "Pricing row" }], blocks: [{ id: BLK, name: "Footer CTA" }, { id: "22222222-2222-4222-8222-222222222222", name: "Banner" }] };

test("hidden kinds stay out of the palette; a single or archive part gets the Content slot", () => {
  assert.deepEqual(P.itemsOf(kinds, sections).map((i) => i.key), ["container", "heading", "component:hero"]);
  const withSlot = P.itemsOf(kinds, sections, { slot: true });
  assert.deepEqual(withSlot.map((i) => i.key), ["container", "heading", "slot", "component:hero"]);
  const slot = withSlot.find((i) => i.key === "slot");
  assert.deepEqual([slot.name, slot.group, slot.hint], ["Content slot", "Layout", "Where the page content goes"]);
  assert.deepEqual(P.makeNode(slot, kinds).t, "slot");
  assert.equal(P.itemsOf(kinds, sections, { slot: true }).some((i) => i.key === "ref"), false, "ref is never offered");
});

test("wantsSlot: only single and archive parts", () => {
  for (const [role, slot, want] of [["part", "single", true], ["part", "archive", true], ["part", "header", false], ["part", "footer", false], ["block", "", false], ["page", "", false]]) {
    assert.equal(U.wantsSlot({ role, slot }), want, `${role} ${slot}`);
  }
});

test("template and block items: groups Patterns and Blocks, never the block being edited", () => {
  const extra = U.paletteExtra(reuse);
  assert.deepEqual(extra.map((i) => [i.key, i.group, i.kind]), [["tpl:t1", "Patterns", "template"], [`blk:${BLK}`, "Blocks", "block"], ["blk:22222222-2222-4222-8222-222222222222", "Blocks", "block"]]);
  assert.deepEqual(U.paletteExtra(reuse, BLK).map((i) => i.name), ["Pricing row", "Banner"]);
  assert.deepEqual(U.paletteExtra({}, "").length, 0);
  const all = [...P.itemsOf(kinds, sections), ...extra];
  assert.deepEqual(S.filterItems(all, "footer").map((i) => i.key), [`blk:${BLK}`]);
  assert.deepEqual(S.filterItems(all, "pricing").map((i) => i.key), ["tpl:t1"]);
});

test("a block item makes a ref node; templates are not dragged", () => {
  const blk = U.paletteExtra(reuse).find((i) => i.kind === "block");
  const n = P.makeNode(blk, kinds);
  assert.deepEqual([n.t, n.p], ["ref", { block: BLK }]);
  assert.match(n.id, /^n[0-9a-f]{10}$/);
  assert.equal(P.makeNode(blk, kinds).id === n.id, false);
});

test("makeNode: a seed with b and nested children inserts bound children with fresh ids", () => {
  const kinds = [{ kind: "loop", children: true, seed: [{ t: "container", c: [{ t: "heading", p: { level: 2 }, b: { text: "loop.item.title" } }] }, { t: "loopempty", c: [{ t: "text", p: { text: "None" } }] }] }];
  const a = P.makeNode({ kind: "loop" }, kinds);
  const b = P.makeNode({ kind: "loop" }, kinds);
  assert.equal(a.c[0].c[0].b.text, "loop.item.title");
  assert.equal(a.c[1].c[0].p.text, "None");
  assert.notEqual(a.c[0].c[0].id, b.c[0].c[0].id);
  a.c[0].c[0].b.text = "x";
  assert.equal(kinds[0].seed[0].c[0].b.text, "loop.item.title");
});

test("plugin widget kinds keep their group and the group sits after Data, before Blocks", async () => {
  const packKinds = [...kinds, { kind: "promo_banner", label: "Promo banner", hint: "A promo strip", group: "Plugin widgets" }];
  const item = P.itemsOf(packKinds, sections).find((i) => i.key === "promo_banner");
  assert.deepEqual([item.name, item.group, item.hint], ["Promo banner", "Plugin widgets", "A promo strip"]);
  const { readFile } = await import("node:fs/promises");
  const src = await readFile(new URL("../../../backend/web/admin/js/builder/palette.js", import.meta.url), "utf8");
  const order = JSON.parse(src.match(/GROUP_ORDER = (\[.*?\]);/)[1]);
  assert.deepEqual(order.slice(0, 4), ["Recent", "Patterns", "Layout", "Elements"]);
  assert.ok(order.indexOf("Data") < order.indexOf("Plugin widgets") && order.indexOf("Plugin widgets") < order.indexOf("Blocks"), order.join());
});

test("rank: prefix before word start before substring, ties keep their order", () => {
  const items = ["Text editor", "Rich text", "Textbox", "Context"].map((name) => ({ name }));
  assert.deepEqual(S.rank(items, "text").map((i) => i.name), ["Text editor", "Textbox", "Rich text", "Context"]);
  assert.equal(S.rank(items, "  "), items);
});

test("sections and templates share the Patterns group", () => {
  assert.equal(P.itemsOf(kinds, sections).find((i) => i.kind === "component").group, "Patterns");
});

const real = [
  { kind: "container", children: true, group: "Layout" },
  { kind: "columns", label: "Columns", children: true, group: "Layout" },
  { kind: "heading", default: { text: "Heading" } },
  { kind: "text" },
  { kind: "image" },
];
const themeSections = ["Hero", "Features", "Comparison", "Card columns", "Client logos", "Header"].map((title) => ({ type: title.toLowerCase().replace(" ", "_"), title }));

test("search matches the visible name (and explicit aliases), never kind or hint strings", () => {
  const items = P.itemsOf(real, themeSections);
  for (const q of ["component", "theme", "section", "the", "sec", "ion"]) assert.deepEqual(S.filterItems(items, q).filter((i) => i.group === "Patterns" && !i.name.toLowerCase().includes(q)), [], q);
  assert.deepEqual(S.filterItems(items, "headline").map((i) => i.key), ["heading"], "an alias finds the element");
});

test("rank: whole name, start of name, start of a word, anywhere; elements before patterns on equal score", () => {
  const items = P.itemsOf(real, themeSections);
  const keys = (q) => S.rank(S.filterItems(items, q), q).map((i) => i.name);
  assert.equal(keys("he")[0], "Heading", "he + Enter inserts Heading, not Hero");
  assert.deepEqual(keys("he").slice(0, 3), ["Heading", "Hero", "Header"]);
  assert.deepEqual(keys("co").slice(0, 3), ["Container", "Columns", "Comparison"], "co: elements first, then the pattern");
  assert.ok(keys("co").indexOf("Card columns") > keys("co").indexOf("Comparison"), "word start after name start");
  assert.equal(keys("heading")[0], "Heading", "whole name first");
  assert.ok(S.score({ name: "Heading", aliases: ["title"] }, "title") > S.score({ name: "Title" }, "title"), "an alias is a half step behind the name");
});

test("sectionPoint: end of the page, after the top-level section, whatever the depth", () => {
  const leaf = { id: "leaf", t: "heading" };
  const doc = { nodes: [{ id: "a", t: "container", c: [{ id: "a1", t: "container", c: [leaf] }] }, { id: "b", t: "container", c: [{ id: "comp", t: "component", p: { type: "hero" } }] }] };
  assert.deepEqual(P.sectionPoint(doc, null), { parent: "", index: 2 });
  assert.deepEqual(P.sectionPoint(doc, "a"), { parent: "", index: 1 });
  assert.deepEqual(P.sectionPoint(doc, "leaf"), { parent: "", index: 1 });
  assert.deepEqual(P.sectionPoint(doc, "comp"), { parent: "", index: 2 }, "a v1 section component lands after its container");
  assert.deepEqual(P.sectionPoint(doc, "nope"), { parent: "", index: 2 });
});

test("insertSplit: patterns are sections, native groups blocks, plugin widgets and saved blocks the rest", () => {
  const items = [
    ...P.itemsOf([{ kind: "container", children: true }, { kind: "heading" }, { kind: "chart", group: "Plugin widgets" }], sections),
    ...U.paletteExtra(reuse),
  ];
  const s = P.insertSplit(items);
  const keys = (l) => l.map((i) => i.key).sort();
  assert.deepEqual(keys(s.sections), ["component:hero", `tpl:t1`].sort());
  assert.deepEqual(keys(s.blocks), ["container", "heading"]);
  assert.deepEqual(keys(s.rest), ["chart", `blk:${BLK}`, "blk:22222222-2222-4222-8222-222222222222"].sort());
  assert.ok(P.isSectionItem({ kind: "template", group: "Patterns" }) && !P.isSectionItem({ kind: "block", group: "Blocks" }));
});
