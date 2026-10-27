// The shared editor kit. v1-12's page editor uses it for richtext fields, the html
// section's Visual tab and the canvas rich-text popover; v1-13's blog editor
// reuses mountRichText (toolbar, links with URL validation, images from the media
// library) and adds its own menus on top.
//
// Every editor is an enhancement of a <textarea>: the textarea keeps the value
// the form posts, is hidden while the editor is mounted and is shown again on
// destroy(). Without JavaScript the textarea is simply the editor.
import { Editor, getSchema } from "@tiptap/core";
import { createEditorExtensions, normalizeEmpty } from "@hm/editor/extensions";
import { validateLinkURL } from "@hm/editor/links";
import { proseExtensions, SingleBlock, flattenBlocks, canRoundTripProse } from "@hm/editor/prose";
import {
  RawContainerWithView, RawHtmlWithView, RawContainer, RawHtml, createHtmlBlockSchemaExtensions, createConvertContext,
  docToHtml, htmlToDoc, normalizeHtml, getHtmlBlocks,
} from "@hm/editor/htmlblocks";

let uid = 0;
const nextId = (p) => `${p}-${++uid}`;

function h(tag, props = {}, ...kids) {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(props)) {
    if (v === false || v === null || v === undefined) continue;
    if (k === "text") e.textContent = v;
    else e.setAttribute(k, v === true ? "" : v);
  }
  e.append(...kids);
  return e;
}

/** Tell the form (and the canvas host) the textarea changed. */
function emitInput(textarea) {
  textarea.dispatchEvent(new Event("input", { bubbles: true }));
}

// ---- toolbar -------------------------------------------------------------------

/** One row of buttons with a roving tab stop (arrow keys, Home, End), like a native toolbar. */
function toolbar(label, specs) {
  const bar = h("div", { class: "hm-ed__bar", role: "toolbar", "aria-label": label });
  const buttons = specs.map((s) => {
    const b = h("button", { type: "button", class: "hm-ed__btn", title: s.title, "aria-label": s.title, text: s.label, "aria-pressed": s.active ? "false" : false, tabindex: "-1" });
    b.addEventListener("mousedown", (e) => e.preventDefault()); // keep the selection in the editor
    b.addEventListener("click", () => s.run());
    bar.append(b);
    return b;
  });
  const focusAt = (i) => {
    buttons.forEach((b, j) => b.setAttribute("tabindex", j === i ? "0" : "-1"));
    buttons[i].focus();
  };
  buttons[0]?.setAttribute("tabindex", "0");
  bar.addEventListener("keydown", (e) => {
    const i = buttons.indexOf(document.activeElement);
    if (i < 0) return;
    const move = { ArrowRight: i + 1, ArrowLeft: i - 1, Home: 0, End: buttons.length - 1 }[e.key];
    if (move === undefined) return;
    e.preventDefault();
    focusAt((move + buttons.length) % buttons.length);
  });
  bar.addEventListener("focusin", (e) => {
    const i = buttons.indexOf(e.target);
    if (i >= 0) buttons.forEach((b, j) => b.setAttribute("tabindex", j === i ? "0" : "-1"));
  });
  const refresh = (editor) => {
    specs.forEach((s, i) => {
      if (s.active) buttons[i].setAttribute("aria-pressed", String(!!s.active(editor)));
      if (s.can) buttons[i].disabled = !s.can(editor);
    });
  };
  return { bar, refresh };
}

/** A one-field inline form under the toolbar (link address, image description). */
export function askRow(label, { validate, onApply, onRemove } = {}) {
  const id = nextId("hm-ed-ask");
  const input = h("input", { class: "hm-field__control", type: "text", id, autocomplete: "off", spellcheck: "false" });
  const err = h("p", { class: "hm-ed__error", role: "alert", id: `${id}-err` });
  err.hidden = true;
  const apply = h("button", { type: "submit", class: "hm-btn hm-btn--sm hm-btn--primary", text: "Apply" });
  const cancel = h("button", { type: "button", class: "hm-btn hm-btn--sm", text: "Cancel" });
  const remove = onRemove ? h("button", { type: "button", class: "hm-btn hm-btn--sm hm-btn--ghost", text: "Remove" }) : null;
  const form = h("form", { class: "hm-ed__linkform", novalidate: true }, h("label", { class: "hm-field__label", for: id, text: label }), input, apply, ...(remove ? [remove] : []), cancel, err);
  form.hidden = true;
  let back = null;
  const close = () => {
    form.hidden = true;
    err.hidden = true;
    input.removeAttribute("aria-invalid");
    back?.();
  };
  form.addEventListener("submit", (e) => {
    e.preventDefault();
    e.stopPropagation();
    const r = validate ? validate(input.value) : { ok: true, value: input.value };
    if (!r.ok) {
      err.textContent = r.error;
      err.hidden = false;
      input.setAttribute("aria-invalid", "true");
      input.setAttribute("aria-describedby", err.id);
      input.focus();
      return;
    }
    onApply(r.value ?? r.href);
    close();
  });
  cancel.addEventListener("click", close);
  remove?.addEventListener("click", () => {
    onRemove();
    close();
  });
  form.addEventListener("keydown", (e) => {
    if (e.key === "Escape") {
      e.stopPropagation();
      close();
    }
  });
  return {
    form,
    open(value, restoreFocus) {
      back = restoreFocus;
      input.value = value ?? "";
      form.hidden = false;
      err.hidden = true;
      input.focus();
      input.select();
    },
  };
}

