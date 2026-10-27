// Design helpers: drift, preset op builders and the style clipboard.
// Run: node --test scripts/admin-qa/unit/builder-design.test.mjs
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
const D0 = await import(new URL("../../../backend/web/admin/js/builder/design.js", import.meta.url));
const O = await import(new URL("../../../backend/web/admin/js/builder/designops.js", import.meta.url));
const D = { ...D0, ...O };
const S = await import(new URL("../../../backend/web/admin/js/builder/store.js", import.meta.url));

const presets = [{ name: "card", label: "Card", s: { base: { pt: "space-4", bg: "surface" }, sm: { pt: "space-2" } } }];
const table = [
  { key: "pt", kind: "space", tokens: { "space-2": "0.5rem", "space-4": "1rem" } },
  { key: "bg", kind: "color", tokens: { surface: "var(--hm-surface)" } },
  { key: "dir", kind: "enum", enum: { row: "row" } },
  { key: "w", kind: "len" },
];
const doc = () => ({
  v: 2,
  nodes: [
    { id: "a", t: "container", p: { preset: "card" }, s: { base: { pt: "space-4", w: "10px" }, sm: { pt: "space-8" } } },
    { id: "b", t: "container", s: { base: { bg: "#fff", dir: "row" } } },
    { id: "c", t: "container", p: { preset: "gone" } },
    { id: "z", t: "component", p: { type: "hero" } },
  ],
});
const node = (id) => S.find(doc(), id);

test("drift: states, overrides, redundant, extras, literals", () => {
  const a = D.drift(node("a"), presets, table, { tokens: true });
  assert.equal(a.state, "overridden");
  assert.deepEqual(a.overrides, [{ bp: "sm", key: "pt", own: "space-8", preset: "space-2", at: "sm" }]);
  assert.deepEqual(a.redundant.map((x) => x.key), ["pt"]);
  assert.deepEqual(a.extras.map((x) => x.key), ["w"]);
  assert.deepEqual(a.literals, [{ bp: "sm", key: "pt", value: "space-8" }]);
  const b = D.drift(node("b"), presets, table, { tokens: true });
  assert.equal(b.state, "none");
  assert.deepEqual(b.literals, [{ bp: "base", key: "bg", value: "#fff" }]); // enum and tokenless len are never literals
  assert.deepEqual(D.drift(node("b"), presets, table).literals, []); // tokens off
  assert.equal(D.drift(node("c"), presets, table).state, "missing");
  assert.equal(D.drift({ id: "q", t: "container", p: { preset: "card" }, s: { base: { pt: "space-4" } } }, presets, table).state, "overridden"); // base space-4 also shows on phones, where the preset says space-2
});

const card = (s) => ({ id: "n", t: "container", p: { preset: "card" }, s });
test("cascade: detach keeps the look when the node owns a wider breakpoint", () => {
  const n = card({ base: { pt: "space-6" } });
  assert.deepEqual(D.detachOps(n, presets[0]).filter((o) => o.op === "setStyle" && o.key === "pt"), []);
  assert.deepEqual(D.detachOps(n, presets[0]).map((o) => o.key), ["bg", "preset"]);
  const m = card({ md: { pt: "space-6" } }); // md owns it, so sm is covered too; base is not
  assert.deepEqual(D.detachOps(m, presets[0]).map((o) => [o.bp, o.key]), [["base", "pt"], ["base", "bg"], [undefined, "preset"]]);
});

test("cascade: drift flags a wider own value that differs from the preset at a narrower breakpoint", () => {
  const d = D.drift(card({ base: { pt: "space-4" } }), presets, table);
  assert.equal(d.state, "overridden");
  assert.deepEqual(d.overrides, [{ bp: "base", key: "pt", own: "space-4", preset: "space-2", at: "sm" }]);
  assert.deepEqual(D.resetOps(card({ base: { pt: "space-4" } }), presets[0]).map((o) => [o.bp, o.key, o.value]), [["base", "pt", null]]);
  const e = D.drift(card({ base: { pt: "space-4" }, sm: { pt: "space-2" } }), presets, table);
  assert.equal(e.state, "ok");
  assert.deepEqual(e.redundant.map((x) => x.bp), ["base", "sm"]);
  const x = D.drift(card({ base: { mt: "space-2" } }), [{ name: "card", s: { sm: { mt: "space-4" } } }], table); // preset only narrower
  assert.equal(x.state, "overridden");
});

