// Inline rich text, parent side: the ops for a canvas text message and the plain projection.
// Run: node --test scripts/admin-qa/unit/builder-inlinehtml.test.mjs
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
const { textOf, inlineOps } = await import(new URL("../../../backend/web/admin/js/builder/inlinehtml.js", import.meta.url));
const S = await import(new URL("../../../backend/web/admin/js/builder/store.js", import.meta.url));

test("textOf drops tags, maps br to a newline, decodes the three bridge entities", () => {
  assert.equal(textOf("a <strong>b</strong> <a href=\"/x\" target=\"_blank\">c</a>"), "a b c");
  assert.equal(textOf("one<br>two<br>three"), "one\ntwo\nthree");
  assert.equal(textOf("1 &lt; 2 &amp;&amp; 3 &gt; 2&nbsp;ok"), "1 < 2 && 3 > 2\u00a0ok");
  assert.equal(textOf("&amp;lt;"), "&lt;"); // decoded once, not twice
});

test("inlineOps: an html message sets html and the text projection; a plain one clears html", () => {
  const n = { id: "a" };
  assert.deepEqual(inlineOps(n, "html", "x <em>y</em>"), [
    { op: "setProp", id: "a", key: "text", value: "x y" },
    { op: "setProp", id: "a", key: "html", value: "x <em>y</em>" },
  ]);
  assert.deepEqual(inlineOps(n, "text", "plain"), [
    { op: "setProp", id: "a", key: "text", value: "plain" },
    { op: "setProp", id: "a", key: "html", value: null },
  ]);
});

test("switching plain <-> html inside one session is one undo step that restores both props", () => {
  const doc = { v: 2, nodes: [{ id: "a", t: "heading", p: { text: "Hi" } }] };
  const key = "inline:a:0";
  let s = S.createStore(doc);
  const send = (prop, v, now) => (s = S.commit(s, inlineOps(S.find(s.doc, "a"), prop, v), { coalesce: key, now }));
  send("text", "Hi!", 0);
  send("html", "Hi <strong>!</strong>", 5000);
  send("text", "Hi?", 9000);
  send("html", "<em>Hi?</em>", 12000);
  assert.equal(s.past.length, 1);
  assert.deepEqual(S.find(s.doc, "a").p, { text: "Hi?", html: "<em>Hi?</em>" });
  const u = S.undo(s);
  assert.deepEqual(S.find(u.doc, "a").p, { text: "Hi" });
  const r = S.redo(u);
  assert.deepEqual(S.find(r.doc, "a").p, { text: "Hi?", html: "<em>Hi?</em>" });
});

test("a new session key is a new undo step", () => {
  let s = S.createStore({ v: 2, nodes: [{ id: "a", t: "text", p: { text: "x" } }] });
  s = S.commit(s, inlineOps({ id: "a" }, "text", "y"), { coalesce: "inline:a:0", now: 0 });
  s = S.commit(s, inlineOps({ id: "a" }, "text", "z"), { coalesce: "inline:a:1", now: 1 });
  assert.equal(s.past.length, 2);
});

test("canonHTML: an editor's output becomes the canonical form shared with Go and the bridge", async () => {
  const { readFileSync } = await import("node:fs");
  const { canonHTML } = await import(new URL("../../../backend/web/admin/js/builder/inlinehtml.js", import.meta.url));
  const cases = JSON.parse(readFileSync(new URL("../../../backend/testdata/inline/canonical.json", import.meta.url), "utf8"));
  for (const c of cases) {
    assert.equal(canonHTML(c.canonical), c.canonical, `${c.name}: canonical is a fixed point`);
    if (c.editor) assert.equal(canonHTML(c.editor), c.canonical, c.name);
  }
});

test("repatchOps: re-sets what the node has, never a made-up value", async () => {
  const { repatchOps } = await import(new URL("../../../backend/web/admin/js/builder/inlinehtml.js", import.meta.url));
  assert.deepEqual(repatchOps({ id: "h", p: { text: "Hi", level: 2 } }), [{ op: "setProp", id: "h", key: "text", value: "Hi" }]);
  assert.deepEqual(repatchOps({ id: "h", p: { text: "a", html: "<em>a</em>" } }), [
    { op: "setProp", id: "h", key: "text", value: "a" },
    { op: "setProp", id: "h", key: "html", value: "<em>a</em>" },
  ]);
  assert.deepEqual(repatchOps({ id: "c", p: { type: "hero", content: {} } }), [{ op: "setProp", id: "c", key: "type", value: "hero" }]);
  assert.deepEqual(repatchOps({ id: "c", p: { navlabel: "N", type: "hero" } }), [{ op: "setProp", id: "c", key: "type", value: "hero" }]);
  assert.deepEqual(repatchOps({ id: "x" }), []);
  assert.deepEqual(repatchOps(undefined), []);
});

test("mountInline.repatch: commits the node's own values (or restages when it has no props)", async () => {
  const { mountInline } = await import(new URL("../../../backend/web/admin/js/builder/inlinehtml.js", import.meta.url));
  const d = { v: 2, nodes: [{ id: "h", t: "heading", p: { text: "Hi" } }, { id: "e", t: "container" }] };
  const calls = [];
  const renderer = { commit: (...a) => calls.push(["commit", a[2]]), stage: () => calls.push(["stage"]) };
  const i = mountInline({ run: () => {}, doc: () => d, renderer });
  i.repatch("h");
  i.repatch("e");
  assert.deepEqual(calls, [["commit", [{ op: "setProp", id: "h", key: "text", value: "Hi" }]], ["stage"]]);
  i.text("h", "text", "x", true);
  i.end("h"); // a plain edit is shown again once it is over
  assert.equal(calls.length, 3);
});
