// A just-enough DOM to run createCanvas (canvas/host.js) in node: elements record attributes, children and
// listeners; frames have a contentWindow whose postMessage calls are collected. Shared by the canvas-host-*.test.mjs files.
// The host talks to a frame over a MessageChannel it hands over when the bridge asks (or on load); the stub replaces
// MessageChannel with a synchronous pair (structured-cloned messages) so the tests stay synchronous. Like the real
// bridge, a frame queues what it sends before it holds a port and flushes it, in order, once the port arrives.
// Real ports are used in bridge-port.test.mjs. The host's long timers (the no-hm:ready watchdog) are held, not run:
// call runTimers() to fire them.
import { register } from "node:module";

const root = new URL("../../../backend/web/admin/js/", import.meta.url).href;
register(
  "data:text/javascript," +
    encodeURIComponent(`export async function resolve(s, c, next) {
      if (s.startsWith("@hm/")) return { url: ${JSON.stringify(root)} + s.slice(4) + ".js", shortCircuit: true };
      return next(s, c);
    }`),
);

class El {
  constructor(tag) {
    this.tagName = tag.toUpperCase();
    this.attrs = {};
    this.children = [];
    this.listeners = {};
    this.style = {};
    this.dataset = {};
    this.className = "";
    this.classList = { toggle() {}, add() {}, remove() {} };
  }
  set src(v) { this.attrs.src = v; }
  get src() { return this.attrs.src; }
  setAttribute(k, v) { this.attrs[k] = String(v); }
  getAttribute(k) { return k in this.attrs ? this.attrs[k] : null; }
  hasAttribute(k) { return k in this.attrs; }
  removeAttribute(k) { delete this.attrs[k]; }
  toggleAttribute(k, on) { if (on) this.attrs[k] = ""; else delete this.attrs[k]; }
  get firstChild() { return this.children[0] ?? null; }
  remove() {}
  append(...kids) { this.children.push(...kids); }
  replaceChildren(...kids) { this.children = kids; }
  addEventListener(t, f) { (this.listeners[t] ??= []).push(f); }
  removeEventListener() {}
  remove() {}
  contains(x) { return x === this || this.children.some((c) => c.contains?.(x)); }
  closest() { return null; }
  querySelector() { return null; }
  querySelectorAll() { return []; }
  getBoundingClientRect() { return { left: 0, top: 0, width: 0, height: 0 }; }
  set textContent(v) { this.text = v; }
  get textContent() { return this.text ?? ""; }
}

class SyncPort {
  constructor() { this.onmessage = null; this.closed = false; this.peer = null; }
  postMessage(m) { const p = this.peer; if (!this.closed && p && !p.closed) p.onmessage?.({ data: structuredClone(m) }); }
  close() { this.closed = true; }
}
class SyncChannel {
  constructor() { this.port1 = new SyncPort(); this.port2 = new SyncPort(); this.port1.peer = this.port2; this.port2.peer = this.port1; }
}

/** Installs the globals; returns {win, frames(stageRoot)} helpers. */
export function installDOM() {
  const listeners = { message: [] };
  const timers = [];
  globalThis.setTimeout = ((real) => (fn, ms, ...a) => (ms >= 1000 ? void timers.push(fn) : real(fn, ms, ...a)))(globalThis.setTimeout);
  globalThis.MessageChannel = SyncChannel;
  globalThis.window = {
    addEventListener: (t, f) => (listeners[t] ??= []).push(f),
    removeEventListener: (t, f) => {
      const i = (listeners[t] ??= []).indexOf(f);
      if (i >= 0) listeners[t].splice(i, 1);
    },
    requestIdleCallback: (f) => setTimeout(f, 0),
  };
  globalThis.document = {
    createElement: (tag) => {
      const e = new El(tag);
      if (tag === "iframe") {
        e.posted = [];
        e.handovers = []; // {hm:1,type:"hm:port"} messages with the port the frame was given
        e.port = null; // the frame's end of the current channel
        e.pending = []; // what the bridge sent before it held a port
        e.contentWindow = {
          postMessage: (m, _origin, transfer) => {
            if (m?.type === "hm:port") {
              e.handovers.push(m);
              e.port = transfer[0];
              e.port.onmessage = (ev) => e.posted.push(ev.data); // host to frame traffic
              for (const q of e.pending.splice(0)) e.port.postMessage(q); // the bridge flushes its queue in order
            } else e.posted.push(m); // a plain window message
          },
        };
      }
      return e;
    },
    addEventListener() {},
    removeEventListener() {},
    activeElement: null,
    body: new El("body"),
  };
  globalThis.matchMedia = () => ({ matches: false, addEventListener() {} });
  const frames = (host) => host.children[0].children.filter((c) => c.tagName === "IFRAME");
  /** Delivers a message as the frame's bridge would: over its port, or queued until it gets one. */
  const deliver = (host, data, idx = 1) => {
    const f = frames(host)[idx];
    if (f.port) f.port.postMessage(data);
    else f.pending.push(data);
  };
  /** Delivers a plain window message as in-frame page code could (origin "null", source = that frame's window). */
  const deliverWindow = (host, data, idx = 1, ports = []) => {
    const f = frames(host)[idx];
    for (const fn of (listeners.message ?? []).slice()) fn({ origin: "null", source: f.contentWindow, data, ports });
  };
  /** The bridge's port request (what it posts first thing in <head>). */
  const request = (host, idx = 1) => deliverWindow(host, { hm: 1, type: "hm:ready", contract: "port" }, idx);
  const fireLoad = (host, idx = 1) => {
    for (const fn of frames(host)[idx].listeners.load ?? []) fn();
  };
  /** Loads the first frame: the bridge asks for its port, sends ready (v1 and v2), then the load event fires. */
  const handshake = (host, canvas) => {
    canvas.load("/preview", 1); // load() fills the hidden buffer, which becomes the active frame
    request(host);
    deliver(host, { hm: 1, type: "hm:ready", contract: "1" });
    deliver(host, { hm: 2, type: "ready", contract: "2" });
    fireLoad(host);
  };
  const runTimers = () => timers.splice(0).forEach((fn) => fn());
  return { deliver, deliverWindow, request, fireLoad, runTimers, frames, handshake, listeners };
}
