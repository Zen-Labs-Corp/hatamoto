// chromemodel: toolbar contents, "Add section" boundaries and their doc index, size text, padding hatch.
// Run: node --test scripts/admin-qa/unit/canvas-chromemodel.test.mjs
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
const { toolbarModel, pillPoints, boundaryIndex, sizeText, hatchRects, sizeAnchor, keepInside } = await import(new URL("canvas/chromemodel.js", root));
const { selectionInfo, sectionOf } = await import(new URL("builder/selinfo.js", root));

const comp = (id, c) => ({ id, t: "component", p: { type: "hero" }, ...(c ? { c } : {}) });
const sec = (id, c) => ({ id, t: "container", p: { tag: "section" }, c });
const head = (id) => ({ id, t: "heading", p: { text: id } });
const doc = { v: 2, nodes: [sec("s1", [comp("c1")]), { id: "hid", t: "container", p: { tag: "div" }, c: [head("x")] }, sec("s3", [head("h"), head("g")])] };

const base = { name: "Hero", kind: "container", parentId: null, parentName: "", canUp: true, canDown: true, hidden: false, editing: false };
const acts = (m) => m.buttons.map((b) => b.act);

test("a section has no way up; the buttons are in the toolbar's order", () => {
  const m = toolbarModel(base);
  assert.equal(m.up, null);
  assert.deepEqual(m.title, { icon: "container", name: "Hero" });
  assert.deepEqual(acts(m), ["moveup", "movedown", "duplicate", "togglehide", "remove", "more"]);
  assert.ok(m.buttons.every((b) => b.icon && b.aria));
});

test("a block gets '↑ <parent name>' and its id; built from the real selection facts", () => {
  const info = selectionInfo(doc, "h");
  const m = toolbarModel({ ...base, name: "Heading", kind: info.kind, parentId: info.parentId, parentName: "Section" });
  assert.deepEqual(m.up, { id: "s3", text: "↑ Section", aria: "Select parent, Section" });
  assert.equal(toolbarModel({ ...base, parentId: "p", parentName: "" }).up.text, "↑ parent", "an unnamed parent still reads");
});

test("move up/down are disabled at the ends, the rest never", () => {
  const m = toolbarModel({ ...base, canUp: false, canDown: true });
  assert.equal(m.buttons.find((b) => b.act === "moveup").disabled, true);
  assert.equal(m.buttons.find((b) => b.act === "movedown").disabled, undefined);
  assert.deepEqual(m.buttons.filter((b) => b.disabled).map((b) => b.act), ["moveup"]);
});

test("hidden reads 'Show on this device'; shown reads 'Hide on this device'", () => {
  assert.equal(toolbarModel({ ...base, hidden: true }).buttons.find((b) => b.act === "togglehide").aria, "Show on this device");
  const shown = toolbarModel(base).buttons.find((b) => b.act === "togglehide");
  assert.equal(shown.aria, "Hide on this device");
  assert.notEqual(shown.icon, toolbarModel({ ...base, hidden: true }).buttons.find((b) => b.act === "togglehide").icon);
});

test("while text is edited the toolbar keeps its title and has no buttons", () => {
  const m = toolbarModel({ ...base, editing: true });
  assert.deepEqual(m.buttons, []);
  assert.equal(m.title.name, "Hero");
});

const box = (id, y, h, p = "") => ({ id, k: "s", t: "x", p, x: 0, y, w: 1280, h });

test("pillPoints: three sections make four boundaries with their neighbours", () => {
  const pts = pillPoints([box("c", 400, 100), box("a", 0, 200), box("b", 200, 200), box("in", 10, 20, "a")]);
  assert.deepEqual(pts, [
    { y: 0, before: null, after: "a" },
    { y: 200, before: "a", after: "b" },
    { y: 400, before: "b", after: "c" },
    { y: 500, before: "c", after: null },
  ]);
});

test("pillPoints: a gap puts the boundary in its middle; an empty page has one boundary", () => {
  assert.equal(pillPoints([box("a", 0, 100), box("b", 140, 100)])[1].y, 120);
  assert.deepEqual(pillPoints([box("in", 0, 10, "a")]), [{ y: 0, before: null, after: null }]);
});

test("boundaryIndex: 0 at the top, total at the bottom, the doc index of the element below between", () => {
  const indexOf = (id) => sectionOf(doc, id)?.index;
  const pts = pillPoints([box("c1", 0, 100), box("s3", 100, 100)]); // the frame renders s1 (as its component) and s3, not "hid"
  assert.deepEqual(pts.map((b) => boundaryIndex(b, indexOf, doc.nodes.length)), [0, 2, 3]);
});

test("boundaryIndex follows the doc, not the frame order, and refuses an unknown element", () => {
  const frameOrder = pillPoints([box("s3", 0, 100), box("s1", 100, 100)]); // a frame that lists them swapped
  const indexOf = (id) => sectionOf(doc, id)?.index;
  assert.equal(boundaryIndex(frameOrder[1], indexOf, 3), 0, "between s3 and s1 inserts where s1 is in the doc");
  assert.equal(boundaryIndex({ y: 0, before: "a", after: "gone" }, indexOf, 3), null);
});

test("sizeText rounds and ignores the zoom", () => {
  assert.equal(sizeText({ w: 1280, h: 411.6 }), "1280 × 412");
  assert.equal(sizeText({ w: 389.5, h: 72.2, x: 5, y: 9 }), "390 × 72");
});

test("hatchRects: the four bands add up to the padding box", () => {
  const bx = { w: 1280, h: 412 };
  const pd = [112, 40, 96, 24];
  const r = hatchRects(bx, pd);
  assert.deepEqual(r.map((x) => x.side), ["top", "right", "bottom", "left"]);
  const area = r.reduce((s, x) => s + x.w * x.h, 0);
  assert.equal(area, 1280 * 412 - (1280 - 40 - 24) * (412 - 112 - 96));
  assert.deepEqual(r[0], { side: "top", x: 0, y: 0, w: 1280, h: 112 });
  assert.deepEqual(r[1], { side: "right", x: 1240, y: 112, w: 40, h: 204 });
  assert.deepEqual(r[2], { side: "bottom", x: 0, y: 316, w: 1280, h: 96 });
  assert.deepEqual(r[3], { side: "left", x: 0, y: 112, w: 24, h: 204 });
});

test("hatchRects: no padding known is no area; padding larger than the box does not go negative", () => {
  assert.ok(hatchRects({ w: 100, h: 50 }, undefined).every((x) => x.w * x.h === 0));
  assert.ok(hatchRects({ w: 100, h: 50 }, [40, 0, 40, 0]).every((x) => x.h >= 0));
});

test("a section's size badge hangs from its right edge, off the centred 'Add section' pill; a block's stays centred", () => {
  const box = { x: 0, y: 100, w: 400, h: 200 };
  assert.deepEqual(sizeAnchor(box, true), { x: 400, align: "end" });
  assert.deepEqual(sizeAnchor(box, false), { x: 200, align: "center" });
});

test("a handle on the frame's edge is pulled inside by half its thickness; a short frame still gives a position", () => {
  assert.equal(keepInside(0, 12, 390), 12);
  assert.equal(keepInside(390, 12, 390), 378);
  assert.equal(keepInside(200, 12, 390), 200);
  assert.equal(keepInside(5, 12, 10), 12);
});
