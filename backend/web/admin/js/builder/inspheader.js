// The inspector header (a lazy chunk, drawn after the element name so first paint carries only
// the name and the tab shell): a type-icon tile, the editable name, "Section · N blocks" or
// "Block in <parent>", and the "..." menu with Copy style, Paste style and Restore from an earlier version.
import { h, actButton } from "@hm/builder/dom";
import { clipboard } from "@hm/builder/design";
import { find } from "@hm/builder/store";
import { ICON_FOR, label } from "@hm/builder/navigator";
import { isCollapsed, parentOf } from "@hm/builder/selinfo";
import { plainLabel } from "@hm/builder/inspector";

let uid = 0;

/** What the header shows: {icon, kind: "section" | "block", count, parent: {id, name} | null}. A section (no parent row) counts its blocks; a collapsed theme section counts its component's. */
export function headerModel({ node, doc, blocks }) {
  const parentId = parentOf(doc, node.id);
  const parentNode = parentId && find(doc, parentId);
  const kids = isCollapsed(node) ? (node.c[0].c ?? []) : (node.c ?? []);
  return {
    icon: node.t === "container" && node.p?.tag === "section" ? "layers" : (ICON_FOR[node.t] ?? "puzzle"),
    kind: parentNode ? "block" : "section",
    count: kids.length,
    parent: parentNode ? { id: parentId, name: plainLabel(label(parentNode, blocks)) } : null,
  };
}

const pluralBlocks = (n) => `${n} block${n === 1 ? "" : "s"}`;

/** The type icon, cloned from the server-rendered template; a bare marker span when the template is missing. */
const typeIcon = (name) => document.querySelector("template[data-hm-bs-ico]")?.content?.querySelector(`[data-ico="${name}"]`)?.cloneNode(true) ?? h("span", { "data-ico": name });

/**
 * header({node, api, name}) -> the header element: icon tile, editable name, subtitle, "..." menu (in that focus order).
 * api: {getDoc(), reuse?, select(id), rename(id, value), copyStyle(), pasteStyle(), restore?(id)}. The restore row shows only when the screen offers it.
 */
export function header({ node, api, name }) {
  const id = `hm-bs-hm${++uid}`;
  const model = headerModel({ node, doc: api.getDoc(), blocks: api.reuse?.blocks });
  const field = h("input", { class: "hm-bs-nameinput", type: "text", maxlength: "80", "aria-label": "Name" });
  field.value = name;
  // Enter and blur save, Escape puts the old name back; an unchanged name writes nothing.
  const save = () => {
    const value = field.value.trim();
    if (value !== name) api.rename(node.id, value);
  };
  field.addEventListener("keydown", (e) => {
    if (e.key === "Enter") field.blur?.();
    else if (e.key === "Escape") {
      field.value = name;
      field.blur?.();
    }
  });
  field.addEventListener("blur", save);
  const sub = model.parent
    ? h("small", { class: "hm-bs-sub" }, h("span", { text: "Block in " }), h("button", { type: "button", class: "hm-bs-sublink", text: model.parent.name, onclick: () => api.select(model.parent.id) }))
    : h("small", { class: "hm-bs-sub", text: model.count ? `Section · ${pluralBlocks(model.count)}` : "Section" });
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
  return h("div", { class: "hm-bs-header" }, h("span", { class: "hm-bs-ico", "aria-hidden": "true" }, typeIcon(model.icon)), h("div", { class: "hm-bs-heading" }, h("h3", { class: "hm-bs-name" }, field), sub), opener, menu);
}
