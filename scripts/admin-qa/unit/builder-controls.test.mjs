// The Style tab's control model (controls.js, presetsrc.js): unit probing from the real StyleSpec fixture,
// bounds and steps, value split/join, box-model sets, row state across breakpoint x state x preset, palette.
// Run: node --test scripts/admin-qa/unit/builder-controls.test.mjs
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
const C = await import(new URL("../../../backend/web/admin/js/builder/controls.js", import.meta.url));
const P = await import(new URL("../../../backend/web/admin/js/builder/presetsrc.js", import.meta.url));

const table = JSON.parse(readFileSync(new URL("../../../backend/testdata/stylespec/table.json", import.meta.url), "utf8"));
const e = (k) => table.find((x) => x.key === k);

test("probeUnits: the units each key's own pattern accepts (no JS table of key -> units)", () => {
  assert.deepEqual(C.probeUnits(e("pt")), ["px", "rem", "em", "%", "vh", "vw"]);
  assert.deepEqual(C.probeUnits(e("w")), ["px", "rem", "em", "%", "vh", "vw"]);
  assert.deepEqual(C.probeUnits(e("radius")), ["px", "rem", "em", "%", "vh", "vw"]);
  assert.deepEqual(C.probeUnits(e("fs")), ["px", "rem", "em", "%", "vh", "vw"]);
  assert.deepEqual(C.probeUnits(e("bw")), ["px"]);
  assert.deepEqual(C.probeUnits(e("ls")), ["px", "rem", "em"]);
  assert.deepEqual(C.probeUnits(e("rot")), ["deg"]);
  assert.deepEqual(C.probeUnits(e("bright")), ["%"]);
  assert.deepEqual(C.probeUnits(e("blur")), ["px"]);
  assert.deepEqual(C.probeUnits(e("tx")), ["px", "%"]);
  assert.deepEqual(C.probeUnits(e("lh")), []);
  assert.deepEqual(C.probeUnits(e("sc")), []);
  assert.deepEqual(C.probeUnits(e("scrollmt")), ["px", "rem", "em", "vh", "vw"]);
});

test("probe: bare numbers, signed and auto come from the pattern too", () => {
  assert.deepEqual(C.probe(e("lh")), { units: [], bare: true, signed: false, auto: false });
  assert.equal(C.probe(e("mt")).auto, true);
  assert.equal(C.probe(e("mt")).signed, true);
  assert.equal(C.probe(e("pt")).auto, false);
  assert.equal(C.probe(e("pt")).signed, false);
  assert.equal(C.probe(e("w")).bare, false);
  assert.equal(C.probe(e("ls")).signed, true);
});

test("every len/space/radius/fs/lit entry with a pattern probes to something (fixture)", () => {
  for (const x of table.filter((t) => ["len", "space", "radius", "fs", "lit"].includes(t.kind) && t.pattern)) {
    const p = C.probe(x);
    assert.ok(p.units.length || p.bare, x.key);
  }
});

test("boundsFor: family bounds converted per unit and clamped by the pattern", () => {
  assert.deepEqual(C.boundsFor("pt", e("pt"), "px"), { min: 0, max: 200 });
  assert.deepEqual(C.boundsFor("mt", e("mt"), "px"), { min: -200, max: 200 });
  assert.deepEqual(C.boundsFor("fs", e("fs"), "px"), { min: 8, max: 96 });
  assert.deepEqual(C.boundsFor("fs", e("fs"), "rem"), { min: 0.5, max: 6 });
  assert.deepEqual(C.boundsFor("radius", e("radius"), "px"), { min: 0, max: 64 });
  assert.deepEqual(C.boundsFor("pt", e("pt"), "%"), { min: 0, max: 100 });
  assert.deepEqual(C.boundsFor("lh", e("lh"), ""), { min: 0.8, max: 3 });
  assert.deepEqual(C.boundsFor("rot", e("rot"), "deg"), { min: -180, max: 180 });
  assert.deepEqual(C.boundsFor("bw", e("bw"), "px"), { min: 0, max: 20 });
  assert.deepEqual(C.boundsFor("tx", e("tx"), "%"), { min: -100, max: 100 }, "tx % stops at 100: the bound is clamped by the pattern");
});

