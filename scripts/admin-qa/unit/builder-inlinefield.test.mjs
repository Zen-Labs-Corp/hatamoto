// The inspector's inline text editor: what it opens with, the ops its output becomes, and when the
// open editor may absorb a change made on the canvas instead of the panel redrawing.
// Run: node --test scripts/admin-qa/unit/builder-inlinefield.test.mjs
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
const { innerOf, opsFor, onlyText, fieldSession } = await import(new URL("../../../backend/web/admin/js/builder/inlinefield.js", import.meta.url));
const S = await import(new URL("../../../backend/web/admin/js/builder/store.js", import.meta.url));

test("innerOf: the html when set, else the escaped plain text", () => {
  assert.equal(innerOf({ p: { text: "a", html: "a <em>b</em>" } }), "a <em>b</em>");
  assert.equal(innerOf({ p: { text: "1 < 2 & 3" } }), "1 &lt; 2 &amp; 3");
  assert.equal(innerOf({}), "");
});

test("opsFor: marks make html (with the plain projection), none make plain text and clear html", () => {
  const n = { id: "a" };
  assert.deepEqual(opsFor(n, "<p>x <strong>y</strong></p>"), [
    { op: "setProp", id: "a", key: "text", value: "x y" },
    { op: "setProp", id: "a", key: "html", value: "x <strong>y</strong>" },
  ]);
  assert.deepEqual(opsFor(n, "<p>1 &lt; 2</p>"), [
    { op: "setProp", id: "a", key: "text", value: "1 < 2" },
    { op: "setProp", id: "a", key: "html", value: null },
  ]);
  assert.equal(opsFor(n, "<p></p>")[0].value, "");
});

test("onlyText: a text/html change alone is absorbed; any other difference redraws", () => {
  const base = { id: "a", t: "heading", p: { text: "H", level: 2 }, s: { base: {} } };
  assert.equal(onlyText({ ...base, p: { text: "H!", html: "H<em>!</em>", level: 2 } }, base), true);
  assert.equal(onlyText({ ...base, p: { text: "H", level: 3 } }, base), false);
  assert.equal(onlyText({ ...base, s: { base: { gap: "m" } } }, base), false);
  assert.equal(onlyText({ ...base, b: { text: "entity.name" } }, base), false, "bound: the editor goes away");
  assert.equal(onlyText({ ...base, id: "b" }, base), false);
  assert.equal(onlyText(base, null), false);
});

test("an editor change and a canvas change in a row are one undo step each, restoring text and html", () => {
  const doc = { v: 2, nodes: [{ id: "a", t: "heading", p: { text: "Hi", level: 2 } }] };
  let s = S.createStore(doc);
  const type = (out, now) => (s = S.commit(s, opsFor(S.find(s.doc, "a"), out), { coalesce: "inline:a:field", now }));
  type("<p>Hi!</p>", 0);
  type("<p>Hi <strong>!</strong></p>", 4000);
  assert.equal(s.past.length, 1);
  assert.deepEqual(S.find(s.doc, "a").p, { text: "Hi !", html: "Hi <strong>!</strong>", level: 2 });
  assert.deepEqual(S.find(S.undo(s).doc, "a").p, { text: "Hi", level: 2 });
});

test("opsFor: the editor's entities and attribute order become the canonical html Go keeps", () => {
  const ops = opsFor({ id: "a" }, '<p>Don&#39;t <a target="_blank" href="/x">a&nbsp;b</a><br />c &quot;q&quot;</p>');
  assert.equal(ops[1].value, 'Don\'t <a href="/x" target="_blank" rel="noopener noreferrer">a\u00a0b</a><br>c "q"');
});

test("fieldSession: edits share an undo key until the editor loses focus, then the next session has its own", () => {
  const s = fieldSession("a");
  const first = s.key();
  assert.equal(s.key(), first);
  s.end();
  assert.notEqual(s.key(), first);
  assert.notEqual(fieldSession("b").key(), fieldSession("a").key());
  // two sessions on one node are two undo steps in the store
  const n = { id: "a", t: "heading", p: { text: "x" } };
  let st = S.createStore({ v: 2, nodes: [n] });
  st = S.commit(st, opsFor(n, "<p>x <strong>y</strong></p>"), { coalesce: "inline:a:field:0", now: 0 });
  st = S.commit(st, opsFor(n, "<p>x <strong>yz</strong></p>"), { coalesce: "inline:a:field:0", now: 1 });
  st = S.commit(st, opsFor(n, "<p>x <strong>yzw</strong></p>"), { coalesce: "inline:a:field:1", now: 2 });
  st = S.undo(st);
  assert.equal(S.find(st.doc, "a").p.html, "x <strong>yz</strong>");
  st = S.undo(st);
  assert.equal(S.find(st.doc, "a").p.text, "x");
});

test("opsFor: typing over a formatted text (Ctrl+A, type) stores plain text and clears html", () => {
  const n = { id: "a", p: { text: "Heading", html: "<strong>Heading</strong>" } };
  assert.deepEqual(opsFor(n, "<p>Third section heading</p>"), [
    { op: "setProp", id: "a", key: "text", value: "Third section heading" },
    { op: "setProp", id: "a", key: "html", value: null },
  ]);
});
