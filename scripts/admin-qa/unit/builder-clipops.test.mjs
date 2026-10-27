// Pure half of the clipboard/bulk ops chunk. Run: node --test scripts/admin-qa/unit/builder-clipops.test.mjs
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
const S = await import(new URL("../../../backend/web/admin/js/builder/store.js", import.meta.url));
const C = await import(new URL("../../../backend/web/admin/js/builder/clipops.js", import.meta.url));

const leaf = (id, t = "text", p = { text: id }) => ({ id, t, p });
const box = (id, c = []) => ({ id, t: "container", ...(c.length ? { c } : {}) });
const base = () => ({ v: 2, nodes: [box("a", [leaf("a1"), leaf("a2"), leaf("a3"), leaf("a4")]), box("b", [box("b1", [leaf("b11")])]), box("e")] });
const ids = (ns) => ns.map((n) => n.id);

test("contiguous: adjacent siblings in doc order, or null", () => {
  const d = base();
  assert.deepEqual(C.contiguous(d, ["a3", "a2"]), { parentId: "a", index: 1, ids: ["a2", "a3"] });
  assert.equal(C.contiguous(d, ["a1", "a3"]), null, "gap");
  assert.equal(C.contiguous(d, ["a1", "b"]), null, "different parents");
  assert.equal(C.contiguous(d, ["a1", "nope"]), null, "unknown");
  assert.equal(C.contiguous(d, []), null);
  assert.deepEqual(C.contiguous(d, ["a", "b", "e"]).ids, ["a", "b", "e"]);
});

test("wrapManyOps: one wrapper holds the run in order; undo restores; unwrap round-trips", () => {
  const d = base();
  const ops = C.wrapManyOps(d, ["a3", "a2"]);
  let s = S.commit(S.createStore(d), ops, { label: "Wrap" });
  const w = S.locate(s.doc, "a2").parent;
  assert.equal(w.t, "container");
  assert.deepEqual(ids(w.c), ["a2", "a3"]);
  assert.deepEqual(ids(S.find(s.doc, "a").c), ["a1", w.id, "a4"]);
  assert.deepEqual(S.undo(s).doc, d);
  s = S.commit(s, S.unwrapOps(s.doc, w.id));
  assert.deepEqual(s.doc, d);
  assert.equal(C.wrapManyOps(d, ["a1", "a3"]), null);
  const top = S.commit(S.createStore(d), C.wrapManyOps(d, ["a", "b"]));
  assert.deepEqual(ids(top.doc.nodes[0].c), ["a", "b"]);
  assert.equal(top.doc.nodes.length, 2);
});

test("deleteManyOps: drops descendants of selected nodes, one undo step", () => {
  const d = base();
  const ops = C.deleteManyOps(d, ["b11", "b", "a2"]);
  assert.deepEqual(ops.map((o) => o.id), ["a2", "b"]);
  const s = S.commit(S.createStore(d), ops, { label: "Delete" });
  assert.deepEqual(ids(S.flat(s.doc)), ["a", "a1", "a3", "a4", "e"]);
  assert.deepEqual(S.undo(s).doc, d);
});

test("duplicateManyOps: a fresh copy after each, in order, ids new", () => {
  const d = base();
  const ops = C.duplicateManyOps(d, ["a1", "a3", "b"]);
  const s = S.commit(S.createStore(d), ops, { label: "Duplicate" });
  const a = S.find(s.doc, "a").c;
  assert.deepEqual(a.map((n) => n.p?.text), ["a1", "a1", "a2", "a3", "a3", "a4"]);
  assert.equal(s.doc.nodes.length, 4);
  assert.equal(s.doc.nodes[2].c[0].c[0].p.text, "b11");
  const all = ids(S.flat(s.doc));
  assert.equal(new Set(all).size, all.length);
  assert.deepEqual(S.undo(s).doc, d);
});

test("envelope round-trip; anything else is not nodes", () => {
  const nodes = C.copyNodes(base(), ["a2", "a1", "b"]);
  assert.deepEqual(ids(nodes), ["a1", "a2", "b"]);
  const env = C.envelope(nodes, "https://x.test");
  assert.deepEqual(env, { hm: "nodes", v: 2, nodes, site: "https://x.test" });
  assert.deepEqual(C.parseEnvelope(JSON.stringify(env)), nodes);
  for (const bad of ["", "nope", "[]", "{}", JSON.stringify({ ...env, hm: "doc" }), JSON.stringify({ ...env, v: 1 }), JSON.stringify({ ...env, nodes: [] }), JSON.stringify({ ...env, nodes: {} })]) {
    assert.equal(C.parseEnvelope(bad), null, bad);
  }
});

// ---- the screen-facing half, with a fake ctx, clipboard and server -----------------------

