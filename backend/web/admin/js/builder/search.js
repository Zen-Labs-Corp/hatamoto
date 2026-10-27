// The Add tab and Ctrl+K search (a lazy chunk, with addtab.js and commandk.js): which palette items match a query and in what order.
// Run: node --test scripts/admin-qa/unit/builder-palette.test.mjs

// Words people use for an element, found by search besides its name (explicit, so no hint or kind string ever matches by accident).
const ALIASES = { heading: ["title", "headline"], text: ["paragraph", "body"], button: ["link"], image: ["picture", "photo"], spacer: ["space", "gap"], divider: ["line", "rule"], container: ["group", "box"] };
const words = (q) => q.toLowerCase().split(/\s+/).filter(Boolean);

/** How well one word matches one lower-case text: 0 is the whole text, 1 its start, 2 the start of a word in it, 3 anywhere in it, null no match. */
const wordScore = (text, w) => (text === w ? 0 : text.startsWith(w) ? 1 : text.split(/[\s\-_/]+/).some((x) => x.startsWith(w)) ? 2 : text.includes(w) ? 3 : null);

/**
 * The match score of an item for a query (lower is better), or null when it does not match: every word of the query must
 * match the visible name or one of its explicit aliases (never the kind, type or hint, which are internal words such as
 * "component" or "Theme section"). An alias match scores half a step behind the same match on the name.
 */
export function score(item, query) {
  const ws = words(query);
  if (!ws.length) return 0;
  let best = null;
  [item.name, ...(item.aliases ?? ALIASES[item.key] ?? [])].forEach((text, k) => {
    const t = String(text).toLowerCase();
    const each = ws.map((w) => wordScore(t, w));
    if (each.some((x) => x === null)) return;
    const s = Math.max(...each) + (k ? 0.5 : 0);
    if (best === null || s < best) best = s;
  });
  return best;
}

/** Items that match every word of query by name or alias. */
export const filterItems = (items, query) => items.filter((i) => score(i, query) !== null);

// On equal score an element comes before a pattern, a pattern before a block, and a command last.
const PRIO = { Patterns: 1, Blocks: 2, Commands: 3 };

/** items ordered best match first (whole name, start of the name, start of a word, anywhere); ties: elements, patterns, blocks, commands, then their own order. */
export function rank(items, query) {
  if (!query.trim()) return items;
  return items
    .map((i, k) => [i, score(i, query) ?? 9, PRIO[i.group] ?? 0, k])
    .sort((a, b) => a[1] - b[1] || a[2] - b[2] || a[3] - b[3])
    .map((x) => x[0]);
}