test("boundsFor: ls and scrollmt ranges follow the unit (no 20rem / 200vh sliders)", () => {
  assert.deepEqual(C.boundsFor("ls", e("ls"), "px"), { min: -5, max: 20 });
  assert.deepEqual(C.boundsFor("ls", e("ls"), "em"), { min: -0.31, max: 1.25 });
  assert.deepEqual(C.boundsFor("scrollmt", e("scrollmt"), "rem"), { min: 0, max: 12.5 });
  assert.deepEqual(C.boundsFor("scrollmt", e("scrollmt"), "vh"), { min: 0, max: 100 });
});

test("every slider bound is a value the pattern accepts", () => {
  for (const x of table.filter((t) => ["len", "space", "radius", "fs", "lit"].includes(t.kind) && t.pattern)) {
    const rx = new RegExp(x.pattern);
    const p = C.probe(x);
    for (const unit of p.units.length ? p.units : [""]) {
      const { min, max } = C.boundsFor(x.key, x, unit);
      assert.ok(rx.test(`${min}${unit}`), `${x.key} ${unit} min ${min}`);
      assert.ok(rx.test(`${max}${unit}`), `${x.key} ${unit} max ${max}`);
    }
  }
});

test("stepValue: no float noise, Shift is x10, bounds hold", () => {
  assert.equal(C.stepValue(0.1, 1, 0.1), 0.2);
  assert.equal(C.stepValue(0.1, 1, 0.2), 0.3, "0.1 + 0.2 is 0.3, not 0.30000000000000004");
  assert.equal(C.stepValue(16, 1, 1), 17);
  assert.equal(C.stepValue(16, -1, 1, true), 6);
  assert.equal(C.stepValue(1.5, 1, 0.1, true), 2.5);
  assert.equal(C.stepValue(195, 1, 1, true, { min: 0, max: 200 }), 200);
  assert.equal(C.stepValue(3, -1, 1, true, { min: 0, max: 200 }), 0);
  assert.equal(C.stepFor(e("pt"), "px"), 1);
  assert.equal(C.stepFor(e("pt"), "rem"), 0.1);
  assert.equal(C.stepFor(e("lh"), ""), 0.05);
});

test("splitValue: token, number+unit, auto, custom; joinValue round-trips", () => {
  assert.deepEqual(C.splitValue(e("pt"), "space-4"), { kind: "token", token: "space-4" });
  assert.deepEqual(C.splitValue(e("pt"), "12px"), { kind: "num", num: "12", unit: "px" });
  assert.deepEqual(C.splitValue(e("pt"), "0"), { kind: "num", num: "0", unit: "" });
  assert.deepEqual(C.splitValue(e("mt"), "-1.5rem"), { kind: "num", num: "-1.5", unit: "rem" });
  assert.deepEqual(C.splitValue(e("mt"), "auto"), { kind: "auto" });
  assert.deepEqual(C.splitValue(e("pt"), "auto"), { kind: "custom", raw: "auto" }, "pt does not take auto");
  assert.deepEqual(C.splitValue(e("pt"), "3ch"), { kind: "custom", raw: "3ch" }, "an unknown unit is custom");
  assert.deepEqual(C.splitValue(e("lh"), "1.5"), { kind: "num", num: "1.5", unit: "" }, "a token value typed as a number stays a number");
  assert.deepEqual(C.splitValue(e("lh"), "1.2"), { kind: "num", num: "1.2", unit: "" });
  assert.deepEqual(C.splitValue(e("rot"), "45deg"), { kind: "num", num: "45", unit: "deg" });
  assert.deepEqual(C.splitValue(e("pt"), null), { kind: "num", num: "", unit: "" });
  for (const [k, v] of [["pt", "space-4"], ["pt", "12px"], ["mt", "auto"], ["pt", "3ch"], ["mt", "-1.5rem"], ["lh", "1.2"]]) assert.equal(C.joinValue(C.splitValue(e(k), v)), v, `${k} ${v}`);
  assert.equal(C.joinValue({ kind: "num", num: "", unit: "px" }), null);
  assert.equal(C.joinValue({ kind: "num", num: "8", unit: "px" }), "8px");
  assert.equal(C.joinValue({ kind: "token", token: "" }), null);
});

