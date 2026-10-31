// direct: padding handles (drag, keys, hatch, badge, hover hook from the inspector) and the "Add section" strips.
// Run: node --test scripts/admin-qa/unit/canvas-direct.test.mjs
import { register } from "node:module";
import { readFileSync } from "node:fs";
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

// A just-enough DOM: elements keep attributes, children, listeners and their style; fire() calls the listeners of an event.
class El {
  constructor(tag) {
    this.tagName = tag.toUpperCase();
    this.attrs = {};
    this.children = [];
    this.listeners = {};
    this.style = {};
    this.dataset = {};
    this.captured = [];
    this.removed = false;
  }
  setAttribute(k, v) { this.attrs[k] = String(v); }
  getAttribute(k) { return k in this.attrs ? this.attrs[k] : null; }
  append(...kids) { kids.forEach((k) => (k.parent = this)); this.children.push(...kids); }
  addEventListener(t, f) { (this.listeners[t] ??= []).push(f); }
  remove() {
    this.removed = true;
    if (this.parent) this.parent.children = this.parent.children.filter((c) => c !== this);
  }
  setPointerCapture(id) { this.captured.push(id); }
  releasePointerCapture(id) { this.captured = this.captured.filter((x) => x !== id); }
  set textContent(v) { this.text = String(v); }
  get textContent() { return this.text ?? ""; }
  fire(type, e = {}) {
    const ev = { preventDefault() {}, stopPropagation() {}, ...e };
    for (const f of this.listeners[type] ?? []) f(ev);
    return ev;
  }
}
const docListeners = {};
globalThis.document = {
  createElement: (t) => new El(t),
  addEventListener: (t, f) => (docListeners[t] ??= []).push(f),
  removeEventListener: (t, f) => (docListeners[t] = (docListeners[t] ?? []).filter((x) => x !== f)),
};
const fireDoc = (type, e) => (docListeners[type] ?? []).slice().forEach((f) => f({ preventDefault() {}, stopPropagation() {}, ...e }));

const { mountDirect } = await import(new URL("canvas/direct.js", root));
const { validToBridge } = await import(new URL("canvas/protocol.js", root));
const { padWrites } = await import(new URL("canvas/padding.js", root));
const table = JSON.parse(readFileSync(new URL("../../../backend/testdata/stylespec/table.json", import.meta.url), "utf8"));

const pdNode = (id, y, h, pd) => ({ id, k: "s", t: "hero", p: "", x: 0, y, w: 1280, h, ...(pd ? { pd } : {}) });
const find = (e, f) => e.children.find(f);

function rig({ nodes, selected = "a", scale = 1, section = true, bp = "base", editing = null } = {}) {
  const stage = new El("div");
  const posted = [];
  const state = { nodes: nodes ?? [pdNode("a", 100, 400, [112, 40, 96, 24]), pdNode("b", 500, 300, [10, 10, 10, 10])], selected, scale, editing, editable: true };
  const canvas = {
    stage,
    get scale() { return state.scale; },
    get nodes() { return state.nodes; },
    get editable() { return state.editable; },
    get editingId() { return state.editing; },
    get selectionBox() {
      const n = state.nodes.find((x) => x.id === state.selected);
      return n ? { id: n.id, k: n.k, t: n.t, x: n.x, y: n.y, w: n.w, h: n.h } : null;
    },
    apply(msgs) {
      if (!msgs.every(validToBridge)) return false;
      posted.push(...msgs);
      return true;
    },
  };
  const commits = [];
  const adds = [];
  const hd = {
    isSection: () => section,
    padState: () => ({ bp }),
    styleEntry: (k) => table.find((e) => e.key === k),
    refOf: (id) => ({ sel: "node", key: id }),
    onPadCommit: (c) => commits.push(c) && true,
    onAddSection: (index, el) => adds.push([index, el]),
    indexOf: (id) => ["a", "b"].indexOf(id),
    total: () => 2,
    nameOf: (id) => `Section ${id}`,
  };
  const direct = mountDirect(canvas, hd);
  const layer = stage.children[0];
  const strips = stage.children[1];
  const handle = (side) => layer.children.find((c) => c.attrs["data-side"] === side && c.attrs.role === "slider");
  const hatch = (side) => layer.children.find((c) => c.attrs["data-side"] === side && c.attrs["data-hm-pe-hatch"] !== undefined);
  const badge = layer.children.find((c) => c.attrs["data-hm-pe-padbadge"] !== undefined);
  return { canvas, state, direct, posted, commits, adds, layer, strips, handle, hatch, badge, hd };
}
const down = (r, side, e = {}) => r.handle(side).fire("pointerdown", { button: 0, pointerId: 7, clientX: 500, clientY: 300, ...e });
const move = (r, side, dx, dy, e = {}) => r.handle(side).fire("pointermove", { pointerId: 7, clientX: 500 + dx, clientY: 300 + dy, ...e });

