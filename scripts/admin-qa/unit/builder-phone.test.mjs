// The builder's phone pieces: preview loading and unavailable states, the bottom dock (Navigator, Add, Inspector), the saved pane
// collapse on a phone, and the width items in the ⋯ menu. dom.js builds elements through `document`,
// so a tiny element stub stands in for the DOM.
// Run: node --test scripts/admin-qa/unit/builder-phone.test.mjs
import { register } from "node:module";
import { test, mock } from "node:test";
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
  constructor(tag = "div") {
    this.tag = tag;
    this.attrs = {};
    this.dataset = {};
    this.kids = [];
    this.on = {};
    this.hidden = false;
    this.textContent = "";
    this.tabIndex = 0;
  }
  setAttribute(k, v) {
    this.attrs[k] = String(v);
    if (k === "hidden") this.hidden = true;
    if (k.startsWith("data-")) this.dataset[k.slice(5).replace(/-(\w)/g, (_, c) => c.toUpperCase())] = String(v);
  }
  getAttribute(k) { return this.attrs[k] ?? null; }
  removeAttribute(k) { delete this.attrs[k]; }
  append(...k) { this.kids.push(...k); k.forEach((x) => (x.parent = this)); }
  remove() { if (this.parent) this.parent.kids = this.parent.kids.filter((x) => x !== this); this.removed = true; }
  addEventListener(t, f) { (this.on[t] ??= []).push(f); }
  fire(t, e = {}) { (this.on[t] ?? []).forEach((f) => f(e)); }
  click() { this.fire("click"); }
  focus() { globalThis.document.activeElement = this; }
  closest() { return null; }
  querySelector(sel) { return this.querySelectorAll(sel)[0] ?? null; }
  querySelectorAll(sel) { return (this.find ?? (() => []))(sel); }
}
globalThis.document = { querySelector: () => null, createElement: (t) => new El(t), activeElement: null };

const { createPreviewState, mountDock, mountToolItems, shouldOpenInspector, PREVIEW_TIMEOUT } = await import(new URL("builder/phone.js", root));
const { mountPanes } = await import(new URL("builder/panes.js", root));
const mq = (matches) => ({ matches, addEventListener(t, f) { this.change = f; } });
const find = (el, pred) => el.kids.find(pred);

test("preview: loading, then unavailable after the timeout, Try again re-stages, a late frame clears it", () => {
  mock.timers.enable({ apis: ["setTimeout"] });
  const canvas = new El("section");
  let retries = 0;
  const st = createPreviewState(canvas, () => retries++);
  const box = canvas.kids[0];
  const [loading, title, text, again] = box.kids;
  assert.equal(box.dataset.hmBsPreviewState, "loading");
  assert.equal(title.hidden, true);
  mock.timers.tick(PREVIEW_TIMEOUT + 1);
  assert.equal(box.dataset.hmBsPreviewState, "error");
  assert.equal(loading.hidden, true);
  assert.equal(title.hidden, false);
  assert.equal(again.hidden, false);
  again.click();
  assert.equal(box.dataset.hmBsPreviewState, "loading");
  assert.equal(retries, 1);
  mock.timers.tick(PREVIEW_TIMEOUT + 1);
  assert.equal(box.dataset.hmBsPreviewState, "error");
  st.ready(); // a frame that arrives late still wins
  assert.ok(box.removed);
  st.fail();
  assert.equal(box.dataset.hmBsPreviewState, "error"); // fail() after ready() is a no-op: state left as it was
  mock.timers.reset();
});

test("preview: a frame error shows unavailable at once", () => {
  const canvas = new El("section");
  const st = createPreviewState(canvas, () => {});
  st.fail();
  assert.equal(canvas.kids[0].dataset.hmBsPreviewState, "error");
  st.ready();
});

function dockRig(narrow, onPick = () => {}) {
  const rootEl = new El("div");
  rootEl.dataset.hmBsTab = "canvas";
  const list = new El("div");
  const tabs = ["nav", "add", "insp"].map((n) => {
    const b = new El("button");
    b.dataset.hmBsDock = n;
    return b;
  });
  list.find = () => tabs;
  rootEl.find = (sel) => (sel === "[data-hm-bs-dock]" ? [list] : []);
  globalThis.matchMedia = () => narrow;
  const dock = mountDock(rootEl, onPick);
  return { rootEl, list, tabs, dock };
}
const sel = (tabs) => tabs.map((t) => t.getAttribute("aria-selected"));

test("dock: roles only while narrow; the page is the base view with no tab selected, the first tab in the tab order", () => {
  const n = mq(true);
  const { list, tabs } = dockRig(n);
  assert.equal(list.getAttribute("role"), "tablist");
  assert.deepEqual(sel(tabs), ["false", "false", "false"]);
  assert.deepEqual(tabs.map((t) => t.tabIndex), [0, -1, -1]);
  n.matches = false;
  n.change();
  assert.equal(list.getAttribute("role"), null);
  assert.equal(tabs[0].getAttribute("role"), null);
});

test("dock: a tab opens its sheet and tells the screen; tapping it again closes the sheet", () => {
  const picked = [];
  const { rootEl, tabs } = dockRig(mq(true), (n) => picked.push(n));
  tabs[1].click();
  assert.equal(rootEl.dataset.hmBsTab, "add");
  assert.deepEqual(sel(tabs), ["false", "true", "false"]);
  assert.deepEqual(tabs.map((t) => t.tabIndex), [-1, 0, -1]);
  tabs[2].click();
  assert.equal(rootEl.dataset.hmBsTab, "insp");
  tabs[2].click();
  assert.equal(rootEl.dataset.hmBsTab, "canvas");
  assert.deepEqual(picked, ["add", "insp"], "closing is not a pick");
});

