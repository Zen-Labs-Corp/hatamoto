// sync.js: the render queue and the save side, with fake fetch, canvas and timers.
// Run: node --test scripts/admin-qa/unit/builder-sync.test.mjs
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
const { createRenderer, createSaver, mapSaveReply, stateLine, fragmentKey } = await import(new URL("../../../backend/web/admin/js/builder/sync.js", import.meta.url));

const leaf = (id, t = "heading", p = { text: id, level: 2 }) => ({ id, t, p });
const box = (id, c = []) => ({ id, t: "container", ...(c.length ? { c } : {}) });
const sec = (id, cid) => ({ id, t: "container", p: { tag: "section" }, c: [{ id: cid, t: "component", p: { type: "hero" } }] });
const doc = () => ({ v: 2, nodes: [sec("s1", "c1"), box("a", [leaf("a1"), leaf("a2")]), box("b")] });

const reply = (status, body) => ({ ok: status >= 200 && status < 300, status, json: async () => body });

function rig({ responses = [], ready = true } = {}) {
  const calls = [];
  const rec = []; // recovery writes and discards, kept apart from the save calls
  let offline = false;
  const posted = [];
  let load = [];
  const queue = [...responses];
  const fetch = async (url, init) => {
    if (url.startsWith("/recovery")) {
      if (offline) throw new TypeError("Failed to fetch");
      rec.push({ url, body: JSON.parse(init.body), csrf: init.headers["X-CSRF-Token"], keepalive: !!init.keepalive });
      return reply(204, {});
    }
    calls.push({ url, body: JSON.parse(init.body), csrf: init.headers["X-CSRF-Token"] });
    const r = queue.shift();
    return typeof r === "function" ? r(url) : (r ?? reply(200, {}));
  };
  const canvas = {
    apply: (msgs) => (ready ? (posted.push(msgs), true) : false),
    load: (url, rev) => load.push([url, rev]),
    expectOrder: (ids) => (rig.order = ids),
  };
  return { calls, rec, posted, load: () => load, fetch, canvas, goOffline: () => (offline = true) };
}

const urls = { stage: "/stage", render: "/render", save: "/save", recovery: "/recovery", recoveryDiscard: "/recovery/discard" };

function renderer(r, current, extra = {}) {
  let errs = [];
  const rr = createRenderer({ urls, csrf: () => "tok", canvas: r.canvas, getDoc: () => current.doc, fetch: r.fetch, onError: (m) => errs.push(m), decls: (n, bp, k, v) => [[k, v]], ...extra });
  return { rr, errs };
}

test("a heading prop change renders the node on the server and patches the canvas", async () => {
  const d = doc();
  const st = S.commit(S.createStore(d), [{ op: "setProp", id: "a1", key: "level", value: 3 }]);
  const r = rig({ responses: [reply(200, { fragments: [{ sel: "node", key: "a1", html: "<h3>a1</h3>", css: {} }], css: {}, order: ["c1", "a"] })] });
  const { rr } = renderer(r, st);
  await rr.commit(d, st.doc, st.applied);
  assert.equal(r.calls.length, 1);
  assert.equal(r.calls[0].url, "/render");
  assert.equal(r.calls[0].csrf, "tok");
  assert.deepEqual(r.calls[0].body.doc, d, "the server gets the doc before the ops and applies them itself");
  assert.deepEqual(r.calls[0].body.ops, st.applied);
  assert.deepEqual(r.calls[0].body.render, ["a1"]);
  assert.equal(r.posted.length, 1);
  assert.equal(r.posted[0][0].type, "patch");
  assert.equal(r.posted[0][0].html, "<h3>a1</h3>");
  assert.equal(r.load().length, 0, "no frame reload");
  assert.deepEqual(rig.order, ["c1"], "only sections count in the frame order");
});

test("a move needs no server round trip", async () => {
  const d = doc();
  const st = S.commit(S.createStore(d), [{ op: "move", id: "a2", parent: "a", index: 0 }]);
  const r = rig();
  const { rr } = renderer(r, st);
  await rr.commit(d, st.doc, st.applied);
  assert.equal(r.calls.length, 0);
  assert.equal(r.posted[0][0].type, "move");
});