test("a section shows four slider handles with the live px; a block shows none", () => {
  const r = rig();
  for (const s of ["top", "right", "bottom", "left"]) {
    assert.equal(r.handle(s).hidden, false);
    assert.equal(r.handle(s).attrs.role, "slider");
  }
  assert.equal(r.handle("top").attrs["aria-label"], "Top padding, 112px");
  assert.equal(r.handle("left").attrs["aria-valuenow"], "24");
  assert.equal(r.handle("top").style.top, "212px", "the inner edge of the top padding: (y 100 + 112)");
  assert.equal(r.handle("right").style.left, "1240px");
  const block = rig({ section: false });
  assert.ok(["top", "right", "bottom", "left"].every((s) => block.handle(s).hidden === true));
  const none = rig({ selected: null });
  assert.equal(none.layer.hidden, true);
});

test("no handles while text is edited, and when the page is not editable", () => {
  const r = rig({ editing: "a" });
  assert.equal(r.layer.hidden, true);
  const r2 = rig();
  r2.state.editable = false;
  r2.direct.update();
  assert.equal(r2.layer.hidden, true);
});

test("an old frame without pd falls back to the authored value, else 0", () => {
  const r = rig({ nodes: [pdNode("a", 0, 300)] });
  assert.equal(r.handle("top").attrs["aria-valuenow"], "0");
  const stage = new El("div");
  const nodes = [pdNode("a", 0, 300)];
  mountDirect({ stage, scale: 1, nodes, selectionBox: { id: "a", x: 0, y: 0, w: 1280, h: 300 }, editable: true, editingId: null, apply: () => true }, {
    isSection: () => true, padState: () => ({ bp: "base" }), styleEntry() {}, refOf: () => ({}), onPadCommit() {}, onAddSection() {}, indexOf() {}, total: () => 0,
    authored: (id, s) => (s === "top" ? "2rem" : null),
  });
  assert.equal(stage.children[0].children.find((c) => c.attrs["data-side"] === "top" && c.attrs.role).attrs["aria-valuenow"], "32");
});

test("dragging previews with setVar (no commit), updates the label, hatch and badge", () => {
  const r = rig();
  down(r, "top");
  move(r, "top", 0, 8);
  move(r, "top", 0, 24);
  assert.equal(r.commits.length, 0, "no commit while dragging");
  assert.equal(r.posted.length, 2);
  assert.deepEqual(r.posted.at(-1), { hm: 2, type: "setVar", sel: "node", key: "a", decls: [["padding-top", "136px"]] });
  assert.equal(r.handle("top").attrs["aria-label"], "Top padding, 136px");
  assert.equal(r.hatch("top").hidden, false);
  assert.equal(r.hatch("bottom").hidden, true);
  assert.equal(r.hatch("top").style.height, "136px");
  assert.equal(r.badge.hidden, false);
  assert.equal(r.badge.textContent, "136");
  assert.equal(r.handle("top").captured.length, 1, "the pointer is captured");
});

test("pointer-up commits exactly once with the final writes and the inspector's label", () => {
  const r = rig();
  down(r, "top");
  for (let i = 1; i <= 30; i++) move(r, "top", 0, i);
  r.handle("top").fire("pointerup", { pointerId: 7 });
  assert.equal(r.commits.length, 1);
  assert.deepEqual(r.commits[0], { id: "a", writes: padWrites({ id: "a", bp: "base", side: "top", px: 142 }), label: "Change Padding top" });
  assert.equal(r.badge.hidden, true);
  r.handle("top").fire("pointerup", { pointerId: 7 });
  assert.equal(r.commits.length, 1, "a stray second pointer-up does nothing");
});

test("a bottom drag grows against the pointer; the zoom divides the delta", () => {
  const r = rig({ scale: 0.5 });
  down(r, "bottom");
  move(r, "bottom", 0, -10);
  r.handle("bottom").fire("pointerup", { pointerId: 7 });
  assert.equal(r.commits[0].writes[0].key, "pb");
  assert.equal(r.commits[0].writes[0].value, "116px", "96 + 10 / 0.5");
});

test("shift snaps to a multiple of 8", () => {
  const r = rig();
  down(r, "top");
  move(r, "top", 0, 3, { shiftKey: true }); // 115 -> 112
  move(r, "top", 0, 10, { shiftKey: true }); // 122 -> 120
  r.handle("top").fire("pointerup", { pointerId: 7 });
  assert.equal(r.commits[0].writes[0].value, "120px");
});

