// The slash menu: type "/" at the start of a line (or after a space) and a list
// of blocks follows the caret. Built on @tiptap/suggestion, which finds the
// "/query", tracks the range and handles Escape; this file supplies the list,
// the filter and the listbox (role=listbox, aria-activedescendant).
//   SLASH_COMMANDS / filterCommands  the list and its filter (pure: tested without a menu)
//   runSlashCommand                  removes "/query" and runs a command's chain
//   SlashMenu                        the Tiptap extension
import { Extension } from "@tiptap/core";
import Suggestion from "@tiptap/suggestion";
import { PluginKey, TextSelection } from "@tiptap/pm/state";

// 24x24 outline glyphs (Lucide, ISC licence): the same drawing the server icons use, as the editor
// menus are built in the browser. bubble.js shares glyph().
const ICON_PATHS = {
  bold: '<path d="M14 12a4 4 0 0 0 0-8H6v8"/><path d="M15 20a4 4 0 0 0 0-8H6v8Z"/>',
  italic: '<line x1="19" x2="10" y1="4" y2="4"/><line x1="14" x2="5" y1="20" y2="20"/><line x1="15" x2="9" y1="4" y2="20"/>',
  code: '<polyline points="16 18 22 12 16 6"/><polyline points="8 6 2 12 8 18"/>',
  link: '<path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/><path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/>',
  text: '<path d="M13 4v16"/><path d="M17 4v16"/><path d="M19 4H9.5a4.5 4.5 0 0 0 0 9H13"/>',
  heading: '<path d="M6 12h12"/><path d="M6 20V4"/><path d="M18 20V4"/>',
  list: '<line x1="8" x2="21" y1="6" y2="6"/><line x1="8" x2="21" y1="12" y2="12"/><line x1="8" x2="21" y1="18" y2="18"/><line x1="3" x2="3.01" y1="6" y2="6"/><line x1="3" x2="3.01" y1="12" y2="12"/><line x1="3" x2="3.01" y1="18" y2="18"/>',
  "list-ordered": '<line x1="10" x2="21" y1="6" y2="6"/><line x1="10" x2="21" y1="12" y2="12"/><line x1="10" x2="21" y1="18" y2="18"/><path d="M4 6h1v4"/><path d="M4 10h2"/><path d="M6 18H4c0-1 2-2 2-3s-1-1.5-2-1"/>',
  quote: '<path d="M3 21c3 0 7-1 7-8V5c0-1.25-.756-2.017-2-2H4c-1.25 0-2 .75-2 1.972V11c0 1.25.75 2 2 2 1 0 1 0 1 1v1c0 1-1 2-2 2s-1 .008-1 1.031V20c0 1 0 1 1 1z"/><path d="M15 21c3 0 7-1 7-8V5c0-1.25-.757-2.017-2-2h-4c-1.25 0-2 .75-2 1.972V11c0 1.25.75 2 2 2h.75c0 2.25.25 4-2.75 4v3c0 1 0 1 1 1z"/>',
  table: '<path d="M12 3v18"/><rect width="18" height="18" x="3" y="3" rx="2"/><path d="M3 9h18"/><path d="M3 15h18"/>',
  image: '<rect width="18" height="18" x="3" y="3" rx="2" ry="2"/><circle cx="9" cy="9" r="2"/><path d="m21 15-3.086-3.086a2 2 0 0 0-2.828 0L6 21"/>',
  upload: '<path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="17 8 12 3 7 8"/><line x1="12" x2="12" y1="3" y2="15"/>',
  video: '<path d="m22 8-6 4 6 4V8Z"/><rect width="14" height="12" x="2" y="6" rx="2" ry="2"/>',
  help: '<circle cx="12" cy="12" r="10"/><path d="M9.09 9a3 3 0 0 1 5.83 1c0 2-3 3-3 3"/><path d="M12 17h.01"/>',
  info: '<circle cx="12" cy="12" r="10"/><path d="M12 16v-4"/><path d="M12 8h.01"/>',
  minus: '<path d="M5 12h14"/>',
};

