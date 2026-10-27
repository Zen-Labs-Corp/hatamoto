// Cleans pasted HTML (Google Docs, Word, web pages) down to the tag set the
// server's sanitiser keeps (entity/sanitize.go, the rich-text policy), before
// ProseMirror reads it. This is a convenience so the text arrives tidy: the
// server sanitiser stays authoritative and runs on every save.
import { Extension } from "@tiptap/core";
import { Plugin } from "@tiptap/pm/state";

/** Elements kept as they are (the sanitiser's allowlist, minus iframe which has its own rule). */
const KEEP = new Set([
  "h1", "h2", "h3", "h4", "h5", "h6", "p", "br", "hr", "div", "span", "ul", "ol", "li", "strong", "em", "b", "i", "u", "s",
  "blockquote", "code", "pre", "figure", "figcaption", "a", "img", "table", "thead", "tbody", "tfoot", "tr", "th", "td", "caption",
]);
/** Elements dropped together with what is inside them. */
const DROP = new Set(["script", "style", "head", "title", "meta", "link", "template", "noscript", "object", "embed", "iframe", "form", "input", "button", "select", "textarea", "svg", "canvas", "audio", "video", "xml"]);
/** Unknown block-level elements become divs, so their text stays apart from its neighbours. */
const BLOCKISH = new Set(["section", "article", "aside", "header", "footer", "main", "nav", "center", "address", "dl", "dt", "dd", "fieldset", "figure", "details", "summary", "hgroup"]);
const ATTRS = {
  a: ["href", "title"],
  img: ["src", "alt", "title", "width", "height"],
  td: ["colspan", "rowspan"],
  th: ["colspan", "rowspan"],
};

const safeHref = (v) => /^(https?:|mailto:|tel:|sms:|\/(?!\/)|#)/i.test(v.trim()) || !/^[a-z][a-z0-9+.-]*:/i.test(v.trim());
const safeSrc = (v) => /^https?:\/\//i.test(v.trim()) || /^\/(?!\/)/.test(v.trim());

function inertBody(html) {
  return new DOMParser().parseFromString(`<!doctype html><body>${html}`, "text/html").body;
}

const cssProp = (el, name) => {
  const m = new RegExp(`(?:^|;)\\s*${name}\\s*:\\s*([^;]+)`, "i").exec(el.getAttribute("style") ?? "");
  return m ? m[1].trim().toLowerCase() : "";
};

function wrap(el, tag) {
  const w = el.ownerDocument.createElement(tag);
  while (el.firstChild) w.appendChild(el.firstChild);
  el.appendChild(w);
}

/** Google Docs and Word say "bold" and "italic" with inline styles on spans. */
function styleToMarks(el) {
  if (el.localName !== "span") return;
  const weight = cssProp(el, "font-weight");
  if ((weight === "bold" || weight === "bolder" || Number(weight) >= 600) && !el.closest("h1, h2, h3, h4, h5, h6")) wrap(el, "strong");
  if (cssProp(el, "font-style") === "italic") wrap(el, "em");
  const deco = cssProp(el, "text-decoration") + cssProp(el, "text-decoration-line");
  if (deco.includes("line-through")) wrap(el, "s");
  if (deco.includes("underline") && !el.closest("a")) wrap(el, "u");
}

const isWordList = (el) => el.localName === "p" && (/^Mso ?List/i.test(el.getAttribute("class") ?? "") || /mso-list\s*:/i.test(el.getAttribute("style") ?? ""));
const BULLET = /^\s*(?:[\u00b7\u2022\u25aa\u25cf\u25cb\u00a7\u2043\u25e6\u2013-]|o(?=\s|\u00a0))\s*/;
const NUMBER = /^\s*(?:\d+|[a-zA-Z]|[ivxlcdm]+)[.)]\s*/i;

/** Word writes lists as paragraphs with a typed bullet; turn runs of them into real lists. */
function wordLists(body) {
  const doc = body.ownerDocument;
  const seen = new Set();
  for (const first of Array.from(body.querySelectorAll("p")).filter(isWordList)) {
    if (seen.has(first)) continue;
    const run = [];
    for (let n = first; n && isWordList(n); n = n.nextElementSibling) {
      run.push(n);
      seen.add(n);
    }
    const ordered = NUMBER.test((run[0].textContent ?? "").replace(/\u00a0/g, " ")) && !BULLET.test(run[0].textContent ?? "");
    const list = doc.createElement(ordered ? "ol" : "ul");
    first.before(list);
    for (const p of run) {
      for (const ignore of p.querySelectorAll('[style*="mso-list:Ignore" i], [style*="mso-list: Ignore" i]')) ignore.remove();
      const li = doc.createElement("li");
      while (p.firstChild) li.appendChild(p.firstChild);
      const text = li.firstChild;
      if (text && text.nodeType === 3) text.data = text.data.replace(ordered ? NUMBER : BULLET, "");
      list.appendChild(li);
      p.remove();
    }
  }
}

