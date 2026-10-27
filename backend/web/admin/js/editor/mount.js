// Tiptap/html editors on textareas, shared by the page editor and the builder shell:
// every richtext and html textarea of a schemaform gets its editor (js/editor/*,
// loaded on demand). The code lives here so the builder can mount editors on the
// forms it fetches without loading the page editor.
const $ = (root, sel) => root.querySelector(sel);
const $$ = (root, sel) => Array.from(root.querySelectorAll(sel));


export const editors = new WeakMap(); // textarea -> controller
const quiet = new Set(); // rich text controllers whose formatting toolbar the bar's Toolbar button shows
let toolbarOn = false;
export const isToolbarOn = () => toolbarOn;
let kitPromise = null;
export const loadKit = () => (kitPromise ??= Promise.all([import("@hm/editor/kit"), import("@hm/editor/fieldsview"), import("@hm/editor/slash"), import("@hm/editor/bubble")]).then(([kit, fields, slash, bubble]) => ({ ...kit, ...fields, slash, bubble })));

// Blocks the page editor's rich text does not have nodes for.
const SLASH_SKIP = new Set(["upload", "embed", "callout", "faq", "html"]);

/**
 * The same quiet rich text as the blog editor: the toolbar stays mounted but hidden
 * (Alt+Shift+T or the bar's Toolbar button shows it), with the slash and bubble menus for everyday formatting.
 */
function mountQuietRichText(kit, ta, opts) {
  let ctl;
  const click = (label) => ctl?.element.querySelector(`[aria-label="${label}"]`)?.click();
  const ctx = { askLink: () => click("Link"), pickImage: () => click("Insert image from the library") };
  ctl = kit.mountRichText(ta, {
    ...opts,
    bar: "hidden",
    placeholder: "Start writing. Type / for blocks.",
    extensions: [kit.slash.SlashMenu.configure({ ctx, commands: kit.slash.SLASH_COMMANDS.filter((c) => !SLASH_SKIP.has(c.id)) }), kit.bubble.BubbleMenu.configure({ ctx })],
  });
  ctl.element.addEventListener("keydown", (e) => {
    if (e.altKey && e.shiftKey && !e.ctrlKey && !e.metaKey && (e.code === "KeyT" || e.key?.toLowerCase() === "t")) {
      e.preventDefault();
      setToolbars(!toolbarOn);
    }
  });
  quiet.add(ctl);
  ctl.setBar(toolbarOn);
  return ctl;
}

/** Shows or hides the formatting toolbar of every rich text field, and the bar button's state. */
export function setToolbars(on) {
  toolbarOn = on;
  for (const ctl of quiet) {
    if (ctl.element.isConnected) ctl.setBar(on);
    else quiet.delete(ctl);
  }
  $(document, "[data-hm-pe-toolbar]")?.setAttribute("aria-pressed", String(on));
}

/** Upgrades every richtext and html textarea under root. canvasMode adds the html Fields tab. */
export async function enhanceEditors(root, { canvasMode = false } = {}) {
  const targets = $$(root, "textarea[data-hm-editor]").filter((t) => !editors.has(t) && !t.disabled);
  if (targets.length === 0) return;
  let kit;
  try {
    kit = await loadKit();
  } catch (err) {
    console.error("hatamoto: the editor could not be loaded; the plain fields still work", err);
    return;
  }
  for (const ta of targets) {
    if (editors.has(ta) || !ta.isConnected) continue;
    const label = ta.labels?.[0]?.textContent?.replace(/\s*\*?\s*\(required\)\s*$/, "").trim() || "Content";
    try {
      if (ta.dataset.hmEditor === "tiptap") editors.set(ta, mountQuietRichText(kit, ta, { label }));
      else if (ta.dataset.hmEditor === "html") editors.set(ta, kit.mountHtmlSection(ta, canvasMode ? { fields: kit.mountFieldsView } : {}));
    } catch (err) {
      console.error("hatamoto: an editor failed to start; the plain field is used", err);
      ta.hidden = false;
    }
  }
}
