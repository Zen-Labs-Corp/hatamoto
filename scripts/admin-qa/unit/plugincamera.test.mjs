// plugincamera.js sizing and error codes, and the camera gate in pluginframe.js.
// Run: node --test scripts/admin-qa/unit/plugincamera.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const { fit, errorCode, capture, MAX_SIDE, CLICK_GUARD_MS } = await import(new URL("../../../backend/web/admin/js/plugincamera.js", import.meta.url));
const frame = readFileSync(new URL("../../../backend/web/admin/js/pluginframe.js", import.meta.url), "utf8").replace(/\/\/.*$/gm, "");

test("fit caps the longest side and keeps the ratio", () => {
  assert.equal(MAX_SIDE, 1280);
  assert.deepEqual(fit(4000, 3000), { width: 1280, height: 960 });
  assert.deepEqual(fit(3000, 4000), { width: 960, height: 1280 });
  assert.deepEqual(fit(800, 600), { width: 800, height: 600 });
});

test("errorCode maps browser errors", () => {
  assert.equal(errorCode({ name: "NotAllowedError" }), "denied");
  assert.equal(errorCode({ name: "SecurityError" }), "denied");
  assert.equal(errorCode({ name: "NotFoundError" }), "unavailable");
  assert.equal(errorCode(undefined), "unavailable");
});

test("camera.capture is refused unless the page holds the grant, before any request", () => {
  assert.match(frame, /const camera = holder\.dataset\.camera === "1"/);
  const gate = frame.search(/m\.op === "camera\.capture"\) \{\s*if \(!camera\) throw new Error/);
  assert.ok(gate > 0, "the op throws without the flag");
  assert.ok(gate < frame.indexOf("await fetch(bridge"), "the gate sits before the bridge request");
  assert.ok(frame.indexOf('op === "camera.capture"') < frame.indexOf('throw new Error("unknown op")'));
});

test("camera.capture needs a click and loads the module on demand", () => {
  const gate = frame.search(/userActivation\?\.isActive\) throw new Error/);
  assert.ok(gate > frame.indexOf("if (!camera) throw") && gate < frame.indexOf('import("@hm/plugincamera")'));
  assert.doesNotMatch(frame, /^import /m, "no static import: ungranted pages load nothing extra");
});

test("the hello carries the camera flag and the sdk exposes it", () => {
  assert.match(frame, /type: "hello", plugin, functions: \[\.\.\.functions\], camera, nonce/);
  const sdk = readFileSync(new URL("../../../backend/web/admin/js/pluginsdk.js", import.meta.url), "utf8");
  assert.match(sdk, /camera\.available = m\.camera === true/);
  assert.match(sdk, /camera: \{ available: false,/);
});

// A minimal dialog + media double; the dialog counts opens and tracks stops.
function double({ gum }) {
  const listeners = {};
  const mk = () => ({ addEventListener: (t, f) => ((listeners[t] ||= []).push(f)), removeEventListener() {} });
  const video = { ...mk(), videoWidth: 0, srcObject: undefined };
  const button = { ...mk(), disabled: false };
  const status = { textContent: "", hidden: true };
  const dlg = {
    open: false,
    opens: 0,
    ...mk(),
    querySelector: (q) => (q.includes("video") ? video : q.includes("status") ? status : button),
    showModal() { this.open = true; this.opens++; },
    close() { if (this.open) { this.open = false; (listeners.close || []).forEach((f) => f()); } },
  };
  let stopped = 0;
  Object.defineProperty(globalThis, "navigator", { value: { mediaDevices: { getUserMedia: gum(() => stopped++) } }, configurable: true });
  return { dlg, button, status, stopped: () => stopped };
}

test("an error keeps the dialog open with the message until it is closed", async () => {
  const d = double({ gum: () => async () => { throw { name: "NotAllowedError" }; } });
  let settled = false;
  const p = capture(d.dlg).then((r) => ((settled = true), r));
  await new Promise((r) => setTimeout(r, 20));
  assert.equal(settled, false, "still waiting for the person");
  assert.equal(d.dlg.open, true);
  assert.equal(d.button.disabled, true, "Capture is disabled");
  assert.match(d.status.textContent, /blocked/);
  d.dlg.close();
  assert.deepEqual(await p, { ok: false, error: "denied" });
});

test("only one capture at a time; a second call is refused and cannot close the first", async () => {
  const d = double({ gum: (stop) => () => new Promise(() => {}) });
  capture(d.dlg);
  await new Promise((r) => setTimeout(r, 5));
  d.dlg.open = false; // the person pressed Esc while the browser prompt was pending
  assert.deepEqual(await capture(d.dlg), { ok: false, error: "cancelled" });
  assert.equal(d.dlg.opens, 1, "no second dialog");
  assert.ok(CLICK_GUARD_MS >= 500);
});