test("presetNote: row suffix", () => {
  assert.equal(D.presetNote(node("b"), presets), "");
  assert.equal(D.presetNote(node("a"), presets), "class card, differs");
  assert.equal(D.presetNote(node("c"), presets), "class removed");
  assert.equal(D.presetNote({ id: "q", t: "container", p: { preset: "card" } }, presets), "class card");
});

const undoesInOne = (ops, id, check) => {
  let st = S.createStore(doc());
  st = S.commit(st, ops, { label: "x" });
  check(S.find(st.doc, id));
  assert.equal(st.past.length, 1);
  st = S.undo(st);
  assert.deepEqual(st.doc, doc());
};

test("apply, reset, detach, save-as-preset are one undo step", () => {
  undoesInOne(D.applyOps(node("b"), "card"), "b", (n) => assert.equal(n.p.preset, "card"));
  assert.deepEqual(D.applyOps(node("a"), "card"), []);
  undoesInOne(D.applyOps(node("a"), ""), "a", (n) => assert.equal(n.p, undefined));
  undoesInOne(D.resetOps(node("a"), presets[0]), "a", (n) => assert.deepEqual(n.s, { base: { w: "10px" } }));
  undoesInOne(D.detachOps(node("a"), presets[0]), "a", (n) => {
    assert.equal(n.p, undefined);
    assert.deepEqual(n.s, { base: { pt: "space-4", w: "10px", bg: "surface" }, sm: { pt: "space-8" } });
  });
  const body = D.presetBodyFrom(node("b"));
  assert.deepEqual(body, { s: { base: { bg: "#fff", dir: "row" } } });
  undoesInOne(D.savePresetOps(node("b"), "mine", body), "b", (n) => {
    assert.equal(n.p.preset, "mine");
    assert.equal(n.s, undefined);
  });
});

test("copy and paste style: replaces map and preset, one undo, refused on source or gone", () => {
  D.clipboard.clear();
  assert.equal(D.clipboard.has(), false);
  assert.deepEqual(D.pasteOps(node("b"), doc()), []);
  D.copyStyle(node("a"));
  assert.equal(D.clipboard.has(), true);
  assert.deepEqual(D.pasteOps(node("a"), doc()), []); // the source itself
  assert.deepEqual(D.pasteOps(node("b"), { v: 2, nodes: [] }), []); // source gone
  undoesInOne(D.pasteOps(node("b"), doc()), "b", (n) => {
    assert.deepEqual(n.s, node("a").s);
    assert.equal(n.p.preset, "card");
  });
  assert.deepEqual(D.pasteOps(node("z"), doc()).filter((o) => o.op === "setProp"), []); // components take no preset
  const copy = node("a");
  D.copyStyle(copy);
  copy.s.base.pt = "changed";
  assert.equal(D.pasteOps(node("b"), doc()).find((o) => o.key === "pt").value, "space-4"); // deep copy
  D.clipboard.clear();
  assert.equal(D.clipboard.has(), false);
});

test("resetOps: one click clears every own value until the node matches the preset", () => {
  const p = { name: "card", s: { base: { pt: "space-4" }, sm: { pt: "space-2" } } };
  const n = { id: "a", t: "box", p: { preset: "card" }, s: { base: { pt: "space-4" }, sm: { pt: "space-4" } } };
  const ops = D.resetOps(n, p);
  assert.deepEqual(ops.map((o) => [o.bp, o.key, o.value]), [["sm", "pt", null], ["base", "pt", null]]);
  const left = structuredClone(n);
  for (const o of ops) delete left.s[o.bp][o.key];
  assert.equal(D.drift(left, [p], null).state, "ok");
  const m = { ...n, s: { md: { pt: "space-6" } } };
  assert.deepEqual(D.resetOps(m, p).map((o) => [o.bp, o.key]), [["md", "pt"]]);
});