// ---- media picker bridge ----------------------------------------------------------

/**
 * The library picker (content.js) works on a button carrying
 * data-hm-media-picker=<input id> and writes the chosen address into that input,
 * firing "change". This is that pair, kept out of sight, so the editor reuses the
 * one picker dialog instead of growing a second one.
 */
function mediaBridge(root, onPick) {
  const input = h("input", { type: "hidden", id: nextId("hm-ed-media") });
  const button = h("button", { type: "button", "data-hm-media-picker": input.id, hidden: true, tabindex: "-1", "aria-hidden": "true", text: "Choose image" });
  input.addEventListener("change", () => {
    if (input.value) onPick(input.value);
    input.value = "";
  });
  root.append(input, button);
  return () => button.click();
}

// ---- rich text ---------------------------------------------------------------------

function richSpecs(editor, { askLink, askImage, withImage }) {
  const chain = () => editor.chain().focus();
  const s = (title, label, run, active, can) => ({ title, label, run, active, can });
  return [
    s("Bold", "B", () => chain().toggleBold().run(), (e) => e.isActive("bold")),
    s("Italic", "I", () => chain().toggleItalic().run(), (e) => e.isActive("italic")),
    s("Strikethrough", "S", () => chain().toggleStrike().run(), (e) => e.isActive("strike")),
    s("Inline code", "</>", () => chain().toggleCode().run(), (e) => e.isActive("code")),
    ...[2, 3, 4].map((level) => s(`H${level} heading`, `H${level}`, () => chain().toggleHeading({ level }).run(), (e) => e.isActive("heading", { level }))),
    s("Bulleted list", "• List", () => chain().toggleBulletList().run(), (e) => e.isActive("bulletList")),
    s("Numbered list", "Numbered", () => chain().toggleOrderedList().run(), (e) => e.isActive("orderedList")),
    s("Quote", "Quote", () => chain().toggleBlockquote().run(), (e) => e.isActive("blockquote")),
    s("Code block", "Code", () => chain().toggleCodeBlock().run(), (e) => e.isActive("codeBlock")),
    s("Link", "Link", askLink, (e) => e.isActive("link")),
    ...(withImage ? [s("Insert image from the library", "Image", askImage)] : []),
    s("Insert table", "Table", () => chain().insertTable({ rows: 3, cols: 3, withHeaderRow: true }).run()),
    s("Horizontal rule", "Rule", () => chain().setHorizontalRule().run()),
    s("Undo", "Undo", () => chain().undo().run(), null, (e) => e.can().undo()),
    s("Redo", "Redo", () => chain().redo().run(), null, (e) => e.can().redo()),
  ];
}

/**
 * Turns textarea into a Tiptap rich text editor. opts: {placeholder, siteHosts,
 * media (default true: image button), onChange(html), container (append the editor
 * there instead of after the textarea), label (accessible name),
 * extensions (extra Tiptap extensions appended to the shared set),
 * toolbar (specs) => specs: reorders, drops or adds toolbar buttons,
 * image (replaces the Image extension: the blog editor's captioned image),
 * bar ("hidden": the toolbar stays mounted but is not shown until setBar(true); its
 * buttons are still clicked by code such as Ctrl+K and the slash commands)}.
 * Returns {editor, element, getHTML, setHTML, focus, setBar, destroy}.
 */