test("a style change previews at once, then confirms the compiled rules", async () => {
  const d = doc();
  const st = S.commit(S.createStore(d), [{ op: "setStyle", id: "a", bp: "base", key: "gap", value: "4" }]);
  const order = [];
  const r = rig({ responses: [(u) => (order.push("fetch"), reply(200, { fragments: [], css: { a: ".x{gap:1px}" }, order: [] }))] });
  const apply = r.canvas.apply;
  r.canvas.apply = (m) => (order.push(m.map((x) => x.type).join()), apply(m));
  const { rr } = renderer(r, st);
  await rr.commit(d, st.doc, st.applied);
  assert.deepEqual(order, ["setVar", "fetch", "css"]);
  assert.deepEqual(r.calls[0].body.css, ["a"]);
  assert.equal(r.posted[1][0].css.a, ".x{gap:1px}");
});

test("an unmappable commit stages the whole doc", async () => {
  const d = doc();
  const st = S.commit(S.createStore(d), [{ op: "setStyle", id: "a", bp: "base", key: "gap", value: "4" }]);
  const r = rig({ responses: [reply(200, { url: "/preview/t1" })] });
  const { rr } = renderer(r, st, { decls: () => null });
  await rr.commit(d, st.doc, st.applied);
  assert.equal(r.calls[0].url, "/stage");
  assert.deepEqual(r.calls[0].body, { doc: st.doc });
  assert.deepEqual(r.load(), [["/preview/t1", 1]]);
});

test("a failed render falls back to staging; a refused patch does too", async () => {
  const d = doc();
  const st = S.commit(S.createStore(d), [{ op: "setProp", id: "a1", key: "level", value: 3 }]);
  const r = rig({ responses: [reply(500, {}), reply(200, { url: "/p/1" })] });
  const { rr, errs } = renderer(r, st);
  await rr.commit(d, st.doc, st.applied);
  assert.deepEqual(r.calls.map((c) => c.url), ["/render", "/stage"]);
  assert.equal(errs.length, 1);

  const r2 = rig({ ready: false, responses: [reply(200, { fragments: [{ key: "a1", html: "x" }] }), reply(200, { url: "/p/2" })] });
  const { rr: rr2 } = renderer(r2, st);
  await rr2.commit(d, st.doc, st.applied);
  assert.deepEqual(r2.calls.map((c) => c.url), ["/render", "/stage"]);
});

test("a node the server renders nothing for stages the page", async () => {
  const d = doc();
  const st = S.commit(S.createStore(d), [{ op: "setProp", id: "a1", key: "level", value: 3 }]);
  const r = rig({ responses: [reply(200, { fragments: [] }), reply(200, { url: "/p/3" })] });
  const { rr } = renderer(r, st);
  await rr.commit(d, st.doc, st.applied);
  assert.deepEqual(r.calls.map((c) => c.url), ["/render", "/stage"]);
});

test("jobs run one at a time, in order", async () => {
  let d = doc();
  const order = [];
  let release;
  const gate = new Promise((res) => (release = res));
  const r = rig({
    responses: [
      async () => (await gate, order.push("first"), reply(200, { fragments: [{ key: "a1", html: "1" }], order: [] })),
      async () => (order.push("second"), reply(200, { fragments: [{ key: "a1", html: "2" }], order: [] })),
    ],
  });
  let st = S.createStore(d);
  const { rr } = renderer(r, { get doc() { return st.doc; } });
  const before1 = st.doc;
  st = S.commit(st, [{ op: "setProp", id: "a1", key: "level", value: 3 }]);
  const p1 = rr.commit(before1, st.doc, st.applied);
  const before2 = st.doc;
  st = S.commit(st, [{ op: "setProp", id: "a1", key: "level", value: 4 }]);
  const p2 = rr.commit(before2, st.doc, st.applied);
  await new Promise((res) => setTimeout(res, 5));
  assert.equal(r.calls.length, 1, "the second job waits for the first");
  release();
  await Promise.all([p1, p2]);
  assert.deepEqual(order, ["first", "second"]);
  assert.deepEqual(r.posted.map((m) => m[0].html), ["1", "2"]);
});