/** A decorative inline SVG for a name in ICON_PATHS (aria-hidden; the control carries the name). */
export function glyph(name) {
  const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
  for (const [k, v] of Object.entries({ class: "hm-icon", viewBox: "0 0 24 24", fill: "none", stroke: "currentColor", "stroke-width": "1.5", "stroke-linecap": "round", "stroke-linejoin": "round", "aria-hidden": "true", focusable: "false" })) svg.setAttribute(k, v);
  svg.innerHTML = ICON_PATHS[name] ?? ""; // constants above, never user input
  return svg;
}

/** Chain command: puts the caret inside the last node of the given type (the FAQ question just inserted). */
const caretInto = (type) => ({ tr, dispatch }) => {
  let at = -1;
  tr.doc.descendants((n, pos) => {
    if (n.type.name === type) at = pos;
  });
  if (at < 0) return false;
  if (dispatch) tr.setSelection(TextSelection.near(tr.doc.resolve(at + 1)));
  return true;
};

/** ctx supplies what only the page knows: ctx.pickImage(), ctx.uploadImage(), ctx.askEmbed(). */
export const SLASH_COMMANDS = [
  { id: "text", label: "Text", group: "Basic", icon: "text", hint: "", description: "Plain paragraph", keywords: "paragraph p", run: (c) => c.setParagraph() },
  { id: "h2", label: "Heading 2", group: "Basic", icon: "heading", hint: "##", description: "Section heading", keywords: "title h2 heading", run: (c) => c.setHeading({ level: 2 }) },
  { id: "h3", label: "Heading 3", group: "Basic", icon: "heading", hint: "###", description: "Sub-section heading", keywords: "h3 heading subtitle", run: (c) => c.setHeading({ level: 3 }) },
  { id: "h4", label: "Heading 4", group: "Basic", icon: "heading", hint: "####", description: "Small heading", keywords: "h4 heading", run: (c) => c.setHeading({ level: 4 }) },
  { id: "bullets", label: "Bulleted list", group: "Basic", icon: "list", hint: "-", description: "A simple list", keywords: "ul list unordered bullet", run: (c) => c.toggleBulletList() },
  { id: "numbers", label: "Numbered list", group: "Basic", icon: "list-ordered", hint: "1.", description: "A list with numbers", keywords: "ol list ordered number", run: (c) => c.toggleOrderedList() },
  { id: "quote", label: "Quote", group: "Basic", icon: "quote", hint: ">", description: "Capture a quotation", keywords: "blockquote citation", run: (c) => c.toggleBlockquote() },
  { id: "code", label: "Code block", group: "Basic", icon: "code", hint: "```", description: "A snippet of code", keywords: "pre snippet", run: (c) => c.toggleCodeBlock() },
  { id: "table", label: "Table", group: "Basic", icon: "table", hint: "", description: "Rows and columns", keywords: "grid", run: (c) => c.insertTable({ rows: 3, cols: 3, withHeaderRow: true }) },
  { id: "image", label: "Image", group: "Media and blocks", icon: "image", hint: "", description: "From the media library", keywords: "picture photo library media", run: (c, ctx) => (ctx.pickImage?.(), c) },
  { id: "upload", label: "Upload image", group: "Media and blocks", icon: "upload", hint: "", description: "Choose a file from your computer", keywords: "picture photo file", run: (c, ctx) => (ctx.uploadImage?.(), c) },
  { id: "embed", label: "Video embed", group: "Media and blocks", icon: "video", hint: "", description: "YouTube or Vimeo", keywords: "youtube vimeo video", run: (c, ctx) => (ctx.askEmbed?.(), c) },
  { id: "divider", label: "Divider", group: "Basic", icon: "minus", hint: "---", description: "A horizontal line", keywords: "hr rule line separator", run: (c) => c.setHorizontalRule() },
  { id: "callout", label: "Callout", group: "Media and blocks", icon: "info", hint: "", description: "Make a note stand out", keywords: "note info tip warning aside", run: (c) => c.insertContent({ type: "callout", content: [{ type: "paragraph" }] }) },
  {
    id: "faq",
    label: "FAQ block", group: "Media and blocks", icon: "help", hint: "",
    description: "Questions and answers, published as FAQPage data",
    keywords: "questions answers faqpage schema",
    run: (c) =>
      c
        .insertContent({ type: "faq", content: [{ type: "faqItem", content: [{ type: "faqQuestion" }, { type: "faqAnswer", content: [{ type: "paragraph" }] }] }] })
        .command(caretInto("faqQuestion")),
  },
  { id: "html", label: "HTML block", group: "Media and blocks", icon: "code", hint: "", description: "Raw HTML, cleaned to the allowed tags", keywords: "raw code markup", run: (c) => c.insertContent([{ type: "htmlBlock", attrs: { html: "<p></p>" } }, { type: "paragraph" }]) },
];