export function mountRichText(textarea, opts = {}) {
  const wrap = h("div", { class: "hm-ed" });
  if (opts.bar === "hidden") wrap.dataset.hmBar = "hidden";
  const content = h("div", { class: "hm-ed__content" });
  const label = opts.label || textarea.labels?.[0]?.textContent?.trim() || "Rich text";
  let lastEmitted = normalizeEmpty(textarea.value);
  let editor;
  let link;
  let image;
  // Image flow: pick from the library, then ask for the description (alt text), then insert.
  let pendingSrc = "";
  const pick =
    opts.media === false
      ? null
      : mediaBridge(wrap, (src) => {
          pendingSrc = src;
          image.open("", () => editor.commands.focus());
        });
  image = askRow("Image description (leave empty for decorative images)", {
    onApply: (alt) => editor.chain().focus().setImage({ src: pendingSrc, alt }).run(),
  });
  const askLink = () => {
    const current = editor.getAttributes("link").href ?? "";
    link.open(current, () => editor.commands.focus());
  };
  link = askRow("Link address", {
    validate: validateLinkURL,
    onApply: (href) => editor.chain().focus().extendMarkRange("link").setLink({ href }).run(),
    onRemove: () => editor.chain().focus().extendMarkRange("link").unsetLink().run(),
  });
  const bar = { current: null };
  editor = new Editor({
    injectCSS: false,
    element: content,
    extensions: [...createEditorExtensions({ placeholder: opts.placeholder ?? "", siteHosts: opts.siteHosts ?? [], ...(opts.image ? { image: opts.image } : {}) }), ...(opts.extensions ?? [])],
    content: textarea.value,
    editorProps: { attributes: { role: "textbox", "aria-multiline": "true", "aria-label": label, class: "prose" } },
    onTransaction: () => bar.current?.refresh(editor),
    onUpdate: ({ editor: ed }) => {
      const html = normalizeEmpty(ed.getHTML());
      if (html === lastEmitted) return;
      lastEmitted = html;
      textarea.value = html;
      emitInput(textarea);
      opts.onChange?.(html);
    },
  });
  const specs = richSpecs(editor, {
    askLink,
    askImage: () => pick?.(),
    withImage: opts.media !== false,
  });
  bar.current = toolbar(`${label} formatting`, opts.toolbar ? opts.toolbar(specs) : specs);
  wrap.append(bar.current.bar, link.form, image.form, content);
  if (opts.container) opts.container.append(wrap);
  else textarea.after(wrap);
  textarea.hidden = true;
  bar.current.refresh(editor);
  return {
    editor,
    element: wrap,
    getHTML: () => normalizeEmpty(editor.getHTML()),
    /** Pushes an external value in without echoing it back out. */
    setHTML(html) {
      const next = normalizeEmpty(html ?? "");
      if (next === lastEmitted || next === normalizeEmpty(editor.getHTML())) {
        lastEmitted = next;
        return;
      }
      lastEmitted = next;
      editor.chain().setMeta("addToHistory", false).setContent(html ?? "", { emitUpdate: false }).run();
    },
    focus: () => editor.commands.focus(),
    /** Shows or hides a toolbar mounted with bar: "hidden". */
    setBar(on) {
      if (on) delete wrap.dataset.hmBar;
      else wrap.dataset.hmBar = "hidden";
    },
    destroy() {
      editor.destroy();
      wrap.remove();
      textarea.hidden = false;
    },
  };
}

// ---- html section: Code / Visual / Preview (/ Fields) -----------------------------------

/**
 * Visual editor for html sections, over the textarea. Opening it never writes:
 * only real document edits do, so untouched sections stay byte-identical.
 */
function mountVisual(textarea, host) {
  const defs = getHtmlBlocks();
  const base = createHtmlBlockSchemaExtensions(defs);
  const ctx = { schema: getSchema(base), blocks: defs };
  const extensions = base.map((e) => (e === RawHtml ? RawHtmlWithView : e === RawContainer ? RawContainerWithView : e));
  let lastHtml = null;
  const editor = new Editor({
    injectCSS: false,
    element: host,
    extensions,
    content: htmlToDoc(textarea.value, ctx),
    editorProps: { attributes: { role: "textbox", "aria-multiline": "true", "aria-label": "Visual editor", class: "prose" } },
    onUpdate: ({ editor: ed }) => {
      const html = docToHtml(ed.state.doc, ed.schema);
      lastHtml = html;
      textarea.value = html;
      emitInput(textarea);
    },
  });
  return {
    editor,
    /** Re-reads the textarea (typed in Code, or changed by the canvas); never emits and is not undoable. */
    sync() {
      const body = textarea.value;
      if (body === lastHtml) return;
      const current = lastHtml ?? docToHtml(editor.state.doc, editor.schema);
      const next = normalizeHtml(body);
      if (next === current) {
        lastHtml = current;
        return;
      }
      lastHtml = next;
      editor.chain().setMeta("addToHistory", false).setContent(htmlToDoc(body, ctx), { emitUpdate: false }).run();
    },
    destroy: () => editor.destroy(),
  };
}

