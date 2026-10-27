// Design system helpers: style presets are named style maps a node references with
// p.preset. Drift, the cascade and the style clipboard live here (first paint); the op builders
// that edit presets are the lazy designops.js. Everything here is pure (no DOM, no fetch, no CSS): drift is derived from
// the doc plus the boot presets, and every edit is a list of the store's existing ops,
// so one commit is one undo step. The values are never interpreted (the server compiles).
import { hasParentHover, STATES } from "@hm/builder/store";

export const BPS = ["base", "md", "sm"];
// Keys are "md" or "md:hover": a breakpoint plus an optional state. Cascade and reach walk the
// breakpoints within one state; a state never reads another state's values.
const KEYS = BPS.flatMap((b) => [b, ...STATES.map((st) => `${b}:${st}`)]);
const split = (bp) => { const [b, st] = bp.split(":"); return [BPS.indexOf(b), st ? `:${st}` : ""]; };
export const entries = (s) => KEYS.flatMap((bp) => Object.entries(s?.[bp] ?? {}).map(([key, value]) => ({ bp, key, value })));
export const get = (s, bp, key) => s?.[bp]?.[key] ?? null;
// The server doubles a preset node's selector, so a value the node owns at a breakpoint also wins
// at every narrower one until the node owns another. cascade() is that lookup: the value at bp,
// or at the nearest wider breakpoint.
export const cascade = (s, bp, key) => {
  const [from, st] = split(bp);
  for (let i = from; i >= 0; i--) { const v = get(s, BPS[i] + st, key); if (v !== null) return v; }
  return null;
};
// The breakpoints an own value shows at: its own and each narrower one the node does not own too.
export const reach = (s, bp, key) => {
  const out = [bp];
  const [from, st] = split(bp);
  for (let i = from + 1; i < BPS.length && get(s, BPS[i] + st, key) === null; i++) out.push(BPS[i] + st);
  return out;
};

// Kinds whose literal values are "custom": lit, media, grad and gtc are never flagged.
const LITERAL_KINDS = new Set(["space", "color", "radius", "fs", "len"]);

/**
 * drift(node, presets, table, {tokens}) -> {state, overrides, redundant, extras, literals}
 * state: none (no preset) | ok | overridden | missing (the preset no longer exists).
 * overrides/redundant: own (bp,key) whose value differs from / equals the preset's at every
 * breakpoint it reaches (an override also carries the field at, the first breakpoint that differs, and its preset value);
 * extras: own keys the preset has no value for at any of them; literals: own values that are not a design token
 * (only when the site has tokens on, and only for keys that have a token vocabulary).
 */
export function drift(node, presets, table, { tokens = false } = {}) {
  const name = node.p?.preset;
  const out = { state: "none", overrides: [], redundant: [], extras: [], literals: [] };
  const preset = name ? (presets ?? []).find((p) => p.name === name) : null;
  if (name) out.state = preset ? "ok" : "missing";
  for (const { bp, key, value } of entries(node.s)) {
    if (preset) {
      const seen = reach(node.s, bp, key).map((at) => ({ at, pv: cascade(preset.s, at, key) })).filter((x) => x.pv !== null);
      const diff = seen.find((x) => x.pv !== value);
      if (!seen.length) out.extras.push({ bp, key, own: value });
      else if (diff) out.overrides.push({ bp, key, own: value, preset: diff.pv, at: diff.at });
      else out.redundant.push({ bp, key, own: value, preset: value });
    }
    const e = tokens && table?.find((x) => x.key === key);
    if (e && LITERAL_KINDS.has(e.kind) && Object.keys(e.tokens ?? {}).length && value !== "auto" && !Object.hasOwn(e.tokens, value)) out.literals.push({ bp, key, value });
  }
  if (out.state === "ok" && out.overrides.length) out.state = "overridden";
  return out;
}

/** The navigator row suffix: "" | "class card" | "class card, differs" | "class removed". */
export function presetNote(node, presets) {
  const name = node.p?.preset;
  if (!name) return "";
  const d = drift(node, presets, null);
  if (d.state === "missing") return "class removed";
  return `class ${name}${d.state === "overridden" ? ", differs" : ""}`;
}

let held = null;

/** The copied style (set by designops.copyStyle); the navigator, inspector and keys only ask whether there is one. */
export const clipboard = { has: () => held !== null, hasParentHover: () => hasParentHover(held?.s), clear: () => { held = null; }, get: () => held, set: (v) => { held = v; } };
