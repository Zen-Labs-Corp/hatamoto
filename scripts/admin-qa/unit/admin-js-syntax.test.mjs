// Every admin ES module must parse. A duplicate `const` in screen.js once broke the whole builder at load
// and no unit test noticed, because the tests import pure modules only.
import { test } from "node:test";
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";

const root = new URL("../../../backend/web/admin/js/", import.meta.url).pathname;
const files = (dir) => fs.readdirSync(dir, { withFileTypes: true }).flatMap((e) => (e.isDirectory() ? files(path.join(dir, e.name)) : e.name.endsWith(".js") ? [path.join(dir, e.name)] : []));

test("every admin js module parses", () => {
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "hm-syntax-"));
  const bad = [];
  for (const f of files(root)) {
    const copy = path.join(tmp, "m.mjs");
    fs.copyFileSync(f, copy);
    try { execFileSync(process.execPath, ["--check", copy], { stdio: "pipe" }); } catch (e) { bad.push(`${path.relative(root, f)}: ${String(e.stderr).split("\n").find((l) => /Error/.test(l))}`); }
  }
  fs.rmSync(tmp, { recursive: true, force: true });
  assert.deepEqual(bad, []);
});

// A local `const jump = lazy(...)` in screen.js shadowed the imported store function `jump`, so the History
// panel's jump called the wrong function and silently did nothing. Imported names must not be redeclared
// in the module that imports them.
test("no admin js module declares a name it imports", () => {
  const found = [];
  const fine = new Set(["builder/style.js: row"]); // a block-scoped `row` in the Hide controls, which uses no imported row there
  for (const f of files(root)) {
    const src = fs.readFileSync(f, "utf8");
    const names = [];
    for (const m of src.matchAll(/^import\s+(?:([\w$]+)\s*,?\s*)?(?:\{([^}]*)\})?\s*(?:\*\s+as\s+([\w$]+))?\s*from\s/gm)) {
      if (m[1]) names.push(m[1]);
      if (m[3]) names.push(m[3]);
      for (const part of (m[2] ?? "").split(",")) { const n = part.trim().split(/\s+as\s+/).pop(); if (n) names.push(n); }
    }
    for (const n of new Set(names)) {
      if (new RegExp(`(?:^|[\\s;(])(?:const|let|var|function)\\s+${n.replace(/\$/g, "\\$")}\\b`, "m").test(src)) found.push(`${path.relative(root, f)}: ${n}`);
    }
  }
  assert.deepEqual(found.filter((x) => !fine.has(x)), []);
});
