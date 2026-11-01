// The shortcut sheet dialog (a lazy chunk, loaded on first "?"): grouped rows and a search box.
// The key -> action table is keymap.js.
import { h } from "@hm/builder/dom";

/** [group, [[keys, what]...]] in reading order. */
export const GROUPS = [
  [
    "Insert",
    [
      ["/ or A", "Insert a block or section (opens the Insert tab)"],
      ["Ctrl/Cmd + K", "Search elements, patterns and commands"],
      ["Enter in the Insert search", "Insert the first result shown"],
      ["Shift + Enter in the Insert search", "Insert it and keep the search open for the next one"],
      ["Ctrl/Cmd + C / X / V", "Copy, cut and paste elements (paste goes after the selected one)"],
      ["Ctrl/Cmd + D", "Duplicate the element"],
    ],
  ],
  [
    "Select and move",
    [
      ["Up / Down", "Select the previous / next element in the Navigator; on the page, the previous / next section"],
      ["Esc", "Select the parent of the selected element"],
      ["Left / Right", "Collapse / expand, or go to the parent / first child"],
      ["Home / End", "Select the first / last element"],
      ["Ctrl/Cmd + A", "Select the element and its siblings"],
      ["Alt + Up / Down", "Move the element among its siblings"],
      ["Alt + Left", "Outdent: move it out, after its parent"],
      ["Alt + Right", "Indent: move it into the previous container"],
      ["Enter", "Edit the selected element in the inspector (Navigator); on the page, edit its text or select its first child"],
      ["Shift + F10 or Menu key", "Open the element's actions menu"],
    ],
  ],
  [
    "Edit",
    [
      ["F2", "Rename the element in the Navigator"],
      ["Delete", "Delete the element"],
      ["Backspace", "Delete the element (on the page or in the Navigator)"],
      ["H", "Hide or show the element at the current screen width (in the Navigator)"],
      ["Ctrl/Cmd + G", "Wrap the selected elements in a container"],
      ["Ctrl/Cmd + Shift + G", "Unwrap a container: its elements take its place"],
      ["Ctrl/Cmd + Alt + C", "Copy the element's style"],
      ["Ctrl/Cmd + Alt + V", "Paste the copied style onto the element"],
      ["Ctrl/Cmd + S", "Save"],
    ],
  ],
  [
    "View and device",
    [
      ["Ctrl/Cmd + Alt + [ / ]", "Show or hide the Navigator / Inspector pane"],
      ["Left / Right on a pane edge", "Resize the pane (Shift for bigger steps, Home / End for the limits, Enter to reset)"],
      ["1 / 2 / 3", "Desktop / Tablet / Phone width"],
      ["?", "Show this list"],
    ],
  ],
  [
    "History",
    [
      ["Ctrl/Cmd + Z", "Undo"],
      ["Ctrl/Cmd + Shift + Z or Ctrl + Y", "Redo"],
      ["Ctrl/Cmd + H", "Open the change history"],
    ],
  ],
];

/** Every [keys, what] row, in reading order. */
export const SHORTCUTS = GROUPS.flatMap(([, rows]) => rows);

/** The groups with only the rows whose keys or description contain every word of q; empty groups drop out. */
export function filterGroups(groups, q) {
  const words = q.toLowerCase().split(/\s+/).filter(Boolean);
  if (!words.length) return groups;
  return groups
    .map(([name, rows]) => [name, rows.filter(([keys, what]) => words.every((w) => `${keys} ${what}`.toLowerCase().includes(w)))])
    .filter(([, rows]) => rows.length);
}

/** The shortcut sheet: a modal <dialog> built once; open() shows it, Escape closes it. */
export function createSheet() {
  const close = h("button", { type: "button", class: "hm-btn hm-btn--primary", text: "Close" });
  const search = h("input", { type: "search", class: "hm-field__control", id: "hm-bs-sheet-q", autocomplete: "off", placeholder: "Search shortcuts…", "aria-label": "Search shortcuts", "aria-controls": "hm-bs-sheet-body" });
  // The list scrolls inside the dialog: it takes a Tab stop (a keyboard user scrolls it with the arrows) and a name, as axe requires of a scroll region.
  const body = h("div", { class: "hm-dialog__body", id: "hm-bs-sheet-body", tabindex: "0", role: "region", "aria-label": "Shortcut list" });
  const status = h("p", { class: "hm-visually-hidden", role: "status" });
  const dlg = h(
    "dialog",
    { class: "hm-dialog", id: "hm-bs-sheet", "aria-labelledby": "hm-bs-sheet-t", "data-size": "lg" },
    h("div", { class: "hm-dialog__panel" }, h("h2", { class: "hm-dialog__title", id: "hm-bs-sheet-t", text: "Keyboard shortcuts" }), h("div", { class: "hm-field hm-bs-sheet__q" }, search), status, body, h("div", { class: "hm-dialog__foot" }, close)),
  );
  /** One keycap per key: "Ctrl/Cmd + Z" -> [Ctrl/Cmd] + [Z]. A phrase ("Enter in the Insert search") stays plain text. The separators keep the row's text as it was. */
  const caps = (keys) => {
    const parts = keys.split(/( \+ | or | \/ )/);
    const isKey = (p, i) => i % 2 === 1 || !/\s/.test(p) || /^Menu key$/.test(p);
    return parts.every(isKey) ? parts.map((p, i) => (i % 2 ? p : h("kbd", { class: "hm-bs-key", text: p }))) : [keys];
  };
  function paint() {
    const shown = filterGroups(GROUPS, search.value);
    body.replaceChildren(
      ...(shown.length
        ? shown.map(([name, rows]) => h("table", { class: "hm-bs-keys" }, h("caption", { class: "hm-bs-keys__g", text: name }), h("tbody", {}, ...rows.map(([keys, what]) => h("tr", {}, h("th", { scope: "row", class: "hm-bs-keys__k" }, ...caps(keys)), h("td", { text: what }))))))
        : [h("p", { class: "hm-muted", text: "No shortcuts match." })]),
    );
    status.textContent = search.value ? `${shown.reduce((n, [, r]) => n + r.length, 0)} shortcuts` : "";
  }
  search.addEventListener("input", paint);
  close.addEventListener("click", () => dlg.close());
  paint();
  document.body.append(dlg);
  return { open: () => !dlg.open && dlg.showModal(), el: dlg };
}
