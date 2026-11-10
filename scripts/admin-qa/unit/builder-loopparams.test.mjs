// A query loop's Filters group: the pure params helpers and the Content tab disclosure.
// Run: node --test scripts/admin-qa/unit/builder-loopparams.test.mjs
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
  constructor(tag) {
    Object.assign(this, { tag, attrs: {}, kids: [], on: {}, dataset: {}, textContent: "", value: "", checked: false, parent: null, hidden: false });
    this.classList = { toggle() {}, add() {}, contains: () => false };
  }
  get isConnected() { return false; }
  setAttribute(k, v) { this.attrs[k] = String(v); }
  getAttribute(k) { return this.attrs[k] ?? null; }
  removeAttribute(k) { delete this.attrs[k]; }
  append(...k) { for (const x of k) { if (typeof x === "object") x.parent = this; this.kids.push(x); } }
  addEventListener(t, f) { (this.on[t] ??= []).push(f); }
  fire(t) { for (const f of this.on[t] ?? []) f({ target: this }); }
  querySelector(sel) { let hit = null; this.walk((n) => !hit && n !== this && n.match(sel) && (hit = n)); return hit; }
  match(sel) { const m = /^\.([\w-]+)$|^select$|^\[data-prop="(\w+)"\]$/.exec(sel); return !m ? false : m[1] ? (this.attrs.class ?? "").split(" ").includes(m[1]) : m[2] ? this.dataset.prop === m[2] : this.tag === "select"; }
  closest() { return null; }
  replaceChildren(...k) { this.kids = []; this.append(...k); }
  contains(n) { let hit = false; this.walk((k) => k === n && (hit = true)); return hit; }
  focus() {}
  walk(f) { f(this); this.kids.forEach((k) => typeof k === "object" && k.walk(f)); }
}
globalThis.document = { querySelector: () => null, createElement: (t) => new El(t), activeElement: null };
globalThis.window = globalThis;

const { createContent } = await import(new URL("builder/inspcontent.js", root));
const { declFor, cleanParams, setCount, nextParams, filtersLabel } = await import(new URL("builder/loopparams.js", root));

const decl = [
  { name: "cat", label: "Category", type: "select", options: [{ value: "ws", label: "Workshops" }, { value: "talk", label: "Talks" }] },
  { name: "venue", label: "Venue", type: "text" },
  { name: "upcoming", label: "Only upcoming", type: "bool" },
  { name: "days", label: "Days ahead", type: "number" },
];
const loopSources = [{ id: "ext.cal.events", label: "Events", plugin: "cal", params: decl }, { id: "ext.cal.all", label: "All", plugin: "cal" }];

test("helpers: declared params, set count and the value a change leaves", () => {
  assert.equal(declFor(loopSources, "ext.cal.events").length, 4);
  assert.deepEqual(declFor(loopSources, "ext.cal.all"), []);
  assert.deepEqual(declFor(loopSources, "ext.nope.x"), []);
  assert.deepEqual(declFor(undefined, "x"), []);
  const stored = { cat: "ws", venue: "", upcoming: false, days: 0, gone: "x" };
  assert.deepEqual(cleanParams(decl, stored), { cat: "ws", days: 0 }, "empty, false and undeclared names are not values; 0 is");
  assert.equal(setCount(decl, stored), 2);
  assert.equal(setCount(decl, undefined), 0);
  assert.equal(filtersLabel(0), "Filters");
  assert.equal(filtersLabel(2), "Filters (2 set)");
  assert.deepEqual(nextParams(decl, { cat: "ws" }, "venue", "Sydney"), { cat: "ws", venue: "Sydney" });
  assert.equal(nextParams(decl, { cat: "ws" }, "cat", ""), null, "the last value cleared leaves null, not {}");
  assert.deepEqual(nextParams(decl, {}, "upcoming", true), { upcoming: true });
  assert.equal(nextParams(decl, { upcoming: true }, "upcoming", false), null);
  assert.deepEqual(nextParams(decl, {}, "days", "2.5"), { days: 2.5 });
  assert.equal(nextParams(decl, {}, "days", "abc"), null);
  assert.equal(nextParams(decl, {}, "days", ""), null);
  assert.equal(nextParams(decl, {}, "nope", "x"), null, "an undeclared name is never stored");
  assert.deepEqual(nextParams(decl, { gone: "x", cat: "ws" }, "venue", "V"), { cat: "ws", venue: "V" }, "stale names are dropped on the next change");
});

