// The canvas host, v2 inline-text plumbing: selection reports reach the handler, text messages carry html or value,
// and format commands are posted to the active frame. Runs createCanvas against a stub DOM.
// Run: node --test scripts/admin-qa/unit/canvas-host-v2.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import { installDOM } from "./canvas-host-stub.mjs";

const dom = installDOM();
const { createCanvas } = await import(new URL("../../../backend/web/admin/js/canvas/host.js", import.meta.url));

const rig = (handlers = {}) => {
  const host = document.createElement("div");
  const calls = [];
  const rec = (name) => (...a) => calls.push([name, ...a]);
  const canvas = createCanvas(host, { onSelect() {}, onNodeText: rec("text"), onNodeTextEnd: rec("end"), onSelection: rec("selection"), ...handlers }, { v2: true });
  dom.handshake(host, canvas);
  return { host, canvas, calls, frame: dom.frames(host)[1] };
};

test("a text message hands html or value to onNodeText by prop", () => {
  const { host, calls } = rig();
  dom.deliver(host, { hm: 2, type: "text", id: "a2", k: "n", prop: "html", html: "a <strong>b</strong>" });
  dom.deliver(host, { hm: 2, type: "text", id: "a2", k: "n", prop: "text", value: "plain" });
  dom.deliver(host, { hm: 2, type: "textend", id: "a2", k: "n", prop: "html" });
  assert.deepEqual(calls, [["text", "a2", "html", "a <strong>b</strong>"], ["text", "a2", "text", "plain"], ["end", "a2", "html"]]);
});

test("a selection report reaches onSelection with the validated fields only", () => {
  const { host, calls } = rig();
  const rect = { x: 10, y: 20, w: 30, h: 16 };
  dom.deliver(host, { hm: 2, type: "selection", id: "a2", k: "n", prop: "html", rect, marks: ["bold", "link"], junk: 1 });
  dom.deliver(host, { hm: 2, type: "selection", id: "a2", k: "n", prop: "html", rect: { x: 1, y: 1, w: NaN, h: 1 }, marks: [] }); // refused
  assert.equal(calls.length, 1);
  assert.deepEqual(calls[0], ["selection", { hm: 2, type: "selection", id: "a2", k: "n", prop: "html", rect, marks: ["bold", "link"] }]);
});

test("format posts a validated command to the active frame; a bad one is dropped", () => {
  const { canvas, frame } = rig();
  const before = frame.posted.length;
  canvas.format("a2", "link", { href: "https://example.com/", newTab: true });
  canvas.format("a2", "link"); // link needs an href
  canvas.format("a2", "explode");
  canvas.format("a2", "done");
  const sent = frame.posted.slice(before);
  assert.deepEqual(sent, [
    { hm: 2, type: "format", sel: "node", key: "a2", cmd: "link", href: "https://example.com/", newTab: true },
    { hm: 2, type: "format", sel: "node", key: "a2", cmd: "done" },
  ]);
});

test("the existing stage and scale getters serve the formatting bar", () => {
  const { canvas, host } = rig();
  assert.equal(canvas.stage, host.children[0]);
  assert.equal(typeof canvas.scale, "number");
});

test("a v1 select after a v2 select never replaces the deeper element (a field click of the same section still passes)", () => {
  const { host, calls } = rig({ onSelect: (...a) => calls.push(["select", ...a]), onSelectNode: (...a) => calls.push(["node", ...a]) });
  dom.deliver(host, { hm: 2, type: "select", id: "h1", k: "n", shift: false, ctrl: false });
  dom.deliver(host, { hm: 1, type: "hm:select", sectionId: "sec", path: "headline" }); // another section's field: ignored
  dom.deliver(host, { hm: 1, type: "hm:select", sectionId: "h1" }); // v2 reports selection itself: ignored without a field
  assert.deepEqual(calls, [["node", "h1", { shift: false, ctrl: false }]]);
  dom.deliver(host, { hm: 2, type: "select", id: "sec", k: "s", shift: false, ctrl: false });
  dom.deliver(host, { hm: 1, type: "hm:select", sectionId: "sec", path: "headline" }); // same section, a field click: focus the field
  assert.deepEqual(calls.slice(-1), [["select", "sec", "headline"]]);
});

