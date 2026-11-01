// One dismissible hint bar (a lazy chunk): how to select, type, resize spacing, insert and step to the parent. It shows
// until it is dismissed or the person makes a first insert, then never again; kept per user in localStorage (blocked
// storage just means the bar shows each visit). A non-modal note: it never takes the focus.
// Run: node --test scripts/admin-qa/unit/builder-coachmarks.test.mjs
import { h } from "@hm/builder/dom";

const KEY = "hm-builder-hints:";
const LEGACY = ["select", "add"]; // the two earlier hints: both dismissed counts as the bar dismissed

const read = (storage, user) => {
  try {
    return JSON.parse(storage.getItem(KEY + user) ?? "{}") ?? {};
  } catch {
    return {};
  }
};

export function shouldShow(storage, user) {
  const flags = read(storage, user);
  return !(flags.bar || LEGACY.every((n) => flags[n]));
}

function mark(storage, user, names) {
  try {
    storage.setItem(KEY + user, JSON.stringify({ ...read(storage, user), ...Object.fromEntries(names.map((n) => [n, 1])) }));
  } catch {
    // shows again next visit
  }
}

export const dismiss = (storage, user) => mark(storage, user, ["bar"]);

/** The bar, for good: the person inserted something before the hints had loaded. */
export const dismissAll = (storage, user) => mark(storage, user, ["bar", ...LEGACY]);

const ITEMS = [
  () => h("span", { text: "Click to select" }),
  () => h("span", { text: "Double-click text to type" }),
  () => h("span", {}, "Drag ", h("span", { class: "hm-bs-hint__bar", "aria-hidden": "true" }), " to resize spacing"),
  () => h("span", {}, h("kbd", { text: "/" }), " to insert"),
  () => h("span", {}, h("kbd", { text: "Esc" }), " for the parent"),
];

/** The server-rendered icon-only x button <template data-hm-bs-actx>; a plain button when the template is absent. */
function dismissButton(onclick) {
  const e = document.querySelector?.("template[data-hm-bs-actx]")?.content.firstElementChild?.cloneNode(true) ?? h("button", { type: "button", text: "x" });
  e.setAttribute("aria-label", "Dismiss hints");
  e.addEventListener("click", onclick);
  return e;
}

/**
 * mountCoachmarks({canvasEl, user, empty, storage}) -> {done}
 * One note on the canvas (not on an empty page, where the start panel leads). done() dismisses it for good (the first insert).
 */
export function mountCoachmarks({ canvasEl, user = "", empty = false, storage = globalThis.localStorage }) {
  if (empty || !shouldShow(storage, user)) return { done() {} };
  const note = h("div", { class: "hm-bs-hint", role: "note" }, ...ITEMS.map((f) => f()));
  const close = () => (dismiss(storage, user), note.remove());
  note.append(dismissButton(close));
  canvasEl?.append(note);
  return { done: close };
}