test("a top-level v1-shaped container is shown under its component's key", () => {
  const d = doc();
  assert.equal(fragmentKey(d, "s1"), "c1");
  assert.equal(fragmentKey(d, "a"), "a");
  assert.equal(fragmentKey(d, "a1"), "a1");
});

// ---- saver --------------------------------------------------------------------------

test("mapSaveReply follows the save endpoint contract", () => {
  assert.deepEqual(mapSaveReply(200, { ok: true, version: 5 }), { ok: true, version: 5, doc: undefined, status: undefined, note: undefined });
  assert.deepEqual(mapSaveReply(409, { conflict: true, version: 7, error: "taken" }), { conflict: true, version: 7, message: "taken" });
  assert.equal(mapSaveReply(409, { error: "Only drafts autosave. Use Update." }).stop, true);
  assert.equal(mapSaveReply(422, { code: "invalid_page_doc", error: "bad" }).code, "invalid_page_doc");
  assert.equal(mapSaveReply(403, {}).stop, true);
  assert.equal(mapSaveReply(500, {}).ok, false);
});

test("stateLine words", () => {
  assert.equal(stateLine({ state: "pending" }, { draft: true }), "Not saved");
  assert.equal(stateLine({ state: "pending" }, { draft: false }), "Unsaved changes");
  assert.equal(stateLine({ state: "pending", keep: "kept" }, { draft: false }), "Unsaved changes, kept for you");
  assert.equal(stateLine({ state: "pending", keep: "failed" }, { draft: false }), "Not saved. You appear to be offline.");
  assert.equal(stateLine({ state: "saved" }, { at: "10:42" }), "Saved 10:42");
  assert.match(stateLine({ state: "error", message: "Offline." }), /Offline\. Retrying/);
  assert.equal(stateLine({ state: "idle" }), "");
});

function fakeTimers() {
  let id = 0;
  const pending = new Map();
  return {
    set: (fn, ms) => (pending.set(++id, { fn, ms }), id),
    clear: (i) => pending.delete(i),
    fire: async (ms) => {
      for (const [i, t] of [...pending]) if (t.ms <= ms) { pending.delete(i); await t.fn(); }
      await new Promise((r) => setTimeout(r, 25));
    },
    count: () => pending.size,
  };
}

function saverRig(status, responses = [], extra = {}) {
  const r = rig({ responses });
  const timers = fakeTimers();
  const states = [];
  const adopted = [];
  let cur = doc();
  const boot = { entity: "e1", user: "u1", status, version: 3, name: "Home", doc: cur, ...extra };
  const sv = createSaver({ urls, csrf: () => "tok", boot, getDoc: () => cur, getName: () => "Home", onState: (q) => states.push(q.state), onAdopt: (d) => adopted.push(d), fetch: r.fetch, timers });
  return { r, sv, timers, states, adopted, edit: () => { cur = { ...cur, nodes: [...cur.nodes] }; sv.changed(); } };
}

test("a draft autosaves with its version and counts the new one", async () => {
  const t = saverRig("draft", [reply(200, { ok: true, version: 4 }), reply(200, { ok: true, version: 5 })]);
  t.edit();
  await t.timers.fire(3000);
  assert.equal(t.r.calls[0].url, "/save");
  assert.equal(t.r.calls[0].body.op, "autosave");
  assert.equal(t.r.calls[0].body.version, 3);
  assert.equal(t.sv.version, 4);
  assert.ok(t.states.includes("saved"));
  t.edit();
  await t.timers.fire(60000);
  assert.equal(t.r.calls[1].body.version, 4);
});

