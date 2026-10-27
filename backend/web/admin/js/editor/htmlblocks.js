// The html-block nodes of the Visual tab (port of editor/html-blocks/*) and the
// lossless rule: HTML <-> ProseMirror with
//     docToHtml(htmlToDoc(x, ctx), schema) === normalizeHtml(x)   for every x.
// A block element is represented structurally only if its ProseMirror
// serialisation equals the source element's DOM serialisation; otherwise it (or
// its nearest non-descendable ancestor) stays an atomic RawHtml. Opening the
// Visual tab never writes: only real edits do. Core ships no block definitions;
// plugins register theirs with registerHtmlBlock.
import { Node, getSchema } from "@tiptap/core";
import StarterKit from "@tiptap/starter-kit";
import { DOMParser as PMDOMParser, DOMSerializer, Node as PMNode } from "@tiptap/pm/model";
import { EntityLink } from "@hm/editor/entitylink";

// ---- DOM normalisation: everything goes through a scratch <template> so our
// output is byte-identical to what innerHTML produces.

let inert = null;

/**
 * A document without a browsing context. Untrusted markup must only ever be
 * created/adopted here: moving e.g. an <img onerror> into the live document
 * starts the load and runs the handler.
 */
export function inertDocument() {
  inert ??= document.implementation.createHTMLDocument("");
  return inert;
}

const scratch = () => inertDocument().createElement("template");

/** Parse html as a fragment and re-serialise it (the "after normalisation" form). */
export function normalizeHtml(html) {
  const tpl = scratch();
  tpl.innerHTML = html;
  return tpl.innerHTML;
}

/** The exact string node contributes to its parent's innerHTML. */
export function serializeDomNode(node) {
  const tpl = scratch();
  tpl.content.appendChild(node.cloneNode(true));
  return tpl.innerHTML;
}

export const closeTag = (el) => `</${el.localName}>`;

/** Opening tag of el (attributes included), e.g. <section class="x">. */
export function openTag(el) {
  const html = el.cloneNode(false).outerHTML;
  const close = closeTag(el);
  return html.endsWith(close) ? html.slice(0, -close.length) : html;
}

// ---- registry

const blocks = new Map();

/** def: {name, label, selector, extensions}; the top node of extensions MUST be in group "htmlBlock". */
export function registerHtmlBlock(def) {
  try {
    inertDocument().createDocumentFragment().querySelector(def.selector);
  } catch {
    console.warn(`html-blocks: block '${def.name}' has an invalid selector '${def.selector}' - not registered`);
    return;
  }
  if (blocks.has(def.name)) blocks.delete(def.name); // last write wins, in latest order
  blocks.set(def.name, def);
}

export const getHtmlBlocks = () => Array.from(blocks.values());
export const clearHtmlBlocks = () => blocks.clear();

// ---- nodes

/** Atomic node holding original markup verbatim. Never produced by PM parsing. */
export const RawHtml = Node.create({
  name: "rawHtml",
  group: "htmlBlock",
  atom: true,
  selectable: true,
  draggable: true,
  addAttributes() {
    return { html: { default: "", rendered: false } };
  },
  // Clipboard round-trip only. convert never accepts a raw node produced by
  // parsing stored markup, so source <div data-raw-html> stays plain raw HTML.
  parseHTML() {
    return [{ tag: "div[data-raw-html]", priority: 1000, getAttrs: (el) => ({ html: el.getAttribute("data-raw-html") ?? "" }) }];
  },
  renderHTML({ node }) {
    return ["div", { "data-raw-html": String(node.attrs.html ?? "") }];
  },
});

/** Wrapper element (open/close tags kept verbatim) around further blocks. */
export const RawContainer = Node.create({
  name: "rawContainer",
  group: "htmlBlock",
  content: "htmlBlock*",
  defining: true,
  isolating: true,
  draggable: true,
  addAttributes() {
    return { open: { default: "", rendered: false }, close: { default: "", rendered: false } };
  },
  parseHTML() {
    return [
      {
        tag: "div[data-raw-container]",
        priority: 1000,
        getAttrs: (el) => ({ open: el.getAttribute("data-open") ?? "", close: el.getAttribute("data-close") ?? "" }),
      },
    ];
  },
  renderHTML({ node }) {
    return ["div", { "data-raw-container": "", "data-open": String(node.attrs.open ?? ""), "data-close": String(node.attrs.close ?? "") }, 0];
  },
});

export const CORE_RICH_TEXT_SELECTOR = "p, h2, h3, h4, ul, ol, blockquote";

const REGROUPED = {
  paragraph: "block htmlBlock",
  heading: "block htmlBlock",
  bulletList: "block list htmlBlock",
  orderedList: "block list htmlBlock",
  blockquote: "block htmlBlock",
};

