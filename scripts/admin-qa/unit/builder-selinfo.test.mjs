// selinfo: the shared selection facts (collapse predicate, row ids, section, parent, steps).
// Run: node --test scripts/admin-qa/unit/builder-selinfo.test.mjs
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
const { isCollapsed, rowIdOf, hideTarget, sectionOf, parentOf, selectionInfo, stepSection, inSection } = await import(new URL("builder/selinfo.js", root));

const comp = (id, c) => ({ id, t: "component", p: { type: "hero" }, ...(c ? { c } : {}) });
const sec = (id, c, extra = {}) => ({ id, t: "container", p: { tag: "section" }, c, ...extra });
const head = (id) => ({ id, t: "heading", p: { text: id } });
const doc = {
  v: 2,
  nodes: [
    sec("s1", [comp("c1", [head("b1"), { id: "g", t: "container", c: [head("b2")] }])], { s: { base: { bg: "#fff" } } }),
    sec("s2", [head("x"), head("y")]),
    { id: "d", t: "container", p: { tag: "div" }, c: [comp("c3")] },
    sec("s4", [comp("c4")]),
  ],
};

test("isCollapsed: tag section with exactly one component child, styles allowed", () => {
  assert.equal(isCollapsed(doc.nodes[0]), true);
  assert.equal(isCollapsed(doc.nodes[1]), false); // two kids
  assert.equal(isCollapsed(doc.nodes[2]), false); // tag != section
  assert.equal(isCollapsed(sec("z", [head("h")])), false); // child is not a component
  assert.equal(isCollapsed(sec("z", [])), false);
  assert.equal(isCollapsed(null), false);
  assert.equal(isCollapsed(comp("c")), false);
});

test("rowIdOf: component -> container, block -> itself, unknown -> null", () => {
  assert.equal(rowIdOf(doc, "c1"), "s1");
  assert.equal(rowIdOf(doc, "s1"), "s1");
  assert.equal(rowIdOf(doc, "b1"), "b1");
  assert.equal(rowIdOf(doc, "c3"), "c3"); // parent is not a collapsed section
  assert.equal(rowIdOf(doc, "nope"), null);
});

test("sectionOf: top-level index and collapsed flag", () => {
  assert.deepEqual({ ...sectionOf(doc, "b2"), node: undefined }, { id: "s1", node: undefined, index: 0, collapsed: true });
  assert.equal(sectionOf(doc, "y").index, 1);
  assert.equal(sectionOf(doc, "y").collapsed, false);
  assert.equal(sectionOf(doc, "s4").id, "s4");
  assert.equal(sectionOf(doc, "nope"), null);
});

test("parentOf: sections and the component of a collapsed section are top level", () => {
  assert.equal(parentOf(doc, "s1"), null);
  assert.equal(parentOf(doc, "c1"), null);
  assert.equal(parentOf(doc, "b1"), "s1");
  assert.equal(parentOf(doc, "b2"), "g");
  assert.equal(parentOf(doc, "g"), "s1");
  assert.equal(parentOf(doc, "x"), "s2");
  assert.equal(parentOf(doc, "c3"), "d");
  assert.equal(parentOf(doc, "nope"), null);
});

test("selectionInfo: row depth, isSection, childCount and subject", () => {
  const sec1 = selectionInfo(doc, "s1");
  assert.equal(sec1.isSection, true);
  assert.equal(sec1.depth, 1);
  assert.equal(sec1.kind, "container");
  assert.equal(sec1.parentId, null);
  assert.equal(sec1.parentKind, null);
  const c1 = selectionInfo(doc, "c1");
  assert.equal(c1.rowId, "s1");
  assert.equal(c1.isSection, true);
  assert.equal(c1.childCount, 2);
  assert.equal(c1.subject, "c1"); // styled container: not v1Shaped, so the component moves itself
  const b1 = selectionInfo(doc, "b1");
  assert.deepEqual([b1.depth, b1.isSection, b1.parentId, b1.parentKind, b1.section.id], [2, false, "s1", "container", "s1"]);
  assert.equal(selectionInfo(doc, "b2").depth, 3);
  const x = selectionInfo(doc, "x"); // plain section: tree depth = row depth
  assert.deepEqual([x.depth, x.parentId], [2, "s2"]);
  assert.equal(selectionInfo(doc, "s4").subject, "s4");
  assert.equal(selectionInfo(doc, "c4").subject, "s4"); // v1-shaped: the section moves
  assert.equal(selectionInfo(doc, "nope"), null);
});

test("stepSection: container ids, null at the ends, no wrap", () => {
  assert.equal(stepSection(doc, "c1", 1), "s2");
  assert.equal(stepSection(doc, "b2", 1), "s2");
  assert.equal(stepSection(doc, "y", -1), "s1");
  assert.equal(stepSection(doc, "s1", -1), null);
  assert.equal(stepSection(doc, "c4", 1), null);
  assert.equal(stepSection(doc, "nope", 1), null);
});

test("inSection: inside but not the row itself", () => {
  assert.equal(inSection(doc, "s1", "b1"), true);
  assert.equal(inSection(doc, "s1", "s1"), false);
  assert.equal(inSection(doc, "s1", "c1"), false); // same row
  assert.equal(inSection(doc, "s1", "x"), false);
  assert.equal(inSection(doc, "s2", "x"), true);
});

test("a nested container(tag=section) > component is two rows: rowIdOf is identity, parentOf steps up once", () => {
  const nested = { v: 2, nodes: [{ id: "d", t: "container", p: { tag: "div" }, c: [sec("in", [comp("c", [head("h")])])] }] };
  assert.equal(rowIdOf(nested, "c"), "c");
  assert.equal(rowIdOf(nested, "in"), "in");
  assert.equal(parentOf(nested, "c"), "in");
  assert.equal(parentOf(nested, "in"), "d");
  assert.equal(selectionInfo(nested, "c").depth, 3);
});

test("hideTarget: the container, or the component when only it is hidden", () => {
  const hid = { base: { hide: true } };
  const mk = (cs, ss) => ({ v: 2, nodes: [{ ...sec("s", [{ ...comp("c"), ...(cs ? { s: cs } : {}) }]), ...(ss ? { s: ss } : {}) }] });
  assert.equal(hideTarget(mk(), "c", "base"), "s"); // the row is the container, whichever id
  assert.equal(hideTarget(mk(), "s", "base"), "s");
  assert.equal(hideTarget(mk(hid), "s", "base"), "c"); // only the component hidden: unhide that
  assert.equal(hideTarget(mk(hid), "c", "base"), "c");
  assert.equal(hideTarget(mk(hid, hid), "c", "base"), "s"); // both: the container first
  assert.equal(hideTarget(doc, "b1", "base"), "b1"); // a block: itself
  assert.equal(hideTarget(doc, "nope", "base"), null);
});