test("alt, decided at pointer-down, writes the opposite side in the same commit and previews both", () => {
  const r = rig();
  down(r, "top", { altKey: true });
  move(r, "top", 0, 8); // alt released mid-drag changes nothing
  move(r, "top", 0, 16, { altKey: false });
  r.handle("top").fire("pointerup", { pointerId: 7 });
  assert.equal(r.commits.length, 1);
  assert.deepEqual(r.commits[0].writes.map((w) => [w.key, w.value]), [["pt", "128px"], ["pb", "128px"]]);
  const last2 = r.posted.slice(-2);
  assert.deepEqual(last2.map((m) => m.decls[0][0]), ["padding-top", "padding-bottom"]);
  const r2 = rig();
  down(r2, "top");
  move(r2, "top", 0, 8, { altKey: true }); // alt pressed after pointer-down is ignored
  r2.handle("top").fire("pointerup", { pointerId: 7 });
  assert.equal(r2.commits[0].writes.length, 1);
});

test("Esc restores the preview and commits nothing; the next pointer-up does nothing", () => {
  const r = rig();
  down(r, "top");
  move(r, "top", 0, 20);
  fireDoc("keydown", { key: "Escape" });
  assert.equal(r.commits.length, 0);
  assert.deepEqual(r.posted.at(-1).decls, [["padding-top", null]], "an inline value cleared, the page's own padding returns");
  r.handle("top").fire("pointerup", { pointerId: 7 });
  assert.equal(r.commits.length, 0);
  assert.equal(r.handle("top").attrs["aria-label"], "Top padding, 112px");
  assert.equal((docListeners.keydown ?? []).length, 0, "the Esc listener is gone");
});

test("pointercancel restores the preview and commits nothing; a click without movement commits nothing", () => {
  const r = rig();
  down(r, "left");
  move(r, "left", 30, 0);
  r.handle("left").fire("pointercancel", { pointerId: 7 });
  assert.equal(r.commits.length, 0);
  assert.deepEqual(r.posted.at(-1).decls, [["padding-left", null]]);
  const n = r.posted.length;
  down(r, "left");
  r.handle("left").fire("pointerup", { pointerId: 7 });
  assert.equal(r.commits.length, 0);
  assert.equal(r.posted.length, n, "nothing previewed, nothing to restore");
});

test("a refused commit restores the preview", () => {
  const r = rig();
  r.hd.onPadCommit = () => false;
  down(r, "top");
  move(r, "top", 0, 8);
  r.handle("top").fire("pointerup", { pointerId: 7 });
  assert.deepEqual(r.posted.at(-1).decls, [["padding-top", null]]);
});

test("at a state (hover) the frame is not previewed, but the drag still commits at that key", () => {
  const r = rig({ bp: "md:hover" });
  down(r, "top");
  move(r, "top", 0, 8);
  assert.equal(r.posted.length, 0);
  r.handle("top").fire("pointerup", { pointerId: 7 });
  assert.equal(r.commits[0].writes[0].bp, "md:hover");
  assert.equal(r.commits[0].label, "Change Padding top (Hover, Tablet)");
});

test("a Tablet drag commits at md only", () => {
  const r = rig({ bp: "md" });
  down(r, "right");
  move(r, "right", -16, 0);
  r.handle("right").fire("pointerup", { pointerId: 7 });
  assert.deepEqual(r.commits[0].writes, [{ op: "setStyle", id: "a", bp: "md", key: "pr", value: "56px" }]);
  assert.equal(r.commits[0].label, "Change Padding right (Tablet)");
});

test("a frame report during the drag does not end it or move the value", () => {
  const r = rig();
  down(r, "top");
  move(r, "top", 0, 8);
  r.state.nodes = [pdNode("a", 100, 408, [999, 40, 96, 24]), pdNode("b", 508, 300)];
  r.direct.update();
  assert.equal(r.handle("top").attrs["aria-label"], "Top padding, 120px");
  r.handle("top").fire("pointerup", { pointerId: 7 });
  assert.equal(r.commits[0].writes[0].value, "120px");
});

test("arrow keys step 1 (shift 8), each press is one commit; grow with up/right", () => {
  const r = rig();
  r.handle("top").fire("keydown", { key: "ArrowUp" });
  r.handle("top").fire("keydown", { key: "ArrowUp", shiftKey: true });
  r.handle("top").fire("keydown", { key: "ArrowDown" });
  assert.deepEqual(r.commits.map((c) => c.writes[0].value), ["113px", "121px", "120px"], "the value follows before the frame reports it");
  assert.equal(r.commits.length, 3);
  assert.equal(r.commits[0].label, "Change Padding top");
  r.handle("left").fire("keydown", { key: "ArrowLeft" });
  assert.equal(r.commits[3].writes[0].value, "23px");
  r.handle("top").fire("keydown", { key: "Enter" });
  assert.equal(r.commits.length, 4);
});

