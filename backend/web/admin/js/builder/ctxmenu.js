// The element actions menu (right-click, Shift+F10, the menu key): a lazy chunk. menuItems is
// the pure availability model; openMenu builds an hm-menu popover at a point. The popover has
// no opener, so it is placed here and focus goes back through restore() when it closes.
import { h, focusKept } from "@hm/builder/dom";
import { find } from "@hm/builder/store";
import { hideTarget } from "@hm/builder/selinfo";
import { hiddenAt } from "@hm/builder/bp";
import { canMakeBlock } from "@hm/builder/reuseops";
import { canWrap, canUnwrap, pastePoint, topmost } from "@hm/builder/clipops";

const MOD = /Mac|iPhone|iPad/.test(globalThis.navigator?.platform ?? "") ? "⌘" : "Ctrl+";

/**
 * The menu rows for a selection: [{id, label, shortcut?, disabled?, danger?}] and {sep: true} dividers.
 * id is the screen action the row runs. ctx: {doc, selection (ids), selected (primary), kinds, reuse (templates and blocks on), device (the width being edited)}.
 */
export function menuItems({ doc, selection, selected, kinds, reuse, device = "base" }) {
  const node = find(doc, selected);
  if (!node) return [];
  const top = topmost(doc, selection);
  const one = selection.length === 1;
  const row = (id, label, shortcut, extra = {}) => ({ id, label, ...(shortcut ? { shortcut: shortcut.replace("^", MOD) } : {}), ...extra });
  return [
    row("inspect", "Edit", "Enter", { disabled: !one }),
    row("togglehide", hiddenAt(find(doc, hideTarget(doc, selected, device)) ?? node, device) ? "Show on this device" : "Hide on this device", "H"),
    { sep: true },
    row("copynode", "Copy", "^C"),
    row("cutnode", "Cut", "^X"),
    row("pastenode", "Paste after", "^V"),
    row("pasteinside", "Paste inside", "", { disabled: !one || !pastePoint(doc, selected, kinds, true) }),
    row("duplicate", "Duplicate", "^D"),
    row("addafter", "Add after", "", { disabled: !one }),
    { sep: true },
    row("rename", "Rename", "F2", { disabled: !one }),
    row("wrap", "Wrap in container", "^G", { disabled: !canWrap(doc, selection) }),
    row("unwrap", "Unwrap", "^⇧G", { disabled: !canUnwrap(doc, selection) }),
    row("copystyle", "Copy style", "", { disabled: !one }),
    row("pastestyle", "Paste style", "", { disabled: !one }),
    row("restore", "Restore from an earlier version…", "", { disabled: !one }),
    ...(reuse ? [{ sep: true }, row("savetpl", "Save as template…", "", { disabled: !one }), row("mkblock", "Save as block…", "", { disabled: !one || !canMakeBlock(doc, selected, kinds) })] : []),
    { sep: true },
    row("remove", top.length > 1 ? `Delete ${top.length} elements` : "Delete", "Del", { danger: true }),
  ];
}

/**
 * Opens the menu at the page point (x, y). onPick(id) runs after the menu has closed and focus is back;
 * restore() puts focus where the person was. Resolves when the popover is shown.
 */
export function openMenu({ items, x, y, onPick, restore }) {
  let pending = null;
  const rows = items.map((it) =>
    it.sep
      ? h("div", { role: "separator" })
      : h("button", { type: "button", role: "menuitem", disabled: it.disabled, "data-tone": it.danger ? "danger" : false, onclick: () => {
          pending = it.id;
          m.hidePopover();
        } }, h("span", { text: it.label }), it.shortcut && h("span", { class: "hm-menu__key", text: it.shortcut })),
  );
  const m = h("div", { class: "hm-menu", popover: "auto", role: "menu", "aria-label": "Element actions" }, ...rows);
  const keptFocus = focusKept(m);
  m.addEventListener("toggle", (e) => {
    if (e.newState !== "closed") return;
    m.remove();
    if (keptFocus()) restore?.();
    if (pending) onPick(pending);
  });
  document.body.append(m);
  m.showPopover();
  const vw = document.documentElement.clientWidth;
  m.style.left = `${Math.max(8, Math.min(x, vw - m.offsetWidth - 8))}px`;
  m.style.top = `${Math.max(8, Math.min(y, innerHeight - m.offsetHeight - 8))}px`;
}