test("box model: T,R,B,L order, link sets, vertical / horizontal quick sets", () => {
  assert.deepEqual(C.boxOrder.margin, ["mt", "mr", "mb", "ml"]);
  assert.deepEqual(C.boxOrder.padding, ["pt", "pr", "pb", "pl"]);
  const all = ["pt", "pb", "pl", "pr", "mt", "mb", "ml", "mr"];
  assert.deepEqual(C.linkSets(all).map((r) => r.name), ["margin", "padding"]);
  assert.deepEqual(C.linkSets(["pt", "pb", "pl", "pr"]).map((r) => r.name), ["padding"]);
  assert.deepEqual(C.linkSets(["pt", "pb"]), []);
  assert.deepEqual(C.verticalHorizontal("pl"), { vertical: ["pt", "pb"], horizontal: ["pr", "pl"] });
  assert.deepEqual(C.verticalHorizontal("mb"), { vertical: ["mt", "mb"], horizontal: ["mr", "ml"] });
  assert.deepEqual(C.verticalHorizontal("gap"), { vertical: [], horizontal: [] });
  assert.equal(C.ringOf("mr"), "margin");
  assert.equal(C.ringOf("w"), null);
  assert.equal(C.isLinked(C.boxOrder.padding, () => null), true);
  assert.equal(C.isLinked(C.boxOrder.padding, () => "8px"), true);
  assert.equal(C.isLinked(C.boxOrder.padding, (k) => (k === "pt" ? "8px" : null)), false);
  assert.deepEqual(C.writesFor(["pt", "pb"], "4px"), [{ key: "pt", value: "4px" }, { key: "pb", value: "4px" }]);
});

test("rowState: set / inherited / preset / none across breakpoint x state x preset", () => {
  const preset = { name: "card", label: "Card", s: { base: { pt: "space-4", bg: "surface" }, "base:hover": { bg: "accent" }, sm: { pt: "space-2" } } };
  const own = { base: { pt: "8px" }, "md:hover": { bg: "muted" } };
  const ownAt = (at, key) => own[at]?.[key] ?? null;
  const row = (at, state, key, inh = null) => {
    const bp = at.split(":")[0];
    return C.rowState({ own: ownAt(at, key), inh, presetVal: P.presetValueAt(preset, bp, state, key), presetName: preset.label });
  };
  assert.deepEqual(row("base", "", "pt"), { dot: "set", source: "local", from: "", value: "8px" });
  assert.deepEqual(row("md", "", "pt", { value: "8px", from: "Desktop" }), { dot: "inherited", source: "inherited", from: "Desktop", value: "8px" }, "the node's own wider value beats the preset");
  assert.deepEqual(row("md", "", "bg"), { dot: "inherited", source: "preset", from: "Card", value: "surface" }, "a preset value holds at narrower widths");
  assert.deepEqual(row("sm", "", "pt"), { dot: "inherited", source: "preset", from: "Card", value: "space-2" });
  assert.deepEqual(row("base:hover", "hover", "bg"), { dot: "inherited", source: "preset", from: "Card", value: "accent" });
  assert.deepEqual(row("md:hover", "hover", "bg"), { dot: "set", source: "local", from: "", value: "muted" });
  assert.deepEqual(row("base:hover", "hover", "pt"), { dot: "inherited", source: "preset", from: "Card", value: "space-4" }, "a state falls back to the preset's Normal value");
  assert.deepEqual(row("base", "", "mt"), { dot: "none", source: "unset", from: "", value: null });
  assert.deepEqual(C.rowState({}), { dot: "none", source: "unset", from: "", value: null });
  assert.deepEqual(C.rowState({ own: "", inh: null }).dot, "set", "an empty string is still a value; only null is unset");
});

test("rowState in a state follows the Go cascade: preset state rule (0,3,0) > node Normal (0,2,0), < node's own state rule", () => {
  const preset = { name: "card", label: "Card", s: { base: { bg: "surface" }, "base:hover": { bg: "accent" } } };
  const at = (inh) => C.rowState({ inh, presetState: P.presetStateValueAt(preset, "base", "hover", "bg"), presetVal: P.presetValueAt(preset, "base", "", "bg"), presetName: "Card" });
  // node Normal bg = surface (seen from the Hover view), preset hover = accent: the Hover view shows the preset's accent.
  assert.deepEqual(at({ value: "surface", from: "Normal, Desktop", normal: true }), { dot: "inherited", source: "preset", from: "Card", value: "accent" });
  // the node's own hover value at a wider breakpoint beats the preset's hover.
  assert.deepEqual(at({ value: "muted", from: "Desktop", normal: false }), { dot: "inherited", source: "inherited", from: "Desktop", value: "muted" });
  // no preset state rule: the node's Normal value beats the preset's Normal one.
  const plain = { name: "p", s: { base: { bg: "surface" } } };
  const node = C.rowState({ inh: { value: "primary", from: "Normal, Desktop", normal: true }, presetState: P.presetStateValueAt(plain, "base", "hover", "bg"), presetVal: "surface", presetName: "p" });
  assert.deepEqual([node.source, node.value], ["inherited", "primary"]);
  assert.equal(P.presetStateValueAt(preset, "base", "", "bg"), null);
});