const find = (el, pred) => { let hit = null; el.walk((n) => !hit && pred(n) && (hit = n)); return hit; };
const all = (el, pred) => { const out = []; el.walk((n) => pred(n) && out.push(n)); return out; };
const kinds = [{ kind: "loop", props: [{ key: "from", type: "string", enum: ["query", "source"] }, { key: "source", type: "string" }, { key: "params", type: "object" }] }];
const setup = (node) => {
  const commits = [];
  const doc = { nodes: [node] };
  const api = { kinds, getDoc: () => doc, bindSources: [], announce() {}, options: {}, loopSources };
  const c = createContent({ api, root: new El("div"), commit: (ops, opts) => (commits.push({ ops, opts }), true), draw() {}, kindInfo: (t) => kinds.find((k) => k.kind === t), gen: () => 0, pushRich() {}, setFields() {} });
  return { panel: c.contentPanel(node), commits };
};
const toggleOf = (panel) => find(panel, (n) => n.tag === "button" && /^Filters/.test(n.textContent));

test("a source without params shows no Filters group", () => {
  const { panel } = setup({ id: "l", t: "loop", p: { from: "source", source: "ext.cal.all" } });
  assert.equal(toggleOf(panel), null);
  const q = setup({ id: "l", t: "loop", p: { from: "query" } });
  assert.equal(toggleOf(q.panel), null);
});

test("a params source: a collapsed real button counts what is set; expanded it has one labelled control per param", () => {
  const node = { id: "l", t: "loop", p: { from: "source", source: "ext.cal.events", params: { cat: "ws", venue: "Sydney" } } };
  const { panel, commits } = setup(node);
  const toggle = toggleOf(panel);
  assert.equal(toggle.textContent, "Filters (2 set)");
  assert.equal(toggle.attrs.type, "button");
  assert.equal(toggle.attrs["aria-expanded"], "false");
  const box = find(panel, (n) => n.attrs.id === toggle.attrs["aria-controls"]);
  assert.ok(box, "aria-controls points at the box");
  assert.equal(box.hidden, true);
  toggle.fire("click");
  assert.equal(toggle.attrs["aria-expanded"], "true");
  assert.equal(box.hidden, false);
  const controls = all(box, (n) => ["input", "select"].includes(n.tag));
  assert.equal(controls.length, 4);
  for (const c of controls) assert.ok(find(box, (n) => n.tag === "label" && n.attrs.for === c.attrs.id), "each control has a label tied to it");
  const sel = find(box, (n) => n.tag === "select");
  assert.equal(sel.value, "ws");
  assert.deepEqual(all(sel, (n) => n.tag === "option").map((o) => o.textContent), ["Not set", "Workshops", "Talks"]);

  sel.value = "talk";
  sel.fire("change");
  assert.deepEqual(commits.at(-1).ops, [{ op: "setProp", id: "l", key: "params", value: { cat: "talk", venue: "Sydney" } }]);
  const days = find(box, (n) => n.attrs.type === "number");
  assert.equal(days.attrs.step, "any");
  days.value = "30";
  days.fire("change");
  assert.equal(toggle.textContent, "Filters (3 set)", "the button relabels after a change");
  const flag = find(box, (n) => n.attrs.role === "switch");
  flag.checked = true;
  flag.fire("change");
  assert.deepEqual(commits.at(-1).ops[0].value.upcoming, true);
  const venue = find(box, (n) => n.attrs.type === "text");
  assert.equal(venue.attrs.maxlength, "200");
  venue.value = "";
  venue.fire("input");
  assert.equal(commits.at(-1).opts.coalesce, true);
});

test("clearing the last filter stores null", () => {
  const node = { id: "l", t: "loop", p: { from: "source", source: "ext.cal.events", params: { cat: "ws" } } };
  const { panel, commits } = setup(node);
  const sel = find(panel, (n) => n.tag === "select" && n.attrs.id?.endsWith("-cat"));
  sel.value = "";
  sel.fire("change");
  assert.deepEqual(commits[0].ops, [{ op: "setProp", id: "l", key: "params", value: null }]);
});
