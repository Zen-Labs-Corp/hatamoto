// The key -> action table (resolveKey), pure so it is unit-tested. Events from text controls
// are never taken, except Ctrl/Cmd+S; those controls have their own undo. The shortcut sheet
// dialog is the lazy keys.js.

const TREE = { ArrowUp: "prev", ArrowDown: "next", ArrowLeft: "collapse", ArrowRight: "expand", Home: "first", End: "last" };
const NODE = { c: "copynode", x: "cutnode", v: "pastenode", a: "selectsiblings", h: "history" };
const ALT = { ArrowUp: "moveup", ArrowDown: "movedown", ArrowLeft: "outdent", ArrowRight: "indent" };

/**
 * resolveKey(e, ctx) -> action name or null.
 * e: {key, ctrlKey, metaKey, shiftKey, altKey}.
 * ctx: {text (target is an input, textarea, select or editable), tree (focus is in the
 * navigator), free (focus is on the page body, canvas pane or navigator), selected (a node is selected),
 * range (text is selected on the page: Ctrl+C/X/A stay the browser's),
 * canvasText (the canvas has a text edit open: Ctrl/Cmd+K is the link shortcut there),
 * control (the target is a button, link or other control: Enter stays its own)}.
 * Actions: save undo redo duplicate copystyle pastestyle remove inspect palette sheet prev next first last
 * collapse expand moveup movedown outdent indent togglenav toggleinspector copynode cutnode pastenode
 * wrap unwrap selectsiblings history rename menu commandk edittext.
 */
export function resolveKey(e, ctx) {
  const mod = e.ctrlKey || e.metaKey;
  const k = e.key;
  const lower = typeof k === "string" ? k.toLowerCase() : "";
  if (mod && !e.altKey && lower === "s") return "save";
  if (mod && e.altKey && !e.shiftKey && !e.getModifierState?.("AltGraph")) {
    // physical key only: on AltGr layouts (Windows reports Ctrl+Alt) "[" and "]" are typed characters
    if (e.code === "BracketLeft") return "togglenav";
    if (e.code === "BracketRight") return "toggleinspector";
  }
  // Ctrl/Cmd+K opens the command palette from anywhere, text fields included; in a text edit on the canvas it stays "insert link".
  if (mod && !e.altKey && !e.shiftKey && lower === "k") return ctx.canvasText ? null : "commandk";
  if (ctx.text) return null;
  if (mod && e.altKey && !e.shiftKey) {
    // by physical key too: Alt changes the character on some keyboards
    const code = e.code ?? "";
    if (!ctx.selected || !(ctx.free || ctx.tree)) return null;
    if (lower === "c" || code === "KeyC") return "copystyle";
    if (lower === "v" || code === "KeyV") return "pastestyle";
    return null;
  }
  if (mod && !e.altKey) {
    if (lower === "z") return e.shiftKey ? "redo" : "undo";
    if (lower === "y" && !e.shiftKey) return "redo";
    if (!(ctx.free || ctx.tree)) return null;
    // Paste (an empty page takes it at the top level) and History need no selection.
    if (!ctx.selected) return !e.shiftKey && (lower === "v" || lower === "h") ? NODE[lower] : null;
    if (lower === "d" && !e.shiftKey) return "duplicate";
    if (lower === "g") return e.shiftKey ? "unwrap" : "wrap";
    if (e.shiftKey) return null;
    if (ctx.range && (lower === "c" || lower === "x" || lower === "a")) return null;
    return NODE[lower] ?? null;
  }
  if (!(ctx.free || ctx.tree)) return null;
  if (e.altKey && !mod) return ctx.tree && ctx.selected ? (ALT[k] ?? null) : null;
  if (mod) return null;
  if (ctx.selected && ((k === "F10" && e.shiftKey) || k === "ContextMenu")) return "menu";
  if (k === "F2" && ctx.tree && ctx.selected && !e.shiftKey) return "rename";
  if (k === "?") return "sheet";
  if ((k === "/" || lower === "a") && !e.shiftKey) return "palette";
  if (k === "Delete" && ctx.selected) return "remove";
  if (ctx.tree) {
    if (k === "Backspace" && ctx.selected) return "remove";
    if (k === "Enter" && ctx.selected) return "inspect";
    if (TREE[k]) return TREE[k];
  }
  if (k === "Enter" && !e.shiftKey && ctx.selected && !ctx.tree && !ctx.control) return "edittext"; // the page or canvas pane has the focus
  return null;
}
