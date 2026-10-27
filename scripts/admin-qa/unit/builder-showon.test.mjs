// Show on: toggling one width never changes another (exhaustive over the 8 visibility vectors x 3 widths).
// Run: node --test scripts/admin-qa/unit/builder-showon.test.mjs
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

const { BPS, hiddenAt, natural } = await import(new URL("builder/bp.js", root));
const { showOnWrites, showOnState } = await import(new URL("builder/showon.js", root));

/** Applies [{bp,key,value}] to a copy of node. */
const apply = (node, writes) => {
  const s = Object.fromEntries(Object.entries(node.s ?? {}).map(([k, m]) => [k, { ...m }]));
  for (const { bp, key, value } of writes) {
    const m = (s[bp] ??= {});
    if (value === null) delete m[key];
    else m[key] = value;
  }
  return { ...node, s };
};
const vis = (n) => BPS.map((b) => !hiddenAt(n, b));
/** A node whose visibility is the vector v (true = shown), built the way the editor would. */
const fromVector = (v, t = "text") => {
  let n = { id: "a", t, s: {} };
  BPS.forEach((b, i) => {
    if (!hiddenAt(n, b) === v[i]) return;
    n = apply(n, [v[i] ? { bp: b, key: "display", value: "block" } : { bp: b, key: "hide", value: "1" }]);
  });
  assert.deepEqual(vis(n), v);
  return n;
};
const vectors = Array.from({ length: 8 }, (_, i) => [!!(i & 1), !!(i & 2), !!(i & 4)]);

test("toggling one width leaves the other two as they were (all 8 starts x 3 widths x on/off)", () => {
  for (const v of vectors) {
    for (const [i, bp] of BPS.entries()) {
      for (const on of [true, false]) {
        const n = fromVector(v);
        const after = vis(apply(n, showOnWrites(n, bp, on)));
        const want = [...v];
        want[i] = on;
        assert.deepEqual(after, want, `start ${v} ${bp} ${on ? "on" : "off"}`);
      }
    }
  }
});

test("toggling a width off and on again restores the visibility of all three", () => {
  for (const v of vectors) {
    for (const bp of BPS) {
      const n = fromVector(v);
      const flipped = apply(n, showOnWrites(n, bp, !vis(n)[BPS.indexOf(bp)]));
      const back = apply(flipped, showOnWrites(flipped, bp, vis(n)[BPS.indexOf(bp)]));
      assert.deepEqual(vis(back), v, `start ${v} ${bp}`);
    }
  }
});

test("only hide and display are written, at the width asked or a narrower one", () => {
  for (const v of vectors) for (const bp of BPS) for (const on of [true, false]) {
    for (const w of showOnWrites(fromVector(v), bp, on)) {
      assert.ok(w.key === "hide" || w.key === "display", w.key);
      assert.ok(BPS.indexOf(w.bp) >= BPS.indexOf(bp), `${w.bp} is wider than ${bp}`);
    }
  }
});

test("a display the author set is kept; showing under a wider hide writes the kind's own display", () => {
  const grid = { id: "a", t: "container", p: { layout: "grid" }, s: { base: { display: "grid" } } };
  assert.deepEqual(showOnWrites({ ...grid, s: { base: { display: "grid", hide: "1" } } }, "sm", true), [{ bp: "sm", key: "display", value: "grid" }]);
  assert.deepEqual(showOnWrites({ id: "a", t: "container", s: { base: { hide: "1" } } }, "sm", true), [{ bp: "sm", key: "display", value: "flex" }]);
  assert.deepEqual(showOnWrites({ id: "a", t: "button", s: { base: { hide: "1" } } }, "sm", true), [{ bp: "sm", key: "display", value: "inline-block" }]);
  for (const t of ["columns", "gallery"]) assert.deepEqual(showOnWrites({ id: "a", t, s: { base: { hide: "1" } } }, "sm", true, "grid"), [{ bp: "sm", key: "display", value: "grid" }]);
  const n = { id: "a", t: "text", s: { base: { display: "flex" }, md: { display: "none" } } };
  assert.deepEqual(showOnWrites(n, "sm", true), [{ bp: "sm", key: "display", value: "flex" }], "the nearest wider display that is not none");
});

test("hiding Desktop does not hide Tablet or Phone: they get their own display", () => {
  const n = { id: "a", t: "text", s: {} };
  assert.deepEqual(showOnWrites(n, "base", false), [{ bp: "base", key: "hide", value: "1" }, { bp: "md", key: "display", value: "block" }]);
  assert.deepEqual(showOnWrites(n, "md", false), [{ bp: "md", key: "hide", value: "1" }, { bp: "sm", key: "display", value: "block" }]);
  assert.deepEqual(showOnWrites(n, "sm", false), [{ bp: "sm", key: "hide", value: "1" }]);
});

test("a hide set here is cleared when the width is shown again, and a narrower width that was only hidden by it stays hidden", () => {
  const n = { id: "a", t: "text", s: { md: { hide: "1" } } };
  assert.deepEqual(showOnWrites(n, "md", true), [{ bp: "md", key: "hide", value: null }, { bp: "sm", key: "hide", value: "1" }]);
});

test("showOnState lists each width", () => {
  assert.deepEqual(showOnState({ id: "a", t: "text", s: { md: { hide: "1" } } }), [{ bp: "base", shown: true }, { bp: "md", shown: false }, { bp: "sm", shown: false }]);
});

test("hide then show a flex container leaves no display behind, so a later Grid layout reaches every width", () => {
  const flex = { id: "c", t: "container", p: { layout: "flex" }, s: {} };
  const hidden = apply(flex, showOnWrites(flex, "base", false));
  assert.deepEqual(vis(hidden), [false, true, true]);
  assert.equal(hidden.s.md.display, "flex", "Tablet stays shown while Desktop is hidden");
  const back = apply(hidden, showOnWrites(hidden, "base", true));
  assert.deepEqual(vis(back), [true, true, true]);
  assert.equal(back.s.md?.display, undefined, "the display hiding wrote is gone");
  assert.equal(back.s.sm?.display, undefined);
  const grid = { ...back, p: { layout: "grid" } };
  assert.equal(natural(grid, "md"), "grid");
});

test("showing a width keeps a display the author set that something still needs", () => {
  const n = { id: "c", t: "container", p: { layout: "flex" }, s: { base: { hide: "1" }, md: { display: "flex" }, sm: { hide: "1" } } };
  const after = apply(n, showOnWrites(n, "base", true));
  assert.deepEqual(vis(after), [true, true, false]);
});
