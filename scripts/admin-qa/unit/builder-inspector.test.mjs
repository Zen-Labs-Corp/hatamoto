// The inspector's pure helpers: which control a prop gets, when an image needs alt text.
// Run: node --test scripts/admin-qa/unit/builder-inspector.test.mjs
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
const { controlOf, needsAlt } = await import(new URL("../../../backend/web/admin/js/builder/inspcontent.js", import.meta.url));
const { presetOptions, driftRows } = await import(new URL("../../../backend/web/admin/js/builder/presetpanel.js", import.meta.url));
const { usageSummary } = await import(new URL("../../../backend/web/admin/js/builder/reuse.js", import.meta.url));
const { refState } = await import(new URL("../../../backend/web/admin/js/builder/reusecore.js", import.meta.url));
const { label, rowName } = await import(new URL("../../../backend/web/admin/js/builder/navigator.js", import.meta.url));
const { detachOps } = await import(new URL("../../../backend/web/admin/js/builder/reuseops.js", import.meta.url));
const S = await import(new URL("../../../backend/web/admin/js/builder/store.js", import.meta.url));

test("controlOf: type, enum and the server's widget hint", () => {
  assert.equal(controlOf({ key: "a", type: "boolean" }), "check");
  assert.equal(controlOf({ key: "a", type: "string", enum: ["x"] }), "select");
  assert.equal(controlOf({ key: "a", type: "integer" }), "number");
  assert.equal(controlOf({ key: "a", type: "string" }), "text");
  for (const w of ["richtext", "media", "textarea", "lines", "code"]) assert.equal(controlOf({ key: "a", type: "string", widget: w }), w);
  assert.equal(controlOf({ key: "slug", type: "string", widget: "datalist:forms" }), "datalist");
  assert.equal(controlOf({ key: "src", type: "string" }), "media");
});

test("needsAlt: only an image with no alt text that is not decorative", () => {
  assert.equal(needsAlt({ t: "image", p: { src: "/a.png" } }), true);
  assert.equal(needsAlt({ t: "image", p: { alt: "  " } }), true);
  assert.equal(needsAlt({ t: "image", p: { alt: "A cat" } }), false);
  assert.equal(needsAlt({ t: "image", p: { decorative: true } }), false);
  assert.equal(needsAlt({ t: "text", p: {} }), false);
});

test("presetOptions: None, presets, and the removed one the node still names", () => {
  const presets = [{ name: "card", label: "Card" }, { name: "plain", label: "" }];
  assert.deepEqual(presetOptions(presets, "card"), [["", "None"], ["card", "Card"], ["plain", "plain"]]);
  assert.deepEqual(presetOptions(presets, "gone").at(-1), ["gone", "gone (removed)"]);
});

test("driftRows: one readable row per override", () => {
  const rows = driftRows({ overrides: [{ bp: "sm", key: "pt", own: "space-8", preset: "space-2" }] });
  assert.deepEqual(rows, [{ bp: "sm", key: "pt", text: "Padding top (Phone): space-8, class has space-2" }]);
});

test("driftRows: names the breakpoint where the preset differs", () => {
  const rows = driftRows({ overrides: [{ bp: "base", key: "pt", own: "space-4", preset: "space-2", at: "sm" }] });
  assert.equal(rows[0].text, "Padding top (Desktop): space-4, class has space-2 on Phone");
});

const BLK = "11111111-1111-4111-8111-111111111111";
const blocks = [{ id: BLK, name: "Footer CTA" }];

test("refState: a listed block has a name, anything else is missing, a malformed id is never linkable", () => {
  assert.deepEqual(refState({ p: { block: BLK } }, blocks), { id: BLK, valid: true, name: "Footer CTA", missing: false });
  const gone = "22222222-2222-4222-8222-222222222222";
  assert.deepEqual(refState({ p: { block: gone } }, blocks), { id: gone, valid: true, name: "", missing: true });
  const bad = refState({ p: { block: '"><script>' } }, blocks);
  assert.deepEqual([bad.valid, bad.missing], [false, true]);
  assert.equal(refState({}, undefined).missing, true);
});

test("usageSummary wording and the 200 cap", () => {
  assert.equal(usageSummary(0, false), "Not used on any page yet");
  assert.equal(usageSummary(1, false), "Used on 1 page");
  assert.equal(usageSummary(2, false), "Used on 2 pages");
  assert.equal(usageSummary(200, true), "Used on 200+ pages");
});

test("navigator row of a ref reads block <name>, missing when unknown", () => {
  const ref = { id: "r", t: "ref", p: { block: BLK } };
  assert.equal(label(ref, blocks), "Block: Footer CTA");
  assert.equal(label(ref, []), "Block: (missing)");
  assert.equal(rowName(ref, "", blocks), "Block: Footer CTA");
  assert.equal(label({ id: "h", t: "heading", p: { text: "Hi" } }, blocks), "Heading · Hi"); // other kinds unchanged
});

test("detach: the ref becomes its nodes in one commit, one undo restores the ref", () => {
  S.setChildKinds([{ kind: "container", children: true }, { kind: "text" }, { kind: "ref" }]);
  const doc = { v: 2, nodes: [{ id: "a", t: "container", c: [{ id: "x", t: "text" }, { id: "r", t: "ref", p: { block: BLK } }, { id: "z", t: "text" }] }] };
  const fresh = [{ id: "n1", t: "text" }, { id: "n2", t: "text" }];
  let st = S.commit(S.createStore(doc), detachOps(doc, "r", fresh), { label: "Detach block" });
  assert.deepEqual(st.doc.nodes[0].c.map((n) => n.id), ["x", "n1", "n2", "z"]);
  st = S.undo(st);
  assert.deepEqual(st.doc.nodes[0].c.map((n) => n.id), ["x", "r", "z"]);
});

test("label: a bound node says so", () => {
  assert.equal(label({ t: "heading", p: {}, b: { text: "entity.name" } }), "Heading (bound)");
  assert.equal(label({ t: "heading", p: { text: "Hi" } }), "Heading · Hi");
});

test("driftRows names a state by its label", () => {
  const rows = driftRows({ overrides: [{ bp: "md:hover", key: "bg", own: "a", preset: "b", at: "base:hover" }] });
  assert.match(rows[0].text, /\(Hover, Tablet\): a, class has b on Hover, Desktop$/);
});