test("a block or part is a draft that never autosaves: Save block posts op save", async () => {
  const t = saverRig("draft", [reply(200, { ok: true, version: 4 })], { reuse: { role: "block" } });
  t.edit();
  await t.timers.fire(60000);
  assert.equal(t.r.calls.length, 0, "no autosave request");
  assert.equal(t.sv.draft, false);
  assert.equal(t.sv.dirty, true);
  const res = await t.sv.save("save");
  assert.equal(res.ok, true);
  assert.deepEqual([t.r.calls[0].body.op, t.r.calls[0].body.version], ["save", 3]);
  assert.equal(t.sv.version, 4);
  assert.equal(stateLine({ state: "pending" }, { draft: t.sv.draft }), "Unsaved changes");
  // a page keeps its autosave
  assert.equal(saverRig("draft", [], { reuse: { role: "page" } }).sv.draft, true);
});

test("a published page never autosaves: it keeps a server copy and saves on Update", async () => {
  const t = saverRig("published", [reply(200, { ok: true, version: 4 })]);
  t.edit();
  await t.timers.fire(60000);
  assert.equal(t.r.calls.length, 0, "no request without Update");
  assert.equal(t.sv.dirty, true);
  assert.equal(t.r.rec.length, 1);
  assert.equal(t.r.rec[0].url, "/recovery");
  assert.equal(t.r.rec[0].csrf, "tok");
  assert.equal(t.r.rec[0].body.version, 3);
  assert.equal(JSON.parse(t.r.rec[0].body.content).v, 2);
  assert.equal(t.sv.q.keep, "kept");

  const res = await t.sv.save("save");
  assert.equal(res.ok, true);
  assert.equal(t.r.calls[0].body.op, "save");
  assert.equal(t.r.rec.at(-1).url, "/recovery/discard", "the copy is discarded by a save that covers it");
  assert.equal(t.sv.q.keep, "");
  assert.equal(t.sv.dirty, false);
});

test("a conflict stops the save; Overwrite retries over the current version", async () => {
  const t = saverRig("published", [reply(409, { conflict: true, version: 9, error: "Changed." }), reply(200, { ok: true, version: 10 })]);
  t.edit();
  const res = await t.sv.save("save");
  assert.equal(res.conflict, true);
  assert.equal(t.sv.q.state, "conflict");
  assert.equal(t.sv.q.conflict.version, 9);
  const again = await t.sv.resolve("overwrite");
  assert.equal(again.ok, true);
  assert.equal(t.r.calls[1].body.version, 9);
  assert.equal(t.sv.version, 10);
});

test("an autosave conflict on a draft surfaces and Overwrite saves over it", async () => {
  const t = saverRig("draft", [reply(409, { conflict: true, version: 8, error: "Changed." }), reply(200, { ok: true, version: 9 })]);
  t.edit();
  await t.timers.fire(3000);
  assert.equal(t.sv.q.state, "conflict");
  await t.sv.resolve("overwrite");
  await t.timers.fire(60000);
  assert.equal(t.r.calls[1].body.version, 8);
  assert.equal(t.sv.version, 9);
});

test("an invalid doc stops with its message and the doc is kept for the person", async () => {
  const t = saverRig("published", [reply(422, { code: "invalid_page_doc", error: "bad node" })]);
  t.edit();
  const res = await t.sv.save("save");
  assert.equal(res.stop, true);
  assert.equal(t.sv.q.state, "stopped");
  assert.equal(t.sv.q.message, "bad node");
  assert.equal(t.r.rec.length, 1, "what could not be saved is sent as a recovery copy at once");
  assert.equal(t.r.rec[0].url, "/recovery");
});

test("a draft stopped by a refused doc saves again after the next edit (an undo)", async () => {
  const t = saverRig("draft", [reply(422, { code: "invalid_page_doc", error: "bad node" }), reply(200, { ok: true, version: 4 })]);
  t.edit();
  await t.timers.fire(3000);
  assert.equal(t.sv.q.state, "stopped");
  t.edit();
  await t.timers.fire(60000);
  assert.equal(t.sv.q.state, "saved");
  assert.equal(t.sv.version, 4);
});

test("a refused doc's message names elements by their labels, not their ids", async () => {
  const id = doc().nodes[0].id;
  const t = saverRig("draft", [reply(422, { code: "invalid_page_doc", error: `${id}: a container with a link cannot hold interactive content` })]);
  t.edit();
  await t.timers.fire(3000);
  assert.equal(t.sv.q.state, "stopped");
  assert.doesNotMatch(t.sv.q.message, new RegExp(id));
  assert.match(t.sv.q.message, /^“[^”]+”: a container with a link/);
});

