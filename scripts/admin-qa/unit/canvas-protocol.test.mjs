// Runs the shared protocol fixture (also read by the Go reference validator
// and the bridge scan test) through the real protocol.js, so the three
// validators and the fixture cannot drift apart.
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const protocol = await import(new URL("../../../backend/web/admin/js/canvas/protocol.js", import.meta.url));
const { parseFrameMessage, validToBridge } = protocol;
const fixture = JSON.parse(readFileSync(new URL("../../../backend/internal/admin/testdata/canvas_protocol_cases.json", import.meta.url), "utf8"));

const expand = (v) => {
  if (Array.isArray(v)) return v.map(expand);
  if (v && typeof v === "object") {
    if (Array.isArray(v.$str)) return v.$str[0].repeat(v.$str[1]);
    if (Array.isArray(v.$repeat)) return Array.from({ length: v.$repeat[1] }, () => expand(structuredClone(v.$repeat[0])));
    return Object.fromEntries(Object.entries(v).map(([k, x]) => [k, expand(x)]));
  }
  return v;
};

for (const c of fixture.cases) {
  test(`protocol fixture: ${c.dir} ${c.name}`, () => {
    const msg = expand(c.msg);
    if (c.dir === "out") return assert.equal(validToBridge(msg), c.valid);
    const got = parseFrameMessage(msg);
    if (c.valid) assert.deepEqual(JSON.parse(JSON.stringify(got)), expand(c.expect));
    else assert.equal(got, null);
  });
}

test("protocol: non-finite coordinates are refused (JSON cannot carry them)", () => {
  for (const bad of [NaN, Infinity, -Infinity]) {
    assert.equal(parseFrameMessage({ hm: 2, type: "contextmenu", id: "a", k: "n", x: bad, y: 1 }), null);
    assert.equal(parseFrameMessage({ hm: 2, type: "contextmenu", id: "a", k: "n", x: 1, y: bad }), null);
  }
});

test("protocol: non-finite selection rect values are refused", () => {
  const base = { hm: 2, type: "selection", id: "a", k: "n", prop: "html", marks: [] };
  for (const bad of [NaN, Infinity, -Infinity]) {
    assert.equal(parseFrameMessage({ ...base, rect: { x: bad, y: 0, w: 1, h: 1 } }), null);
    assert.equal(parseFrameMessage({ ...base, rect: { x: 0, y: 0, w: 1, h: bad } }), null);
  }
  assert.notEqual(parseFrameMessage({ ...base, rect: { x: 0, y: 0, w: 1, h: 1 } }), null);
});

test("protocol: toBridge.format builds messages validToBridge accepts", () => {
  const { toBridge } = protocol;
  const ref = { sel: "node", key: "h1" };
  for (const cmd of ["bold", "italic", "underline", "strike", "code", "unlink", "clear", "done"]) {
    assert.equal(validToBridge(toBridge.format({ ...ref, cmd })), true, cmd);
  }
  const link = toBridge.format({ ...ref, cmd: "link", href: "/x", newTab: true });
  assert.deepEqual(link, { hm: 2, type: "format", sel: "node", key: "h1", cmd: "link", href: "/x", newTab: true });
  assert.equal(validToBridge(link), true);
  assert.equal(validToBridge(toBridge.format({ ...ref, cmd: "link" })), false);
  assert.equal(validToBridge(toBridge.format({ ...ref, cmd: "link", href: "x".repeat(2001) })), false);
});

test("protocol: layout pd survives as a copy and an old bridge's omission stays absent", () => {
  const node = { id: "s1", k: "s", t: "hero", p: "", x: 0, y: 0, w: 800, h: 300 };
  const layout = (n) => ({ hm: 2, type: "layout", nodes: [n], main: { x: 0, y: 0, w: 800, h: 300 }, docH: 300 });
  const pd = [96, 0, 96, 12];
  const got = parseFrameMessage(layout({ ...node, pd }));
  assert.deepEqual(got.nodes[0].pd, pd);
  assert.notEqual(got.nodes[0].pd, pd);
  assert.equal("pd" in parseFrameMessage(layout(node)).nodes[0], false);
});
