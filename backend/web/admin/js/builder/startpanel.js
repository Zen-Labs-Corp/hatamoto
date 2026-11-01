// The empty-page start panel (a lazy chunk): laid over the canvas with a few starting points as
// thumbnail tiles and "Start blank". screen.js removes it on the first insert, whatever path made it.
// Run: node --test scripts/admin-qa/unit/builder-startpanel.test.mjs
import { h, actButton } from "@hm/builder/dom";
import { shapeOf, startersFirst, templateShapes, thumb } from "@hm/builder/patterns";

export const MAX = 6;

/** The panel shows while the page has nothing on it. */
export const shouldShowStart = (doc) => !doc?.nodes?.length;

/**
 * mountStart(canvasEl, {patterns, urls, onPattern(item), onBlank}) -> {destroy}
 * patterns are palette items (group "Patterns"); a saved template's thumbnail is fetched, the rest are drawn at once.
 * Start blank takes the panel away first, then calls onBlank (which opens the Insert tab with its search focused).
 */
export function mountStart(canvasEl, { patterns = [], urls = null, onPattern, onBlank }) {
  const shown = startersFirst(patterns).slice(0, MAX); // hero, features, call to action, FAQ first, not the theme's alphabet
  const shapes = new Map();
  const slots = new Map();
  const tile = (i) => {
    const slot = h("span", { class: "hm-bs-start__t" }, thumb(i.kind === "template" ? shapeOf("") : shapeOf(i.type)));
    slots.set(i.key, slot);
    return h("li", {}, h("button", { type: "button", class: "hm-bs-start__pat", onclick: () => onPattern(i) }, slot, h("span", { class: "hm-bs-start__n", text: i.name })));
  };
  const destroy = () => box.remove();
  const box = h(
    "section",
    { class: "hm-bs__start", "aria-labelledby": "hm-bs-start-h" },
    h("h2", { class: "hm-bs-start__h", id: "hm-bs-start-h", text: "Start this page" }),
    h("p", { class: "hm-muted", text: shown.length ? "Pick a starting point, or start blank and insert blocks one at a time." : "Insert your first block, one at a time." }),
    shown.length ? h("ul", { class: "hm-bs-start__list" }, ...shown.map(tile)) : null,
    actButton("Start blank", { onclick: () => (destroy(), onBlank()) }),
  );
  canvasEl.append(box);
  const templates = urls?.template ? shown.filter((i) => i.kind === "template") : [];
  if (templates.length) templateShapes(templates, urls, shapes).then(() => templates.forEach((i) => shapes.has(i.key) && slots.get(i.key)?.replaceChildren(thumb(shapes.get(i.key)))));
  return { destroy };
}