const KINDS = [{ kind: "container", children: true }, { kind: "text", children: false }];
function harness(doc, selected) {
  const h = { doc, selected, said: [], runs: [], picked: null, kinds: KINDS, csrf: () => "tok", url: "/paste" };
  h.announce = (m) => h.said.push(m);
  h.run = (ops, o) => {
    h.runs.push(o.label);
    h.s = S.commit(S.createStore(h.doc), ops, o);
    h.doc = h.s.doc;
    return true;
  };
  h.select = (id) => (h.picked = [id]);
  h.selectAll = (ids) => (h.picked = ids);
  return h;
}
function fakeClipboard(text) {
  const box = { text, fail: false };
  Object.defineProperty(globalThis, "navigator", { value: { clipboard: { writeText: async (t) => (box.fail ? Promise.reject(new Error("no")) : (box.text = t)), readText: async () => (box.fail ? Promise.reject(new Error("no")) : box.text) } }, configurable: true });
  globalThis.location = { origin: "https://x.test" };
  return box;
}

test("pastePoint: after the selection, inside a container, top level without one", () => {
  const d = base();
  assert.deepEqual(C.pastePoint(d, "a2", KINDS, false), { parent: "a", index: 2 });
  assert.deepEqual(C.pastePoint(d, "a", KINDS, true), { parent: "a", index: 4 });
  assert.equal(C.pastePoint(d, "a2", KINDS, true), null, "a text cannot hold nodes");
  assert.deepEqual(C.pastePoint(d, null, KINDS, false), { parent: "", index: 3 });
});

test("copy writes the envelope; cut also removes in one step", async () => {
  const box = fakeClipboard("");
  const h = harness(base(), "a2");
  assert.equal(await C.copy(h, ["a2", "a1"]), true);
  assert.deepEqual(ids(C.parseEnvelope(box.text)), ["a1", "a2"]);
  assert.match(h.said.at(-1), /Copied 2 elements/);
  await C.cut(h, ["a2"]);
  assert.equal(h.runs.at(-1), "Cut 1 element");
  assert.equal(S.find(h.doc, "a2"), null);
  assert.match(h.said.at(-1), /^Cut 1 element\./);
  box.fail = true;
  await C.copy(h, ["a1"]);
  assert.match(h.said.at(-1), /system clipboard is not available/);
});

test("paste: envelope from the clipboard goes through the server, lands after the selection and is selected", async () => {
  const box = fakeClipboard("");
  const h = harness(base(), "a2");
  await C.copy(h, ["a1"]);
  const seen = [];
  let calls = 0;
  globalThis.fetch = async (url, init) => {
    seen.push({ url, init });
    const sent = JSON.parse(init.body);
    return { ok: true, json: async () => ({ nodes: sent.nodes.map((x) => ({ ...x, id: calls++ ? "fresh2" : "fresh" })), missingBlocks: ["x"] }) };
  };
  await C.paste(h, []);
  assert.equal(seen[0].url, "/paste");
  assert.equal(seen[0].init.headers["X-CSRF-Token"], "tok");
  assert.deepEqual(ids(S.find(h.doc, "a").c), ["a1", "a2", "fresh", "a3", "a4"]);
  assert.deepEqual(h.picked, ["fresh"]);
  assert.equal(h.runs.at(-1), "Paste 1 element");
  assert.match(h.said.at(-1), /a synced block does not exist/);
  await C.paste(harnessWith(h, "a"), [], { inside: true });
  assert.equal(h.runs.at(-1), "Paste 1 element inside");
  assert.equal(S.find(h.doc, "a").c.at(-1).id, "fresh2");
  void box;
});

function harnessWith(h, selected) {
  h.selected = selected;
  return h;
}

test("paste: memory when the clipboard is denied, a message when empty or refused", async () => {
  const box = fakeClipboard("plain text");
  const h = harness(base(), "b");
  globalThis.fetch = async () => ({ ok: false, json: async () => ({ error: "Too large." }) });
  await C.paste(h, []);
  assert.match(h.said.at(-1), /Nothing to paste|Too large/); // memory may hold an earlier copy
  box.fail = true;
  await C.copy(harness(base(), "a1"), ["a1"]);
  await C.paste(h, []);
  assert.equal(h.said.at(-1), "Too large.");
  assert.equal(h.runs.length, 0);
  await C.paste(harnessWith(h, "a1"), [], { inside: true });
  assert.equal(h.said.at(-1), "This element cannot hold others");
});

test("wrap, unwrap, select siblings", () => {
  const h = harness(base(), "a2");
  C.wrap(h, ["a3", "a2"]);
  assert.equal(h.runs.at(-1), "Wrap in Container");
  const w = S.find(h.doc, "a").c[1];
  assert.deepEqual(ids(w.c), ["a2", "a3"]);
  assert.deepEqual(h.picked, [w.id]);
  C.unwrap(h, [w.id]);
  assert.equal(h.runs.at(-1), "Unwrap Container");
  assert.deepEqual(ids(S.find(h.doc, "a").c), ["a1", "a2", "a3", "a4"]);
  assert.deepEqual(h.picked, ["a2", "a3"]);
  const n = h.runs.length;
  C.wrap(h, ["a1", "a3"]);
  C.unwrap(h, ["a1"]);
  C.unwrap(h, ["e"]);
  assert.equal(h.runs.length, n, "gaps, a leaf and an empty container are refused");
  assert.equal(h.said.length, 3);
  C.selectSiblings(h, ["a3"]);
  assert.deepEqual(h.picked, ["a1", "a2", "a3", "a4"]);
});