test("an explicit zoom survives fit(); Fit follows the pane again; the scale never goes below 0.5", () => {
  const { canvas } = rig();
  canvas.setViewport(1280, 700);
  const fitted = canvas.scale;
  assert.ok(fitted >= 0.5 && fitted < 1);
  canvas.setZoom(75);
  canvas.fit(900);
  assert.equal(canvas.scale, 0.75);
  assert.equal(canvas.zoom, 75);
  canvas.setViewport(390, 900);
  assert.equal(canvas.scale, 0.75, "a device change keeps an explicit zoom");
  canvas.setZoom("fit");
  canvas.fit(100);
  assert.equal(canvas.scale, 0.5, "the floor");
});

test("nodeChain reads the parent ids of the latest layout", () => {
  const { canvas, host } = rig();
  const box = { x: 0, y: 0, w: 10, h: 10 };
  dom.deliver(host, { hm: 2, type: "layout", docH: 100, main: box, nodes: [{ id: "s1", k: "s", t: "section", p: "", ...box }, { id: "n1", k: "n", t: "heading", p: "s1", ...box }] });
  assert.deepEqual(canvas.nodeChain("n1").map((c) => c.id), ["s1", "n1"]);
});

test("the toolbar is built once and survives mark redraws (hover, selection, layout)", async () => {
  const { host, canvas } = rig({ nodeLabel: (id, _k, t) => t, onAction() {} });
  const box = { x: 0, y: 0, w: 100, h: 20 };
  dom.deliver(host, { hm: 2, type: "layout", docH: 400, main: { x: 0, y: 0, w: 100, h: 400 }, nodes: [{ id: "s1", k: "s", t: "section", p: "", ...box }, { id: "h1", k: "n", t: "heading", p: "s1", ...box }] });
  dom.deliver(host, { hm: 2, type: "select", id: "h1", k: "n", shift: false, ctrl: false });
  await new Promise((r) => setTimeout(r, 150)); // the strip and toolbar are a lazy chunk
  const find = () => { let hit = null; const walk = (e) => { if (e.attrs?.["data-hm-pe-toolbar"] !== undefined) hit = e; (e.children ?? []).forEach(walk); }; walk(canvas.stage); return hit; };
  const bar = find();
  assert.ok(bar, "the toolbar is mounted in the stage");
  assert.equal(bar.attrs.role, "toolbar");
  dom.deliver(host, { hm: 2, type: "hover", id: "s1", k: "s" });
  dom.deliver(host, { hm: 2, type: "layout", docH: 420, main: { x: 0, y: 0, w: 100, h: 420 }, nodes: [{ id: "s1", k: "s", t: "section", p: "", ...box }, { id: "h1", k: "n", t: "heading", p: "s1", ...box }] });
  assert.equal(find(), bar, "same element after the redraws");
  assert.equal(bar.hidden, false);
});

test("the toolbar hint under the formatting bar moves down when the link row opens", async () => {
  let watch = null;
  const seen = [];
  globalThis.requestAnimationFrame = () => {}; // the chunk looks once more a frame later; here the test drives it
  globalThis.ResizeObserver = class { constructor(f) { watch = f; } observe(e) { seen.push(e); } disconnect() {} };
  const { host, canvas } = rig({ nodeLabel: (id, _k, t) => t, onAction() {} });
  const fmt = { offsetTop: 40, offsetHeight: 30 }; // the formatting bar under the element: one row
  canvas.stage.querySelector = (q) => (q === ".hm-pe__fmt:not([hidden])" ? fmt : null);
  const box = { x: 0, y: 0, w: 100, h: 20 };
  dom.deliver(host, { hm: 2, type: "layout", docH: 400, main: { x: 0, y: 0, w: 100, h: 400 }, nodes: [{ id: "s1", k: "s", t: "section", p: "", ...box }, { id: "h1", k: "n", t: "heading", p: "s1", ...box }] });
  dom.deliver(host, { hm: 2, type: "select", id: "h1", k: "n", shift: false, ctrl: false });
  dom.deliver(host, { hm: 2, type: "selection", id: "h1", k: "n", prop: "html", rect: { x: 1, y: 1, w: 5, h: 5 }, marks: [] });
  await new Promise((r) => setTimeout(r, 150)); // the toolbar is a lazy chunk
  let bar = null;
  const walk = (e) => { if (e.attrs?.["data-hm-pe-toolbar"] !== undefined) bar = e; (e.children ?? []).forEach(walk); };
  walk(canvas.stage);
  assert.ok(bar && watch, "the toolbar mounted and watches the formatting bar");
  assert.equal(seen.at(-1), fmt, "it watches the formatting bar's size");
  Object.assign(bar, { offsetWidth: 200, offsetHeight: 20 });
  watch();
  const before = parseFloat(bar.style.top);
  assert.ok(before >= 40 + 30, `the hint starts below the one-row formatting bar, at ${before}`);
  fmt.offsetHeight = 90; // the link row opened
  watch();
  assert.ok(parseFloat(bar.style.top) >= 40 + 90, `the hint starts below the taller formatting bar, at ${bar.style.top}`);
  delete globalThis.ResizeObserver;
  delete globalThis.requestAnimationFrame;
});

