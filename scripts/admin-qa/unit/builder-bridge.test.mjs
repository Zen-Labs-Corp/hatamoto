// planCommit: which bridge messages a commit needs. The @hm specifiers resolve to the
// real modules (both are pure), so toBridge shapes are checked with validToBridge.
// Run: node --test scripts/admin-qa/unit/builder-bridge.test.mjs
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
const { planCommit, innerRef, finish, reanchor, loopAbove } = await import(new URL("../../../backend/web/admin/js/builder/bridge.js", import.meta.url));
const { validToBridge, toBridge } = await import(new URL("../../../backend/web/admin/js/canvas/protocol.js", import.meta.url));

const leaf = (id, t = "text", p = { text: id }) => ({ id, t, p });
const box = (id, c = []) => ({ id, t: "container", ...(c.length ? { c } : {}) });
const sec = (id, cid) => ({ id, t: "container", p: { tag: "section" }, c: [{ id: cid, t: "component", p: { type: "hero" } }] });
const base = () => ({
  v: 2,
  nodes: [sec("s1", "c1"), box("a", [leaf("a1"), leaf("a2", "heading", { text: "H", level: 2 })]), box("b", [box("b1", [leaf("b11")])]), box("e")],
});

const plan = (doc, ops, opts) => {
  const after = S.commit(S.createStore(doc), ops).doc;
  return planCommit(doc, after, ops, { decls: (n, bp, key, v) => [[key, v]], ...opts });
};
const shape = (p) => p.messages.map((e) => [e.when, e.msg.type, e.frag]);
const sec1 = (key) => ({ sel: "section", key });
const node = (key) => ({ sel: "node", key });

test("every planned message is valid once finished", () => {
  const d = base();
  const ops = [
    { op: "insert", parent: "e", index: 0, node: leaf("x") },
    { op: "setStyle", id: "a", bp: "base", key: "gap", value: "s" },
    { op: "move", id: "a1", parent: "b1", index: 0 },
  ];
  for (const op of ops) {
    const p = plan(d, [op]);
    assert.equal(p.reload, false);
    for (const e of p.messages) {
      const m = finish(e, { frags: [{ html: "<p>x</p>", css: { x: "r" } }], css: { a: "rule" } });
      assert.ok(validToBridge(m), JSON.stringify(m));
    }
  }
});

test("the first native node on a page drawn from v1 sections needs a full render (the frame has no base sheet)", () => {
  const v1 = { v: 2, nodes: [sec("s1", "c1"), sec("s2", "c2")] };
  assert.equal(plan(v1, [{ op: "insert", parent: "", index: 1, node: box("n1") }]).reload, true);
  assert.equal(plan(v1, [{ op: "insert", parent: "", index: 1, node: sec("s3", "c3") }]).reload, false, "another v1 section patches in");
  assert.equal(plan(base(), [{ op: "insert", parent: "", index: 1, node: box("n2") }]).reload, false, "a page that already has native nodes keeps patching");
});

test("insert top-level container: fragment inserted next to the last top node (a plain node: only v1-shaped tops are sections)", () => {
  const p = plan(base(), [{ op: "insert", parent: "", index: 4, node: box("t") }]);
  assert.deepEqual(shape(p), [["fragment", "insert", 0]]);
  assert.deepEqual(p.render, ["t"]);
  assert.deepEqual(p.messages[0].msg.after, node("e"));
});

test("insert before a v1-shaped top node uses its component as the anchor", () => {
  const p = plan(base(), [{ op: "insert", parent: "", index: 0, node: box("t") }]);
  assert.deepEqual(p.messages[0].msg.before, sec1("c1"));
});

test("insert nested: before a sibling, after the last, or into an empty parent", () => {
  let p = plan(base(), [{ op: "insert", parent: "a", index: 1, node: leaf("x") }]);
  assert.deepEqual(p.messages[0].msg.before, node("a2"));
  p = plan(base(), [{ op: "insert", parent: "a", index: 2, node: leaf("x") }]);
  assert.deepEqual(p.messages[0].msg.after, node("a2"));
  p = plan(base(), [{ op: "insert", parent: "e", index: 0, node: leaf("x") }]);
  assert.deepEqual(p.messages[0].msg.parent, node("e"));
  assert.deepEqual(p.render, ["x"]);
});

