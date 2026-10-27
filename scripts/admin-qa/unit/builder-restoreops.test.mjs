// Element restore: the op builder and the dialog's small helpers. Run: node --test scripts/admin-qa/unit/builder-restoreops.test.mjs
import { register } from "node:module";
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const root = new URL("../../../backend/web/admin/js/", import.meta.url).href;
register(
  "data:text/javascript," +
    encodeURIComponent(`export async function resolve(s, c, next) {
      if (s.startsWith("@hm/")) return { url: ${JSON.stringify(root)} + s.slice(4) + ".js", shortCircuit: true };
      return next(s, c);
    }`),
);
const { restoreOps } = await import(new URL("../../../backend/web/admin/js/builder/restoreops.js", import.meta.url));
const { commit, createStore, undo, find } = await import(new URL("../../../backend/web/admin/js/builder/store.js", import.meta.url));
const N = await import(new URL("../../../backend/web/admin/js/builder/noderestore.js", import.meta.url));
const { rows } = JSON.parse(`{"rows":${readFileSync(new URL("../../../backend/internal/admin/testdata/builder_node_versions.golden.json", import.meta.url), "utf8")}}`);

const at = { parentId: "hero", index: 1 };

test("the same kind changes only what differs", () => {
  const cur = { id: "h", t: "heading", p: { text: "Now", level: 2, same: 1 }, s: { base: { fs: "xl", c: "ink" }, md: { fs: "lg" } }, b: { text: "page.title" } };
  const old = { t: "heading", p: { text: "Then", level: 2, extra: true }, s: { base: { fs: "xl" } }, b: {} };
  assert.deepEqual(restoreOps(cur, old, at), [
    { op: "setProp", id: "h", key: "text", value: "Then" },
    { op: "setProp", id: "h", key: "same", value: null },
    { op: "setProp", id: "h", key: "extra", value: true },
    { op: "setStyle", id: "h", bp: "base", key: "c", value: null },
    { op: "setStyle", id: "h", bp: "md", key: "fs", value: null },
    { op: "setBind", id: "h", key: "text", value: null },
  ]);
});

test("no difference, no ops", () => {
  const cur = { id: "h", t: "heading", p: { text: "A" }, s: { base: { fs: "xl" } }, c: [{ id: "x", t: "text" }] };
  assert.deepEqual(restoreOps(cur, { t: "heading", p: { text: "A" }, s: { base: { fs: "xl" } } }, at), []);
});

test("another kind is removed and inserted at the same place, keeping the children it has now", () => {
  const cur = { id: "b", t: "container", p: { tag: "div" }, c: [{ id: "k", t: "text" }] };
  const ops = restoreOps(cur, { t: "section", p: { tag: "section" }, s: { base: { p: "4" } } }, at);
  assert.deepEqual(ops, [
    { op: "remove", id: "b" },
    { op: "insert", parent: "hero", index: 1, node: { id: "b", t: "section", p: { tag: "section" }, s: { base: { p: "4" } }, c: [{ id: "k", t: "text" }] } },
  ]);
});

test("one commit is one undo step, and undo brings the element back as it was", () => {
  const doc = { v: 2, nodes: [{ id: "hero", t: "container", c: [{ id: "a", t: "text" }, { id: "h", t: "heading", p: { text: "Now", level: 3 }, s: { base: { fs: "lg" } } }] }] };
  let st = createStore(doc);
  const cur = find(st.doc, "h");
  st = commit(st, restoreOps(cur, { t: "heading", p: { text: "Then", level: 2 }, s: { base: { fs: "xl" } } }, at), { label: "Restore Heading from version 4" });
  assert.deepEqual(find(st.doc, "h"), { id: "h", t: "heading", p: { text: "Then", level: 2 }, s: { base: { fs: "xl" } } });
  st = undo(st);
  assert.deepEqual(find(st.doc, "h"), cur);
});

test("the shape fixture the route writes is what the builder reads", () => {
  assert.equal(rows[0].version, 12);
  assert.deepEqual(restoreOps({ id: "h", t: "heading" }, rows[0].node, at).map((o) => o.op), ["setProp", "setProp", "setStyle", "setBind"]);
});

test("dates read as Today / Yesterday / a day", () => {
  const now = new Date(2026, 9, 26, 18, 0);
  assert.match(N.when(new Date(2026, 9, 26, 9, 5).toISOString(), now), /^Today /);
  assert.match(N.when(new Date(2026, 9, 25, 23, 30).toISOString(), now), /^Yesterday /);
  assert.doesNotMatch(N.when(new Date(2026, 9, 1, 9, 5).toISOString(), now), /^(Today|Yesterday)/);
});

test("the sample is the text without markup, shortened, else the kind", () => {
  const kind = (t) => `Kind ${t}`;
  assert.equal(N.sample({ t: "text", p: { html: "<p>Hello <b>you</b></p>" } }, kind), "Hello you");
  assert.equal(N.sample({ t: "image", p: {} }, kind), "Kind image");
  assert.ok(N.sample({ t: "text", p: { text: "x".repeat(200) } }, kind).length <= 60);
});

test("dates use the house format: 24 h clock, day month year", () => {
  const now = new Date(2026, 9, 26, 18, 0);
  assert.equal(N.when(new Date(2026, 9, 26, 15, 4).toISOString(), now), "Today 15:04");
  assert.equal(N.when(new Date(2026, 9, 1, 9, 5).toISOString(), now), "1 Oct 2026 09:05");
});

test("the title names the element; the first choice is the newest version that differs from now", () => {
  assert.equal(N.restoreTitle("Heading · Build pages", "Heading"), "Restore “Heading · Build pages” from an earlier version");
  assert.equal(N.restoreTitle("", "Heading"), "Restore “Heading” from an earlier version");
  const cur = { node: { id: "h", t: "heading", p: { text: "Now" } }, parentId: "", index: 0 };
  const same = { node: { t: "heading", p: { text: "Now" } } };
  const older = { node: { t: "heading", p: { text: "Then" } } };
  assert.equal(N.defaultVersion([same, older], cur), 1, "the latest save equals the page, so the next one is chosen");
  assert.equal(N.defaultVersion([same], cur), 0);
  assert.equal(N.defaultVersion([older, same], cur), 0);
});
