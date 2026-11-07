// The designer's key -> action table (resolveKey), pure so it is unit-tested. Own vocabulary:
// it does not share the page builder's. Events from text controls are never taken (they keep
// their own undo and cursor keys), except Ctrl/Cmd+S. AltGraph and IME composition never act.

/**
 * resolveKey(e, ctx) -> action name or null.
 * e: {key, ctrlKey, metaKey, shiftKey, altKey, isComposing, getModifierState?}.
 * ctx: {text (the target is an input, textarea, select or editable), selected (a field is
 * selected), control (the target is a button, link or other control: it keeps Enter, Space,
 * arrows and Backspace)}.
 * Actions: move-up move-down duplicate delete undo redo save deselect prev next help.
 */
export function resolveKey(e, ctx) {
  if (e.isComposing || e.key === "Process" || e.keyCode === 229) return null;
  if (e.getModifierState?.("AltGraph")) return null;
  const mod = e.ctrlKey || e.metaKey;
  const k = e.key;
  const lower = typeof k === "string" ? k.toLowerCase() : "";
  if (mod && !e.altKey && lower === "s") return "save";
  if (ctx.text) return null;
  if (mod) {
    if (e.altKey) return null;
    if (lower === "z") return e.shiftKey ? "redo" : "undo";
    if (lower === "y" && !e.shiftKey) return "redo";
    if (lower === "d" && !e.shiftKey && ctx.selected) return "duplicate";
    return null;
  }
  if (e.altKey) {
    if (!ctx.selected || e.shiftKey) return null;
    return k === "ArrowUp" ? "move-up" : k === "ArrowDown" ? "move-down" : null;
  }
  if (k === "?") return "help";
  if (e.shiftKey || ctx.control || !ctx.selected) return null;
  switch (k) {
    case "Delete":
    case "Backspace":
      return "delete";
    case "Escape":
      return "deselect";
    case "ArrowUp":
      return "prev";
    case "ArrowDown":
      return "next";
  }
  return null;
}
