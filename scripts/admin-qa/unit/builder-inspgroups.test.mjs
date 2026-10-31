// The Figma group model of the Style tab.
// Run: node --test scripts/admin-qa/unit/builder-inspgroups.test.mjs
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

const { figmaGroups, groupHasValue, initialOpen, SHORT, canScrub } = await import(new URL("builder/inspgroups.js", root));
const table = JSON.parse(readFileSync(new URL("../../../backend/testdata/stylespec/table.json", import.meta.url), "utf8"));
const by = (keys) => keys.map((k) => table.find((e) => e.key === k)).filter(Boolean);

// What style.js groupsFor returns for a container in a container (names and keys as GROUPS lists them).
const old = [
  ["Layout", ["dir", "ai", "jc", "ac", "wrap", "gap", "rgap", "cgap"]],
  ["In its parent", ["as", "order"]],
  ["Spacing", ["pt", "pb", "pl", "pr", "mt", "mb", "ml", "mr"]],
  ["Size", ["w", "minw", "maxw", "h", "minh", "maxh"]],
  ["Position", ["pos"]],
  ["Typography", ["ff", "fs", "fw", "lh", "ls", "ta", "color"]],
  ["Background", ["bg", "bgimg"]],
  ["Border", ["radius", "bw", "bs", "bc"]],
  ["Effects", ["shadow", "op", "tx", "ty", "sc"]],
  ["Transform", ["rot"]],
  ["Filter", ["blur"]],
  ["Transition", ["trans"]],
  ["Visibility", ["display"]],
].map(([name, keys]) => ({ name, entries: by(keys) }));
const keysOf = (gs) => gs.flatMap((g) => g.entries.map((e) => e.key));

test("the six Figma groups in order, every key exactly once", () => {
  const gs = figmaGroups(old);
  assert.deepEqual(gs.map((g) => g.name), ["Layout", "Spacing", "Fill", "Typography", "Corners & border", "Effects"]);
  const want = old.flatMap((g) => g.entries.map((e) => e.key)).sort();
  assert.deepEqual(keysOf(gs).sort(), want);
  assert.equal(new Set(keysOf(gs)).size, want.length, "no key twice");
});

test("Layout takes Layout, In its parent and Size; Effects takes the sub-blocks", () => {
  const gs = figmaGroups(old);
  assert.deepEqual(gs[0].sub.map((s) => s.name), ["Layout", "In its parent", "Size"]);
  assert.deepEqual(gs[5].sub.map((s) => s.name), ["Effects", "Position", "Transform", "Filter", "Transition", "Visibility"]);
  assert.equal(gs[1].sub.length, 1);
});

test("a group without entries is dropped (a state view lists few keys)", () => {
  const gs = figmaGroups(old.filter((g) => g.name === "Background"));
  assert.deepEqual(gs.map((g) => g.name), ["Fill"]);
});

test("Typography is offered for every kind, closed when nothing is set, so no style key is lost", () => {
  for (const t of ["heading", "button", "text", "divider", "spacer", "image", "container", "columns", "some-plugin-block"]) {
    const gs = figmaGroups(old, { t });
    assert.deepEqual(gs.find((g) => g.name === "Typography")?.entries.map((e) => e.key), ["ff", "fs", "fw", "lh", "ls", "ta", "color"], t);
    assert.equal(initialOpen("Typography", groupHasValue(gs.find((g) => g.name === "Typography").entries, () => null), { t }), false, t + " starts closed");
  }
});

test("open state: a value set here opens a group, none starts it closed, a section opens Layout and Spacing", () => {
  const own = (m) => (k) => m[k] ?? null;
  const fill = by(["bg", "bgimg"]);
  assert.equal(groupHasValue(fill, own({ bg: "accent" })), true);
  assert.equal(groupHasValue(fill, own({})), false);
  assert.equal(initialOpen("Fill", true, { t: "heading" }), true);
  assert.equal(initialOpen("Fill", false, { t: "container" }), false);
  assert.equal(initialOpen("Layout", false, { t: "container" }), true);
  assert.equal(initialOpen("Spacing", false, { t: "container" }), true);
  assert.equal(initialOpen("Spacing", false, { t: "heading" }), false);
  assert.equal(initialOpen("Effects", false, null), false);
});

test("short labels and which labels scrub", () => {
  assert.deepEqual(SHORT, { w: "W", h: "H", gap: "Gap", fs: "Sz", lh: "LH", ls: "LS", fw: "Wt" });
  for (const k of ["w", "h", "gap", "fs", "lh", "ls", "radius"]) assert.equal(canScrub(table.find((e) => e.key === k)), true, k);
  for (const k of ["pt", "ml", "bg", "ta", "shadow"]) assert.equal(canScrub(table.find((e) => e.key === k)), false, k);
});