test("sourceOf: a chip text for every source, never colour alone", () => {
  assert.deepEqual(C.sourceOf(C.rowState({ own: "1px" })), { kind: "set", text: "Set here" });
  assert.deepEqual(C.sourceOf({ ...C.rowState({ own: "1px" }), classValue: "2px" }), { kind: "custom", text: "Custom value" });
  assert.deepEqual(C.sourceOf({ ...C.rowState({ own: "1px" }), classValue: "1px" }), { kind: "set", text: "Set here" });
  assert.deepEqual(C.sourceOf(C.rowState({ presetVal: "1px", presetName: "card" })), { kind: "class", text: "From class .card" });
  assert.deepEqual(C.sourceOf(C.rowState({ inh: { value: "1px", from: "Desktop" } })), { kind: "inherited", text: "Inherited from Desktop" });
  assert.deepEqual(C.sourceOf(C.rowState({})), { kind: "none", text: "" });
  assert.deepEqual(C.sourceOf(C.rowState({ own: "1px", presetState: "2px" })), { kind: "set", text: "Set here" }, "a state value with no class value of its own");
  assert.equal(C.sourceLabel(C.rowState({ presetVal: "1px", presetName: "Card" })), "From class .Card");
  assert.equal(C.sourceLabel(C.rowState({ inh: { value: "1px", from: "Desktop" } })), "Inherited from Desktop");
  assert.equal(C.sourceLabel(C.rowState({ inh: { value: "1px", from: "Normal, Desktop" } })), "Inherited from Normal, Desktop");
  assert.equal(C.sourceLabel(C.rowState({})), "");
});

test("groupDot: any entry set here", () => {
  const entries = [{ key: "pt" }, { key: "pb" }];
  assert.equal(C.groupDot(entries, (k) => (k === "pb" ? "1px" : null)), true);
  assert.equal(C.groupDot(entries, () => null), false);
  assert.equal(C.groupDot([], () => "x"), false);
});

test("presetValueAt: reuses the design.js cascade; presetOf finds the node's preset", () => {
  const preset = { name: "card", s: { base: { pt: "a" }, md: { pt: "b" }, "base:hover": { pt: "h" } } };
  assert.equal(P.presetValueAt(preset, "base", "", "pt"), "a");
  assert.equal(P.presetValueAt(preset, "md", "", "pt"), "b");
  assert.equal(P.presetValueAt(preset, "sm", "", "pt"), "b", "narrower widths inherit");
  assert.equal(P.presetValueAt(preset, "md", "hover", "pt"), "h", "state cascades within the state");
  assert.equal(P.presetValueAt(preset, "base", "focus", "pt"), "a", "no focus values: falls back to Normal");
  assert.equal(P.presetValueAt(preset, "base", "", "mt"), null);
  assert.equal(P.presetValueAt(null, "base", "", "pt"), null);
  assert.equal(P.presetOf({ p: { preset: "card" } }, [preset]), preset);
  assert.equal(P.presetOf({ p: { preset: "gone" } }, [preset]), null);
  assert.equal(P.presetOf({ p: {} }, [preset]), null);
});

test("buildPalette: boot palette wins; without one the table defaults are parsed", () => {
  const boot = { colors: { primary: "#112233", bg: "#fff", text: "#000", surface: "#010203", muted: "#eef0f4", accent: "#c8102e" }, space: { 4: "2rem" }, fs: { xl: "1.5rem" } };
  const p = C.buildPalette(boot, table);
  assert.deepEqual(p.colors.map((c) => c.name), ["primary", "bg", "text", "surface", "muted", "accent"]);
  assert.equal(p.colors[0].hex, "#112233");
  assert.equal(p.colors[0].css, "var(--hm-primary)");
  assert.equal(p.space[4], "2rem");
  const d = C.buildPalette(undefined, table);
  assert.equal(d.colors.find((c) => c.name === "surface").hex, "#fff");
  assert.equal(d.colors.find((c) => c.name === "accent").hex, "#c8102e");
  assert.equal(d.colors.find((c) => c.name === "primary").hex, "", "a theme colour has no default in the table");
  assert.equal(d.space["4"], "1rem");
  assert.equal(d.space["0"], "0");
  assert.equal(d.fs["2xl"], "2rem");
});

