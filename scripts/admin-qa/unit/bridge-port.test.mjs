// The bridge latches the one MessagePort the host hands over and queues its posts until then. The port block
// (between the hm:port markers) is sliced out of the bridge and run here with a fake window and real ports.
// Run: node --test scripts/admin-qa/unit/bridge-port.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const src = readFileSync(new URL("../../../backend/internal/render/canvas_bridge.js", import.meta.url), "utf8");
const a = src.indexOf("// hm:port:start");
const b = src.indexOf("// hm:port:end");
assert.ok(a > 0 && b > a, "port marker block");
const block = src.slice(a, b);

const tick = () => new Promise((r) => setImmediate(r));

function rig() {
  const hellos = [];
  const parent = { postMessage: (m, origin) => hellos.push({ m, origin }) };
  const listeners = []; // {f, capture}, in registration order
  const win = {
    parent,
    addEventListener: (t, f, o) => t === "message" && listeners.push({ f, capture: o === true || o?.capture === true }),
    removeEventListener: (t, f) => { const i = listeners.findIndex((l) => l.f === f); if (t === "message" && i >= 0) listeners.splice(i, 1); },
  };
  const got = [];
  const onHostMessage = (e) => got.push(e.data);
  const api = new Function("window", "onHostMessage", block + "; return { send: send, onPort: onPort };")(win, onHostMessage);
  // The DOM's order at the target: capture listeners first, then the rest; stopImmediatePropagation ends it.
  const handover = (source, port, data = { hm: 1, type: "hm:port" }) => {
    let stopped = false;
    const e = { source, data, ports: port ? [port] : [], stopImmediatePropagation: () => (stopped = true) };
    for (const l of [...listeners.filter((x) => x.capture), ...listeners.filter((x) => !x.capture)]) {
      if (stopped) break;
      l.f(e);
    }
    return e;
  };
  return { win, parent, listeners, got, api, handover, hellos };
}

test("a handover from another window or with a bad shape is ignored", () => {
  const r = rig();
  const ch = new MessageChannel();
  r.handover({}, ch.port2); // not window.parent
  r.handover(r.parent, ch.port2, { hm: 1, type: "hm:other" });
  r.handover(r.parent, null); // no port
  r.handover(r.parent, ch.port2, { hm: 2, type: "hm:port" });
  r.handover(r.parent, ch.port2, { hm: 1, type: "hm:other" });
  assert.equal(r.got.length, 0);
  ch.port1.close();
  ch.port2.close();
});

test("the first valid port latches and receives host messages", async () => {
  const r = rig();
  const ch = new MessageChannel();
  r.handover(r.parent, ch.port2);
  ch.port1.postMessage({ hm: 1, type: "hm:init" });
  await tick();
  assert.deepEqual(r.got, [{ hm: 1, type: "hm:init" }]);
  ch.port1.close();
});

test("a second port is refused", async () => {
  const r = rig();
  const real = new MessageChannel();
  const fake = new MessageChannel();
  r.handover(r.parent, real.port2);
  r.api.onPort({ source: r.parent, data: { hm: 1, type: "hm:port" }, ports: [fake.port2], stopImmediatePropagation() {} }); // even if a listener were still called
  fake.port1.postMessage({ hm: 1, type: "hm:init", note: "fake" });
  real.port1.postMessage({ hm: 1, type: "hm:init", note: "real" });
  await tick();
  assert.deepEqual(r.got.map((m) => m.note), ["real"]);
  real.port1.close();
  fake.port1.close();
});

test("posts made before the port arrives are queued and flushed in order", async () => {
  const r = rig();
  const ch = new MessageChannel();
  const seen = [];
  ch.port1.onmessage = (e) => seen.push(e.data);
  r.api.send({ n: 1 });
  r.api.send({ n: 2 });
  await tick();
  assert.deepEqual(seen, []);
  r.handover(r.parent, ch.port2);
  r.api.send({ n: 3 });
  await tick();
  assert.deepEqual(seen.map((m) => m.n), [1, 2, 3]);
  ch.port1.close();
});

test("the bridge asks for the port once, with a data-free window message", () => {
  const r = rig();
  assert.deepEqual(r.hellos, [{ m: { hm: 1, type: "hm:ready", contract: "port" }, origin: "*" }]);
  const ch = new MessageChannel();
  r.handover(r.parent, ch.port2);
  r.api.send({ n: 1 });
  assert.equal(r.hellos.length, 1, "nothing else is posted to the parent window");
  ch.port1.close();
});

test("a listener registered after the bridge never sees the port, capture or not", () => {
  const r = rig();
  const seen = [];
  r.win.addEventListener("message", (e) => seen.push(["capture", e.ports.length]), true);
  r.win.addEventListener("message", (e) => seen.push(["bubble", e.ports.length]));
  const ch = new MessageChannel();
  r.handover(r.parent, ch.port2);
  assert.deepEqual(seen, [], "the handover is swallowed in the capture phase");
  const other = new MessageChannel();
  r.handover(r.parent, other.port2); // a repeat is swallowed too
  assert.deepEqual(seen, []);
  r.handover(r.parent, null, { hm: 1, type: "hm:select" }); // other messages still reach them
  assert.equal(seen.length, 2);
  ch.port1.close();
  other.port1.close();
});

test("a fake port from a non-parent source is refused and hidden, and does not latch", async () => {
  const r = rig();
  const seen = [];
  r.win.addEventListener("message", (e) => seen.push(e));
  const fake = new MessageChannel();
  const real = new MessageChannel();
  r.handover({}, fake.port2); // in-frame code posting its own port to window
  assert.deepEqual(seen, []);
  fake.port1.postMessage({ hm: 1, type: "hm:init", note: "fake" });
  r.handover(r.parent, real.port2);
  real.port1.postMessage({ hm: 1, type: "hm:init", note: "real" });
  await tick();
  assert.deepEqual(r.got.map((m) => m.note), ["real"]);
  fake.port1.close();
  real.port1.close();
});

test("a page that patches MessagePort.prototype.postMessage later does not see the bridge's sends", async () => {
  const r = rig();
  const ch = new MessageChannel();
  r.handover(r.parent, ch.port2);
  const real = MessagePort.prototype.postMessage;
  const spied = [];
  MessagePort.prototype.postMessage = function (...a) { spied.push(this); return real.apply(this, a); };
  try {
    const seen = [];
    ch.port1.onmessage = (e) => seen.push(e.data);
    r.api.send({ n: 1 });
    await tick();
    assert.deepEqual(seen, [{ n: 1 }]);
    assert.equal(spied.length, 0);
  } finally {
    MessagePort.prototype.postMessage = real;
    ch.port1.close();
  }
});
