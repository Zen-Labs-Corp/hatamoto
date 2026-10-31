// The element menu availability model. Run: node --test scripts/admin-qa/unit/builder-ctxmenu.test.mjs
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
const M = await import(new URL("../../../backend/web/admin/js/builder/ctxmenu.js", import.meta.url));

const KINDS = [{ kind: "container", children: true }, { kind: "text", children: false }, { kind: "ref", children: false }];
const leaf = (id) => ({ id, t: "text", p: { text: id } });
const box = (id, c = []) => ({ id, t: "container", ...(c.length ? { c } : {}) });
const doc = { v: 2, nodes: [box("a", [leaf("a1"), leaf("a2"), leaf("a3")]), box("e"), { id: "r", t: "ref", p: { block: "x" } }] };
const rows = (selection, extra = {}) => M.menuItems({ doc, selection, selected: selection.at(-1), kinds: KINDS, reuse: false, ...extra }).filter((r) => !r.sep);
const by = (list) => Object.fromEntries(list.map((r) => [r.id, r]));

test("a single leaf: copy/cut/paste/duplicate/rename/delete on, paste inside, unwrap off", () => {
  const r = by(rows(["a2"]));
  for (const id of ["copynode", "cutnode", "pastenode", "duplicate", "rename", "wrap", "copystyle", "pastestyle", "remove"]) assert.ok(!r[id].disabled, id);
  assert.ok(r.pasteinside.disabled, "a text cannot hold elements");
  assert.ok(r.unwrap.disabled, "a leaf has nothing to unwrap");
  assert.equal(r.remove.danger, true);
  assert.equal(r.remove.label, "Delete");
  assert.ok(!r.savetpl && !r.mkblock, "no reuse rows without templates and blocks");
});

test("Add after is a row for one element, not for several (it opens the picker after the selection)", () => {
  assert.equal(by(rows(["a2"])).addafter.label, "Add after");
  assert.ok(!by(rows(["a2"])).addafter.disabled);
  assert.ok(by(rows(["a1", "a2"])).addafter.disabled);
});

test("the rows say what they do in plain words", () => {
  const labels = M.menuItems({ doc, selection: ["a2"], selected: "a2", kinds: KINDS, reuse: true }).filter((r) => !r.sep).map((r) => r.label);
  for (const l of ["Delete", "Duplicate", "Wrap in container", "Save as block…", "Save as template…", "Copy style", "Paste style"]) assert.ok(labels.includes(l), l);
});

test("Restore from an earlier version is offered for one element, not for several", () => {
  assert.equal(by(rows(["a2"])).restore.label, "Restore from an earlier version…");
  assert.ok(!by(rows(["a2"])).restore.disabled);
  assert.ok(by(rows(["a1", "a2"])).restore.disabled);
});

test("a container with children can take pastes and be unwrapped; an empty one cannot", () => {
  assert.ok(!by(rows(["a"])).pasteinside.disabled);
  assert.ok(!by(rows(["a"])).unwrap.disabled);
  assert.ok(by(rows(["e"])).unwrap.disabled);
});

test("a multi-selection: wrap only when adjacent, no rename/style/inside/unwrap, delete counts", () => {
  const adjacent = by(rows(["a1", "a2"]));
  assert.ok(!adjacent.wrap.disabled);
  assert.equal(adjacent.remove.label, "Delete 2 elements");
  for (const id of ["rename", "copystyle", "pastestyle", "pasteinside", "unwrap"]) assert.ok(adjacent[id].disabled, id);
  assert.ok(by(rows(["a1", "a3"])).wrap.disabled, "a gap");
  assert.ok(by(rows(["a1", "e"])).wrap.disabled, "different parents");
  assert.equal(by(rows(["a", "a1"])).remove.label, "Delete", "a child of a selected element counts once");
});

test("reuse rows appear with templates and blocks; a ref cannot become a block", () => {
  const r = by(rows(["a2"], { reuse: true }));
  assert.ok(!r.savetpl.disabled && !r.mkblock.disabled);
  assert.deepEqual([r.savetpl.label, r.mkblock.label], ["Save as template…", "Save as block…"], "one term set: block and template");
  assert.ok(by(rows(["r"], { reuse: true })).mkblock.disabled);
  assert.ok(by(rows(["a1", "a2"], { reuse: true })).savetpl.disabled);
});

test("separators never lead, trail or double; shortcuts name the platform key", () => {
  const all = M.menuItems({ doc, selection: ["a2"], selected: "a2", kinds: KINDS, reuse: true });
  assert.ok(!all[0].sep && !all.at(-1).sep);
  all.forEach((r, i) => assert.ok(!(r.sep && all[i - 1]?.sep)));
  assert.match(by(rows(["a2"])).copynode.shortcut, /^(Ctrl\+|⌘)C$/);
  assert.deepEqual(M.menuItems({ doc, selection: ["gone"], selected: "gone", kinds: KINDS, reuse: false }), []);
});

test("Edit and Hide on this device: Edit needs one element; the hide row flips with the device state", () => {
  assert.ok(!by(rows(["a2"])).inspect.disabled);
  assert.ok(by(rows(["a1", "a2"])).inspect.disabled);
  assert.equal(by(rows(["a2"])).togglehide.label, "Hide on this device");
  const hidden = { v: 2, nodes: [{ id: "h", t: "text", p: { text: "x" }, s: { base: { hide: "1" } } }] };
  const r = by(M.menuItems({ doc: hidden, selection: ["h"], selected: "h", kinds: KINDS, reuse: false }).filter((x) => !x.sep));
  assert.equal(r.togglehide.label, "Show on this device");
});

test("Hide on a v1 section selected as its component targets the section (a hidden section is no longer v1, so it reads on its own row)", () => {
  const sec = (s) => ({ id: "s", t: "container", p: { tag: "section" }, ...(s ? { s } : {}), c: [{ id: "k", t: "component", p: { type: "hero" } }] });
  const label = (d, id) => by(M.menuItems({ doc: d, selection: [id], selected: id, kinds: KINDS, reuse: false }).filter((x) => !x.sep)).togglehide.label;
  assert.equal(label({ v: 2, nodes: [sec()] }, "k"), "Hide on this device");
  assert.equal(label({ v: 2, nodes: [sec({ base: { hide: "1" } })] }, "s"), "Show on this device");
});