/** StarterKit whose top-level nodes are in htmlBlock and whose doc is htmlBlock+. */
const HtmlBlockKit = StarterKit.configure({
  heading: { levels: [2, 3, 4] },
  link: false,
  underline: false,
  codeBlock: false,
  horizontalRule: false,
  trailingNode: false,
}).extend({
  addExtensions() {
    const inner = this.parent?.() ?? [];
    return inner.map((ext) => {
      if (ext.name === "doc") return ext.extend({ content: "htmlBlock+" });
      const group = REGROUPED[ext.name];
      return group ? ext.extend({ group }) : ext;
    });
  },
});

/** The full extension list of the html block editor: core kit, entity links, raw nodes, registered blocks. */
export function createHtmlBlockSchemaExtensions(defs = getHtmlBlocks()) {
  return [
    HtmlBlockKit,
    // Plain links must round-trip untouched, so drop the kit's default class/rel/target.
    EntityLink.configure({ openOnClick: false, HTMLAttributes: { class: null, rel: null, target: null } }),
    RawHtml,
    RawContainer,
    ...defs.flatMap((b) => b.extensions),
  ];
}

// ---- conversion

/** Only flow-content wrappers become a RawContainer; anything with a context-sensitive content model stays raw. */
const CONTAINER = new Set(["div", "section", "article", "aside", "header", "footer", "main", "nav", "figure"]);
const HTML_NS = "http://www.w3.org/1999/xhtml";

const raw = (html) => ({ type: "rawHtml", attrs: { html } });
const isRaw = (n) => n.type === "rawHtml";

/** ctx: {schema, blocks}. */
export function createConvertContext(defs = getHtmlBlocks()) {
  return { schema: getSchema(createHtmlBlockSchemaExtensions(defs)), blocks: defs };
}

/** Source markup must never be able to smuggle a raw node (e.g. via data-raw-html). */
function containsRawNode(node) {
  let found = node.type.name === "rawHtml" || node.type.name === "rawContainer";
  if (!found) {
    node.descendants((n) => {
      if (n.type.name === "rawHtml" || n.type.name === "rawContainer") found = true;
      return !found;
    });
  }
  return found;
}

function serializePmNode(node, schema) {
  return serializeDomNode(DOMSerializer.fromSchema(schema).serializeNode(node, { document: inertDocument() }));
}

/** Parse one element with the PM schema; accept only if it re-serialises identically. */
function tryParseBlock(el, ctx) {
  const wrapper = inertDocument().createElement("div");
  wrapper.appendChild(el.cloneNode(true));
  let slice;
  try {
    slice = PMDOMParser.fromSchema(ctx.schema).parseSlice(wrapper);
  } catch {
    return null;
  }
  if (slice.content.childCount !== 1) return null;
  const node = slice.content.firstChild;
  if (!node.type.spec.group?.split(" ").includes("htmlBlock")) return null;
  if (containsRawNode(node)) return null;
  return serializePmNode(node, ctx.schema) === serializeDomNode(el) ? node.toJSON() : null;
}

function mergeRaw(nodes) {
  const out = [];
  for (const n of nodes) {
    const last = out[out.length - 1];
    if (last && isRaw(last) && isRaw(n)) out[out.length - 1] = raw(String(last.attrs.html) + String(n.attrs.html));
    else out.push(n);
  }
  return out;
}

/** Structural conversion WITHOUT the safety net; tests use it to prove the invariant holds by construction. */
export function htmlToDocStrict(html, ctx) {
  const tpl = inertDocument().createElement("template");
  tpl.innerHTML = html;
  const selectors = [...ctx.blocks.map((b) => b.selector), CORE_RICH_TEXT_SELECTOR].join(", ");
  const walk = (parent) => {
    const out = [];
    parent.childNodes.forEach((child) => {
      if (child.nodeType === 1) {
        const accepted = child.matches(selectors) ? tryParseBlock(child, ctx) : null;
        if (accepted) {
          out.push(accepted);
          return;
        }
        if (child.namespaceURI === HTML_NS && CONTAINER.has(child.localName) && child.querySelector(selectors)) {
          const content = walk(child);
          if (!content.every(isRaw)) {
            out.push({ type: "rawContainer", attrs: { open: openTag(child), close: closeTag(child) }, content });
            return;
          }
        }
      }
      out.push(raw(serializeDomNode(child)));
    });
    return mergeRaw(out);
  };
  const content = mergeRaw(walk(tpl.content));
  return { type: "doc", content: content.length ? content : [raw("")] };
}

