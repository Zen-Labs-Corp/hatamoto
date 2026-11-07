// The designer's shortcut list for the help sheet, in the shape of the page builder's
// (builder/keys.js): [group, [[keys, what], ...]]. Pure data; keep it in step with keymap.js.

export const GROUPS = [
  [
    "Fields",
    [
      ["Alt + Up / Down", "Move the field"],
      ["Ctrl/Cmd + D", "Duplicate"],
      ["Delete", "Delete the field"],
      ["Up / Down", "Select previous / next"],
      ["Esc", "Deselect"],
    ],
  ],
  [
    "Edit",
    [
      ["Ctrl/Cmd + Z", "Undo"],
      ["Ctrl/Cmd + Shift + Z or Ctrl + Y", "Redo"],
      ["Ctrl/Cmd + S", "Save"],
      ["?", "Show this list"],
    ],
  ],
];

// The same shortcuts as a table of key caps for the inspector's idle view: [[combo, ...], what].
// A combo is key names; "Mod" is Cmd on a Mac and Ctrl elsewhere. Keep it in step with keymap.js.
export const KEYS = [
  [[["Mod", "S"]], "Save"],
  [[["Mod", "Z"], ["Mod", "Shift", "Z"]], "Undo, redo"],
  [[["Up"], ["Down"]], "Select previous or next field"],
  [[["Alt", "Up"], ["Alt", "Down"]], "Move the selected field"],
  [[["Mod", "D"]], "Duplicate"],
  [[["Delete"]], "Remove the field"],
  [[["Esc"]], "Deselect"],
  [[["?"]], "Show this list"],
];

export const isMac = () => /Mac|iPhone|iPad/.test(globalThis.navigator?.platform || globalThis.navigator?.userAgent || "");

const MAC = { Mod: "⌘", Shift: "⇧", Alt: "⌥", Up: "↑", Down: "↓" };
const MAC_ORDER = ["Alt", "Shift", "Mod"]; // the Mac way: Option, Shift, Command, then the key
const OTHER = { Mod: "Ctrl", Up: "↑", Down: "↓" };

/** One combo as the text of a key cap: "⇧⌘Z" on a Mac, "Ctrl+Shift+Z" elsewhere. */
export function keyCap(combo, mac = isMac()) {
  if (!mac) return combo.map((k) => OTHER[k] ?? k).join("+");
  const mods = MAC_ORDER.filter((m) => combo.includes(m));
  return [...mods, ...combo.filter((k) => !MAC_ORDER.includes(k))].map((k) => MAC[k] ?? k).join("");
}