// A v1 section is selected on the canvas as its component: every clipboard and bulk action acts on the section.
const v1doc = () => ({
  v: 2,
  nodes: [
    { id: "s1", t: "container", p: { tag: "section", v1: true }, c: [{ id: "c1", t: "component", p: { type: "hero" } }] },
    { id: "s2", t: "container", p: { tag: "section", v1: true }, c: [{ id: "c2", t: "component", p: { type: "text" } }] },
    { id: "r", t: "ref", p: { block: "x" } },
  ],
});

test("v1 section selected through its component: copy, delete, duplicate and paste point use the section", () => {
  const d = v1doc();
  assert.deepEqual(ids(C.copyNodes(d, ["c1"])), ["s1"]);
  assert.deepEqual(C.deleteManyOps(d, ["c1"]), [{ op: "remove", id: "s1" }]);
  assert.deepEqual(C.duplicateManyOps(d, ["c1"]).map((o) => [o.parent, o.index]), [["", 1]]);
  assert.deepEqual(C.pastePoint(d, "c1", KINDS, false), { parent: "", index: 1 });
  assert.equal(C.pastePoint(d, "c1", KINDS, true), null, "nothing is pasted into a v1 section");
});

test("cut of a v1 section by its component leaves no empty shell", async () => {
  fakeClipboard("");
  const h = harness(v1doc(), "c1");
  await C.cut(h, ["c1"]);
  assert.deepEqual(ids(h.doc.nodes), ["s2", "r"]);
});

test("wrap is refused on refs, components and v1 sections; unwrap on a v1 section", () => {
  const d = v1doc();
  assert.equal(C.canWrap(d, ["c1"]), false, "v1 internals");
  assert.equal(C.canWrap(d, ["s1", "s2"]), false, "v1 sections");
  assert.equal(C.canWrap(d, ["r"]), false, "a ref");
  assert.equal(C.canWrap(base(), ["a1"]), true);
  assert.equal(C.canUnwrap(d, ["c1"]), false);
  const h = harness(d, "r");
  C.wrap(h, ["r"]);
  assert.equal(h.runs.length, 0);
  assert.equal(h.said.length, 1);
});

test("refusals go to ctx.refuse (the visible line) when the screen gives one", async () => {
  fakeClipboard("plain text");
  const h = harness(v1doc(), "r");
  h.refuse = (m) => (h.refused ??= []).push(m);
  C.wrap(h, ["r"]);
  C.unwrap(h, ["r"]);
  globalThis.fetch = async () => {
    throw new TypeError("Failed to fetch");
  };
  await C.copy(harness(base(), "a1"), ["a1"]);
  fakeClipboard("");
  await C.paste(h, []);
  assert.equal(h.refused.length, 3);
  assert.match(h.refused[2], /could not be reached/);
  assert.equal(h.said.filter((m) => /Select|reached/.test(m)).length, 0, "not only announced");
});

test("paste prefers the newest copy: a failed system write beats an older system envelope", async () => {
  const box = fakeClipboard("");
  const h = harness(base(), "b");
  await C.copy(h, ["a1"]); // reaches the system clipboard
  box.fail = false;
  const older = box.text;
  const orig = navigator.clipboard.writeText;
  navigator.clipboard.writeText = async () => Promise.reject(new Error("no"));
  await C.copy(h, ["a2"]); // newest, memory only
  navigator.clipboard.writeText = orig;
  assert.ok(older.includes('"a1"'));
  let sent;
  globalThis.fetch = async (_u, init) => {
    sent = JSON.parse(init.body);
    return { ok: true, json: async () => ({ nodes: sent.nodes.map((n) => ({ ...n, id: "fresh" })) }) };
  };
  await C.paste(h, []);
  assert.deepEqual(ids(sent.nodes), ["a2"]);
});

test("delete and cut read as Delete / Cut, never Remove", () => {
  const h = harness(base(), "a2");
  C.removeMany(h, ["a2", "a3"]);
  assert.equal(h.runs.at(-1), "Delete");
  assert.equal(h.said.at(-1), "Deleted 2 elements.");
  const h2 = harness(base(), "a2");
  C.removeMany(h2, ["a2"], "Cut");
  assert.equal(h2.runs.at(-1), "Cut 1 element");
  assert.equal(h2.said.at(-1), "Cut 1 element.");
});
