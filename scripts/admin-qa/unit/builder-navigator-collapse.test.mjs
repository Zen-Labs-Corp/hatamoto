// Navigator collapse: a section container wrapping one component is one row. Run: node --test scripts/admin-qa/unit/builder-navigator-collapse.test.mjs
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
const { rowsOf, rowName, sectionName, metaOf, thumbOf, collapseAll, allCollapsed, step, label } = await import(new URL("builder/navigator.js", root));

const head = (id, text) => ({ id, t: "heading", p: { text } });
const comp = (id, p = {}, c) => ({ id, t: "component", p: { type: "hero_banner", ...p }, ...(c ? { c } : {}) });
const sec = (id, c, p = {}, s) => ({ id, t: "container", p: { tag: "section", ...p }, c, ...(s ? { s } : {}) });
const sections = [{ type: "hero_banner", title: "Hero banner" }];
const nameOf = (n) => (n.t === "container" && n.c?.length === 1 && n.c[0].t === "component" ? sectionName(n, [], undefined, sections) : label(n, [], undefined, sections));

test("a container wrapping one component is one row, its blocks level 2 under the container id", () => {
  const doc = { v: 2, nodes: [sec("s", [comp("c", {}, [head("h1", "Hi"), head("h2", "Yo")])]), head("t", "Top")] };
  const rows = rowsOf(doc, new Set(), "", nameOf);
  assert.deepEqual(rows.map((r) => [r.id, r.level, r.parentId]), [["s", 1, ""], ["h1", 2, "s"], ["h2", 2, "s"], ["t", 1, ""]]);
  assert.equal(rows[0].collapsed.id, "c");
  assert.equal(rows[0].kids, 2);
  assert.equal(rows[0].section, true);
  assert.equal(rows[1].collapsed, null);
  assert.deepEqual(rows.map((r) => r.pos), [1, 1, 2, 2]);
});

test("a row without blocks has no caret and a plain section keeps both levels", () => {
  const plain = sec("p", [head("a", "A"), head("b", "B")]);
  const doc = { v: 2, nodes: [sec("s", [comp("c")]), plain] };
  const rows = rowsOf(doc, new Set(), "", nameOf);
  assert.deepEqual(rows.map((r) => r.id), ["s", "p", "a", "b"]);
  assert.equal(rows[0].kids, 0);
  assert.equal(rows[0].expanded, false);
  assert.equal(rows[1].collapsed, null);
});

test("name precedence: container navlabel, component navlabel, section title, type name", () => {
  assert.equal(sectionName(sec("s", [comp("c")], { navlabel: "Mine" }), [], undefined, sections), "Mine");
  assert.equal(sectionName(sec("s", [comp("c", { navlabel: "Inner" })]), [], undefined, sections), "Inner");
  assert.equal(sectionName(sec("s", [comp("c")]), [], undefined, sections), "Hero banner");
  assert.equal(sectionName(sec("s", [comp("c")]), [], undefined, []), "Hero banner"); // typeName fallback
  assert.equal(rowName(sec("s", [comp("c")], { navlabel: "Mine" }), "preset", [], undefined, sections), "Mine, preset");
});

test("index counts top-level rows and ignores the filter", () => {
  const doc = { v: 2, nodes: [sec("a", [comp("c1", { navlabel: "One" })]), sec("b", [comp("c2", { navlabel: "Two" })]), sec("c", [comp("c3", { navlabel: "Three" })])] };
  assert.deepEqual(rowsOf(doc, new Set(), "", nameOf).map((r) => r.index), [1, 2, 3]);
  assert.deepEqual(rowsOf(doc, new Set(), "three", nameOf).map((r) => [r.id, r.index]), [["c", 3]]);
});