test("tokenLabel: Spacing 4 (1rem), Font size 2xl (2rem), plain names otherwise", () => {
  const p = C.buildPalette(undefined, table);
  assert.equal(C.tokenLabel(e("pt"), "space-4", p), "Spacing 4 (1rem)");
  assert.equal(C.tokenLabel(e("fs"), "2xl", p), "Font size 2xl (2rem)");
  assert.equal(C.tokenLabel(e("w"), "container", p), "container");
  assert.equal(C.tokenLabel(e("pt"), "space-4", null), "Spacing 4");
});

test("hexFor: #rgb expands, tokens resolve, anything else is custom", () => {
  const p = C.buildPalette({ colors: { primary: "#0EA5E9", text: "rgb(0,0,0)", accent: "#abc12345" } }, table);
  assert.equal(C.hexFor("#1E293B"), "#1e293b");
  assert.equal(C.hexFor("#abc"), "#aabbcc");
  assert.equal(C.hexFor("#aabbccdd"), "", "8-digit hex has alpha: custom");
  assert.equal(C.hexFor("primary", p), "#0ea5e9");
  assert.equal(C.hexFor("text", p), "", "a theme colour that is not hex");
  assert.equal(C.hexFor("accent", p), "");
  assert.equal(C.hexFor("nope", p), "");
  assert.equal(C.hexFor(null), "");
});

test("isLength: length kinds and numeric lit keys get number + unit + slider, nothing else does", () => {
  const yes = table.filter((x) => C.isLength(x)).map((x) => x.key);
  for (const k of ["pt", "mt", "w", "radius", "fs", "tx", "ty", "lh", "sc", "rot", "bw", "blur", "bright", "scrollmt"]) assert.ok(yes.includes(k), k);
  for (const k of ["bg", "color", "op", "dir", "grad", "bgimg", "torigin", "gtc", "ff"]) assert.ok(!yes.includes(k), k);
});

test("tokenOrder: spacing by step, font sizes by resolved size, others as listed", () => {
  const pal = { fs: { xs: "0.75rem", sm: "0.875rem", md: "1rem", lg: "1.125rem", xl: "1.25rem", "2xl": "1.5rem", "3xl": "1.875rem", "4xl": "2.25rem" } };
  assert.deepEqual(C.tokenOrder(e("pt")).slice(0, 3), ["space-0", "space-1", "space-2"]);
  assert.equal(C.tokenOrder(e("pt")).at(-1), "space-12");
  assert.deepEqual(C.tokenOrder(e("fs"), pal), ["xs", "sm", "md", "lg", "xl", "2xl", "3xl", "4xl"]);
  assert.deepEqual(C.tokenOrder(e("radius")), Object.keys(e("radius").tokens));
});

test("buildPalette: each colour carries its plain name, the commit key stays the token", () => {
  const p = C.buildPalette(undefined, table);
  assert.deepEqual(p.colors.map((c) => c.label), ["Brand", "Background", "Text", "Surface", "Muted", "Accent"]);
  assert.deepEqual(p.colors.map((c) => c.name), ["primary", "bg", "text", "surface", "muted", "accent"]);
});

test("recent colours: newest first, once, at most 6, per user key", () => {
  const mem = new Map();
  const storage = { getItem: (k) => mem.get(k) ?? null, setItem: (k, v) => mem.set(k, v) };
  for (const c of ["#111111", "#222222", "#333333", "#444444", "#555555", "#666666", "#777777", "#222222"]) C.pushRecent("u1", c, storage);
  assert.deepEqual(C.loadRecent("u1", storage), ["#222222", "#777777", "#666666", "#555555", "#444444", "#333333"]);
  assert.deepEqual(C.loadRecent("u2", storage), []);
  assert.ok(mem.has("hm.builder.recent-colours.u1"));
  assert.deepEqual(C.loadRecent("u1", { getItem: () => { throw new Error("blocked"); } }), []);
});

test("stepValue: a value already outside the bounds is stepped, not snapped to the bound", () => {
  const b = { min: 0, max: 200 };
  assert.equal(C.stepValue(500, -1, 1, false, b), 499);
  assert.equal(C.stepValue(500, 1, 1, false, b), 500);
  assert.equal(C.stepValue(-8, 1, 1, false, b), -7);
  assert.equal(C.stepValue(0, -1, 1, false, b), 0, "padding still stops at 0");
});
