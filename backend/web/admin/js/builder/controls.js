// The Style tab's control model: pure, DOM-free logic the number / unit / slider, colour, box-model and
// row controls (controlsui.js, boxmodel.js) are built on, so it is node-tested. A lazy chunk.
// Nothing here knows a key's units or tokens: they are read from the StyleSpec table entry (its
// `pattern` and `tokens`), the one source the server validates with. Run: node --test scripts/admin-qa/unit/builder-controls.test.mjs

import { COLOUR_NAME } from "@hm/builder/colournames";

/** Units a length control may offer; which of them a key accepts is probed from the entry's own pattern. */
const UNITS = ["px", "rem", "em", "%", "vh", "vw", "deg"];

const rxOf = (entry) => {
  try {
    return new RegExp(entry?.pattern ?? "");
  } catch {
    return null;
  }
};
// "1"+u, falling back to "10"+u: a range-limited pattern (bright, tx) still accepts one of them.
const accepts = (rx, ...forms) => !!rx && forms.some((f) => rx.test(f));

/** The units entry's pattern accepts, in UNITS order. */
export const probeUnits = (entry) => {
  const rx = rxOf(entry);
  return UNITS.filter((u) => accepts(rx, `1${u}`, `10${u}`));
};

/** What the entry's pattern accepts besides units: {units, bare (a number with no unit: lh, sc), signed, auto}. */
export function probe(entry) {
  const rx = rxOf(entry);
  const units = probeUnits(entry);
  return {
    units,
    bare: accepts(rx, "1", "10"),
    signed: accepts(rx, ...(units.length ? units.map((u) => `-1${u}`) : ["-1"])),
    auto: accepts(rx, "auto"),
  };
}

// Slider bounds by key family, in px for lengths. Unit conversions below; a bound the pattern refuses is
// walked inward until it accepts one, so a slider position is never a value the table would refuse.
const FAMILY = { space: [0, 200], len: [0, 200], radius: [0, 64], fs: [8, 96] };
// lit keys have their own ranges (what the pattern allows is the final say).
const LIT = {
  lh: [0.8, 3], sc: [0.5, 2], ls: [-5, 20], rot: [-180, 180], skx: [-45, 45], sky: [-45, 45], hue: [0, 360], bright: [0, 200], contrast: [0, 200], sat: [0, 200], gray: [0, 100],
  blur: [0, 20], bblur: [0, 40], bw: [0, 20], bwt: [0, 20], bwr: [0, 20], bwb: [0, 20], bwl: [0, 20], tx: [-200, 200], ty: [-200, 200], scrollmt: [0, 200],
};
const PERCENT = [0, 100];
const BY_UNIT = new Set(["ls", "scrollmt"]); // lit keys whose range follows the unit chosen, like a length

/** Whether the entry is edited as number + unit + slider: the length kinds and the numeric lit keys. */
export const isLength = (entry) => ["space", "len", "radius", "fs"].includes(entry?.kind) || (entry?.kind === "lit" && Object.hasOwn(LIT, entry.key));

/** Round away float noise (0.1 + 0.2): at most 2 decimals, the most any pattern allows. */
export const tidy = (n) => Number(n.toFixed(2));

const fmt = (n, unit) => `${tidy(n)}${unit}`;

/** The step of the number field and slider for the unit. */
export const stepFor = (_entry, unit) => (unit === "rem" || unit === "em" ? 0.1 : unit === "" ? 0.05 : 1);

/** {min, max} of the slider for key at unit ("" = unitless), inside what the pattern accepts. */
export function boundsFor(key, entry, unit) {
  const rx = rxOf(entry);
  let [min, max] = LIT[key] ?? FAMILY[entry?.kind] ?? PERCENT;
  if (!LIT[key] || BY_UNIT.has(key)) {
    if (unit === "rem" || unit === "em") [min, max] = [min / 16, max / 16];
    else if (unit === "%" || unit === "vh" || unit === "vw") [min, max] = LIT[key] ? [Math.max(min, PERCENT[0]), Math.min(max, PERCENT[1])] : PERCENT;
    else if (unit === "deg" && !LIT[key]) [min, max] = [0, 360];
  }
  if (!LIT[key] && probe(entry).signed && min === 0) min = -max;
  const step = stepFor(entry, unit) * (unit === "" ? 2 : 1);
  const ok = (n) => !!rx && rx.test(fmt(n, unit));
  while (min < max && !ok(min)) min = tidy(min + step);
  while (max > min && !ok(max)) max = tidy(max - step);
  return { min: tidy(min), max: tidy(max) };
}

/** n kept inside bounds ({min, max}), except that a start already outside them is not pulled in: the range widens to include it. */
export const clampFrom = (n, start, bounds) => (bounds ? Math.min(Math.max(bounds.max, start), Math.max(Math.min(bounds.min, start), n)) : n);

/** One arrow-key step: n moved by dir (+1 / -1) times step, times 10 with Shift; no float noise, never outside bounds (an out-of-range n is not snapped back). */
export const stepValue = (n, dir, step, big = false, bounds = null) => clampFrom(tidy(n + dir * step * (big ? 10 : 1)), n, bounds);