test("dock: arrows and Home/End move, open the sheet and wrap", () => {
  const { rootEl, list, tabs } = dockRig(mq(true));
  tabs[0].focus();
  list.fire("keydown", { key: "ArrowLeft", preventDefault() {} });
  assert.equal(rootEl.dataset.hmBsTab, "insp");
  assert.equal(globalThis.document.activeElement, tabs[2]);
  list.fire("keydown", { key: "ArrowRight", preventDefault() {} });
  assert.equal(rootEl.dataset.hmBsTab, "nav");
  list.fire("keydown", { key: "End", preventDefault() {} });
  assert.equal(rootEl.dataset.hmBsTab, "insp");
});

test("dock: Escape in a sheet closes it and returns focus to its tab; a popover or an open dialog keeps it", () => {
  const { rootEl, tabs, dock } = dockRig(mq(true));
  const inSheet = { closest: () => ({}) };
  dock.pick("add");
  rootEl.fire("keydown", { key: "Escape", target: { closest: () => null } });
  assert.equal(rootEl.dataset.hmBsTab, "add", "outside the dock and sheets: untouched");
  rootEl.fire("keydown", { key: "Escape", target: inSheet, defaultPrevented: true });
  assert.equal(rootEl.dataset.hmBsTab, "add");
  globalThis.document.querySelector = () => ({});
  rootEl.fire("keydown", { key: "Escape", target: inSheet });
  assert.equal(rootEl.dataset.hmBsTab, "add", "a popover is open");
  globalThis.document.querySelector = () => null;
  rootEl.fire("keydown", { key: "Escape", target: inSheet });
  assert.equal(rootEl.dataset.hmBsTab, "canvas");
  assert.equal(globalThis.document.activeElement, tabs[1]);
});

test("dock: show marks the sheet without a pick; no dock markup gives a no-op on desktop-only pages", () => {
  const picked = [];
  const { rootEl, tabs, dock } = dockRig(mq(true), (n) => picked.push(n));
  dock.show("add");
  assert.equal(sel(tabs)[1], "true");
  assert.deepEqual(picked, []);
  assert.equal(dock.stacked(), true);
  const bare = new El("div");
  bare.dataset.hmBsTab = "canvas";
  bare.find = () => [];
  globalThis.matchMedia = () => mq(false);
  const none = mountDock(bare);
  assert.doesNotThrow(() => none.pick("nav"));
  assert.equal(none.stacked(), false);
});

test("the Inspector opens by itself only for a person's own pick on a phone", () => {
  assert.equal(shouldOpenInspector({ stacked: true, selected: "a", userPick: true }), true);
  assert.equal(shouldOpenInspector({ stacked: true, selected: "a", userPick: false }), false, "an insert or undo selecting");
  assert.equal(shouldOpenInspector({ stacked: false, selected: "a", userPick: true }), false, "desktop");
  assert.equal(shouldOpenInspector({ stacked: true, selected: null, userPick: true }), false, "a click on nothing");
});

test("a pane collapsed in storage still shows on a phone, where the dock chooses the pane", () => {
  const nav = new El("div");
  const insp = new El("aside");
  const splits = { nav: new El("div"), insp: new El("div") };
  const rootEl = new El("div");
  rootEl.style = { setProperty() {} };
  rootEl.clientWidth = 390;
  const part = { '[data-hm-bs-pane="nav"]': nav, '[data-hm-bs-pane="insp"]': insp, '[data-hm-bs-split="nav"]': splits.nav, '[data-hm-bs-split="insp"]': splits.insp };
  rootEl.find = (sel) => (part[sel] ? [part[sel]] : []);
  globalThis.document.querySelector = () => null;
  globalThis.getComputedStyle = () => ({ fontSize: "16px" });
  globalThis.window = { addEventListener() {} };
  const stacked = mq(true);
  globalThis.matchMedia = () => stacked;
  const storage = { getItem: () => JSON.stringify({ navClosed: true, inspClosed: true }), setItem() {} };
  mountPanes(rootEl, { user: "u", storage });
  assert.equal(nav.hidden, false);
  assert.equal(insp.hidden, false);
  stacked.matches = false; // a desktop window honours the saved collapse
  stacked.change();
  assert.equal(nav.hidden, true);
});

test("⋯ menu keeps an always-visible tool item on desktop, other items only on a phone", () => {
  const rootEl = new El("div");
  const keys = new El("button");
  keys.dataset.hmBsAlways = "";
  const undo = new El("button");
  rootEl.find = (sel) => (sel === "[data-hm-bs-proxy]" ? [keys, undo] : []);
  const q = mq(false);
  mountToolItems(rootEl, () => q);
  assert.deepEqual([keys.hidden, undo.hidden], [false, true]);
  q.matches = true;
  q.change();
  assert.deepEqual([keys.hidden, undo.hidden], [false, false]);
});

test("⋯ menu tool items (undo, redo, help) show only on a phone and press the bar buttons", () => {
  const rootEl = new El("div");
  const undo = new El("button");
  let pressed = 0;
  undo.on.click = [() => pressed++];
  const item = new El("button");
  item.dataset.hmBsProxy = "[data-hm-bs-undo]";
  item.hidden = true;
  rootEl.find = (sel) => (sel === "[data-hm-bs-proxy]" ? [item] : sel === "[data-hm-bs-undo]" ? [undo] : []);
  const q = mq(true);
  mountToolItems(rootEl, () => q);
  assert.equal(item.hidden, false);
  item.click();
  assert.equal(pressed, 1);
  q.matches = false;
  q.change();
  assert.equal(item.hidden, true);
});