test("drift: lit, media, grad and gtc values are never custom values, even with a token map", () => {
  const t = [...table, { key: "lh", kind: "lit", tokens: { tight: "1.1" } }, { key: "grad", kind: "grad" }, { key: "bgimg", kind: "media", tokens: { x: "y" } }];
  const n = { id: "l", t: "container", s: { base: { lh: "1.4", grad: "linear-180-000-fff", bgimg: "/media/a.jpg", bg: "#fff" } } };
  assert.deepEqual(D.drift(n, presets, t, { tokens: true }).literals, [{ bp: "base", key: "bg", value: "#fff" }]);
});

test("state keys: drift compares within the same state; cascade and reach stay in it", () => {
  const ps = [{ name: "card", s: { base: { bg: "surface" }, "base:hover": { bg: "accent" }, "sm:hover": { bg: "muted" } } }];
  const n = { id: "n", t: "container", p: { preset: "card" }, s: { "base:hover": { bg: "danger" }, "md:hover": { bg: "accent" }, base: { bg: "surface" } } };
  const d = D.drift(n, ps, null);
  assert.deepEqual(d.overrides.map((o) => [o.bp, o.key, o.preset, o.at]), [["base:hover", "bg", "accent", "base:hover"], ["md:hover", "bg", "muted", "sm:hover"]]);
  assert.deepEqual(d.redundant.map((o) => [o.bp, o.preset]), [["base", "surface"]]);
  const bare = { id: "n", t: "container", p: { preset: "card" }, s: { "base:hover": { bg: "accent" } } };
  assert.equal(D.drift(bare, ps, null).overrides.length, 1, "own base:hover reaches sm:hover where the preset says muted");
  assert.deepEqual(D.drift({ ...bare, s: { base: { bg: "accent" } } }, ps, null).overrides.map((o) => o.at), ["base"], "Normal never reads hover values");
});

test("state keys: presetBodyFrom keeps them (not parent-hover), detach copies, reset clears", () => {
  const n = { id: "n", t: "container", p: { preset: "card" }, s: { base: { pt: "a" }, "base:hover": { bg: "x" }, "md:parent-hover": { bg: "y" } } };
  assert.deepEqual(D.presetBodyFrom(n), { s: { base: { pt: "a" }, "base:hover": { bg: "x" } } });
  const pr = { s: { "base:hover": { bg: "accent" }, "sm:focus": { bg: "z" } } };
  assert.deepEqual(D.detachOps({ id: "n", s: { "sm:focus": { bg: "own" } } }, pr).map((o) => [o.bp ?? o.op, o.value]), [["base:hover", "accent"], ["setProp", null]]);
  const own = { id: "n", t: "container", p: { preset: "card" }, s: { "base:hover": { bg: "danger" } } };
  assert.deepEqual(D.resetOps(own, { s: { "base:hover": { bg: "accent" } } }), [{ op: "setStyle", id: "n", bp: "base:hover", key: "bg", value: null }]);
  assert.deepEqual(D.savePresetOps(own, "p2", { s: { "base:hover": { bg: "danger" } } }).slice(1), [{ op: "setStyle", id: "n", bp: "base:hover", key: "bg", value: null }]);
});

test("state keys: paste sets and clears them; parent-hover only when the target can take it", () => {
  const src = { id: "s", t: "container", s: { "base:hover": { bg: "a" }, "base:parent-hover": { bg: "b" } } };
  D.copyStyle(src);
  const target = { id: "t", t: "container", s: { "md:focus": { bg: "old" } } };
  const ops = (o) => D.pasteOps(target, null, o).map((x) => [x.bp ?? x.op, x.value]);
  assert.deepEqual(ops(), [["md:focus", null], ["base:hover", "a"]]);
  assert.deepEqual(ops({ parentHover: true }), [["md:focus", null], ["base:hover", "a"], ["base:parent-hover", "b"]]);
  D.clipboard.clear();
});

test("clipboard.hasParentHover tells when a paste would drop parent-hover values", () => {
  D.clipboard.clear();
  assert.equal(D.clipboard.hasParentHover(), false);
  D.copyStyle({ id: "x", t: "container", s: { "md:parent-hover": { op: "1" } } });
  assert.equal(D.clipboard.hasParentHover(), true);
  D.copyStyle({ id: "x", t: "container", s: { base: { op: "1" } } });
  assert.equal(D.clipboard.hasParentHover(), false);
});
