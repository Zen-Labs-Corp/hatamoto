// Tiny SVG element factory. Attributes only (the CSP forbids style="" and
// inline handlers); text goes in as text nodes, never as markup.
const NS = "http://www.w3.org/2000/svg";

/** el("path", {d, class: "x"}, "optional text") -> SVGElement. null/undefined/false attributes are skipped. */
export function el(name, attrs = {}, ...kids) {
  const node = document.createElementNS(NS, name);
  for (const [k, v] of Object.entries(attrs)) if (v !== null && v !== undefined && v !== false) node.setAttribute(k, v);
  node.append(...kids);
  return node;
}