test("a draft stopped by the session ending stays stopped after an edit", async () => {
  const t = saverRig("draft", [reply(401, {}), reply(200, { ok: true, version: 4 })]);
  t.edit();
  await t.timers.fire(3000);
  assert.equal(t.sv.q.state, "stopped");
  t.edit();
  await t.timers.fire(60000);
  assert.equal(t.sv.q.state, "stopped");
});

test("the blog editor's autosave stays stopped after a change", async () => {
  const { createAutosave } = await import(new URL("../../../backend/web/admin/js/editor/autosave.js", import.meta.url));
  let saves = 0;
  const a = createAutosave({ save: async () => (saves++, { stop: true, message: "no", code: "invalid_page_doc" }), version: 1, delay: 0, minGap: 0 });
  a.change();
  await a.flush();
  assert.equal(a.q.state, "stopped");
  a.change();
  await a.flush();
  assert.equal(a.q.state, "stopped");
  assert.equal(saves, 1);
});

test("the server's doc is adopted when its sanitiser changed it", async () => {
  const t = saverRig("published", [reply(200, { ok: true, version: 4, doc: { v: 2, nodes: [] } })]);
  t.edit();
  await t.sv.save("save");
  assert.deepEqual(t.adopted, [{ v: 2, nodes: [] }]);
});

test("the copy the server kept is offered on load, and a planted browser copy is not", () => {
  const body = { content: JSON.stringify({ v: 2, nodes: [] }), version: 3, name: "Home", at: "2026-10-26T14:02:11Z" };
  assert.equal(saverRig("published").sv.restorable(), null);
  const globalStore = { getItem: () => JSON.stringify({ ...body, content: "{}" }), setItem() {}, removeItem() {} };
  globalThis.localStorage = globalStore;
  assert.equal(saverRig("published").sv.restorable(), null, "browser storage is never read");
  delete globalThis.localStorage;
  assert.equal(saverRig("published", [], { recovery: body }).sv.restorable().action, "restore");
  assert.equal(saverRig("published", [], { recovery: { ...body, version: 2 } }).sv.restorable().action, "stale");
  assert.equal(saverRig("published", [], { recovery: { content: 5 } }).sv.restorable(), null, "a malformed copy is ignored");
});

test("Discard deletes the server copy once", async () => {
  const t = saverRig("published", [], { recovery: { content: "{}", version: 3, name: "Home" } });
  await t.sv.discardLocal();
  await t.sv.discardLocal();
  assert.deepEqual(t.r.rec.map((c) => c.url), ["/recovery/discard"]);
});

test("a save inside the write window leaves no recovery copy behind", async () => {
  const t = saverRig("published", [reply(200, { ok: true, version: 4 })]);
  t.edit();
  await t.sv.save("save");
  await t.timers.fire(60000);
  assert.equal(t.r.rec.length, 0, "the pending write must not send a copy of a saved page");
  assert.equal(t.sv.dirty, false);
});

test("a copy written while Update is in flight waits for the reply", async () => {
  let release;
  const gate = new Promise((r) => (release = r));
  const t = saverRig("published", [async () => (await gate, reply(200, { ok: true, version: 4 }))]);
  t.edit();
  const saving = t.sv.save("save");
  t.edit();
  await t.timers.fire(60000);
  assert.equal(t.r.rec.length, 0, "no copy goes out while the save is under way");
  release();
  await saving;
  await t.timers.fire(60000);
  assert.deepEqual(t.r.rec.map((c) => c.url), ["/recovery"], "the edits typed meanwhile are kept, and nothing is discarded under them");
});

test("a refused Update lets the held copy go out", async () => {
  const t = saverRig("published", [reply(500, {})]);
  t.edit();
  await t.sv.save("save");
  assert.deepEqual(t.r.rec.map((c) => c.url), ["/recovery"]);
});