const NUM = /^(-?[0-9]+(?:\.[0-9]+)?)([a-z%]*)$/;

/**
 * How a stored value shows in the control: {kind: "token", token} | {kind: "num", num, unit} | {kind: "auto"} | {kind: "custom", raw}.
 * A value the control cannot express (unknown unit, 8-digit hex, a token the table dropped) is custom and is never rewritten.
 * An empty value is an empty number (the control picks its unit).
 */
export function splitValue(entry, value) {
  if (value === null || value === undefined || value === "") return { kind: "num", num: "", unit: "" };
  const v = String(value);
  if (Object.hasOwn(entry?.tokens ?? {}, v)) return { kind: "token", token: v };
  const p = probe(entry);
  if (v === "auto" && p.auto) return { kind: "auto" };
  const m = NUM.exec(v);
  if (m && (m[2] === "" ? p.bare || Number(m[1]) === 0 : p.units.includes(m[2]))) return { kind: "num", num: m[1], unit: m[2] };
  return { kind: "custom", raw: v };
}

/** The value to commit for a split: null clears it. */
export function joinValue(s) {
  switch (s.kind) {
    case "token":
      return s.token || null;
    case "auto":
      return "auto";
    case "custom":
      return s.raw ?? null;
    default:
      return s.num === "" || s.num === undefined ? null : `${s.num}${s.unit ?? ""}`;
  }
}

// ---- box model ---------------------------------------------------------------------

/** The two rings of the Spacing group, each in top, right, bottom, left order (the tab order of the box model). */
export const boxOrder = { margin: ["mt", "mr", "mb", "ml"], padding: ["pt", "pr", "pb", "pl"] };

/** The rings whose four sides are all in keys: [{name, keys}], margin first. */
export const linkSets = (keys) => Object.entries(boxOrder).filter(([, sides]) => sides.every((k) => keys.includes(k))).map(([name, sides]) => ({ name, keys: [...sides] }));

/** The ring side belongs to, or null. */
export const ringOf = (side) => Object.keys(boxOrder).find((n) => boxOrder[n].includes(side)) ?? null;

/** The Vertical / Horizontal quick sets of side's ring: {vertical: [top, bottom], horizontal: [right, left]}. */
export function verticalHorizontal(side) {
  const ring = boxOrder[ringOf(side)];
  return ring ? { vertical: [ring[0], ring[2]], horizontal: [ring[1], ring[3]] } : { vertical: [], horizontal: [] };
}

/** Whether a ring is linked: all four sides carry the same value (or none is set). valueOf(key) -> value | null. */
export const isLinked = (keys, valueOf) => new Set(keys.map((k) => valueOf(k) ?? null)).size === 1;

/** The writes that give every key one value, for commitMany: [{key, value}]. */
export const writesFor = (keys, value) => keys.map((key) => ({ key, value }));

// ---- row state -----------------------------------------------------------------------

/**
 * Where a row's value comes from, for the dot and the source label.
 * own: the value set here (breakpoint + state) or null; inh: {value, from, normal} the nearest wider breakpoint / Normal state
 * the node owns (normal: it is the Normal state's value seen from a state view), or null; presetVal / presetName: what the
 * node's preset gives (Normal, or the plain breakpoint); presetState: what it gives in this state.
 * The server's cascade (design.go, style.go): the node's own rules beat the preset's at narrower widths; in a state the
 * preset's state rule (doubled selector, 0,3,0) beats the node's Normal rule (0,2,0) but loses to the node's own state rule.
 * So: own, the node's same-state value, the preset's state value, the node's Normal value, the preset's value.
 * -> {dot: "set" | "inherited" | "none", source: "local" | "inherited" | "preset" | "unset", from, value}
 */
export function rowState({ own = null, inh = null, presetVal = null, presetState = null, presetName = "" } = {}) {
  const given = (v) => v !== null && v !== undefined;
  const fromPreset = (value) => ({ dot: "inherited", source: "preset", from: presetName, value });
  const fromNode = () => ({ dot: "inherited", source: "inherited", from: inh.from ?? "", value: inh.value });
  if (given(own)) return { dot: "set", source: "local", from: "", value: own };
  if (inh && !inh.normal) return fromNode();
  if (given(presetState)) return fromPreset(presetState);
  if (inh) return fromNode();
  if (given(presetVal)) return fromPreset(presetVal);
  return { dot: "none", source: "unset", from: "", value: null };
}

/**
 * The source chip of a row state: {kind: "set" | "custom" | "class" | "inherited" | "none", text}. A value set here is "Set here",
 * or "Custom value" when the node's class gives another value for the same key (state.classValue); otherwise the chip says where
 * the shown value comes from: "From class .card", "Inherited from Desktop"; "" when nothing is set.
 */