test("filtering by the component's name keeps the row and its blocks", () => {
  const doc = { v: 2, nodes: [sec("s", [comp("c", {}, [head("h", "Hi")])], { navlabel: "Mine" }), head("t", "Top")] };
  assert.deepEqual(rowsOf(doc, new Set(), "hero", nameOf).map((r) => r.id), ["s"]);
  assert.deepEqual(rowsOf(doc, new Set(), "mine", nameOf).map((r) => r.id), ["s"]);
  const byBlock = rowsOf(doc, new Set(), "hi", nameOf);
  assert.deepEqual(byBlock.map((r) => r.id), ["s", "h"]);
  assert.deepEqual(byBlock.filter((r) => r.match).map((r) => r.id), ["h"]);
});

const metaFor = (node) => metaOf(rowsOf({ v: 2, nodes: [node] }, new Set(), "", nameOf)[0]);

test("meta: block count and background", () => {
  const blocks = [comp("c", {}, [head("a", "A"), head("b", "B")])];
  assert.equal(metaFor(sec("s", blocks, {}, { base: { bg: "#ffffff" } })), "2 blocks · #ffffff");
  assert.equal(metaFor(sec("s", [comp("c", {}, [head("a", "A")])], {}, { base: { bg: "surface" } })), "1 block · surface");
  assert.equal(metaFor(sec("s", blocks)), "2 blocks");
  assert.equal(metaFor(sec("s", [comp("c")], {}, { base: { bg: "#000" } })), "#000");
  assert.equal(metaFor(sec("s", [comp("c")])), "");
  assert.equal(metaFor(sec("s", [head("a", "A"), head("b", "B"), head("c", "C")])), "3 blocks");
});

test("meta: hidden replaces the background and lists the devices", () => {
  const hidSm = sec("s", [comp("c", {}, [head("a", "A")])], {}, { base: { bg: "#fff" }, sm: { hide: true } });
  assert.equal(metaFor(hidSm), "1 block · Hidden on Phone");
  assert.equal(metaFor(sec("s", [comp("c")], {}, { md: { hide: true } })), "Hidden on Tablet, Phone");
  assert.equal(metaFor(sec("s", [comp("c")], {}, { base: { hide: true }, md: { display: "block" } })), "Hidden on Desktop");
  assert.equal(metaFor(sec("s", [comp("c")], {}, { base: { hide: true } })), "Hidden");
});

test("meta is empty for block rows", () => {
  const doc = { v: 2, nodes: [sec("s", [comp("c", {}, [head("a", "A")])])] };
  assert.equal(metaOf(rowsOf(doc, new Set(), "", nameOf)[1]), "");
});

test("thumbOf: background and alignment", () => {
  assert.deepEqual(thumbOf(sec("s", [], {}, { base: { bg: "#112233", ta: "center" } })), { bg: "#112233", align: "center" });
  assert.deepEqual(thumbOf(sec("s", [], {}, { base: { bg: "accent", ta: "right" } })), { bg: "accent", align: "right" });
  assert.deepEqual(thumbOf(sec("s", [])), { bg: null, align: "left" });
  assert.equal(thumbOf(sec("s", [], {}, { base: { ta: "justify" } })).align, "left");
});

test("collapse all counts only rows with blocks", () => {
  const doc = { v: 2, nodes: [sec("s", [comp("c")]), sec("t", [comp("d", {}, [head("h", "H")])])] };
  assert.deepEqual([...collapseAll(doc)], ["t"]);
  assert.equal(allCollapsed(doc, new Set(["t"])), true);
  assert.equal(allCollapsed(doc, new Set()), false);
  assert.deepEqual(rowsOf(doc, new Set(["t"]), "", nameOf).map((r) => r.id), ["s", "t"]);
});

test("ArrowLeft from a block lands on the container; ArrowRight opens a collapsed row", () => {
  const doc = { v: 2, nodes: [sec("s", [comp("c", {}, [head("h", "H")])])] };
  const rows = rowsOf(doc, new Set(), "", nameOf);
  assert.deepEqual(step(rows, "h", "collapse"), { select: "s" });
  const closed = rowsOf(doc, new Set(["s"]), "", nameOf);
  assert.deepEqual(step(closed, "s", "expand"), { expand: "s" });
});