test("the bar says when the session ended or the copy was refused", () => {
  assert.equal(stateLine({ state: "pending", keep: "expired" }, { draft: false }), "Your session ended: sign in again in another tab.");
  assert.match(stateLine({ state: "pending", keep: "refused" }, { draft: false }), /not kept/);
  assert.equal(stateLine({ state: "pending", keep: "expired" }, { draft: true }), "Not saved");
});

test("offline, the copy stays in the page and the bar says so", async () => {
  const t = saverRig("published");
  t.r.goOffline();
  t.edit();
  await t.timers.fire(60000);
  assert.equal(t.sv.q.keep, "failed");
  assert.equal(t.sv.dirty, true);
  assert.equal(stateLine(t.sv.q, { draft: false }), "Not saved. You appear to be offline.");
});

test("a top-level insert that renders nothing patches in place and re-anchors the next one", async () => {
  const d = { v: 2, nodes: [sec("s1", "c1")] };
  const blank = { id: "k1", t: "component", p: { type: "content" } };
  const st1 = S.commit(S.createStore(d), [{ op: "insert", parent: "", index: 1, node: blank }]);
  const st2 = S.commit(S.createStore(st1.doc), [{ op: "insert", parent: "", index: 2, node: box("n2") }]);
  const r = rig({
    responses: [
      reply(200, { fragments: [], css: {}, order: ["c1"] }), // k1 renders nothing
      reply(200, { fragments: [{ sel: "node", key: "n2", html: "<div></div>", css: {} }], css: {}, order: ["c1", "n2"] }),
    ],
  });
  const cur = { doc: st1.doc };
  const { rr, errs } = renderer(r, cur);
  await rr.commit(d, st1.doc, st1.applied);
  cur.doc = st2.doc;
  await rr.commit(st1.doc, st2.doc, st2.applied);
  assert.deepEqual(r.calls.map((c) => c.url), ["/render", "/render"], "no stage");
  assert.deepEqual(errs, []);
  assert.equal(r.posted.length, 1, "the first insert posts nothing");
  assert.deepEqual(r.posted[0][0].after, { sel: "section", key: "c1" }, "anchored on the rendered neighbour, not the blank section");
});

test("a save reply's doc is not adopted over edits made while it was in flight", async () => {
  let release;
  const gate = new Promise((res) => (release = res));
  const t = saverRig("draft", [async () => (await gate, reply(200, { ok: true, version: 4, doc: { v: 2, nodes: [] } })), reply(200, { ok: true, version: 5 })]);
  t.edit();
  const first = t.timers.fire(3000);
  await new Promise((res) => setTimeout(res, 5));
  t.edit(); // typed while the autosave is in flight
  release();
  await first;
  await new Promise((res) => setTimeout(res, 5));
  assert.deepEqual(t.adopted, [], "the server doc would drop the new edit");
  await t.timers.fire(60000);
  assert.equal(t.r.calls.length, 2, "the newer doc is saved next");
  assert.equal(t.sv.dirty, false);
});

test("a stage invalidates jobs queued before it", async () => {
  let release;
  const gate = new Promise((res) => (release = res));
  const r = rig({ responses: [async () => (await gate, reply(200, { fragments: [{ key: "a1", html: "old" }], order: [] })), reply(200, { url: "/frame" })] });
  let st = S.createStore(doc());
  const { rr } = renderer(r, { get doc() { return st.doc; } });
  const before = st.doc;
  st = S.commit(st, [{ op: "setProp", id: "a1", key: "level", value: 3 }]);
  const p = rr.commit(before, st.doc, st.applied);
  await new Promise((res) => setTimeout(res, 5));
  await rr.stage(); // a self-heal while the render is in flight
  release();
  await p;
  assert.equal(r.posted.length, 0, "the stale patch is not applied to the new frame");
  assert.equal(r.load().length, 1);
});

