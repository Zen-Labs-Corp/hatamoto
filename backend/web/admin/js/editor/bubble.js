// The bubble menu: a small toolbar over a text selection with the four things
// people reach for first (bold, italic, link, inline code). Positioning, show
// and hide come from @tiptap/extension-bubble-menu (floating-ui); this file
// supplies the toolbar and when it is wanted.
import { Extension } from "@tiptap/core";
import TiptapBubbleMenu from "@tiptap/extension-bubble-menu";
import { glyph } from "@hm/editor/slash";

/** The buttons, in order: {title, icon, separatorBefore?, run(editor, ctx), active(editor)}. Bold, italic and code, then link. */
export const BUBBLE_ITEMS = [
  { title: "Bold", icon: "bold", run: (e) => e.chain().focus().toggleBold().run(), active: (e) => e.isActive("bold") },
  { title: "Italic", icon: "italic", run: (e) => e.chain().focus().toggleItalic().run(), active: (e) => e.isActive("italic") },
  { title: "Inline code", icon: "code", run: (e) => e.chain().focus().toggleCode().run(), active: (e) => e.isActive("code") },
  { title: "Link", icon: "link", separatorBefore: true, run: (_e, ctx) => ctx.askLink?.(), active: (e) => e.isActive("link") },
];

/** Whether a selection earns a bubble: a non-empty text selection outside code blocks. */
export function wantsBubble(state) {
  const { selection } = state;
  if (selection.empty || selection.node) return false;
  return !selection.$from.parent.type.spec.code;
}

// The editor is not on the extension context yet when the toolbar is built
// (addExtensions runs first), so the toolbar learns it from shouldShow, before
// it can be shown or clicked.
function createToolbar(ctx) {
  let editor = null;
  const bar = document.createElement("div");
  bar.className = "hm-bubble";
  bar.setAttribute("role", "toolbar");
  bar.setAttribute("aria-label", "Selection formatting");
  const buttons = BUBBLE_ITEMS.map((it) => {
    const b = document.createElement("button");
    b.type = "button";
    b.className = "hm-ed__btn";
    b.append(glyph(it.icon));
    b.title = it.title;
    b.setAttribute("tabindex", "-1");
    b.setAttribute("aria-label", it.title);
    b.setAttribute("aria-pressed", "false");
    b.addEventListener("mousedown", (e) => e.preventDefault());
    b.addEventListener("click", () => it.run(editor, ctx));
    if (it.separatorBefore) {
      const sep = document.createElement("span");
      sep.className = "hm-bubble__sep";
      sep.setAttribute("aria-hidden", "true");
      bar.append(sep);
    }
    bar.append(b);
    return b;
  });
  buttons[0].setAttribute("tabindex", "0");
  bar.addEventListener("keydown", (e) => {
    const i = buttons.indexOf(document.activeElement);
    const move = { ArrowRight: i + 1, ArrowLeft: i - 1, Home: 0, End: buttons.length - 1 }[e.key];
    if (i >= 0 && move !== undefined) {
      e.preventDefault();
      const to = (move + buttons.length) % buttons.length;
      buttons.forEach((b, j) => b.setAttribute("tabindex", j === to ? "0" : "-1"));
      buttons[to].focus();
      return;
    }
    if (e.key === "Escape") {
      e.stopPropagation();
      editor?.commands.focus();
    }
  });
  const refresh = (ed) => {
    editor = ed;
    BUBBLE_ITEMS.forEach((it, i) => buttons[i].setAttribute("aria-pressed", String(!!it.active(editor))));
  };
  return { bar, refresh };
}

/** options: {ctx}. The toolbar is attached next to the editor while a selection wants it. */
export const BubbleMenu = Extension.create({
  name: "hmBubbleMenu",
  addOptions: () => ({ ctx: {} }),
  addExtensions() {
    const { bar, refresh } = createToolbar(this.options.ctx);
    return [
      TiptapBubbleMenu.configure({
        pluginKey: "hmBubble",
        element: bar,
        updateDelay: 100,
        options: { placement: "top", offset: 6 },
        shouldShow: ({ editor, view, state, element }) => {
          if (!wantsBubble(state) || (!view.hasFocus() && !element.contains(document.activeElement))) return false;
          element.removeAttribute("tabindex"); // the plugin makes the bar a Tab stop of its own; the buttons are the stops
          refresh(editor);
          return true;
        },
      }),
    ];
  },
});
