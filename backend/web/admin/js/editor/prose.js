// Prose regions of an html field (port of entities/fields/prose-region-editor):
// the extension list, the render-equivalence check and the lossless verdict the
// html Fields view uses to decide whether a region may become a Rich Text editor.
import { Editor, Extension, Node as TipTapNode } from "@tiptap/core";
import { createEditorExtensions } from "@hm/editor/extensions";
import { fragment } from "@hm/editor/htmltext";

const ATTR_TYPES = ["heading", "paragraph", "bulletList", "orderedList", "listItem", "blockquote", "link"];

/**
 * Keeps id and class on prose blocks and links (anchors, styling hooks). Enter
 * keeps the class on the new block (continuing a styled paragraph) but never
 * the id: a copied id would be a duplicate anchor target.
 */
export const ProseAttrs = Extension.create({
  name: "proseAttrs",
  addGlobalAttributes() {
    const attr = (name) => ({
      default: null,
      keepOnSplit: name === "class",
      parseHTML: (el) => el.getAttribute(name),
      renderHTML: (attrs) => (attrs[name] ? { [name]: attrs[name] } : {}),
    });
    return [{ types: ATTR_TYPES, attributes: { id: attr("id"), class: attr("class") } }];
  },
});

/** Inline regions are one paragraph: Enter inserts a line break instead of splitting it (or does nothing when the region takes no line break, hardBreak: false). */
export const SingleBlock = Extension.create({
  name: "singleBlock",
  priority: 1000,
  addOptions: () => ({ hardBreak: true }),
  addKeyboardShortcuts() {
    return { Enter: () => !this.options.hardBreak || this.editor.commands.setHardBreak() };
  },
});

/** Inline regions hold exactly one paragraph, so no command or paste can add another block. */
export const OneParagraph = TipTapNode.create({ name: "doc", topNode: true, content: "paragraph" });

/** Block boundaries become <br>, block tags disappear (inline regions hold one paragraph). */
export const flattenBlocks = (html) =>
  html
    .replace(/<\/(p|li|h[1-6]|div|blockquote)>/gi, "<br>")
    .replace(/<\/?(p|ul|ol|li|h[1-6]|div|blockquote)\b[^>]*>/gi, "")
    .replace(/(<br\s*\/?>)+$/i, "");

/** No injected link/image/entity-link classes, and only markup the html Fields gate accepts. */
export function proseExtensions(opts = {}) {
  return [
    ...createEditorExtensions({
      linkClass: null,
      imageClass: null,
      placeholder: "",
      entityLinkClass: null,
      siteRel: false,
      prose: true,
      document: opts.inline ? OneParagraph : undefined,
      marks: opts.marks,
      hardBreak: opts.hardBreak,
    }),
    ProseAttrs,
  ];
}

const LINE_BREAKERS = new Set(["p", "h1", "h2", "h3", "h4", "h5", "h6", "ul", "ol", "li", "blockquote", "hr", "br"]);
const HTML_SPACE = /[\t\n\f\r ]+/g;

/**
 * Serialised html with insignificant whitespace normalised the way CSS
 * white-space: normal renders it: runs of HTML whitespace collapse to one space,
 * and whitespace at the start/end of a line (block boundary or <br>) is dropped.
 * Nothing else changes, so tags, attributes and entities still compare exactly.
 */
export function renderEquivalent(html) {
  const root = fragment(html);
  let last = null;
  let lineStart = true;
  const endLine = () => {
    if (last) last.data = last.data.replace(/ $/, "");
    last = null;
    lineStart = true;
  };
  const walk = (parent) => {
    for (const n of Array.from(parent.childNodes)) {
      if (n.nodeType === 3) {
        let data = n.data.replace(HTML_SPACE, " ");
        if (data.startsWith(" ") && (lineStart || last?.data.endsWith(" "))) data = data.slice(1);
        n.data = data;
        if (data) {
          last = n;
          lineStart = false;
        }
      } else if (n.nodeType === 1) {
        const breaks = LINE_BREAKERS.has(n.localName.toLowerCase());
        if (breaks) endLine();
        walk(n);
        if (breaks) endLine();
      }
    }
  };
  walk(root);
  for (const n of Array.from(root.querySelectorAll("*"))) {
    for (const c of Array.from(n.childNodes)) if (c.nodeType === 3 && !c.data) c.remove();
  }
  return root.innerHTML;
}

const VERDICT_LIMIT = 400;
const verdicts = new Map();

/**
 * True when inner parses and serialises through the prose editor unchanged
 * (modulo innerHTML serialisation and insignificant whitespace). Anything else
 * must stay as text fields so no markup is ever lost. Memoised in a bounded LRU:
 * every keystroke in a region is a new key.
 */
export function canRoundTripProse(inner) {
  const cached = verdicts.get(inner);
  if (cached !== undefined) {
    verdicts.delete(inner);
    verdicts.set(inner, cached);
    return cached;
  }
  let ok = false;
  try {
    const editor = new Editor({ extensions: proseExtensions(), content: inner, element: document.createElement("div"), injectCSS: false });
    try {
      ok = renderEquivalent(editor.getHTML()) === renderEquivalent(inner);
    } finally {
      editor.destroy();
    }
  } catch {
    ok = false;
  }
  verdicts.set(inner, ok);
  if (verdicts.size > VERDICT_LIMIT) verdicts.delete(verdicts.keys().next().value);
  return ok;
}