export function sourceOf(s) {
  const given = (v) => v !== null && v !== undefined;
  if (s.source === "local") return given(s.classValue) && s.classValue !== s.value ? { kind: "custom", text: "Custom value" } : { kind: "set", text: "Set here" };
  if (s.source === "preset") return { kind: "class", text: `From class .${s.from}`.trim() };
  if (s.source === "inherited") return { kind: "inherited", text: `Inherited from ${s.from}`.trim() };
  return { kind: "none", text: "" };
}
export const sourceLabel = (s) => sourceOf(s).text;

/** Whether a group header shows the set-here dot: any entry is set here. own(key) -> value | null. */
export const groupDot = (entries, own) => entries.some((e) => (own(e.key) ?? null) !== null);

// ---- colours ---------------------------------------------------------------------------

/** The names of the colour palette in display order (the theme's, then the design tokens'). */
const COLOUR_ORDER = ["primary", "bg", "text", "surface", "muted", "accent"];

// "var(--hm-surface,#fff)" -> "#fff": the table's default for a token, used only when boot carries no palette.
const fallbackOf = (css) => /^var\([^,]+,\s*(.+)\)$/.exec(css ?? "")?.[1] ?? "";

const fromTable = (entry, prefix = "") => Object.fromEntries(Object.entries(entry?.tokens ?? {}).filter(([n]) => n.startsWith(prefix)).map(([n, css]) => [n.slice(prefix.length), fallbackOf(css) || css]));

/**
 * The palette the controls show, from boot.design.palette ({colors, space, fs}, resolved from the site settings) or, when
 * boot has none (older server, cached page), the table's own defaults. table is the StyleSpec table.
 * -> {colors: [{name, hex, css}], space: {step: value}, fs: {name: value}}; css is the table's token reference for a swatch preview.
 */
export function buildPalette(boot, table) {
  const by = (k) => table?.find((e) => e.key === k);
  const colourTokens = by("bg")?.tokens ?? {};
  const names = [...COLOUR_ORDER.filter((n) => Object.hasOwn(colourTokens, n)), ...Object.keys(colourTokens).filter((n) => !COLOUR_ORDER.includes(n)).sort()];
  const own = boot?.colors ?? {};
  return {
    colors: names.map((name) => ({ name, label: COLOUR_NAME[name] ?? name, hex: own[name] ?? fallbackOf(colourTokens[name]), css: colourTokens[name] })),
    space: boot?.space ?? fromTable(by("pt"), "space-"),
    fs: boot?.fs ?? fromTable(by("fs")),
  };
}

const RECENT_KEY = "hm.builder.recent-colours.";

/** The custom colours this user picked last (at most 6, newest first); [] when none or storage fails. */
export function loadRecent(user = "", storage = globalThis.localStorage) {
  try {
    const v = JSON.parse(storage?.getItem(RECENT_KEY + user) ?? "null");
    return Array.isArray(v) ? v.filter((x) => typeof x === "string").slice(0, 6) : [];
  } catch {
    return [];
  }
}

/** hex first (once), at most 6; stored per user (storage may be full or blocked: then it just does not persist). */
export function pushRecent(user, hex, storage = globalThis.localStorage) {
  const list = [hex, ...loadRecent(user, storage).filter((x) => x !== hex)].slice(0, 6);
  try {
    storage?.setItem(RECENT_KEY + user, JSON.stringify(list));
  } catch {
    // not remembered
  }
  return list;
}

/** A token's option label: "Spacing 4 (1rem)", "Font size 2xl (2rem)", else the plain name. */
export function tokenLabel(entry, name, palette) {
  const step = name.startsWith("space-") ? name.slice(6) : null;
  if (entry?.kind === "space" && step !== null) return `Spacing ${step} (${palette?.space?.[step] ?? ""})`.replace(" ()", "");
  if (entry?.kind === "fs" && palette?.fs?.[name]) return `Font size ${name} (${palette.fs[name]})`;
  return name;
}

/** The entry's token names in a useful order: spacing by step, font sizes by resolved size, the rest as the table lists them. */
export function tokenOrder(entry, palette = null) {
  const names = Object.keys(entry?.tokens ?? {});
  const rank = (n) => (entry.kind === "space" ? Number(n.replace(/^space-/, "")) : entry.kind === "fs" ? parseFloat(palette?.fs?.[n]) : NaN);
  if (!names.every((n) => Number.isFinite(rank(n)))) return names;
  return names.sort((a, b) => rank(a) - rank(b));
}

const HEX = /^#([0-9a-f]{3}|[0-9a-f]{6})$/i;

/**
 * The #rrggbb a native colour input starts from: a hex value expanded and lower-cased, a token resolved through the
 * palette; "" when there is none (8-digit hex with alpha, a theme colour that is not hex, an unknown value).
 */
export function hexFor(value, palette = null) {
  let v = String(value ?? "");
  const hit = palette?.colors?.find((c) => c.name === v);
  if (hit) v = hit.hex;
  if (!HEX.test(v)) return "";
  v = v.toLowerCase();
  return v.length === 4 ? `#${v[1]}${v[1]}${v[2]}${v[2]}${v[3]}${v[3]}` : v;
}
