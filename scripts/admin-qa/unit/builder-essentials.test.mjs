// Style tab Essentials: the per-kind table against the real StyleSpec table, no key twice on the tab, Effects group.
// Run: node --test scripts/admin-qa/unit/builder-essentials.test.mjs
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

import { readFileSync } from "node:fs";
const { ESSENTIALS, essentialsFor, coveredKeys, PADDING } = await import(new URL("builder/essentials.js", root));
const { groupsFor } = await import(new URL("builder/style.js", root));
const full = JSON.parse(readFileSync(new URL("../../../backend/testdata/stylespec/table.json", import.meta.url), "utf8"));
const keys = new Set(full.map((e) => e.key));

test("every Essentials key exists in the StyleSpec table", () => {
  for (const [kind, list] of Object.entries(ESSENTIALS)) for (const k of list) assert.ok(k === "spacing" || keys.has(k), `${kind}: ${k}`);
  for (const k of PADDING) assert.ok(keys.has(k));
});

test("essentialsFor: the kind's list, the fallback for other kinds, direction only for flex", () => {
  assert.deepEqual(essentialsFor({ t: "heading" }, full), ["fs", "fw", "color", "ta", "spacing"]);
  assert.ok(essentialsFor({ t: "image" }, full).includes("spacing"), "the box model is one click away for every kind");
  assert.deepEqual(essentialsFor({ t: "text" }, full), ["spacing", "bg", "radius", "w"]);
  assert.ok(essentialsFor({ t: "container", p: {} }, full).includes("dir"));
  assert.ok(!essentialsFor({ t: "container", p: { layout: "grid" } }, full).includes("dir"));
  assert.deepEqual(essentialsFor({ t: "heading" }, full.filter((e) => e.key !== "ta")), ["fs", "fw", "color", "spacing"], "a key the table lacks is skipped");
  assert.ok(!essentialsFor({ t: "button" }, full.filter((e) => e.key !== "pl")).includes("spacing"), "the box model needs all four padding sides");
});

test("no key appears twice on the tab: the groups leave the Essentials out", () => {
  for (const node of [{ id: "a", t: "heading", p: {} }, { id: "b", t: "button", p: {} }, { id: "c", t: "container", p: {} }, { id: "d", t: "image", p: {} }, { id: "e", t: "text", p: {} }]) {
    const ess = essentialsFor(node, full);
    const covered = new Set(coveredKeys(ess));
    const inGroups = groupsFor(full, node, null, "", covered).flatMap((g) => g.entries.map((e) => e.key));
    assert.equal(inGroups.filter((k) => covered.has(k)).length, 0, node.t);
    assert.equal(new Set(inGroups).size, inGroups.length, `${node.t} groups have no duplicates`);
  }
});

test("Effects holds shadow, opacity, move X/Y and scale; Transform keeps the rest", () => {
  const g = Object.fromEntries(groupsFor(full, { id: "a", t: "text" }).map((x) => [x.name, x.entries.map((e) => e.key)]));
  assert.deepEqual(g.Effects, ["shadow", "op", "tx", "ty", "sc"]);
  assert.deepEqual(g.Transform, ["rot", "skx", "sky", "torigin"]);
  assert.ok(g.Filter.includes("blend") && g.Filter.includes("bblur"), "no key lost");
});

test("margin and padding are one nested control for every kind: Essentials has the box model, no spacing key is left in More or in a second place", () => {
  for (const t of ["heading", "image", "button", "container", "text", "columns"]) {
    const node = { id: "a", t, p: {} };
    const ess = essentialsFor(node, full);
    assert.equal(ess.filter((k) => k === "spacing").length, 1, t);
    assert.ok(!ess.includes("mb") && !ess.includes("mt"), `${t}: no loose margin essential`);
    const rest = groupsFor(full, node, null, "", new Set(coveredKeys(ess)));
    assert.ok(!rest.some((g) => g.name === "Spacing"), `${t}: nothing of Spacing is left under More`);
    assert.deepEqual(new Set(coveredKeys(["spacing"])), new Set(["mt", "mb", "ml", "mr", "pt", "pb", "pl", "pr"]));
  }
});
