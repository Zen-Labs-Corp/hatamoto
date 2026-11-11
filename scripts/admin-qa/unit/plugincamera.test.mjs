// plugincamera.js sizing and error codes, and the camera gate in pluginframe.js.
// Run: node --test scripts/admin-qa/unit/plugincamera.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const { fit, errorCode, MAX_SIDE } = await import(new URL("../../../backend/web/admin/js/plugincamera.js", import.meta.url));
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