/**
 * Wraps an html textarea in Code / Visual / Preview tabs (and Fields when
 * opts.fields(container, textarea) is given: the canvas panel). Returns {select,
 * destroy}. Code is the textarea itself, so the no-JavaScript form is unchanged.
 */
export function mountHtmlSection(textarea, opts = {}) {
  const id = nextId("hm-hs");
  const tabs = [{ key: "code", label: "Code" }, { key: "visual", label: "Visual" }, { key: "preview", label: "Preview" }];
  if (opts.fields) tabs.unshift({ key: "fields", label: "Fields" });
  const wrap = h("div", { class: "hm-ed hm-ed--html" });
  const list = h("div", { class: "hm-ed__tabs", role: "tablist", "aria-label": "How to edit this HTML" });
  const panels = {};
  const tabEls = {};
  for (const t of tabs) {
    const tab = h("button", { type: "button", class: "hm-ed__btn hm-ed__tab", role: "tab", id: `${id}-tab-${t.key}`, "aria-controls": `${id}-panel-${t.key}`, "aria-selected": "false", tabindex: "-1", text: t.label });
    const panel = h("div", { role: "tabpanel", id: `${id}-panel-${t.key}`, "aria-labelledby": tab.id, hidden: true });
    tabEls[t.key] = tab;
    panels[t.key] = panel;
    list.append(tab);
    wrap.append(panel);
  }
  wrap.prepend(list);
  textarea.after(wrap);
  panels.code.append(textarea);

  let visual = null;
  let fields = null;
  let preview = null;
  let current = null;
  const select = (key, focus = false) => {
    if (current === "visual" && key !== "visual") visual?.sync?.();
    current = key;
    for (const t of tabs) {
      const on = t.key === key;
      tabEls[t.key].setAttribute("aria-selected", String(on));
      tabEls[t.key].setAttribute("tabindex", on ? "0" : "-1");
      panels[t.key].hidden = !on;
    }
    if (key === "visual") {
      if (!visual) visual = mountVisual(textarea, panels.visual);
      else visual.sync();
    } else if (key === "preview") {
      if (!preview) {
        // Sandboxed without allow-scripts, and the admin CSP forbids inline script: the page's own scripts never run here.
        preview = h("iframe", { class: "hm-ed__preview", sandbox: "", title: "HTML preview" });
        panels.preview.append(preview);
      }
      preview.srcdoc = textarea.value;
    } else if (key === "fields") {
      fields?.destroy?.();
      panels.fields.replaceChildren();
      fields = opts.fields(panels.fields, textarea);
    }
    if (focus) tabEls[key].focus();
  };
  list.addEventListener("click", (e) => {
    const t = e.target.closest?.('[role="tab"]');
    if (t) select(tabs.find((x) => tabEls[x.key] === t).key);
  });
  list.addEventListener("keydown", (e) => {
    const i = tabs.findIndex((t) => tabEls[t.key] === document.activeElement);
    if (i < 0) return;
    const to = { ArrowRight: i + 1, ArrowLeft: i - 1, Home: 0, End: tabs.length - 1 }[e.key];
    if (to === undefined) return;
    e.preventDefault();
    select(tabs[(to + tabs.length) % tabs.length].key, true);
  });
  // A change made elsewhere (the canvas edited a text node) refreshes the visible tab.
  textarea.addEventListener("hm:external", () => {
    if (current === "visual") visual?.sync();
    else if (current === "preview") preview.srcdoc = textarea.value;
    else if (current === "fields") select("fields");
  });
  select(opts.fields ? "fields" : "code");
  return {
    select,
    destroy() {
      visual?.destroy();
      fields?.destroy?.();
      wrap.after(textarea);
      wrap.remove();
    },
  };
}

// ---- prose regions (html Fields) ----------------------------------------------------

/**
 * A compact Rich Text editor for one prose region of an html field, over a
 * detached textarea holding the region's inner html. Block ids/classes are
 * preserved; inline regions are one paragraph. onChange(inner) may return false
 * to refuse the write, and the editor then re-shows what is stored.
 * An inline region may also keep marks: ["underline", "code"] (with their buttons), and take no
 * line break (hardBreak: false: a heading is one line).
 */
