// The per-loop filters of a plugin items source (pure, no DOM): the params the source declares, the values a
// loop stores in p.params, how many are set and the value a change leaves behind. inspcontent.js draws them.

/** The params a source declares; [] for a source without any (or an unknown one). */
export const declFor = (loopSources, id) => (loopSources ?? []).find((s) => s.id === id)?.params ?? [];

/** The longest text value the server keeps, in UTF-8 bytes (an input's maxlength counts characters, not bytes). */
export const MAX_PARAM_BYTES = 200;
export const tooLong = (v) => typeof v === "string" && new TextEncoder().encode(v).length > MAX_PARAM_BYTES;

const usable = (d, v) => {
  if (d.type === "bool") return v === true;
  if (d.type === "number") return typeof v === "number" && Number.isFinite(v);
  return typeof v === "string" && v !== "" && !tooLong(v);
};

/** The stored values the declaration still knows and that carry something; a name the source does not declare is ignored. */
export function cleanParams(decl, values) {
  const out = {};
  for (const d of decl) if (usable(d, values?.[d.name])) out[d.name] = values[d.name];
  return out;
}

export const setCount = (decl, values) => Object.keys(cleanParams(decl, values)).length;

/** The params object after `name` changes to `raw` (a string from a field, a boolean from a switch), or null when none is left. */
export function nextParams(decl, values, name, raw) {
  const d = decl.find((x) => x.name === name);
  const out = cleanParams(decl, values);
  delete out[name];
  if (d) {
    const v = d.type === "number" ? (String(raw).trim() === "" ? NaN : Number(raw)) : raw;
    if (usable(d, v)) out[name] = v;
  }
  return Object.keys(out).length ? out : null;
}

export const filtersLabel = (n) => (n ? `Filters (${n} set)` : "Filters");
