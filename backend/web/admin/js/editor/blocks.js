// The blog editor's own nodes: callout, FAQ, video embed, html block and the
// image with a caption. Each serialises to markup the server's sanitiser keeps
// (classes on div, figure/figcaption, an allowlisted iframe) and the public
// theme can style; FAQ blocks are what the public page turns into FAQPage.
import { Extension, Node, mergeAttributes } from "@tiptap/core";
import Image from "@tiptap/extension-image";
import { Plugin, PluginKey } from "@tiptap/pm/state";
import { Decoration, DecorationSet } from "@tiptap/pm/view";
import { parseEmbedURL } from "@hm/editor/embed";
import { cleanPastedHTML } from "@hm/editor/paste";

export const Callout = Node.create({
  name: "callout",
  group: "block",
  content: "block+",
  defining: true,
  parseHTML: () => [{ tag: "div.callout" }],
  renderHTML: ({ HTMLAttributes }) => ["div", mergeAttributes(HTMLAttributes, { class: "callout" }), 0],
});

export const FaqQuestion = Node.create({
  name: "faqQuestion",
  content: "inline*",
  defining: true,
  parseHTML: () => [{ tag: "h3.faq-q", priority: 60 }],
  renderHTML: () => ["h3", { class: "faq-q" }, 0],
});

export const FaqAnswer = Node.create({
  name: "faqAnswer",
  content: "block+",
  defining: true,
  parseHTML: () => [{ tag: "div.faq-a" }],
  renderHTML: () => ["div", { class: "faq-a" }, 0],
});

export const FaqItem = Node.create({
  name: "faqItem",
  content: "faqQuestion faqAnswer",
  defining: true,
  parseHTML: () => [{ tag: "div.faq-item" }],
  renderHTML: () => ["div", { class: "faq-item" }, 0],
  addKeyboardShortcuts() {
    // Enter at the end of a question moves to its answer instead of splitting it.
    return {
      Enter: ({ editor }) => {
        const { $from, empty } = editor.state.selection;
        if (!empty || $from.parent.type.name !== "faqQuestion") return false;
        if ($from.parentOffset !== $from.parent.content.size) return false;
        return editor.commands.setTextSelection($from.after() + 2);
      },
    };
  },
});

export const Faq = Node.create({
  name: "faq",
  group: "block",
  content: "faqItem+",
  defining: true,
  parseHTML: () => [{ tag: "div.faq" }],
  renderHTML: () => ["div", { class: "faq" }, 0],
});

/** A YouTube or Vimeo player. Only allowlisted addresses are ever read or written. */
export const Embed = Node.create({
  name: "embed",
  group: "block",
  atom: true,
  draggable: true,
  addAttributes() {
    return { src: { default: null }, title: { default: "Video" } };
  },
  parseHTML() {
    return [
      {
        tag: "div.embed",
        getAttrs: (el) => {
          const f = el.querySelector("iframe");
          const e = f && parseEmbedURL(f.getAttribute("src") ?? "");
          return e && e.src === f.getAttribute("src") ? { src: e.src, title: e.title } : false;
        },
      },
    ];
  },
  renderHTML({ node }) {
    const e = parseEmbedURL(node.attrs.src);
    if (!e) return ["div", { class: "embed" }];
    return ["div", { class: "embed" }, ["iframe", { src: e.src, width: "560", height: "315", title: e.title, loading: "lazy", allowfullscreen: "true", frameborder: "0" }]];
  },
  addNodeView() {
    // The admin's CSP frames nothing but itself, so the editor shows a card; the
    // player appears on the public page and in the sandboxed preview.
    return ({ node }) => {
      const dom = document.createElement("div");
      dom.className = "embed hm-embed-card";
      dom.setAttribute("contenteditable", "false");
      const e = parseEmbedURL(node.attrs.src);
      dom.textContent = e ? `${e.title} (${e.id}). It plays on the published page.` : "Video";
      return { dom };
    };
  },
});

/**
 * Raw html the editor does not model. The source is cleaned to the sanitiser's
 * tag set (classes kept) when it is read and written, and shown as source in the
 * editor: nothing in it is ever rendered inside the admin.
 */
