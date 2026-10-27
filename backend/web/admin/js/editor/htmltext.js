// Pure text-node addressing for html-kind fields. The walk mirrors the Go twin
// in backend/internal/render/canvas_text.go: pre-order over the body parsed as
// a <div> fragment, counting only editable text nodes. The shared golden files
// in backend/internal/render/testdata/canvas-text pin both. Needs a DOM
// (document.implementation); no other I/O.

/** Subtrees that are never editable (compared against the lower-cased localName). */
export const SKIP = new Set([
  "script", "style", "iframe", "svg", "math", "noscript", "template", "textarea", "pre", "code", "select", "option", "object",
]);

/** Explicit blank set (not \s, which differs from the Go side). */
export const BLANK = /^[\t\n\f\r \u00a0]*$/;

/**
 * Parse in an inert document (scripts never run) with a <div> context, the same
 * fragment context as Go's html.ParseFragment. Not DOMParser().body (hoists
 * leading <style>/<meta>) and not <template> (different content model).
 */
export function fragment(html) {
  const doc = document.implementation.createHTMLDocument("");
  const div = doc.createElement("div");
  div.innerHTML = html;
  return div;
}

export function collect(root) {
  const out = [];
  const walk = (n) => {
    for (let c = n.firstChild; c; c = c.nextSibling) {
      if (c.nodeType === 3) {
        if (!BLANK.test(c.data)) out.push(c);
      } else if (c.nodeType === 1) {
        if (!SKIP.has(c.localName.toLowerCase())) walk(c);
      }
    }
  };
  walk(root);
  return out;
}

/** Editable text nodes of html in walk order: [{index, tag, text}]. */
export function editableTextNodes(html) {
  return collect(fragment(html)).map((n, index) => ({ index, tag: n.parentNode.localName.toLowerCase(), text: n.data }));
}

/**
 * Sets editable text node `index` to `value` if its current text equals
 * `expect`. Only Text.data changes, so the serialiser escapes any markup the
 * user typed. Rejections ({ok:false, reason}) leave the input untouched.
 */
export function replaceTextNode(html, index, expect, value) {
  const div = fragment(html);
  const nodes = collect(div);
  if (!Number.isInteger(index) || index < 0 || index >= nodes.length) return { ok: false, reason: "range" };
  if (nodes[index].data !== expect) return { ok: false, reason: "stale" };
  if (BLANK.test(value)) return { ok: false, reason: "blank" };
  nodes[index].data = value;
  return { ok: true, html: div.innerHTML };
}

/**
 * True when path (dotted, array indexes numeric) addresses an html-kind field
 * in schema. Own-property lookups only, so __proto__ etc. never match.
 */
export function isHtmlFieldPath(schema, path) {
  let node = schema;
  for (const seg of path.split(".")) {
    if (!node || typeof node !== "object") return false;
    if (/^\d+$/.test(seg)) {
      node = node.items;
    } else {
      const props = node.properties;
      node = props && Object.prototype.hasOwnProperty.call(props, seg) ? props[seg] : undefined;
    }
  }
  return !!node && node["x-hm-field"] === "html";
}
