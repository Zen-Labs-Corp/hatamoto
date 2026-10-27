// Pattern thumbnails for the Add tab (a lazy chunk): small schematic SVGs drawn from a fixed shape
// per theme section type, or from a saved template's own tree. No images, nothing server-side.
// Run: node --test scripts/admin-qa/unit/builder-patterns.test.mjs
const NS = "http://www.w3.org/2000/svg";
export const W = 48;
export const H = 32;
export const MAX_FETCH = 12;

// Shapes are [x, y, w, h] boxes in a 48 x 32 frame.
const SHAPES = {
  generic: [[4, 4, 40, 6], [4, 14, 40, 4], [4, 21, 26, 4]],
  hero: [[4, 4, 22, 4], [4, 11, 20, 3], [4, 17, 12, 5], [30, 4, 14, 24]],
  features: [[4, 4, 40, 3], [4, 12, 11, 16], [18.5, 12, 11, 16], [33, 12, 11, 16]],
  faq: [[4, 4, 40, 4], [4, 11, 40, 4], [4, 18, 40, 4], [4, 25, 40, 4]],
  pricing: [[4, 6, 11, 22], [18.5, 4, 11, 26], [33, 6, 11, 22]],
  cta: [[10, 8, 28, 4], [14, 15, 20, 3], [17, 21, 14, 5]],
  quote: [[6, 6, 36, 4], [6, 13, 30, 4], [6, 20, 14, 3], [6, 26, 8, 2]],
  gallery: [[4, 4, 18, 11], [26, 4, 18, 11], [4, 18, 18, 11], [26, 18, 18, 11]],
  form: [[8, 4, 32, 3], [8, 11, 32, 5], [8, 19, 32, 5], [8, 27, 12, 3]],
};
const RULES = [
  [/hero|banner|intro|splash/, "hero"],
  [/feature|benefit|service|column|grid|card/, "features"],
  [/faq|accordion|question/, "faq"],
  [/pric|plan|tier/, "pricing"],
  [/cta|call|action|signup|subscribe|newsletter/, "cta"],
  [/testimonial|quote|review|logo/, "quote"],
  [/gallery|image|photo|portfolio/, "gallery"],
  [/contact|form/, "form"],
];

const kindOf = (type = "") => RULES.find(([rx]) => rx.test(String(type).toLowerCase()))?.[1];

/** The shape for a theme section type ("hero", "features_3", "faq"...): a generic one when nothing matches. */
export const shapeOf = (type = "") => SHAPES[kindOf(type)] ?? SHAPES.generic;

// The first blocks of a page: what to offer before anything else.
const STARTERS = ["hero", "features", "cta", "faq", "pricing", "quote", "gallery", "form"];

/** Palette pattern items with the usual first blocks (hero, features, call to action, FAQ...) first, then the rest, each keeping its own order. A saved template has no type: it goes after the theme's sections. */
export function startersFirst(items) {
  const rank = (i) => {
    if (i.kind === "template") return [STARTERS.length + 1, 0];
    const text = String(i.type ?? i.name).toLowerCase();
    const at = STARTERS.indexOf(kindOf(text));
    return at < 0 ? [STARTERS.length, 0] : [at, text.startsWith(STARTERS[at]) ? 0 : 1]; // "features" before "card_columns", both being features-like
  };
  const scored = items.map((i, k) => ({ i, k, r: rank(i) })).sort((a, b) => a.r[0] - b.r[0] || a.r[1] - b.r[1] || a.k - b.k);
  const seen = new Set();
  // one of each starting kind first (so Card columns does not crowd out Call to action), then the rest in the same order
  const lead = scored.filter((x) => x.r[0] < STARTERS.length && !seen.has(x.r[0]) && seen.add(x.r[0]));
  return [...lead, ...scored.filter((x) => !lead.includes(x))].map((x) => x.i);
}

/** The Add tab's Patterns row: the first `row` starters until "Show all patterns" opens the rest. */
export function patternRow(items, all = false, row = 4) {
  const ordered = startersFirst(items);
  return { shown: all ? ordered : ordered.slice(0, row), total: ordered.length, more: ordered.length > row };
}

const SIDE_BY_SIDE = new Set(["columns", "grid"]);

/** Boxes for a saved template's nodes: one band per top-level node, children split across or down, three levels deep. */
export function shapeOfTree(nodes = []) {
  const out = [];
  const gap = 2;
  const put = (list, x, y, w, h, depth, across) => {
    const n = list.length;
    if (!n) return;
    const cw = across ? (w - gap * (n - 1)) / n : w;
    const ch = across ? h : (h - gap * (n - 1)) / n;
    list.forEach((node, i) => {
      const bx = across ? x + i * (cw + gap) : x;
      const by = across ? y : y + i * (ch + gap);
      const kids = (node.c ?? []).slice(0, 4);
      if (depth >= 3 || !kids.length) out.push([bx, by, cw, ch]);
      else put(kids, bx, by, cw, ch, depth + 1, SIDE_BY_SIDE.has(node.t) ? true : !across);
    });
  };
  put(nodes.slice(0, 5), 4, 4, W - 8, H - 8, 1, false);
  return out.length ? out : SHAPES.generic;
}

/** An aria-hidden inline SVG of the boxes (createElementNS: the dom.js h() cannot make SVG). */
export function thumb(shape) {
  const s = document.createElementNS(NS, "svg");
  s.setAttribute("viewBox", `0 0 ${W} ${H}`);
  s.setAttribute("class", "hm-bs-thumb");
  s.setAttribute("aria-hidden", "true");
  for (const [x, y, w, h] of shape) {
    const r = document.createElementNS(NS, "rect");
    for (const [k, v] of Object.entries({ x, y, width: w, height: h, rx: 1 })) r.setAttribute(k, String(v));
    s.append(r);
  }
  return s;
}

/**
 * Loads the shapes of templates not in cache yet, at most `max` in total over the cache's life (cache: Map key -> shape); a failed one is
 * cached as generic so it is not asked again. items carry {key: "tpl:<id>"}; urls.template is the base.
 * Resolves when all asked are settled.
 */
export async function templateShapes(items, urls, cache, { fetch = globalThis.fetch, max = MAX_FETCH } = {}) {
  const todo = items.filter((i) => !cache.has(i.key)).slice(0, Math.max(0, max - cache.size)); // max is for the whole cache (a session), not per call
  for (const i of todo) cache.set(i.key, SHAPES.generic); // claimed: a repaint during the load does not ask twice
  await Promise.all(
    todo.map(async (i) => {
      try {
        const res = await fetch(`${urls.template}/${encodeURIComponent(i.key.slice(4))}`, { credentials: "same-origin" });
        if (res.ok) cache.set(i.key, shapeOfTree((await res.json()).nodes));
      } catch {
        // stays generic
      }
    }),
  );
  return todo.length;
}
