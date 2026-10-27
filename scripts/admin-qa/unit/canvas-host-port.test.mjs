// The canvas host trusts only the MessagePort it handed to a frame: plain window messages are ignored, a frame
// that never got a port cannot speak, a frame-originated save key is dropped, and each load gets a fresh port.
// Run: node --test scripts/admin-qa/unit/canvas-host-port.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import { installDOM } from "./canvas-host-stub.mjs";

const dom = installDOM();
const { createCanvas } = await import(new URL("../../../backend/web/admin/js/canvas/host.js", import.meta.url));

const rig = ({ ready = true } = {}) => {
  const host = document.createElement("div");
  const calls = { select: [], key: [], text: [] };
  const canvas = createCanvas(
    host,
    { onSelect: (...a) => calls.select.push(a), onKey: (...a) => calls.key.push(a), onNodeText: (...a) => calls.text.push(a), onSelectNode: (...a) => calls.select.push(a) },
    { v2: true },
  );
  if (ready) dom.handshake(host, canvas);
  return { host, canvas, calls, frame: dom.frames(host)[1] };
};
const select = { hm: 2, type: "select", sel: "section", key: "n1", id: "n1", k: "s", path: "" };

test("a plain window message is ignored after the handshake", () => {
  const before = dom.listeners.message.length;
  const { host, calls } = rig();
  dom.deliverWindow(host, { hm: 2, type: "key", key: "Delete", shift: false });
  dom.deliverWindow(host, { hm: 1, type: "hm:select", sectionId: "n1", path: "" });
  dom.deliverWindow(host, select);
  assert.equal(calls.key.length, 0);
  assert.equal(calls.select.length, 0);
  assert.equal(dom.listeners.message.length, before + 1, "one window listener, for the port request only");
});

test("a frame that never got a port cannot speak, and a foreign port is never adopted", () => {
  const { host, canvas, calls } = rig({ ready: false });
  canvas.load("/preview", 1); // load requested, no request and no load event yet: no port was handed over
  const f = dom.frames(host)[1];
  assert.equal(f.handovers.length, 0);
  dom.deliver(host, { hm: 2, type: "key", key: "Delete", shift: false });
  assert.equal(calls.key.length, 0, "queued in the frame, never read");
  // A script in the frame connects its own channel and offers it to the host as a window message.
  const foreign = new MessageChannel();
  dom.deliverWindow(host, { hm: 1, type: "hm:port" }, 1, [foreign.port1]);
  dom.deliverWindow(host, { hm: 2, type: "key", key: "Delete", shift: false }, 1, [foreign.port1]);
  assert.equal(foreign.port1.onmessage, null, "the host never attached to the foreign port");
  foreign.port2.postMessage({ hm: 2, type: "key", key: "Delete", shift: false });
  assert.equal(calls.key.length, 0);
  assert.equal(f.handovers.length, 0, "and no port was handed back for it");
});

test("the window request hands the port over before load, once per load", () => {
  const { host, canvas, calls } = rig({ ready: false });
  canvas.load("/preview", 1);
  const f = dom.frames(host)[1];
  dom.request(host);
  assert.equal(f.handovers.length, 1, "port posted before the load event");
  assert.deepEqual(f.handovers[0], { hm: 1, type: "hm:port" });
  dom.fireLoad(host); // the load event does not hand over a second port
  assert.equal(f.handovers.length, 1);
  dom.deliver(host, { hm: 2, type: "hm:ready", contract: "1" });
  dom.request(host); // once the bridge is ready, a request is ignored
  assert.equal(f.handovers.length, 1);
  dom.deliver(host, { hm: 2, type: "ready", contract: "2" });
  dom.deliver(host, { hm: 2, type: "key", key: "Delete", shift: false });
  assert.equal(calls.key.length, 1);
});

test("a request from a frame we are not loading, or in another shape, is ignored", () => {
  const { host, canvas } = rig({ ready: false });
  const [f0, f1] = dom.frames(host);
  dom.request(host, 1); // nothing expected yet
  assert.equal(f1.handovers.length, 0);
  canvas.load("/preview", 1); // expects frame 1 only
  dom.request(host, 0);
  assert.equal(f0.handovers.length, 0, "frame 0 is not expecting a load");
  for (const data of [{ hm: 1, type: "hm:ready", contract: "1" }, { hm: 2, type: "ready", contract: "port" }, { hm: 1, type: "hm:select", sectionId: "n1" }, null]) {
    dom.deliverWindow(host, data, 1);
  }
  const wrongOrigin = dom.listeners.message.at(-1);
  wrongOrigin({ origin: "https://evil.example", source: f1.contentWindow, data: { hm: 1, type: "hm:ready", contract: "port" } });
  wrongOrigin({ origin: "null", source: {}, data: { hm: 1, type: "hm:ready", contract: "port" } });
  wrongOrigin({ origin: "null", source: null, data: { hm: 1, type: "hm:ready", contract: "port" } });
  assert.equal(f1.handovers.length, 0);
  dom.request(host, 1);
  assert.equal(f1.handovers.length, 1);
});

test("without a request the port is handed over at load", () => {
  const { host, canvas } = rig({ ready: false });
  canvas.load("/preview", 1);
  dom.fireLoad(host);
  assert.equal(dom.frames(host)[1].handovers.length, 1);
});