test("a k key reaches onKey with the edit state: in a text edit it is the link, otherwise the command palette", () => {
  const { host, calls } = rig({ onKey: (...a) => calls.push(["key", ...a]) });
  dom.deliver(host, { hm: 2, type: "key", key: "k", shift: false });
  dom.deliver(host, { hm: 2, type: "selection", id: "a2", k: "n", prop: "html", rect: { x: 1, y: 1, w: 5, h: 5 }, marks: [] });
  dom.deliver(host, { hm: 2, type: "key", key: "k", shift: false });
  dom.deliver(host, { hm: 2, type: "textend", id: "a2", k: "n", prop: "html" });
  dom.deliver(host, { hm: 2, type: "key", key: "k", shift: false });
  assert.deepEqual(calls.filter((c) => c[0] === "key").map((c) => c[3]), [{ editing: false }, { editing: true }, { editing: false }]);
});

test("edit() posts the edit message for a node to the active frame", () => {
  const { canvas, frame } = rig();
  const before = frame.posted.length;
  canvas.edit("a2");
  canvas.edit("sec", "section");
  assert.deepEqual(frame.posted.slice(before), [{ hm: 2, type: "edit", sel: "node", key: "a2" }, { hm: 2, type: "edit", sel: "section", key: "sec" }]);
});

test("a swapped-in frame ends the old frame's edit: Ctrl+K is the palette again", () => {
  const { host, canvas } = rig();
  dom.deliver(host, { hm: 2, type: "selection", id: "a2", k: "n", prop: "html", rect: { x: 1, y: 1, w: 5, h: 5 }, marks: [] });
  assert.equal(canvas.editingId, "a2");
  canvas.load("/preview", 99);
  for (const fn of dom.frames(host)[0].listeners.load ?? []) fn();
  assert.equal(canvas.editingId, null);
});

test("a stale load into the hidden buffer leaves the active frame's edit alone", () => {
  const { host, canvas } = rig();
  dom.deliver(host, { hm: 2, type: "selection", id: "a2", k: "n", prop: "html", rect: { x: 1, y: 1, w: 5, h: 5 }, marks: [] });
  canvas.load("/preview", 99);
  canvas.load("/preview", 50); // overwrites the buffer's rev with an older one: this load is stale
  for (const fn of dom.frames(host)[0].listeners.load ?? []) fn();
  assert.equal(canvas.editingId, "a2");
});

test("the hover is a dashed outline and nothing else: no name tag over the first line", () => {
  const { host, canvas } = rig();
  const box = { x: 0, y: 0, w: 100, h: 20 };
  dom.deliver(host, { hm: 2, type: "layout", docH: 420, main: { x: 0, y: 0, w: 100, h: 420 }, nodes: [{ id: "s1", k: "s", t: "section", p: "", ...box }, { id: "h1", k: "n", t: "heading", p: "s1", ...box }] });
  dom.deliver(host, { hm: 2, type: "hover", id: "h1", k: "n" });
  const rings = [];
  const walk = (e) => { if (e.attrs?.["data-hm-pe-hover"] !== undefined) rings.push(e); (e.children ?? []).forEach(walk); };
  walk(host);
  assert.equal(rings.length, 1, "a hover ring is drawn");
  assert.deepEqual(rings[0].children, [], "with no label inside it");
  assert.ok(canvas);
});