function clean(node, doc, verbatim) {
  for (const child of Array.from(node.childNodes)) {
    if (child.nodeType === 8) {
      child.remove(); // comments, including Word's conditional blocks
      continue;
    }
    if (child.nodeType !== 1) continue;
    const tag = child.localName;
    if (DROP.has(tag)) {
      child.remove();
      continue;
    }
    styleToMarks(child);
    clean(child, doc, verbatim);
    if (!KEEP.has(tag) && BLOCKISH.has(tag)) {
      retag(child, "div"); // keeps the line break the element stood for
      continue;
    }
    if (!KEEP.has(tag)) {
      child.replaceWith(...Array.from(child.childNodes)); // o:p, font, section, ...: keep the text
      continue;
    }
    for (const a of Array.from(child.attributes)) {
      const ok = (a.name === "class" && verbatim || (ATTRS[tag] ?? []).includes(a.name)) && (a.name !== "href" || safeHref(a.value)) && (a.name !== "src" || safeSrc(a.value));
      if (!ok) child.removeAttribute(a.name);
    }
    if (tag === "img" && !child.hasAttribute("src")) child.remove();
    else if (tag === "a" && !child.hasAttribute("href")) child.replaceWith(...Array.from(child.childNodes));
    else if (!verbatim && tag === "h1") retag(child, "h2");
    else if (!verbatim && (tag === "h5" || tag === "h6")) retag(child, "h4");
    else if (!verbatim && tag === "b") retag(child, "strong");
    else if (!verbatim && tag === "i") retag(child, "em");
  }
}

function retag(el, tag) {
  const n = el.ownerDocument.createElement(tag);
  for (const a of Array.from(el.attributes)) n.setAttribute(a.name, a.value);
  while (el.firstChild) n.appendChild(el.firstChild);
  el.replaceWith(n);
}

/** Collapses what clean-up leaves behind: empty spans, runs of spaces, empty paragraphs. */
function tidy(body) {
  for (const el of Array.from(body.querySelectorAll("span"))) el.replaceWith(...Array.from(el.childNodes));
  for (const el of Array.from(body.querySelectorAll("strong, em, u, s, b, i"))) {
    if (!el.textContent.trim() && !el.querySelector("img, br")) el.remove();
  }
  const walker = body.ownerDocument.createTreeWalker(body, 4 /* NodeFilter.SHOW_TEXT */);
  const texts = [];
  for (let t = walker.nextNode(); t; t = walker.nextNode()) texts.push(t);
  for (const t of texts) {
    if (t.parentElement?.closest("pre")) continue;
    t.data = t.data.replace(/[\u00a0\s]+/g, " ");
  }
  for (const p of Array.from(body.querySelectorAll("p, li, h2, h3, h4"))) {
    if (!p.textContent.trim() && !p.querySelector("img, br, hr")) p.remove();
  }
}

/**
 * Cleans pasted html. Google Docs' wrapper and Word's list paragraphs are
 * unwrapped, inline styles become bold/italic/underline/strike marks, every
 * other attribute, class and style goes, unknown elements keep their text,
 * scripts and embeds go entirely, h1 becomes h2 (the title is the page's H1).
 * verbatim (the html block): only what is unsafe or outside the tag set goes;
 * classes, headings and spacing are kept as written.
 */
export function cleanPastedHTML(html, { verbatim = false } = {}) {
  const body = inertBody(html);
  const doc = body.ownerDocument;
  if (!verbatim) {
    for (const g of body.querySelectorAll('b[id^="docs-internal-guid"]')) g.replaceWith(...Array.from(g.childNodes));
    wordLists(body);
  }
  clean(body, doc, verbatim);
  if (!verbatim) tidy(body);
  return body.innerHTML;
}

/** ProseMirror: runs cleanPastedHTML on every HTML paste. */
export const PasteClean = Extension.create({
  name: "pasteClean",
  addProseMirrorPlugins() {
    return [new Plugin({ props: { transformPastedHTML: (html) => cleanPastedHTML(html) } })];
  },
});