export const HtmlBlock = Node.create({
  name: "htmlBlock",
  group: "block",
  atom: true,
  draggable: true,
  addAttributes() {
    return { html: { default: "" } };
  },
  parseHTML() {
    return [{ tag: "div.html-block", getAttrs: (el) => ({ html: cleanPastedHTML(el.innerHTML, { verbatim: true }) }) }];
  },
  renderHTML({ node }) {
    // Built in an inert document so the markup does not load anything on the way out.
    const doc = document.implementation.createHTMLDocument("");
    const el = doc.createElement("div");
    el.className = "html-block";
    el.innerHTML = cleanPastedHTML(String(node.attrs.html ?? ""), { verbatim: true });
    return { dom: el };
  },
  addNodeView() {
    return ({ node, editor, getPos }) => {
      const dom = document.createElement("div");
      dom.className = "html-block hm-html-block";
      const label = document.createElement("label");
      label.className = "hm-field__label";
      label.textContent = "HTML block (cleaned to the allowed tags when saved)";
      const area = document.createElement("textarea");
      area.className = "hm-field__control hm-html-block__src";
      area.spellcheck = false;
      area.rows = 5;
      area.value = node.attrs.html;
      area.id = `hm-html-block-${Math.random().toString(36).slice(2, 8)}`;
      label.htmlFor = area.id;
      dom.append(label, area);
      area.addEventListener("input", () => {
        const pos = getPos();
        if (typeof pos !== "number") return;
        editor.view.dispatch(editor.state.tr.setNodeMarkup(pos, undefined, { html: area.value }));
      });
      return {
        dom,
        stopEvent: (e) => e.target === area,
        ignoreMutation: () => true,
        update: (n) => n.type === node.type,
      };
    };
  },
});

/**
 * Image with an optional caption, stored as <figure><img><figcaption> when it
 * has one and as a plain <img> when not. Alt text and caption are edited under
 * the picture.
 */
export const CaptionedImage = Image.extend({
  addAttributes() {
    return { ...this.parent?.(), caption: { default: "", rendered: false } };
  },
  parseHTML() {
    return [
      {
        tag: "figure",
        priority: 60,
        getAttrs: (el) => {
          const img = el.querySelector("img[src]");
          if (!img) return false;
          return { src: img.getAttribute("src"), alt: img.getAttribute("alt") ?? "", title: img.getAttribute("title"), caption: el.querySelector("figcaption")?.textContent?.trim() ?? "" };
        },
      },
      ...(this.parent?.() ?? []),
    ];
  },
  renderHTML({ node, HTMLAttributes }) {
    const img = ["img", mergeAttributes(this.options.HTMLAttributes, HTMLAttributes)];
    return node.attrs.caption ? ["figure", {}, img, ["figcaption", {}, node.attrs.caption]] : img;
  },
  addNodeView() {
    return ({ node, editor, getPos }) => {
      let current = node;
      const dom = document.createElement("figure");
      dom.className = "hm-fig";
      const img = document.createElement("img");
      img.src = node.attrs.src;
      img.alt = node.attrs.alt ?? "";
      const field = (cls, label, placeholder, key) => {
        const input = document.createElement("input");
        input.type = "text";
        input.className = `hm-field__control hm-fig__${cls}`;
        input.placeholder = placeholder;
        input.setAttribute("aria-label", label);
        input.value = node.attrs[key] ?? "";
        input.addEventListener("input", () => {
          const pos = getPos();
          if (typeof pos === "number") editor.view.dispatch(editor.state.tr.setNodeMarkup(pos, undefined, { ...current.attrs, [key]: input.value }));
        });
        input.addEventListener("keydown", (e) => {
          if (e.key === "Enter") {
            e.preventDefault();
            editor.commands.focus();
          }
        });
        return input;
      };
      const alt = field("alt", "Image description (alt text)", "Describe the image (leave empty for a decorative image)", "alt");
      const caption = field("caption", "Image caption", "Add a caption", "caption");
      dom.append(img, alt, caption);
      return {
        dom,
        stopEvent: (e) => e.target instanceof HTMLInputElement,
        ignoreMutation: () => true,
        update: (n) => {
          if (n.type !== current.type) return false;
          current = n;
          if (img.getAttribute("src") !== n.attrs.src) img.src = n.attrs.src;
          img.alt = n.attrs.alt ?? "";
          if (document.activeElement !== alt) alt.value = n.attrs.alt ?? "";
          if (document.activeElement !== caption) caption.value = n.attrs.caption ?? "";
          return true;
        },
      };
    };
  },
});

/** Marks the top-level block holding the caret (focus mode dims the others). */
export const CurrentBlock = Extension.create({
  name: "currentBlock",
  addProseMirrorPlugins() {
    return [
      new Plugin({
        key: new PluginKey("hmCurrentBlock"),
        props: {
          decorations(state) {
            const { $from } = state.selection;
            if ($from.depth < 1) return null;
            return DecorationSet.create(state.doc, [Decoration.node($from.before(1), $from.after(1), { class: "hm-be-current" })]);
          },
        },
      }),
    ];
  },
});