test("every '+' button is visible (data-show), so none is an invisible click-swallower; the layer hides while text is edited", async () => {
  globalThis.requestAnimationFrame = () => {};
  const { host, canvas } = rig({ nodeLabel: (id, _k, t) => t, onAdd() {} });
  const n = (id, p, y) => ({ id, k: "n", t: "container", p, x: 0, y, w: 100, h: 20, c: 1, dir: "column" });
  dom.deliver(host, { hm: 2, type: "layout", docH: 400, main: { x: 0, y: 0, w: 100, h: 400 }, nodes: [n("a", "", 0), n("b", "", 60), { id: "h", k: "n", t: "heading", p: "b", x: 0, y: 60, w: 100, h: 20 }] });
  dom.deliver(host, { hm: 2, type: "select", id: "h", k: "n", shift: false, ctrl: false });
  dom.deliver(host, { hm: 2, type: "hover", id: "a", k: "n" });
  await new Promise((r) => setTimeout(r, 150)); // the chrome is a lazy chunk
  const plus = [];
  let layer = null;
  const walk = (e) => { if (e.attrs?.["data-hm-pe-plus"] !== undefined) plus.push(e); if (e.attrs?.["data-hm-pe-pluses"] !== undefined) layer = e; (e.children ?? []).forEach(walk); };
  walk(canvas.stage);
  assert.ok(plus.length > 0, "there are '+' buttons");
  assert.ok(plus.every((b) => b.attrs["data-show"] === "sel" || b.attrs["data-show"] === "hover"), "all visible");
  dom.deliver(host, { hm: 2, type: "selection", id: "h", k: "n", prop: "html", rect: { x: 1, y: 1, w: 5, h: 5 }, marks: [] });
  assert.equal(layer.hidden, true, "no '+' layer while typing");
  delete globalThis.requestAnimationFrame;
});

test("the toolbar: the tab stop lands on the first enabled button of a top-level element, Hide turns into Show, and there is an Add after", async () => {
  globalThis.requestAnimationFrame = () => {};
  let hidden = false;
  const added = [];
  const { host, canvas } = rig({ nodeLabel: (id, _k, t) => t, onAction() {}, isHidden: () => hidden, onAddAfter: (b) => added.push(b) });
  dom.deliver(host, { hm: 2, type: "layout", docH: 400, main: { x: 0, y: 0, w: 100, h: 400 }, nodes: [{ id: "top", k: "n", t: "columns", p: "", x: 0, y: 0, w: 100, h: 20 }] });
  dom.deliver(host, { hm: 2, type: "select", id: "top", k: "n", shift: false, ctrl: false });
  await new Promise((r) => setTimeout(r, 150));
  const btn = {};
  const walk = (e) => { if (e.attrs?.["data-act"]) btn[e.attrs["data-act"]] = e; (e.children ?? []).forEach(walk); };
  walk(canvas.stage);
  assert.ok(btn.parent.hasAttribute("disabled"), "a top-level element has no parent to select");
  const stops = Object.values(btn).filter((b) => b.attrs.tabindex === "0");
  assert.equal(stops.length, 1, "one tab stop");
  assert.ok(!stops[0].hasAttribute("disabled"), "and it is not the disabled Parent");
  assert.equal(btn.duplicate.attrs.tabindex, "0", "the first enabled button");
  assert.equal(btn.togglehide.textContent, "Hide");
  hidden = true;
  dom.deliver(host, { hm: 2, type: "hover", id: "top", k: "n" }); // any repaint
  dom.deliver(host, { hm: 2, type: "select", id: "top", k: "n", shift: false, ctrl: false });
  canvas.setDevice("md");
  assert.equal(btn.togglehide.textContent, "Show");
  assert.equal(btn.togglehide.attrs["aria-label"], "Show on this device");
  assert.ok(btn.addafter, "an Add after button");
  delete globalThis.requestAnimationFrame;
});

test("the device label is not a live region (it is repainted on every hover) and is its own element, apart from the crumbs", async () => {
  const { mountStrip } = await import(new URL("../../../backend/web/admin/js/canvas/chrome.js", import.meta.url));
  let strip = null;
  const stage = { parentElement: { insertBefore: (e) => (strip = e) }, parentNode: null };
  const canvas = { device: "md", selectedId: null, nodeChain: () => [], zoom: "fit", setInset() {}, setZoom() {}, stage };
  mountStrip(canvas, {});
  const band = strip.children.find((c) => c.attrs.class === "hm-pe__bandlabel");
  assert.ok(band, "a band label element");
  assert.equal(band.attrs.role, undefined);
  assert.match(band.textContent, /^Tablet · editing styles/);
  assert.ok(!strip.children[0].children.some((c) => c === band), "not inside the crumbs nav");
});