export function htmlToDoc(html, ctx) {
  const doc = htmlToDocStrict(html, ctx);
  // Safety net: should be unreachable, but never let a conversion bug lose bytes.
  const normalized = normalizeHtml(html);
  if (docToHtml(doc, ctx.schema) !== normalized) return { type: "doc", content: [raw(normalized)] };
  return doc;
}

const serialized = new WeakMap();

function nodeToHtml(node, schema) {
  if (node.type.name === "rawHtml") return String(node.attrs.html ?? "");
  if (node.type.name === "rawContainer") {
    let inner = "";
    node.forEach((c) => {
      inner += nodeToHtml(c, schema);
    });
    return String(node.attrs.open ?? "") + inner + String(node.attrs.close ?? "");
  }
  // PM nodes are immutable, so unchanged nodes keep their cached markup across edits.
  let html = serialized.get(node);
  if (html === undefined) {
    html = serializePmNode(node, schema);
    serialized.set(node, html);
  }
  return html;
}

/** Serialise a doc (PM node or JSON) back to HTML. */
export function docToHtml(doc, schema) {
  const root = doc instanceof PMNode ? doc : PMNode.fromJSON(schema, doc);
  let out = "";
  root.forEach((c) => {
    out += nodeToHtml(c, schema);
  });
  return out;
}

// ---- node views (vanilla DOM; the React views are gone)

/** <section class="a b" ...> becomes <section.a> */
export function tagLabel(open) {
  const m = /^<\s*([a-zA-Z][\w:-]*)(?:[^>]*?\sclass\s*=\s*["']\s*([^\s"']+))?/.exec(open);
  if (!m) return "<?>";
  return `<${m[1].toLowerCase()}${m[2] ? `.${m[2]}` : ""}>`;
}

function el(tag, props = {}, ...kids) {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(props)) {
    if (k === "text") e.textContent = v;
    else e.setAttribute(k, v);
  }
  e.append(...kids);
  return e;
}

/** Raw markup: a framed, script-free preview with a source toggle. Whitespace-only blocks show no chrome. */
export function rawHtmlView() {
  return ({ node, getPos, editor }) => {
    let current = node;
    let editing = false;
    const dom = el("div", { class: "hm-ed__raw", contenteditable: "false", "data-raw-html-view": "" });
    const head = el("div", { class: "hm-ed__rawhead" }, el("span", { text: "HTML" }));
    const toggle = el("button", { type: "button", class: "hm-btn hm-btn--sm hm-btn--ghost", "aria-expanded": "false", text: "Edit HTML" });
    head.append(toggle);
    const body = el("div", { class: "hm-ed__rawbody" });
    dom.append(head, body);

    const html = () => String(current.attrs.html ?? "");
    const render = () => {
      const h = html();
      const blank = !editing && h !== "" && h.trim() === "";
      dom.hidden = blank;
      dom.setAttribute("data-raw-html-view", blank ? "ws" : "");
      body.replaceChildren();
      if (editing) {
        const ta = el("textarea", { class: "hm-ed__rawsrc", "aria-label": "Raw HTML", spellcheck: "false" });
        ta.value = h;
        ta.addEventListener("input", () => {
          const pos = getPos();
          if (typeof pos === "number") editor.view.dispatch(editor.state.tr.setNodeMarkup(pos, undefined, { html: ta.value }));
        });
        body.append(ta);
      } else {
        // No scripts run in the preview: sandboxed without allow-scripts, and the admin CSP forbids inline script anyway.
        const frame = el("iframe", { class: "hm-ed__rawpreview", sandbox: "", title: "Preview of this HTML", tabindex: "-1" });
        frame.srcdoc = h;
        body.append(frame);
      }
      toggle.textContent = editing ? "Hide HTML" : "Edit HTML";
      toggle.setAttribute("aria-expanded", String(editing));
    };
    toggle.addEventListener("click", () => {
      editing = !editing;
      render();
    });
    render();
    return {
      dom,
      stopEvent: (e) => dom.contains(e.target) && e.target !== dom,
      ignoreMutation: () => true,
      update(next) {
        if (next.type !== current.type) return false;
        current = next;
        if (!editing) render();
        return true;
      },
    };
  };
}

/** Dashed frame around a wrapper element whose children are edited as blocks. */
export function rawContainerView() {
  return ({ node }) => {
    const dom = el("div", { class: "hm-ed__container" });
    const label = el("div", { class: "hm-ed__tag", contenteditable: "false", text: tagLabel(String(node.attrs.open ?? "")) });
    const contentDOM = el("div");
    dom.append(label, contentDOM);
    return { dom, contentDOM };
  };
}

export const RawHtmlWithView = RawHtml.extend({ addNodeView: () => rawHtmlView() });
export const RawContainerWithView = RawContainer.extend({ addNodeView: () => rawContainerView() });
