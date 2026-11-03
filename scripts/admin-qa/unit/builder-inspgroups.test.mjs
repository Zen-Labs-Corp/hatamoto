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

const { figmaGroups, groupHasValue, moreHasValue, initialOpen, SHORT, canScrub, PRIMARY, WIDTH_PRESETS, widthOf } = await import(new URL("builder/inspgroups.js", root));
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

test("Typography is offered for every kind, closed when nothing is set unless the kind is text, so no style key is lost", () => {
  for (const t of ["heading", "button", "text", "divider", "spacer", "image", "container", "columns", "some-plugin-block"]) {
    const gs = figmaGroups(old, { t });
    assert.deepEqual(gs.find((g) => g.name === "Typography")?.entries.map((e) => e.key), ["ff", "fs", "fw", "lh", "ls", "ta", "color"], t);
    assert.equal(initialOpen("Typography", groupHasValue(gs.find((g) => g.name === "Typography").entries, () => null), { t }), ["heading", "button", "text"].includes(t), t + " starts " + (["heading", "button", "text"].includes(t) ? "open" : "closed"));
  }
});

test("a text kind lists Typography first and opens it; other kinds keep the order and Typography closed", () => {
  for (const t of ["heading", "text", "button"]) {
    const gs = figmaGroups(old, { t });
    assert.deepEqual(gs.map((g) => g.name), ["Typography", "Layout", "Spacing", "Fill", "Corners & border", "Effects"], t);
    assert.equal(initialOpen("Typography", false, { t }), true, t);
    assert.equal(initialOpen("Layout", false, { t }), false, "only Typography opens by default");
  }
  for (const t of ["container", "image", "divider"]) {
    assert.deepEqual(figmaGroups(old, { t }).map((g) => g.name), ["Layout", "Spacing", "Fill", "Typography", "Corners & border", "Effects"], t);
    assert.equal(initialOpen("Typography", false, { t }), false, t);
  }
  assert.equal(keysOf(figmaGroups(old, { t: "heading" })).length, keysOf(figmaGroups(old)).length, "reordering loses no key");
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
  assert.deepEqual(SHORT, { w: "W", maxw: "W", h: "H", minh: "H", gap: "Gap", fs: "Sz", fw: "Wt", lh: "LH", ls: "LS", radius: "R", bw: "B", mt: "↑", mb: "↓" });
  for (const k of ["w", "h", "gap", "fs", "lh", "ls", "radius"]) assert.equal(canScrub(table.find((e) => e.key === k)), true, k);
  for (const k of ["pt", "ml", "bg", "ta", "shadow"]) assert.equal(canScrub(table.find((e) => e.key === k)), false, k);
});

const split = (node) => Object.fromEntries(figmaGroups(old, node).map((g) => [g.name, g]));
const ks = (es) => es.map((e) => e.key);

test("primary and more split every group's entries exactly once", () => {
  for (const t of ["container", "heading", "image", "divider"]) {
    for (const g of figmaGroups(old, { t })) {
      assert.deepEqual([...ks(g.primary), ...ks(g.more)].sort(), ks(g.entries).sort(), `${t} ${g.name}`);
      assert.equal(new Set([...ks(g.primary), ...ks(g.more)]).size, g.entries.length, `${t} ${g.name} no key twice`);
    }
  }
});

test("a section's Layout primary rows are Width, Align, Gap, Height; the rest is More", () => {
  const l = split({ t: "container" }).Layout;
  assert.deepEqual(ks(l.primary), PRIMARY.Layout.filter((k) => ks(l.entries).includes(k)));
  assert.deepEqual(ks(l.primary), ["maxw", "ai", "gap", "minh"]);
  for (const k of ["dir", "jc", "wrap", "as", "w", "h", "maxh"]) assert.ok(ks(l.more).includes(k), k);
});

test("a state view drops keys: the primary list only has the keys present", () => {
  const l = figmaGroups(old.map((g) => ({ ...g, entries: g.entries.filter((e) => e.key !== "maxw") })), { t: "container" }).find((g) => g.name === "Layout");
  assert.deepEqual(ks(l.primary), ["ai", "gap", "minh"]);
});

test("a non-section's Layout is all More", () => {
  for (const t of ["image", "heading", "divider"]) {
    const l = split({ t }).Layout;
    assert.deepEqual(l.primary, [], t);
    assert.equal(l.more.length, l.entries.length, t);
  }
});

test("a text block's Spacing primary is the top and bottom margin, the rest is More; other kinds have the box model only", () => {
  const s = split({ t: "heading" }).Spacing;
  assert.deepEqual(ks(s.primary), ["mt", "mb"]);
  assert.deepEqual(ks(s.more).sort(), ["ml", "mr", "pb", "pl", "pr", "pt"]);
  assert.deepEqual(split({ t: "container" }).Spacing.primary, []);
});

test("Fill, Corners and Effects primary rows; More holds the rest", () => {
  const g = split({ t: "container" });
  assert.deepEqual(ks(g.Fill.primary), ["bg"]);
  assert.deepEqual(ks(g.Fill.more), ["bgimg"]);
  assert.deepEqual(ks(g["Corners & border"].primary), ["radius", "bw"]);
  assert.deepEqual(ks(g["Corners & border"].more), ["bs", "bc"]);
  assert.deepEqual(ks(g.Effects.primary), ["shadow"]);
  assert.ok(ks(g.Effects.more).includes("op") && ks(g.Effects.more).includes("display"));
  assert.deepEqual(ks(split({ t: "heading" }).Typography.primary), ["fs", "fw", "lh", "ls", "ta", "color"]);
  assert.deepEqual(ks(split({ t: "heading" }).Typography.more), ["ff"]);
});

test("More opens when any of its keys has a value here", () => {
  const g = split({ t: "container" }).Layout;
  assert.equal(moreHasValue(g, (k) => ({ dir: "row" })[k] ?? null), true);
  assert.equal(moreHasValue(g, (k) => ({ maxw: "full" })[k] ?? null), false, "a primary key does not open More");
  assert.equal(moreHasValue(g, () => null), false);
});

test("width presets map to the existing maxw values and back", () => {
  assert.deepEqual(WIDTH_PRESETS, { full: "full", wide: "container", text: "45rem" });
  assert.equal(widthOf("full"), "full");
  assert.equal(widthOf("container"), "wide");
  assert.equal(widthOf("45rem"), "text");
  assert.equal(widthOf("720px"), "");
  assert.equal(widthOf(null), "");
});
