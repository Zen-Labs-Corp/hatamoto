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
  removeAttribute(k) { delete this.attrs[k]; }
  hasAttribute(k) { return k in this.attrs; }
  getAttribute(k) { return k in this.attrs ? this.attrs[k] : null; }
  append(...kids) { kids.forEach((k) => (k.parent = this)); this.children.push(...kids); }
  addEventListener(t, f) { (this.listeners[t] ??= []).push(f); }
  remove() {
    this.removed = true;
    if (this.parent) this.parent.children = this.parent.children.filter((c) => c !== this);
  }
  focus(o) { this.focused = o ?? true; }
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
const { overlaps, pillShift } = await import(new URL("canvas/chromemodel.js", root));
const { validToBridge } = await import(new URL("canvas/protocol.js", root));
const { padWrites } = await import(new URL("canvas/padding.js", root));
const table = JSON.parse(readFileSync(new URL("../../../backend/testdata/stylespec/table.json", import.meta.url), "utf8"));

const pdNode = (id, y, h, pd) => ({ id, k: "s", t: "hero", p: "", x: 0, y, w: 1280, h, ...(pd ? { pd } : {}) });
const find = (e, f) => e.children.find(f);

function rig({ nodes, selected = "a", scale = 1, section = true, bp = "base", editing = null, hover = null } = {}) {
  const stage = new El("div");
  const posted = [];
  const state = { nodes: nodes ?? [pdNode("a", 100, 400, [112, 40, 96, 24]), pdNode("b", 500, 300, [10, 10, 10, 10])], selected, scale, editing, hover, editable: true };
  const canvas = {
    stage,
    get scale() { return state.scale; },
    get nodes() { return state.nodes; },
    get editable() { return state.editable; },
    get editingId() { return state.editing; },
    get hoverId() { return state.hover; },
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
  const history = []; // stands in for the store's past: a new object per history change
  const adds = [];
  const hd = {
    isSection: () => section,
    padState: () => ({ bp }),
    styleEntry: (k) => table.find((e) => e.key === k),
    refOf: (id) => ({ sel: "node", key: id }),
    onPadCommit: (c) => {
      commits.push(c);
      const last = history.at(-1);
      history.splice(last && c.coalesce && last.ckey === c.coalesce ? -1 : history.length, 1, { ckey: c.coalesce }); // like the store: a session key merges into the last entry
      return true;
    },
    stamp: () => history.at(-1),
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
  return { history, canvas, state, direct, posted, commits, adds, layer, strips, handle, hatch, badge, hd };
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
  assert.equal(r.handle("top").style.top, "144px", "the middle of the top band (y 100 + 112/2 = 156), the 24px box hanging 12px above it");
  assert.equal(r.handle("right").style.left, "1260px", "the middle of the right band (1280 - 40/2)");
  assert.equal(r.handle("left").style.left, "12px", "the middle of the left band (12), kept 12px inside the frame");
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

test("a measured pd wins over a token authored value: the drag starts from the rendered 48px, not 0", () => {
  const stage = new El("div");
  const nodes = [{ id: "a", k: "n", t: "container", p: "", x: 0, y: 0, w: 1280, h: 300, pd: [48, 0, 0, 0] }];
  const commits = [];
  const canvas = { stage, scale: 1, nodes, selectionBox: { id: "a", x: 0, y: 0, w: 1280, h: 300 }, editable: true, editingId: null, apply: () => true };
  mountDirect(canvas, {
    isSection: () => true, padState: () => ({ bp: "base" }), styleEntry: (k) => table.find((e) => e.key === k), refOf: () => ({ sel: "node", key: "a" }),
    onPadCommit: (c) => commits.push(c) && true, onAddSection() {}, indexOf() {}, total: () => 0,
    authored: (id, s) => (s === "top" ? "space-9" : null), // a token: startPx cannot read it
  });
  const top = stage.children[0].children.find((c) => c.attrs["data-side"] === "top" && c.attrs.role === "slider");
  assert.equal(top.attrs["aria-valuenow"], "48");
  top.fire("pointerdown", { button: 0, pointerId: 7, clientX: 500, clientY: 300 });
  top.fire("pointermove", { pointerId: 7, clientX: 500, clientY: 310 });
  top.fire("pointerup", { pointerId: 7 });
  assert.equal(commits[0].writes[0].value, "58px");
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

test("the grabbed handle follows the pointer, not the band's middle (a lagging handle leaves the pointer over the frame, which swallows the release)", () => {
  const r = rig();
  const top0 = parseFloat(r.handle("top").style.top);
  down(r, "top");
  move(r, "top", 0, 24);
  assert.equal(parseFloat(r.handle("top").style.top), top0 + 24, "the handle moved by the pointer's 24px, not by half the padding change");
  move(r, "top", 0, 40);
  assert.equal(parseFloat(r.handle("top").style.top), top0 + 40);
  const left0 = parseFloat(r.handle("left").style.left);
  r.handle("top").fire("pointerup", { pointerId: 7 });
  down(r, "left");
  move(r, "left", 16, 0);
  assert.equal(parseFloat(r.handle("left").style.left), left0 + 16, "a side handle follows along x");
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

test("pointer-down focuses the handle (so Esc reaches the host); losing the capture without a pointer-up cancels, after one it does not", () => {
  const r = rig();
  down(r, "top");
  assert.deepEqual(r.handle("top").focused, { preventScroll: true });
  move(r, "top", 0, 20);
  r.handle("top").fire("lostpointercapture", { pointerId: 7 });
  assert.equal(r.commits.length, 0);
  assert.deepEqual(r.posted.at(-1).decls, [["padding-top", null]]);
  const r2 = rig();
  down(r2, "top");
  move(r2, "top", 0, 20);
  r2.handle("top").fire("pointerup", { pointerId: 7 });
  r2.handle("top").fire("lostpointercapture", { pointerId: 7 });
  assert.equal(r2.commits.length, 1);
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

test("arrow presses in one focus session share a coalesce key; a blur, an undo (history moved) or another side starts a new one and drops the stale value", () => {
  const r = rig();
  const key = (i) => r.commits[i].coalesce;
  const press = (side = "top") => r.handle(side).fire("keydown", { key: "ArrowUp" });
  press();
  press();
  assert.equal(key(0), key(1));
  assert.equal(r.commits[1].writes[0].value, "114px");
  assert.equal(r.history.length, 1, "two presses, one history entry");
  press("left");
  assert.notEqual(key(2), key(0), "another side is another entry");
  r.handle("top").fire("blur");
  press();
  assert.notEqual(key(3), key(0), "a new focus session");
  r.history.pop(); // an undo: the last entry is gone, the frame still reports 112
  r.handle("top").fire("keydown", { key: "ArrowUp" });
  assert.equal(r.commits.at(-1).writes[0].value, "113px", "starts from the reported value, not the pending 113+1");
  assert.notEqual(r.commits.at(-1).coalesce, key(3));
});

test("an Alt press and a plain press never share a coalesce key: undo must restore every side written", () => {
  const r = rig();
  r.handle("top").fire("keydown", { key: "ArrowDown" });
  r.handle("top").fire("keydown", { key: "ArrowDown", altKey: true });
  assert.notEqual(r.commits[0].coalesce, r.commits[1].coalesce);
});

test("arrow keys stop at 0 without committing, and alt carries the opposite side", () => {
  const r = rig({ nodes: [pdNode("a", 0, 100, [0, 0, 0, 0])] });
  r.handle("top").fire("keydown", { key: "ArrowDown" });
  assert.equal(r.commits.length, 0);
  r.handle("top").fire("keydown", { key: "ArrowUp", altKey: true });
  assert.deepEqual(r.commits[0].writes.map((w) => w.key), ["pt", "pb"]);
});

test("hovering a handle shows its band's hatch and a value badge off to the side; leaving clears them", () => {
  const r = rig();
  r.handle("top").fire("pointerenter");
  assert.equal(r.hatch("top").hidden, false);
  assert.equal(r.badge.textContent, "112");
  assert.equal(r.badge.style.left, "680px", "40px right of the band's middle (640)");
  r.handle("left").fire("pointerenter");
  assert.equal(r.badge.style.top, "278px", "30px above the left band's middle (100 + 112 + 192/2 = 308)");
  r.handle("left").fire("pointerleave");
  assert.equal(r.hatch("left").hidden, true);
  assert.equal(r.badge.hidden, true);
});

test("a handle hover does not stick to the next section when the selection moves under a still pointer", () => {
  const r = rig();
  r.handle("top").fire("pointerenter");
  assert.equal(r.hatch("top").hidden, false);
  r.state.selected = "b"; // no pointerleave comes: the handles moved away from the pointer
  r.direct.update();
  assert.equal(r.hatch("top").hidden, true);
  assert.equal(r.badge.hidden, true);
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

test("a pill shows around the hover, alone on an empty page and on the selected section's edges only while it is hovered; the boundary after the selection is the one tab stop", () => {
  const shows = (r) => r.strips.children.map((s) => s.attrs["data-show"] ?? null);
  const r = rig({ nodes: sections() }); // a selected, nothing hovered
  assert.deepEqual(shows(r), [null, null, null], "at rest the selected section's edges show no pill: its handles and badge own them");
  assert.deepEqual(r.strips.children.map((s) => s.attrs["data-sel"] ?? null), ["", "", null], "its edges are marked, so the pill is drawn clear of the centred handle and badge");
  assert.deepEqual(r.strips.children.map((s) => s.attrs.tabindex), ["-1", "0", "-1"], "the boundary after it is the one tab stop");
  r.state.selected = "x"; // a block inside a: its section's boundary
  r.direct.update();
  assert.deepEqual(r.strips.children.map((s) => s.attrs.tabindex), ["-1", "0", "-1"]);
  r.state.selected = null;
  r.state.hover = "b";
  r.direct.update();
  assert.deepEqual(shows(r), [null, "hover", "hover"], "the hovered section's two boundaries");
  assert.deepEqual(r.strips.children.map((s) => s.attrs.tabindex), ["-1", "-1", "-1"]);
  r.state.hover = null; // the pointer crossed onto the pill: the frame's hover ended, the pill stays
  r.direct.update();
  assert.deepEqual(shows(r), [null, "hover", "hover"]);
  const own = rig({ nodes: sections(), hover: "a" }); // hovering the selected section shows its two edge pills
  assert.deepEqual(shows(own), ["sel", "sel", null]);
  const next = rig({ nodes: sections(), hover: "b" }); // hovering the next section shows its edges, the selected section's bottom one included
  assert.deepEqual(shows(next), [null, "hover", "hover"]);
  assert.equal(rig({ nodes: [], selected: null }).strips.children[0].attrs["data-show"], "page", "an empty page's only pill shows");
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

test("the boundary row never takes the pointer itself; its pill does while shown; the focus ring is real", () => {
  const css = readFileSync(new URL("../../../backend/web/admin/css/pages/pageeditor.css", import.meta.url), "utf8");
  const line = /\.hm-pe__gapline \{([^}]*)\}/.exec(css)?.[1] ?? "";
  assert.match(line, /pointer-events:\s*none/);
  assert.match(line, /translateY\(-50%\)/, "centred on the boundary");
  assert.match(css, /\.hm-pe__gapline\[data-show\] \.hm-pe__pill \{[^}]*pointer-events: auto/, "only the shown pill takes the pointer, not the full-width row");
  assert.match(css, /\.hm-pe__gapline:not\(\[data-show\]\):not\(\[tabindex="0"\]\) \{ visibility: hidden/, "an inert row is hidden; the tab stop stays focusable");
  assert.match(css, /\.hm-pe__gapline:focus-visible \.hm-pe__pill \{ outline/);
  assert.doesNotMatch(css, /\.hm-pe__pill:focus-visible/, "the pill is never the focused element");
});

test("the hatch is shown by dropping `hidden`: no stylesheet rule keeps it transparent behind attributes direct.js never sets", () => {
  const css = readFileSync(new URL("../../../backend/web/admin/css/pages/pageeditor.css", import.meta.url), "utf8");
  const rule = /\.hm-pe__hatch \{([^}]*)\}/.exec(css)?.[1] ?? "";
  assert.ok(rule, "no .hm-pe__hatch rule");
  assert.doesNotMatch(rule, /opacity:\s*0\b/);
  assert.doesNotMatch(css, /\.hm-pe__hatch\[data-(show|active)\]/);
});

test("the stylesheet keeps no dead `[data-active]` rule (direct.js never sets it) and declares the --pe-* aliases once", () => {
  const css = readFileSync(new URL("../../../backend/web/admin/css/pages/pageeditor.css", import.meta.url), "utf8");
  assert.doesNotMatch(css, /\[data-active\]/);
  assert.equal(css.match(/--pe-guide-pad:\s*var/g)?.length, 1);
});

test("padding handles are at least 24px on both axes (WCAG 2.5.8)", () => {
  const css = readFileSync(new URL("../../../backend/web/admin/css/pages/pageeditor.css", import.meta.url), "utf8");
  const sizes = [...css.matchAll(/\.hm-pe__padh\[data-side="\w+"\](?:, \.hm-pe__padh\[data-side="\w+"\])? \{ width: (\d+)px; height: (\d+)px; cursor/g)];
  assert.equal(sizes.length, 2, "the two handle size rules");
  assert.match(css, /\.hm-pe__padh\[data-side="top"\] \{ transform: translate\(-50%, 0\)/, "the top handle is not under the floating toolbar");
  for (const m of sizes) assert.ok(+m[1] >= 24 && +m[2] >= 24, m[0]);
});

test("a short section: the top handle never lies over the bottom one (the 24px targets would cover each other)", () => {
  const r = rig({ nodes: [pdNode("a", 100, 30, [0, 0, 0, 0])] });
  const top = parseFloat(r.handle("top").style.top);
  const bottomStart = parseFloat(r.handle("bottom").style.top) - 12; // centred on the inner edge, 24px tall
  assert.ok(top + 24 <= bottomStart, `top handle ends at ${top + 24}, bottom starts at ${bottomStart}`);
});

test("a selected section's pill stays at the row's right end when it clears the handle and badge, and drops to its own row when it does not", () => {
  const wide = { handle: { x: 598, y: 478, w: 44, h: 24 }, badge: { x: 584, y: 506, w: 72, h: 22 } }; // a 1240px stage: both centred, far left of the pill
  const pill = (W) => ({ x: W - 16 - 88, y: 487, w: 88, h: 26 }); // the row's right end, centred on a boundary at y 500
  assert.equal(pillShift(pill(1240), [wide.handle, wide.badge], 1), 0, "wide: stays at the right end");
  // a phone at 50%: the stage is 195px, the handle [75,119] and badge [61,133] reach the pill [91,179]
  const narrow = [{ x: 75, y: 480, w: 44, h: 24 }, { x: 61, y: 508, w: 72, h: 22 }];
  const dy = pillShift(pill(195), narrow, 1);
  assert.equal(dy, 508 + 22 + 4 - 487, "narrow: its own row under the badge");
  const moved = { ...pill(195), y: 487 + dy };
  assert.ok(narrow.every((o) => moved.y >= o.y + o.h + 4 || moved.x + moved.w + 4 <= o.x || o.x + o.w + 4 <= moved.x), "and clear of every obstacle");
  // the top pill goes above the boundary
  const top = [{ x: 75, y: 496, w: 44, h: 24 }, { x: 61, y: 466, w: 72, h: 22 }];
  assert.ok(pillShift(pill(195), top, -1) < 0, "above for a top edge");
  // a toolbar over the top pill's upper edge: above is no better, so it goes inward
  assert.ok(pillShift(pill(390), [{ x: 100, y: 450, w: 290, h: 40 }], -1) > 0, "inward when above is blocked");
});

test("a pill never leaves the visible stage: the page-top pill on a phone at 50% goes inward, not under the sticky bar", () => {
  // the stage starts at y 40 (below the bar); the top boundary is at y 40, the top handle [75,119] x [36,60], the toolbar inside at y 48
  const bounds = { x: 0, y: 40, w: 195, h: 600 };
  const pill = { x: 91, y: 27, w: 88, h: 26 };
  const handle = { x: 75, y: 36, w: 44, h: 24 };
  const dy = pillShift(pill, [handle], -1, 4, bounds);
  assert.ok(pill.y + dy >= bounds.y, `the pill's top is at ${pill.y + dy}, above the bounds' ${bounds.y}`);
  assert.ok(pill.y + dy >= handle.y + handle.h + 4, "inward: below the handle");
  assert.equal(pillShift(pill, [handle], -1, 4, null), 36 - 4 - 26 - 27, "without bounds the old spot above the handle");
  // nothing free: the caller keeps the default and yields the neighbour
  assert.equal(pillShift(pill, [handle], -1, 4, { x: 0, y: 40, w: 195, h: 40 }), null);
});

test("a bottom pill does not land on the next boundary's pill when the next section is short (60px)", () => {
  const sel = { x: 91, y: 487, w: 88, h: 26 }; // boundary at y 500
  const handle = { x: 75, y: 480, w: 44, h: 24 };
  const badge = { x: 61, y: 508, w: 72, h: 22 };
  const next = { x: 53, y: 530, w: 88, h: 26 }; // the next boundary, 60px further down (centred)
  const dy = pillShift(sel, [handle, badge, next], 1, 4, { x: 0, y: 40, w: 195, h: 900 });
  assert.notEqual(dy, null);
  const moved = { ...sel, y: sel.y + dy };
  assert.ok([handle, badge, next].every((o) => !overlaps(moved, o)), `the moved pill ${JSON.stringify(moved)} touches an obstacle`);
});

test("pillShift: a pill whose own row is outside the bounds stays on it (a section taller than the pane)", () => {
  const obstacle = { x: 0, y: 440, w: 300, h: 40 };
  const pill = { x: 91, y: 900, w: 88, h: 26 };
  assert.equal(pillShift(pill, [obstacle], 1, 4, { x: 0, y: 40, w: 195, h: 700 }), 0, "default row below the bounds");
  assert.equal(pillShift({ ...pill, y: -60 }, [obstacle], -1, 4, { x: 0, y: 40, w: 195, h: 700 }), 0, "default row above the bounds");
});