test("layout sent before load is applied before the swap, and ready first fixes editable", () => {
  const host = document.createElement("div");
  const events = [];
  const canvas = createCanvas(host, { onSelect() {}, onLayout: () => events.push("layout"), onSwap: () => events.push("swap"), onContract: (ok) => events.push(`contract:${ok}`) }, {});
  canvas.load("/preview", 1);
  dom.request(host);
  dom.deliver(host, { hm: 1, type: "hm:ready", contract: "9" }); // a mismatching contract
  dom.deliver(host, { hm: 1, type: "hm:layout", docHeight: 300, sections: [{ id: "n1", type: "hero", top: 0, height: 300 }], header: { top: 0, height: 0 }, footer: { top: 300, height: 0 } });
  assert.deepEqual(events, ["contract:false", "layout"], "both reach the host before the load event");
  assert.equal(canvas.layout.sections.length, 1);
  dom.fireLoad(host);
  assert.deepEqual(events, ["contract:false", "layout", "swap"]);
  const init = dom.frames(host)[1].posted.filter((m) => m.type === "hm:init");
  assert.deepEqual(init.map((m) => m.editable), [false, false], "init never carries a stale editable");
});

test("no hm:ready over the port within the grace period reports a contract mismatch", () => {
  const host = document.createElement("div");
  const contracts = [];
  const canvas = createCanvas(host, { onSelect() {}, onContract: (ok) => contracts.push(ok) }, {});
  canvas.load("/preview", 1); // an older bridge never asks and never sends hm:ready over a port
  dom.fireLoad(host);
  assert.deepEqual(contracts, []);
  dom.runTimers();
  assert.deepEqual(contracts, [false]);
});

test("a bridge that answers in time is not reported", () => {
  const host = document.createElement("div");
  const contracts = [];
  const canvas = createCanvas(host, { onSelect() {}, onContract: (ok) => contracts.push(ok) }, {});
  canvas.load("/preview", 1);
  dom.request(host);
  dom.fireLoad(host);
  dom.deliver(host, { hm: 1, type: "hm:ready", contract: "1" });
  dom.runTimers();
  assert.deepEqual(contracts, [true]);
});

test("a frame that navigated itself loses its port", () => {
  const { host, calls, frame } = rig();
  for (const fn of frame.listeners.load ?? []) fn(); // a second load we never asked for
  assert.equal(frame.port.peer.closed, true, "the host closed its end");
  dom.deliver(host, { hm: 2, type: "key", key: "Delete", shift: false });
  assert.equal(calls.key.length, 0);
});

test("destroy closes the ports", () => {
  const { host, canvas, calls, frame } = rig();
  canvas.destroy();
  dom.deliver(host, { hm: 2, type: "key", key: "Delete", shift: false });
  assert.equal(calls.key.length, 0);
  assert.equal(frame.port.peer.closed, true);
});

test("key s from the frame never reaches onKey, Delete does", () => {
  const { host, calls } = rig();
  dom.deliver(host, { hm: 2, type: "key", key: "s", shift: false });
  dom.deliver(host, { hm: 2, type: "key", key: "S", shift: true });
  assert.equal(calls.key.length, 0);
  dom.deliver(host, { hm: 2, type: "key", key: "Delete", shift: false });
  assert.deepEqual(calls.key, [["Delete", false, { editing: false }]]);
});

test("hm:port is handed over once per load, before hm:init, and the old port is closed on reload", () => {
  const { host, canvas, frame } = rig();
  assert.equal(frame.handovers.length, 1);
  assert.deepEqual(frame.handovers[0], { hm: 1, type: "hm:port" });
  assert.ok(frame.posted.some((m) => m.type === "hm:init"), "hm:init travels over the port, after the handover");
  assert.ok(frame.posted.every((m) => m.type !== "hm:port"), "the handover is not a port message");
  const first = frame.port;
  canvas.load("/preview2", 2); // loads into the other buffer
  const next = dom.frames(host)[0];
  assert.equal(next.handovers.length, 0, "no port before the bridge asks or the load event");
  for (const fn of next.listeners.load ?? []) fn();
  assert.equal(next.handovers.length, 1);
  assert.notEqual(next.port, first);
  canvas.load("/preview3", 3); // back to the first buffer: its old port must be closed
  assert.equal(first.peer.closed, true);
});

test("a restage during an in-flight request: the new document's request replaces the outgoing document's port", () => {
  const { host, canvas, calls } = rig({ ready: false });
  canvas.load("/preview", 1);
  canvas.load("/preview", 2); // restage lands before the host reads doc1's request
  const f = dom.frames(host)[1];
  dom.request(host); // doc1's request: the port goes to the outgoing document
  const first = f.port;
  dom.request(host); // doc2's request, no hm:ready seen yet
  assert.equal(f.handovers.length, 2);
  assert.notEqual(f.port, first);
  assert.equal(first.peer.closed, true, "the first port was closed");
  dom.fireLoad(host);
  dom.deliver(host, { hm: 2, type: "ready", contract: "2" });
  dom.deliver(host, { hm: 2, type: "key", key: "Delete", shift: false });
  assert.equal(calls.key.length, 1, "a message on the second port is read");
});
