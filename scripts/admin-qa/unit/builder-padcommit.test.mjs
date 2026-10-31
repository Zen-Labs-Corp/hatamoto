// A canvas padding drag's commit as screen.js wires it: onPadCommit hands the writes to run() once (one history entry),
// and the inherited value is where a drag starts without a measured pd.
// Run: node --test scripts/admin-qa/unit/builder-padcommit.test.mjs
import { register } from "node:module";
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const root = new URL("../../../backend/web/admin/js/", import.meta.url).href;
register(
  "data:text/javascript," +
    encodeURIComponent(`export async function resolve(s, c, next) {
      if (s.startsWith("@hm/")) return { url: ${JSON.stringify(root)} + s.slice(4) + ".js", shortCircuit: true };
      return next(s, c);
    }`),
);
const S = await import(new URL("builder/store.js", root));
const { selectionInfo } = await import(new URL("builder/selinfo.js", root));
const { ownValue, inheritedValue } = await import(new URL("builder/bp.js", root));
const { startPx } = await import(new URL("canvas/padding.js", root));
const src = readFileSync(new URL("builder/screen.js", root), "utf8");
const insp = readFileSync(new URL("builder/inspector.js", root), "utf8");

// a collapsed theme section: the padding lives on the component
const hero = { id: "h", t: "component", p: { name: "Hero" }, s: { base: { pt: "6rem" } } };
const doc = { v: 2, nodes: [{ id: "sec", t: "container", p: { tag: "section" }, c: [hero] }] };

test("a collapsed theme section shows no padding handles: its component styles do not render", () => {
  const info = selectionInfo(doc, "h");
  assert.equal(info.isSection, true);
  assert.equal(info.section.collapsed, true); // screen.js isSection excludes this
  assert.match(src, /return !!info\?\.isSection && !info\.section\.collapsed && info\.kind !== "component";/);
  const plain = { v: 2, nodes: [{ id: "c", t: "container", p: { tag: "section" }, c: [{ id: "t", t: "text" }, { id: "t2", t: "text" }] }] };
  assert.equal(selectionInfo(plain, "c").section.collapsed, false);
});

test("a top-level theme component with no container shows no padding handles either", () => {
  const bare = { v: 2, nodes: [hero] };
  const info = selectionInfo(bare, "h");
  assert.equal(info.isSection, true);
  assert.equal(info.section.collapsed, false);
  assert.equal(info.kind, "component"); // screen.js isSection excludes it
  assert.match(src, /info\.kind !== "component"/);
});

test("the start value: own at the key, else the wider width's, rem as 16px", () => {
  const n = S.find(doc, "h");
  assert.equal(ownValue(n, "md", "pt"), null);
  assert.equal(inheritedValue(n, "md", "pt").value, "6rem");
  assert.equal(startPx(null, "top", inheritedValue(n, "md", "pt").value), 96);
});

test("screen.js wires the handlers the direct layer needs", () => {
  assert.match(src, /onAddSection: \(index, el\) => addAt\(\{ parent: "", index \}/);
  assert.match(src, /onPadCommit: \(\{ id, writes, label: name, coalesce \}\) => run\(writes, \{ label: name, coalesce \}\)/);
  assert.match(src, /padState: \(\) => \(\{ bp: device \}\)/); // spacing is Normal-only: never a state key
  assert.doesNotMatch(src, /inspector\.state\(\)/);
  assert.match(src, /authored: \(id, side\)/);
  assert.doesNotMatch(insp, /state: \(\) => state/);
});
