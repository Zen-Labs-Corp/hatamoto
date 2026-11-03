// The section card shared by the Insert tab and the "/" popover: thumbnail, name and a one-line description.
// Pure DOM building through the caller's h (so tests and lazy chunks reuse it); styling is in builder.css.
import { blurbOf, isDark, thumb } from "@hm/builder/patterns";

/** The tooltip of an insertable item. */
export const whereText = (i) => `${i.name}${i.hint ? `: ${i.hint}` : ""}. Click or Enter to insert, or drag onto the page.`;

// A theme section's line and dark flag come from its type here, not in palette.js: patterns.js is a lazy chunk and palette.js is eager.
const blurbFor = (i) => i.blurb || i.description || (i.type ? blurbOf(i.type) : i.hint || "");

/**
 * sectionCard(item, shapeFor, h) -> li > button[data-pal] with the thumbnail, name and description.
 * shapeFor(item) gives the thumbnail shape; a dark item (CTA bands) adds hm-bs-pal__card--dark.
 */
export function sectionCard(i, shapeFor, h) {
  const cls = `hm-bs-pal__item hm-bs-pal__item--pat hm-bs-pal__card${(i.dark ?? isDark(i.type)) ? " hm-bs-pal__card--dark" : ""}`;
  return h("li", {}, h("button", { type: "button", class: cls, "data-pal": i.key, title: whereText(i) }, thumb(shapeFor(i)), h("span", { class: "hm-bs-pal__n", text: i.name }), h("span", { class: "hm-bs-pal__h", text: blurbFor(i) })));
}