test("move across parents is a bridge move without a fragment", () => {
  const p = plan(base(), [{ op: "move", id: "a1", parent: "b1", index: 0 }]);
  assert.deepEqual(shape(p), [["now", "move", undefined]]);
  assert.deepEqual(p.render, []);
  assert.deepEqual(p.messages[0].msg, { hm: 2, type: "move", ...node("a1"), before: node("b11") });
});

test("a nested move of a parent-hover node asks for its css again", () => {
  const d = base();
  d.nodes[1].c[0].s = { "base:parent-hover": { op: "0.5" } };
  let p = plan(d, [{ op: "move", id: "a1", parent: "b1", index: 0 }]);
  assert.deepEqual(shape(p), [["now", "move", undefined], ["confirm", "css", undefined]]);
  assert.deepEqual(p.css, ["a1"]);
  assert.deepEqual(p.messages[1].msg.css, { a1: "" });
  p = plan(d, [{ op: "move", id: "a2", parent: "b1", index: 0 }]);
  assert.deepEqual(shape(p), [["now", "move", undefined]]);
});

test("innerRef names the element that carries the styles", () => {
  assert.deepEqual(innerRef(base(), "c1"), sec1("c1"));
  assert.deepEqual(innerRef(base(), "s1"), node("s1"));
  assert.deepEqual(innerRef(base(), "a1"), node("a1"));
});

test("move between top level and nested re-renders the node", () => {
  const p = plan(base(), [{ op: "move", id: "b1", parent: "", index: 0 }]);
  assert.deepEqual(shape(p), [["now", "remove", undefined], ["fragment", "insert", 0]]);
  assert.deepEqual(p.messages[0].msg.key, "b1");
  assert.deepEqual(p.render, ["b1"]);
});

test("remove sends the outer ref of the removed node", () => {
  let p = plan(base(), [{ op: "remove", id: "a1" }]);
  assert.deepEqual(p.messages.map((e) => e.msg), [{ hm: 2, type: "remove", ...node("a1") }]);
  p = plan(base(), [{ op: "remove", id: "a" }]);
  assert.deepEqual(p.messages[0].msg, { hm: 2, type: "remove", ...node("a") });
});

test("setStyle: setVar at once, css after confirm, no render", () => {
  const p = plan(base(), [{ op: "setStyle", id: "a1", bp: "base", key: "gap", value: "s" }]);
  assert.deepEqual(shape(p), [["now", "setVar", undefined], ["confirm", "css", undefined]]);
  assert.deepEqual(p.css, ["a1"]);
  assert.deepEqual(p.render, []);
  assert.deepEqual(p.messages[0].msg.decls, [["gap", "s"]]);
  assert.deepEqual(finish(p.messages[1], { css: { a1: ".r{}" } }).css, { a1: ".r{}" });
  assert.deepEqual(finish(p.messages[1], {}).css, { a1: "" });
});

test("setStyle off the device's breakpoint: no preview, only the css confirm, no reload", () => {
  const p = plan(base(), [{ op: "setStyle", id: "a1", bp: "md", key: "gap", value: "s" }], { decls: () => [] });
  assert.equal(p.reload, false);
  assert.deepEqual(shape(p), [["confirm", "css", undefined]]);
  assert.deepEqual(p.css, ["a1"]);
  assert.deepEqual(p.render, []);
});

test("setStyle without a preview table is unmappable", () => {
  const d = base();
  const op = { op: "setStyle", id: "a1", bp: "base", key: "gap", value: "s" };
  assert.equal(planCommit(d, S.commit(S.createStore(d), [op]).doc, [op], { decls: () => null }).reload, true);
});

test("setProp on heading level patches the node after its fragment", () => {
  const p = plan(base(), [{ op: "setProp", id: "a2", key: "level", value: 3 }]);
  assert.deepEqual(shape(p), [["fragment", "patch", 0]]);
  assert.deepEqual(p.render, ["a2"]);
  const m = finish(p.messages[0], { frags: [{ html: "<h3>H</h3>", css: { a2: "" } }] });
  assert.deepEqual(m, { hm: 2, type: "patch", ...node("a2"), html: "<h3>H</h3>", css: { a2: "" } });
});

test("element settings repaint the node; navlabel (never rendered) sends nothing", () => {
  for (const [id, key, value] of [["a", "anchor", "pricing"], ["a", "aria", "Offers"], ["a", "tip", "More"], ["a", "href", "/x"], ["a", "target", "_blank"], ["a", "rel", "nofollow"], ["a1", "caption", "C"], ["a1", "lightbox", true]]) {
    const p = plan(base(), [{ op: "setProp", id, key, value }]);
    assert.deepEqual(shape(p).map((m) => m[1]), ["patch"], `${key} repaints`);
    assert.equal(p.reload, false, `${key} needs no reload`);
  }
  assert.equal(plan(base(), [{ op: "setProp", id: "a", key: "navlabel", value: "N" }]).messages.length, 0);
});

