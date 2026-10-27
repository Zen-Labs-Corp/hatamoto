// Two one-time hints (a lazy chunk): how to select, and how to add. Each shows until it is dismissed
// or the person makes a first insert, then never again; kept per user in localStorage (blocked storage
// just means the hints show each visit). Non-modal notes: they never take the focus.
// Run: node --test scripts/admin-qa/unit/builder-coachmarks.test.mjs
import { h, actButton } from "@hm/builder/dom";

const KEY = "hm-builder-hints:";
const HINTS = [
  ["select", "Click to select. Double-click text to type."],
  ["add", "Press / or use Add to insert anything."],
];

const read = (storage, user) => {
  try {
    return JSON.parse(storage.getItem(KEY + user) ?? "{}") ?? {};
  } catch {
    return {};
  }
};

export const shouldShow = (storage, user, name) => !read(storage, user)[name];

export function dismiss(storage, user, name) {
  try {
    storage.setItem(KEY + user, JSON.stringify({ ...read(storage, user), [name]: 1 }));
  } catch {
    // shows again next visit
  }
}

/** Both hints, for good: the person inserted something before the hints had loaded. */
export function dismissAll(storage, user) {
  for (const [name] of HINTS) dismiss(storage, user, name);
}

/**
 * mountCoachmarks({canvasEl, navFoot, user, empty, storage}) -> {done}
 * The select hint sits on the canvas (not on an empty page, where the start panel leads); the add hint sits above the
 * left pane's footer. done() dismisses whatever is showing for good (the first insert).
 */
export function mountCoachmarks({ canvasEl, navFoot, user = "", empty = false, storage = globalThis.localStorage }) {
  const marks = [];
  for (const [name, text] of HINTS) {
    if (!shouldShow(storage, user, name) || (name === "select" && empty)) continue;
    const note = h("div", { class: `hm-bs-hint hm-bs-hint--${name}`, role: "note" }, h("p", { text }));
    const close = () => (dismiss(storage, user, name), note.remove());
    note.append(actButton("Got it", { onclick: close }));
    (name === "add" ? navFoot : canvasEl)?.append(note);
    marks.push({ note, name });
  }
  return {
    done() {
      for (const m of marks) (dismiss(storage, user, m.name), m.note.remove());
    },
  };
}