test("arrow keys stop at 0 without committing, and alt carries the opposite side", () => {
  const r = rig({ nodes: [pdNode("a", 0, 100, [0, 0, 0, 0])] });
  r.handle("top").fire("keydown", { key: "ArrowDown" });
  assert.equal(r.commits.length, 0);
  r.handle("top").fire("keydown", { key: "ArrowUp", altKey: true });
  assert.deepEqual(r.commits[0].writes.map((w) => w.key), ["pt", "pb"]);
});

test("the inspector's hover event shows that side's hatch and value; null clears it", () => {
  const r = rig();
  fireDoc("hm-pad-hover", { detail: { side: "left" } });
  assert.equal(r.hatch("left").hidden, false);
  assert.equal(r.hatch("top").hidden, true);
  assert.equal(r.badge.hidden, false);
  assert.equal(r.badge.textContent, "24");
  assert.equal(r.hatch("left").style.width, "24px");
  fireDoc("hm-pad-hover", { detail: { side: null } });
  assert.ok(["top", "right", "bottom", "left"].every((s) => r.hatch(s).hidden));
  assert.equal(r.badge.hidden, true);
});

test("destroy removes the layers and the listeners", () => {
  const r = rig();
  const before = (docListeners["hm-pad-hover"] ?? []).length;
  r.direct.destroy();
  assert.equal(r.layer.removed, true);
  assert.equal(r.strips.removed, true);
  assert.equal((docListeners["hm-pad-hover"] ?? []).length, before - 1);
});

const sections = () => [pdNode("a", 0, 200), pdNode("x", 10, 20), pdNode("b", 200, 200)].map((n) => (n.id === "x" ? { ...n, p: "a" } : n));

test("a strip sits on every boundary, leading and trailing included, and a click adds at the doc index", () => {
  const r = rig({ nodes: sections() });
  const strips = r.strips.children;
  assert.equal(strips.length, 3);
  assert.deepEqual(strips.map((s) => s.style.top), ["0px", "200px", "400px"]);
  assert.equal(strips[1].attrs["aria-label"], "Add section after Section a");
  assert.equal(strips[1].attrs.class, "hm-pe__gapline", "not the breadcrumb strip class");
  assert.equal(find(strips[0], () => true).textContent, "Add section");
  strips.forEach((s) => s.fire("click"));
  assert.deepEqual(r.adds.map(([i]) => i), [0, 1, 2], "leading 0, between = the doc index of the one below, trailing = total");
  assert.equal(r.adds[1][1], strips[1], "the picker anchors on the strip");
});

test("strips follow layout changes in place and go while text is edited", () => {
  const r = rig({ nodes: sections() });
  const first = r.strips.children[1];
  r.state.nodes = [pdNode("a", 0, 300), pdNode("b", 300, 200)];
  r.direct.update();
  assert.equal(r.strips.children[1], first, "the element is reused");
  assert.equal(first.style.top, "300px");
  r.state.nodes = [pdNode("a", 0, 300)];
  r.direct.update();
  assert.equal(r.strips.children.length, 2);
  r.state.editing = "a";
  r.direct.update();
  assert.equal(r.strips.hidden, true);
});

test("strips scale with the zoom", () => {
  const r = rig({ nodes: sections(), scale: 0.5 });
  assert.deepEqual(r.strips.children.map((s) => s.style.top), ["0px", "100px", "200px"]);
});

test("a padding handle stacks above the boundary strips: a section edge with 0 padding has both", () => {
  const css = readFileSync(new URL("../../../backend/web/admin/css/pages/pageeditor.css", import.meta.url), "utf8");
  const handle = /\.hm-pe__padh \{([^}]*)\}/.exec(css)?.[1] ?? "";
  const strip = /\.hm-pe__gapline \{([^}]*)\}/.exec(css)?.[1] ?? "";
  const z = (rule) => (/z-index:\s*var\(--z-raised\)/.test(rule) ? 1 : 0);
  assert.ok(z(handle) > z(strip), `handle z-index ${z(handle)} must exceed the strip's ${z(strip)}`);
});

test("the hatch is shown by dropping `hidden`: no stylesheet rule keeps it transparent behind attributes direct.js never sets", () => {
  const css = readFileSync(new URL("../../../backend/web/admin/css/pages/pageeditor.css", import.meta.url), "utf8");
  const rule = /\.hm-pe__hatch \{([^}]*)\}/.exec(css)?.[1] ?? "";
  assert.ok(rule, "no .hm-pe__hatch rule");
  assert.doesNotMatch(rule, /opacity:\s*0\b/);
  assert.doesNotMatch(css, /\.hm-pe__hatch\[data-(show|active)\]/);
});