test("text prop: inline typing needs no message, an inspector edit patches", () => {
  const op = { op: "setProp", id: "a2", key: "text", value: "New" };
  const inline = plan(base(), [op], { inline: true });
  assert.deepEqual([inline.messages, inline.render, inline.reload], [[], [], false]);
  assert.deepEqual(shape(plan(base(), [op])), [["fragment", "patch", 0]]);
});

test("html prop: inline typing needs no message on a heading/text, a non-inline write patches", () => {
  const ops = [{ op: "setProp", id: "a2", key: "text", value: "N" }, { op: "setProp", id: "a2", key: "html", value: "<em>N</em>" }];
  const inline = plan(base(), ops, { inline: true });
  assert.deepEqual([inline.messages, inline.render, inline.reload], [[], [], false]);
  assert.ok(plan(base(), ops).render.includes("a2"), "the re-render after the edit patches the node");
});

test("bind and unbind patch the node from a server fragment (no reload), even for inline text", () => {
  const bind = [{ op: "setBind", id: "a2", key: "text", value: "entity.name" }, { op: "setProp", id: "a2", key: "text", value: null }];
  const p = plan(base(), bind);
  assert.equal(p.reload, false);
  assert.deepEqual(p.render, ["a2"]);
  assert.equal(shape(p)[0][1], "patch");
  assert.equal(plan(base(), [{ op: "setBind", id: "a2", key: "text", value: "entity.name" }], { inline: true }).render.length, 1);
});

test("component content patches the section", () => {
  const p = plan(base(), [{ op: "setProp", id: "c1", key: "title", value: "Hi" }]);
  assert.deepEqual(p.messages[0].msg.key, "c1");
  assert.deepEqual(p.messages[0].msg.sel, "section");
  assert.deepEqual(p.render, ["c1"]);
});

test("v1Shaped flip patches the whole top-level container", () => {
  const p = plan(base(), [{ op: "insert", parent: "s1", index: 1, node: leaf("x") }]);
  assert.deepEqual(shape(p), [["fragment", "patch", 0]]);
  assert.deepEqual(p.render, ["s1"]);
  assert.deepEqual(p.messages[0].msg.key, "c1", "patched where the v1 component was");
  const q = plan(base(), [{ op: "setProp", id: "s1", key: "tag", value: "div" }]);
  assert.deepEqual(shape(q), [["fragment", "patch", 0]]);
  assert.deepEqual(q.render, ["s1"]);
  const r = plan(base(), [{ op: "setStyle", id: "s1", bp: "base", key: "gap", value: "s" }]);
  assert.deepEqual(r.render, ["s1"]);
  assert.deepEqual(r.css, [], "the whole patch carries its css");
});

test("a commit inserting then filling a new node renders it once", () => {
  const d = base();
  const dup = S.duplicateOps(d, "a");
  const p = plan(d, dup);
  assert.deepEqual(shape(p), [["fragment", "insert", 0]]);
  assert.deepEqual(p.messages[0].msg.before, node("b"));
  const id = dup[0].node.id;
  const q = plan(d, [...dup, { op: "setProp", id, key: "gap", value: "x" }]);
  assert.deepEqual(shape(q), [["fragment", "insert", 0]]);
});

test("moving something into a node inserted by the same commit reloads", () => {
  const d = base();
  assert.equal(plan(d, S.wrapOps(d, "a1")).reload, true);
});

test("unmappable input reloads: unknown op, bad replay, mismatched after", () => {
  const d = base();
  assert.equal(planCommit(d, d, [{ op: "frob" }]).reload, true);
  assert.equal(planCommit(d, d, [{ op: "remove", id: "nope" }]).reload, true);
  assert.equal(planCommit(d, d, [{ op: "remove", id: "a1" }]).reload, true);
  assert.deepEqual(planCommit(d, d, []), { messages: [], render: [], css: [], reload: false });
});

