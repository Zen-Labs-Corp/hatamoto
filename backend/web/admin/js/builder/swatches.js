// The Fill group's theme colour pills: one button per theme colour, committing the token NAME (never the hex).
// The hex field, native picker and Recent list stay in colourControl (controlsui.js); style.js shows both on one
// apply. Colours reach the DOM only through the --hm-bs-sw custom property (CSSOM), as colourControl paints its swatch.
// A lazy chunk. Run: node --test scripts/admin-qa/unit/builder-swatches.test.mjs

import { h } from "@hm/builder/dom";

/**
 * swatchRow({name, palette, current, apply}) -> {el, sync(value)}: el is div.hm-bs-swatches (role group, labelled
 * "<name> theme colours"; hidden and empty when the palette has no colours). apply(token) commits the colour's name;
 * sync(value) marks the pill whose token is the value set here (aria-pressed), none for a custom or unset value.
 */
export function swatchRow({ name, palette, current = null, apply }) {
  const pills = (palette?.colors ?? []).map((c) => {
    const dot = h("span", { class: "hm-bs-sw", "aria-hidden": "true" });
    const css = c.hex || c.css;
    if (css) dot.style.setProperty("--hm-bs-sw", css);
    const b = h("button", { type: "button", class: "hm-bs-pill", "aria-pressed": "false", title: `${c.label ?? c.name} ${c.hex ?? ""}`.trim() }, dot, h("span", { text: c.label ?? c.name }));
    b.addEventListener("click", () => apply(c.name));
    return { c, b };
  });
  const el = h("div", { class: "hm-bs-swatches", role: "group", "aria-label": `${name} theme colours`, hidden: !pills.length }, ...pills.map((p) => p.b));
  const sync = (value) => {
    for (const { c, b } of pills) b.setAttribute("aria-pressed", String(value === c.name));
  };
  sync(current);
  return { el, sync };
}
