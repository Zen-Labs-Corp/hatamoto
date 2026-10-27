// host.js pure helpers (the DOM half runs in the browser QA).
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
const { framePoint } = await import(new URL("../../../backend/web/admin/js/canvas/host.js", import.meta.url)).catch(() => ({}));

test("framePoint: the frame's screen box plus the frame point scaled", { skip: !framePoint && "host.js needs a DOM to load" }, () => {
  assert.deepEqual(framePoint({ left: 100, top: 40 }, 0.5, 200, 60), { x: 200, y: 70 });
  assert.deepEqual(framePoint({ left: 0, top: 0 }, 1, 12.5, 3), { x: 12.5, y: 3 });
});

// The move grip is pointer-only: not a button that ignores Enter (Alt+arrows in the structure pane move by keyboard).
test("the move handle is hidden from assistive technology and is not a button", async () => {
  const { readFileSync } = await import("node:fs");
  const src = readFileSync(new URL("../../../backend/web/admin/js/canvas/chrome.js", import.meta.url), "utf8"); // the handle lives in the toolbar
  const line = src.split("\n").find((l) => l.includes('class: "hm-pe__handle"'));
  assert.ok(line, "handle found");
  assert.match(line, /h\("span"/);
  assert.match(line, /"aria-hidden": "true"/);
});
