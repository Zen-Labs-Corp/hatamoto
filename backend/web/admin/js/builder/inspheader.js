// The inspector header (a lazy chunk, drawn after the element name so first paint carries only
// the name and the tab shell): the name, the "Editing Tablet" chip, and the "..." menu with
// Copy style, Paste style and Restore from an earlier version.
import { h, actButton, chip } from "@hm/builder/dom";
import { clipboard } from "@hm/builder/design";
import { BP_NAME } from "@hm/builder/bp";

let uid = 0;

/** The device chip text: "Editing Tablet" on Tablet and Phone, nothing on Desktop. */
export const deviceChipText = (device) => (device === "base" || !BP_NAME[device] ? "" : `Editing ${BP_NAME[device]}`);

/**
 * header({node, api, name}) -> the header element: name, device chip, "..." menu (in that focus order).
 * api: {device(), copyStyle(), pasteStyle(), restore?(id)}. The restore row shows only when the screen offers it.
 */
export function header({ node, api, name }) {
  const id = `hm-bs-hm${++uid}`;
  const text = deviceChipText(api.device());
  const tag = text ? chip(text, { class: api.device() === "md" ? "hm-badge--tablet" : "hm-badge--phone", "data-chip-device": api.device() }) : null;
  const copy = h("button", { type: "button", role: "menuitem", text: "Copy style" });
  const paste = h("button", { type: "button", role: "menuitem", text: "Paste style" });
  const rows = [copy, paste];
  const restore = api.restore ? h("button", { type: "button", role: "menuitem", text: "Restore from an earlier version…" }) : null;
  if (restore) rows.push(h("div", { role: "separator" }), restore);
  const menu = h("div", { class: "hm-menu", id, popover: "auto", role: "menu", "aria-label": "Element options" }, ...rows);
  const opener = actButton("…", { "aria-label": "Element options", "aria-haspopup": "menu", "aria-expanded": "false", popovertarget: id, class: "hm-bs-more" });
  // Until something was copied the row says so in its own text and stays reachable by keyboard (a disabled item is skipped
  // by the menu's arrow keys, and a title is not read); choosing it announces the same reason.
  const syncPaste = () => {
    paste.textContent = clipboard.has() ? "Paste style" : "Paste style (copy a style first)";
  };
  syncPaste();
  menu.addEventListener("beforetoggle", (e) => e.newState === "open" && syncPaste());
  const run = (fn) => () => {
    menu.hidePopover?.();
    fn();
  };
  copy.addEventListener("click", run(() => api.copyStyle()));
  paste.addEventListener("click", run(() => api.pasteStyle()));
  restore?.addEventListener("click", run(() => api.restore(node.id)));
  return h("div", { class: "hm-bs-header" }, h("h3", { class: "hm-bs-name", text: name }), tag, opener, menu);
}