test("an edit made while a stage is in flight is staged again, once", async () => {
  let release;
  const gate = new Promise((res) => (release = res));
  const r = rig({ responses: [async () => (await gate, reply(200, { url: "/f1" })), reply(200, { url: "/f2" })] });
  let st = S.createStore(doc());
  const { rr } = renderer(r, { get doc() { return st.doc; } });
  const p = rr.stage();
  await new Promise((res) => setTimeout(res, 5));
  const before = st.doc;
  st = S.commit(st, [{ op: "move", id: "a2", parent: "a", index: 0 }]);
  rr.commit(before, st.doc, st.applied);
  release();
  await p;
  await new Promise((res) => setTimeout(res, 10));
  assert.deepEqual(r.calls.map((c) => c.url), ["/stage", "/stage"]);
  assert.deepEqual(r.calls[1].body.doc, st.doc, "the second frame has the newer doc");
  assert.equal(r.load().length, 2);
});

test("a stage with no edits meanwhile does not repeat", async () => {
  const r = rig({ responses: [reply(200, { url: "/f1" })] });
  const { rr } = renderer(r, { doc: doc() });
  await rr.stage();
  assert.equal(r.calls.length, 1);
});

test("typing on a canvas whose frame is still loading stages the doc", async () => {
  const d = doc();
  const st = S.commit(S.createStore(d), [{ op: "setProp", id: "a1", key: "text", value: "hi" }]);
  const r = rig({ ready: false, responses: [reply(200, { url: "/f" })] });
  const { rr } = renderer(r, st);
  await rr.commit(d, st.doc, st.applied, { inline: true });
  assert.deepEqual(r.calls.map((c) => c.url), ["/stage"]);
  const ok = rig({});
  await renderer(ok, st).rr.commit(d, st.doc, st.applied, { inline: true });
  assert.equal(ok.calls.length, 0, "a ready frame already has the typed text");
});

test("an Update does not say Saved while edits typed during it are unsaved", async () => {
  let release;
  const gate = new Promise((res) => (release = res));
  const t = saverRig("published", [async () => (await gate, reply(200, { ok: true, version: 4 }))]);
  t.edit();
  const p = t.sv.save("save");
  await new Promise((res) => setTimeout(res, 5));
  t.edit();
  release();
  await p;
  assert.equal(t.states.at(-1), "pending");
  assert.equal(t.sv.dirty, true);
});

test("a published Update with nothing newer says Saved", async () => {
  const t = saverRig("published", [reply(200, { ok: true, version: 4 })]);
  t.edit();
  await t.sv.save("save");
  assert.equal(t.states.at(-1), "saved");
});

const { nameNodes } = await import(new URL("../../../backend/web/admin/js/builder/elementsettings.js", import.meta.url));
test("nameNodes puts the labels the navigator shows where the server printed node ids", () => {
  const doc = { nodes: [{ id: "n0b7331", t: "container", p: { tag: "section" }, c: [{ id: "bt7", t: "button", p: { text: "Buy" } }, { id: "x", t: "text", p: { navlabel: "Intro" } }] }] };
  assert.equal(nameNodes("n0b7331: a container with a link cannot hold interactive content (button bt7)", doc), "“Section”: a container with a link cannot hold interactive content (button “Button · Buy”)");
  assert.equal(nameNodes("nothing to name here", doc), "nothing to name here");
  assert.equal(nameNodes("anchor “x” is reserved", doc), "anchor “x” is reserved"); // short words stay
  assert.equal(nameNodes(undefined, doc), "");
});

test("leave() (Load their version): the server copy goes, the page is no longer dirty, and a pending write does not come back", async () => {
  const t = saverRig("published", [reply(200, { ok: true, version: 4 })]);
  t.edit();
  await t.timers.fire(1500);
  assert.deepEqual(t.r.rec.map((c) => c.url), ["/recovery"], "the copy was written");
  t.edit();
  assert.equal(t.sv.dirty, true);
  t.sv.leave();
  assert.equal(t.sv.dirty, false, "no 'Leave site?' prompt");
  assert.equal(t.r.rec.at(-1).url, "/recovery/discard", "no recovery copy to offer back");
  const sent = t.r.rec.length;
  t.sv.flushLocal();
  await t.timers.fire(60000);
  assert.equal(t.r.rec.length, sent, "nothing is written after leaving");
  assert.equal(t.r.calls.length, 0, "and nothing is autosaved");
});