/** The group titles in display order. */
export const SLASH_GROUPS = ["Basic", "Media and blocks"];

/** Items reordered so each group is contiguous (groups by first appearance), keeping their order inside a group. */
export function groupCommands(items) {
  const order = [];
  for (const c of items) if (!order.includes(c.group)) order.push(c.group);
  return order.flatMap((g) => items.filter((c) => c.group === g));
}

/** Commands matching the typed query: label prefix first, then any word in the label, id or keywords. */
export function filterCommands(query, commands = SLASH_COMMANDS) {
  const q = String(query ?? "").trim().toLowerCase();
  if (!q) return commands;
  const rank = (c) => {
    const label = c.label.toLowerCase();
    if (label.startsWith(q) || c.id.startsWith(q)) return 0;
    if (label.split(/\s+/).some((w) => w.startsWith(q))) return 1;
    if (`${c.id} ${c.keywords ?? ""}`.split(/\s+/).some((w) => w.startsWith(q))) return 2;
    return 3;
  };
  return commands.map((c, i) => ({ c, r: rank(c), i })).filter((x) => x.r < 3).sort((a, b) => a.r - b.r || a.i - b.i).map((x) => x.c);
}

/** Runs command at the slash range: removes "/query", then the command's chain. */
export function runSlashCommand(editor, command, range, ctx) {
  const chain = editor.chain().focus().deleteRange({ from: range.from, to: range.to });
  command.run(chain, ctx).run();
}

const key = new PluginKey("hmSlash");
const optionID = (i) => `hm-slash-opt-${i}`;

