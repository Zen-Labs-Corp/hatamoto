// The left pane's Layers | Insert | Pages tabs: roles, roving tabindex, arrows, the panel that shows.
// dom.js builds elements through `document`, so a tiny element stub stands in for the DOM.
// Run: node --test scripts/admin-qa/unit/builder-lefttabs.test.mjs
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
  constructor(data = {}) {
    this.attrs = {};
    this.dataset = data;
    this.on = {};
    this.hidden = false;
    this.tabIndex = 0;
    this.kids = [];
    this.find = () => [];
  }
  setAttribute(k, v) { this.attrs[k] = String(v); }
  addEventListener(t, f) { (this.on[t] ??= []).push(f); }
  fire(t, e = {}) { (this.on[t] ?? []).forEach((f) => f({ preventDefault() {}, ...e })); }
  focus() { globalThis.document.activeElement = this; }
  querySelector(sel) { return this.querySelectorAll(sel)[0] ?? null; }
  querySelectorAll(sel) { return this.find(sel); }
}
globalThis.document = { activeElement: null };

const { mountLeftTabs } = await import(new URL("builder/panes.js", root));

function rig() {
  const nav = new El({ hmBsLtab: "nav" });
  const add = new El({ hmBsLtab: "add" });
  add.tabIndex = -1;
  const pages = new El({ hmBsLtab: "pages" });
  pages.tabIndex = -1;
  const list = new El();
  list.find = () => [nav, add, pages];
  const pNav = new El({ hmBsLpanel: "nav" });
  const pAdd = new El({ hmBsLpanel: "add" });
  pAdd.hidden = true;
  const pPages = new El({ hmBsLpanel: "pages" });
  pPages.hidden = true;
  const rootEl = new El();
  rootEl.find = (sel) => (sel === "[data-hm-bs-ltabs]" ? [list] : sel === "[data-hm-bs-lpanel]" ? [pNav, pAdd, pPages] : []);
  const picked = [];
  const tabs = mountLeftTabs(rootEl, (n) => picked.push(n));
  return { nav, add, pages, list, pNav, pAdd, pPages, tabs, picked };
}

test("pick shows one panel, marks one tab selected and roves tabindex", () => {
  const r = rig();
  r.tabs.pick("add");
  assert.equal(r.add.attrs["aria-selected"], "true");
  assert.equal(r.nav.attrs["aria-selected"], "false");
  assert.deepEqual([r.nav.tabIndex, r.add.tabIndex, r.pages.tabIndex], [-1, 0, -1]);
  assert.deepEqual([r.pNav.hidden, r.pAdd.hidden, r.pPages.hidden], [true, false, true]);
  assert.deepEqual(r.picked, ["add"]);
});

test("arrows, Home and End move between the tabs and wrap", () => {
  const r = rig();
  r.nav.focus();
  r.list.fire("keydown", { key: "ArrowRight" });
  assert.equal(globalThis.document.activeElement, r.add);
  assert.equal(r.pAdd.hidden, false);
  r.list.fire("keydown", { key: "ArrowRight" });
  assert.equal(globalThis.document.activeElement, r.pages);
  assert.equal(r.pPages.hidden, false);
  r.list.fire("keydown", { key: "ArrowRight" });
  assert.equal(globalThis.document.activeElement, r.nav, "wraps");
  r.list.fire("keydown", { key: "ArrowLeft" });
  assert.equal(globalThis.document.activeElement, r.pages, "wraps backwards");
  r.list.fire("keydown", { key: "Home" });
  assert.equal(globalThis.document.activeElement, r.nav);
  r.list.fire("keydown", { key: "End" });
  assert.equal(globalThis.document.activeElement, r.pages);
  r.list.fire("keydown", { key: "Home" });
  assert.equal(globalThis.document.activeElement, r.nav);
});

test("a click on a tab shows its panel without moving focus; other keys are left alone", () => {
  const r = rig();
  r.add.fire("click");
  assert.equal(r.pAdd.hidden, false);
  let stopped = false;
  r.add.focus();
  r.list.fire("keydown", { key: "a", preventDefault: () => (stopped = true) });
  assert.equal(stopped, false);
});

test("without the tabs markup mounting is harmless", () => {
  const rootEl = new El();
  const t = mountLeftTabs(rootEl);
  assert.doesNotThrow(() => t.pick("add"));
});