export function mountProseRegion(container, innerHtml, { inline = false, marks = [], hardBreak = true, onChange, label = "Rich text" }) {
  const ta = h("textarea");
  ta.value = innerHtml;
  const content = h("div", { class: "hm-ed__content" });
  const wrap = h("div", { class: "hm-ed" });
  let stored = innerHtml;
  let lastHtml = null;
  const editor = new Editor({
    injectCSS: false,
    element: content,
    extensions: inline ? [...proseExtensions({ inline, marks, hardBreak }), SingleBlock.configure({ hardBreak })] : proseExtensions(),
    content: innerHtml,
    editorProps: {
      transformPastedHTML: inline ? (hardBreak ? flattenBlocks : (html) => flattenBlocks(html).replace(/<br\s*\/?>/gi, " ")) : undefined,
      transformPastedText: inline ? (t) => t.replace(/\s*[\r\n]+\s*/g, " ") : undefined,
      attributes: { role: "textbox", "aria-multiline": "true", "aria-label": label, class: "prose" },
    },
    onUpdate: ({ editor: ed }) => {
      const out = ed.getHTML();
      lastHtml = out;
      if (onChange(out) === false) {
        lastHtml = stored;
        ed.chain().setMeta("addToHistory", false).setContent(stored, { emitUpdate: false }).run();
      } else {
        stored = out;
      }
    },
  });
  const chain = () => editor.chain().focus();
  const specs = [
    { title: "Bold", label: "B", run: () => chain().toggleBold().run(), active: (e) => e.isActive("bold") },
    { title: "Italic", label: "I", run: () => chain().toggleItalic().run(), active: (e) => e.isActive("italic") },
    ...(marks.includes("underline") ? [{ title: "Underline", label: "U", run: () => chain().toggleUnderline().run(), active: (e) => e.isActive("underline") }] : []),
    { title: "Strikethrough", label: "S", run: () => chain().toggleStrike().run(), active: (e) => e.isActive("strike") },
    ...(marks.includes("code") ? [{ title: "Code", label: "</>", run: () => chain().toggleCode().run(), active: (e) => e.isActive("code") }] : []),
    ...(inline
      ? []
      : [2, 3, 4].map((level) => ({ title: `H${level} heading`, label: `H${level}`, run: () => chain().toggleHeading({ level }).run(), active: (e) => e.isActive("heading", { level }) }))),
    ...(inline
      ? []
      : [
          { title: "Bulleted list", label: "• List", run: () => chain().toggleBulletList().run(), active: (e) => e.isActive("bulletList") },
          { title: "Numbered list", label: "Numbered", run: () => chain().toggleOrderedList().run(), active: (e) => e.isActive("orderedList") },
          { title: "Quote", label: "Quote", run: () => chain().toggleBlockquote().run(), active: (e) => e.isActive("blockquote") },
          { title: "Horizontal rule", label: "Rule", run: () => chain().setHorizontalRule().run() },
        ]),
    { title: "Link", label: "Link", run: () => link.open(editor.getAttributes("link").href ?? "", () => editor.commands.focus()), active: (e) => e.isActive("link") },
  ];
  const link = askRow("Link address", {
    validate: validateLinkURL,
    onApply: (href) => editor.chain().focus().extendMarkRange("link").setLink({ href }).run(),
    onRemove: () => editor.chain().focus().extendMarkRange("link").unsetLink().run(),
  });
  const tb = toolbar(`${label} formatting`, specs);
  editor.on("transaction", () => tb.refresh(editor));
  // inline regions (one field of a form): the editable comes first in the tab order, the toolbar right after it
  if (inline) wrap.append(content, tb.bar, link.form);
  else wrap.append(tb.bar, link.form, content);
  container.append(wrap);
  tb.refresh(editor);
  return {
    editor,
    /** Shows html from outside (an in-frame edit, undo) without emitting and without history. */
    setHTML(html) {
      if (html === lastHtml) return;
      const current = lastHtml ?? editor.getHTML();
      if (normalizeHtml(html) === normalizeHtml(current)) {
        lastHtml = current;
        return;
      }
      lastHtml = html;
      stored = html;
      editor.chain().setMeta("addToHistory", false).setContent(html, { emitUpdate: false }).run();
    },
    destroy() {
      editor.destroy();
      wrap.remove();
    },
  };
}

export { createConvertContext, canRoundTripProse };