/** The listbox for one editor: show(props) paints, hide() removes, key(event) handles navigation. */
function createListbox(editor) {
  let menu = null;
  let list = null;
  let items = [];
  let index = 0;
  let command = null;

  const dom = () => editor.view.dom;
  function hide() {
    menu?.remove();
    menu = null;
    list = null;
    items = [];
    dom().removeAttribute("aria-activedescendant");
    dom().removeAttribute("aria-controls");
  }
  function choose(i) {
    const item = items[i];
    const run = command;
    if (!item || !run) return false;
    hide();
    run(item);
    return true;
  }
  const el = (tag, cls, text) => {
    const n = document.createElement(tag);
    n.className = cls;
    if (text !== undefined) n.textContent = text;
    return n;
  };
  function paint(clientRect) {
    const host = dom().parentElement;
    if (!menu) {
      menu = el("div", "hm-slash");
      list = el("div", "hm-slash__list");
      list.id = "hm-slash-list";
      list.setAttribute("role", "listbox");
      list.setAttribute("tabindex", "-1"); // a scrolling list needs a focusable stop for axe; real focus stays in the editor
      list.setAttribute("aria-label", "Insert block");
      const foot = el("div", "hm-slash__foot");
      for (const [keys, what] of [[["↑", "↓"], "choose"], [["Enter"], "insert"], [["Esc"], "close"]]) {
        const hint = el("span", "");
        for (const k of keys) hint.append(el("kbd", "", k));
        hint.append(` ${what}`);
        foot.append(hint);
      }
      menu.append(list, foot);
      menu.addEventListener("mousedown", (e) => e.preventDefault()); // keep the caret
      host.append(menu);
    }
    list.textContent = "";
    let group = null;
    let box = list;
    items.forEach((c, i) => {
      if (c.group !== group) {
        group = c.group;
        const gid = `hm-slash-g-${SLASH_GROUPS.indexOf(group)}`;
        box = el("div", "hm-slash__group");
        box.setAttribute("role", "group");
        box.setAttribute("aria-labelledby", gid);
        const title = el("div", "hm-slash__grouptitle", group);
        title.id = gid;
        box.append(title);
        list.append(box);
      }
      const row = el("div", "hm-slash__item");
      row.id = optionID(i);
      row.setAttribute("role", "option");
      row.setAttribute("tabindex", "-1");
      row.setAttribute("aria-selected", String(i === index));
      const tile = el("span", "hm-slash__glyph");
      tile.append(glyph(c.icon));
      const text = el("span", "hm-slash__text");
      text.append(el("span", "hm-slash__label", c.label), el("span", "hm-slash__desc", c.description));
      row.append(tile, text);
      if (c.hint) row.append(el("kbd", "hm-slash__hint", c.hint));
      row.addEventListener("click", () => choose(i));
      box.append(row);
    });
    dom().setAttribute("aria-controls", list.id);
    dom().setAttribute("aria-activedescendant", optionID(index));
    const rect = clientRect?.();
    if (rect) {
      const hostBox = host.getBoundingClientRect();
      menu.style.left = `${Math.max(0, rect.left - hostBox.left)}px`;
      menu.style.top = `${rect.bottom - hostBox.top + 4}px`;
    }
    list.querySelector('[aria-selected="true"]')?.scrollIntoView?.({ block: "nearest" });
  }

  return {
    /** props: the suggestion's {items, command, clientRect}. An empty list closes the menu. */
    show(props) {
      const next = groupCommands(props.items);
      const same = items.length === next.length && items.every((c, i) => c === next[i]);
      command = props.command;
      if (props.items.length === 0) return hide();
      items = next;
      index = same ? index : 0;
      paint(props.clientRect);
    },
    hide,
    key(event) {
      if (!menu) return false;
      const n = items.length;
      switch (event.key) {
        case "ArrowDown":
        case "ArrowUp":
          index = (index + (event.key === "ArrowDown" ? 1 : n - 1)) % n;
          paint();
          return true;
        case "Enter":
        case "Tab":
          return choose(index);
      }
      return false;
    },
  };
}

/** options: {ctx, commands (default: all of SLASH_COMMANDS; pass a subset when the editor lacks the block nodes)}. The menu is appended to the editor's parent element. */
export const SlashMenu = Extension.create({
  name: "slashMenu",
  addOptions: () => ({ ctx: {}, commands: SLASH_COMMANDS }),
  addProseMirrorPlugins() {
    const editor = this.editor;
    const ctx = this.options.ctx;
    return [
      Suggestion({
        editor,
        pluginKey: key,
        char: "/",
        // Default prefixes: start of a block or after a space.
        items: ({ query }) => filterCommands(query, this.options.commands),
        allow: ({ state, range }) => !state.doc.resolve(range.from).parent.type.spec.code,
        command: ({ editor: ed, range, props }) => runSlashCommand(ed, props, range, ctx),
        render: () => {
          const list = createListbox(editor);
          return {
            onStart: (props) => list.show(props),
            onUpdate: (props) => list.show(props),
            onKeyDown: ({ event }) => list.key(event),
            onExit: () => list.hide(),
          };
        },
      }),
    ];
  },
});
