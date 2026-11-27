// pluginframe.js: a frame that loads a second time is blanked, never reloaded
// from its (spent, one-time) ticket URL. Run: node --test scripts/admin-qa/unit/pluginframe.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const src = readFileSync(new URL("../../../backend/web/admin/js/pluginframe.js", import.meta.url), "utf8");

test("a second load blanks the frame and shows the notice", () => {
  const code = src.replace(/\/\/.*$/gm, "");
  assert.equal((code.match(/frame\.src\s*=/g) || []).length, 2, "the first load and the blanking, nothing else");
  assert.match(code, /dead = true;\s*frame\.src = "about:blank";\s*if \(notice\) notice\.hidden = false;/);
  assert.doesNotMatch(code, /frame\.src\s*=\s*src\s*;[\s\S]*frame\.src\s*=\s*src\s*;/, "the ticket URL is set once");
});

test("the height op is local: after the nonce check, before forward, no frame.src", () => {
  const code = src.replace(/\/\/.*$/gm, "");
  const nonce = code.indexOf("m.nonce !== nonce");
  const height = code.indexOf('m.op === "height"');
  const forward = code.indexOf("await forward(m)");
  assert.ok(nonce > 0 && nonce < height && height < forward, "source/nonce checks come first, forward last");
  assert.equal((code.match(/frame\.src\s*=/g) || []).length, 2, "the height op adds no navigation");
});

// Runs pluginframe.js against a fake page. The frame's content box is `style.height`
// minus a 2px border, as in a border-box iframe; reports come through window "message".
function mountFrame() {
  const frame = {
    style: {},
    contentWindow: {},
    listeners: {},
    addEventListener(t, f) { this.listeners[t] = f; },
    get offsetHeight() { return parseInt(this.style.height || "400", 10); },
    get clientHeight() { return this.offsetHeight - 2; },
    cloneNode() { return this; },
    postMessage() {},
  };
  frame.contentWindow.postMessage = () => {};
  const holder = {
    dataset: { fit: "1", src: "/x", plugin: "p", bridge: "/b", functions: "" },
    querySelector(sel) {
      if (sel === "template") return { content: { querySelector: () => frame }, replaceWith() {} };
      return null;
    },
  };
  const frames = [];
  const win = { handlers: {}, addEventListener(t, f) { this.handlers[t] = f; } };
  const doc = {
    querySelector: (s) => (s === "[data-hm-pluginframe]" ? holder : null),
    addEventListener() {},
    getElementById: () => null,
  };
  const clock = { t: 1000 };
  const g = {
    performance: { now: () => clock.t },
    window: win, document: doc, crypto: { getRandomValues: (a) => a },
    requestAnimationFrame: (f) => frames.push(f),
  };
  new Function(...Object.keys(g), src)(...Object.values(g));
  frame.listeners.load?.();
  const nonce = "00".repeat(16);
  const report = (height) => win.handlers.message({ source: frame.contentWindow, data: { hm: 1, op: "height", nonce, height } });
  const flush = () => { const q = frames.splice(0); q.forEach((f) => f()); return q.length; };
  return { frame, report, flush, frames, clock };
}

test("a page whose height follows the frame stops growing", () => {
  const { frame, report, flush, clock } = mountFrame();
  report(1000);
  flush();
  for (let i = 0; i < 40; i++) {
    clock.t += 10; // the echo comes right after our resize
    report(frame.clientHeight + 16); // body { min-height: 100vh } + 8px margins
    flush();
  }
  assert.ok(frame.clientHeight < 1000 + 16 * 8, `frame ran away to ${frame.clientHeight}px`);
});

test("a page sized in proportion to the frame stops growing", () => {
  const { frame, report, flush, clock } = mountFrame();
  report(1000);
  flush();
  for (let i = 0; i < 30; i++) {
    clock.t += 10;
    report(Math.round(frame.clientHeight * 1.1)); // height: 110vh
    flush();
  }
  assert.ok(frame.clientHeight < 2000, `frame ran away to ${frame.clientHeight}px`);
});

test("growth in equal steps with time between them keeps growing", () => {
  const { frame, report, flush, clock } = mountFrame();
  report(1000);
  flush();
  for (let i = 1; i <= 12; i++) {
    clock.t += 1000; // a click on "Load more", 800px each time
    report(1000 + 800 * i);
    flush();
  }
  assert.equal(frame.clientHeight, 1000 + 800 * 12);
});

test("a user-driven report after a freeze resumes growth", () => {
  const { frame, report, flush, clock } = mountFrame();
  report(1000);
  flush();
  for (let i = 0; i < 10; i++) {
    clock.t += 10;
    report(frame.clientHeight + 16);
    flush();
  }
  const frozenAt = frame.clientHeight;
  clock.t += 1000;
  report(frozenAt + 800);
  flush();
  assert.equal(frame.clientHeight, frozenAt + 800);
});

test("real growth of changing steps is followed", () => {
  const { frame, report, flush, clock } = mountFrame();
  for (const h of [500, 650, 900, 1100, 1500, 1600]) {
    clock.t += 1000;
    report(h);
    flush();
  }
  assert.equal(frame.clientHeight, 1600);
});

test("unchanged heights are dropped and reports in one frame apply once", () => {
  const { frame, report, flush, frames } = mountFrame();
  report(700);
  report(800);
  report(900);
  assert.equal(frames.length, 1, "one apply per animation frame");
  flush();
  assert.equal(frame.clientHeight, 900);
  report(900);
  assert.equal(frames.length, 0, "an unchanged height schedules nothing");
});