test("reanchor: a top-level neighbour the server rendered nothing for is replaced by the nearest present one", () => {
  const d = base(); // s1 (v1, key c1), a, b, e
  const ins = (place) => ({ hm: 2, type: "insert", html: "<p></p>", ...place });
  const order = ["c1", "b", "e"]; // a renders nothing
  assert.deepEqual(reanchor(d, order, ins({ after: node("a") })), ins({ after: sec1("c1") }));
  assert.deepEqual(reanchor(d, order, ins({ before: node("a") })), ins({ before: node("b") }));
  assert.deepEqual(reanchor(d, ["a"], ins({ after: node("b") }), new Set()), ins({ after: node("a") }));
  assert.deepEqual(reanchor(d, [], ins({ after: node("a") })), ins({ parent: { sel: "main", key: "" } }));
  assert.deepEqual(reanchor(d, order, ins({ after: node("a") }), new Set(["c1"])), ins({ before: node("b") }), "keys not in the frame yet are skipped");
  const kept = ins({ after: node("b") });
  assert.equal(reanchor(d, order, kept), kept, "a present neighbour is untouched");
  const nested = ins({ after: node("a1") });
  assert.equal(reanchor(d, order, nested), nested, "nested placements are untouched");
});

test("an edit under a loop re-renders the loop, not the lone node", () => {
  const d = { v: 2, nodes: [{ id: "lp", t: "loop", p: { from: "archive" }, c: [box("it", [leaf("t1", "heading", { text: "H" })])] }, box("o", [leaf("o1")])] };
  S.setChildKinds([{ kind: "container", children: true }, { kind: "loop", children: true }]);
  const patchLoop = [["fragment", "patch", 0]];
  for (const ops of [
    [{ op: "setProp", id: "t1", key: "text", value: "Z" }],
    [{ op: "setBind", id: "t1", key: "text", value: "loop.item.title" }],
    [{ op: "remove", id: "t1" }],
    [{ op: "insert", parent: "it", index: 1, node: leaf("n1") }],
    [{ op: "insert", parent: "lp", index: 1, node: box("n2") }],
  ]) {
    const p = plan(d, ops);
    assert.deepEqual(p.render, ["lp"], JSON.stringify(ops));
    assert.deepEqual(shape(p), patchLoop, JSON.stringify(ops));
    assert.deepEqual(p.messages[0].msg.key ?? p.messages[0].msg.target?.key ?? "lp", "lp");
  }
  // inline typing under a loop sends nothing (item 1's DOM has the text; a patch would destroy the active editor)
  const typed = plan(d, [{ op: "setProp", id: "t1", key: "text", value: "Z" }], { inline: true });
  assert.deepEqual(typed.render, []);
  assert.deepEqual(typed.messages, []);
  assert.equal(typed.reload, false);
  // the deferred re-render at edit end: the host replays the final value as a plain (non-inline) commit
  const done = { ...d, nodes: [{ ...d.nodes[0], c: [box("it", [leaf("t1", "heading", { text: "Z" })])] }, d.nodes[1]] };
  const flush = planCommit(done, done, [{ op: "setProp", id: "t1", key: "text", value: "Z" }], {});
  assert.deepEqual(flush.render, ["lp"]);
  assert.deepEqual(shape(flush), patchLoop);
  assert.equal(loopAbove(done, "t1"), "lp");
  assert.equal(loopAbove(done, "o1"), null);
  // a node outside the loop is unchanged
  assert.deepEqual(plan(d, [{ op: "setProp", id: "o1", key: "text", value: "Z" }]).render, ["o1"]);
  // into / out of a loop
  assert.deepEqual(shape(plan(d, [{ op: "move", id: "o1", parent: "it", index: 1 }])), [["now", "remove", undefined], ["fragment", "patch", 0]]);
  assert.deepEqual(shape(plan(d, [{ op: "move", id: "t1", parent: "o", index: 0 }])), [["fragment", "patch", 0], ["fragment", "insert", 1]]);
  S.setChildKinds([{ kind: "container", children: true }]);
});

test("toBridge.force builds a valid message; bad states and a missing state are refused", () => {
  for (const state of [null, undefined, "hover", "focus", "active", "parent-hover"]) {
    const m = toBridge.force({ sel: "node", key: "a", state });
    assert.equal(m.state, state ?? null);
    assert.ok(validToBridge(m), JSON.stringify(m));
  }
  for (const state of ["visited", "HOVER", "", 1]) assert.ok(!validToBridge(toBridge.force({ sel: "node", key: "a", state })));
  assert.ok(!validToBridge({ hm: 2, type: "force", sel: "node", key: "a" }));
  assert.ok(!validToBridge(toBridge.force({ sel: "main", key: "a", state: "hover" })));
});
