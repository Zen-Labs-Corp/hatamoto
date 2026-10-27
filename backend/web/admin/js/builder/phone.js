// The builder's phone-facing pieces: the preview loading and unavailable panel and the dock (Navigator, Add, Inspector). DOM-light (dom.js only) so node can unit test them.
import { h, actButton, $, $$ } from "@hm/builder/dom";

export const PREVIEW_TIMEOUT = 8000;

/** The panel laid over the canvas for the first render: loading, then ready (removed) or unavailable. */
export function createPreviewState(canvasEl, retry) {
  const loading = h("div", { class: "hm-bs__skeleton", "aria-hidden": "true" }, h("span"), h("span"), h("span"));
  const text = h("p", { class: "hm-muted", text: "Loading the preview…" });
  const title = h("p", { class: "hm-empty__title", hidden: true, text: "Preview unavailable" });
  const again = actButton("Try again", { hidden: true });
  const box = h("div", { class: "hm-bs__preview-state", role: "status", "data-hm-bs-preview-state": "loading" }, loading, title, text, again);
  canvasEl.append(box);
  let timer = 0;
  let done = false;
  const show = (state) => {
    box.dataset.hmBsPreviewState = state;
    const bad = state === "error";
    loading.hidden = bad;
    title.hidden = !bad;
    again.hidden = !bad;
    text.textContent = bad ? "The page could not render." : "Loading the preview…";
  };
  const start = () => {
    clearTimeout(timer);
    timer = setTimeout(() => !done && show("error"), PREVIEW_TIMEOUT);
  };
  again.addEventListener("click", () => {
    show("loading");
    start();
    retry();
  });
  start();
  return {
    ready() {
      done = true;
      clearTimeout(timer);
      box.remove();
    },
    fail() {
      if (!done) {
        clearTimeout(timer);
        show("error");
      }
    },
  };
}

/** A person's own pick (a tap in the tree or on the page) opens the Inspector sheet on a phone; an insert or undo selecting something does not. */
export const shouldOpenInspector = ({ stacked, selected, userPick }) => !!(stacked && selected && userPick);

/**
 * The phone dock (Navigator, Add, Inspector): the page is the base view and a tab opens one pane as a sheet over it
 * ([data-hm-bs-tab] on the root: nav, add, insp, or canvas for none). The open tab or Escape closes it.
 * onPick(name) runs when a sheet opens. Returns {pick, show (no onPick), toggle, close, stacked}.
 */
export function mountDock(root, onPick = () => {}) {
  const list = $(root, "[data-hm-bs-dock]");
  const narrow = matchMedia("(max-width: 59.99rem)"); // tablet portrait too
  const tabs = list ? $$(list, "button") : [];
  const current = () => root.dataset.hmBsTab;
  const open = () => tabs.find((t) => t.dataset.hmBsDock === current());
  function paint(focus) {
    const on = narrow.matches;
    if (on) list.setAttribute("role", "tablist");
    else list.removeAttribute("role");
    tabs.forEach((t, i) => {
      const sel = t === open();
      if (on) {
        t.setAttribute("role", "tab");
        t.setAttribute("aria-selected", String(sel));
        t.tabIndex = sel || (!open() && !i) ? 0 : -1;
        if (sel && focus) t.focus();
      } else for (const a of ["role", "aria-selected", "tabindex"]) t.removeAttribute(a);
    });
  }
  const show = (name, focus) => {
    root.dataset.hmBsTab = name;
    if (list) paint(focus);
  };
  const close = () => show("canvas");
  const pick = (name, focus) => (show(name, focus), onPick(name));
  const toggle = (name) => (current() === name ? close() : pick(name));
  if (list) {
    paint(false);
    narrow.addEventListener("change", () => paint(false));
    tabs.forEach((t) => t.addEventListener("click", () => toggle(t.dataset.hmBsDock)));
    list.addEventListener("keydown", (e) => {
      const i = tabs.indexOf(document.activeElement);
      const to = { ArrowRight: i + 1, ArrowLeft: i - 1, Home: 0, End: tabs.length - 1 }[e.key];
      if (!narrow.matches || i < 0 || to === undefined) return;
      e.preventDefault();
      pick(tabs[(to + tabs.length) % tabs.length].dataset.hmBsDock, true);
    });
    root.addEventListener("keydown", (e) => {
      const was = open();
      if (e.key !== "Escape" || e.defaultPrevented || !narrow.matches || !was || !e.target.closest?.(".hm-bs__dock, .hm-bs__nav, .hm-bs__inspector") || document.querySelector(":popover-open, dialog[open]")) return;
      close();
      was.focus();
    });
  }
  return { pick, show, toggle, close, stacked: () => narrow.matches };
}

/**
 * Phones fold Undo, Redo and the shortcuts sheet into ⋯ (the bar keeps one row). Each item presses the bar button
 * it stands for and mirrors its disabled state when the menu opens. Hidden above 640px, except items marked data-hm-bs-always.
 */
export function mountToolItems(root, mm) {
  const items = $$(root, "[data-hm-bs-proxy]");
  const q = mm("(max-width: 40rem)");
  const apply = () => items.forEach((i) => (i.hidden = !q.matches && !("hmBsAlways" in i.dataset)));
  apply();
  q.addEventListener("change", apply);
  const target = (item) => $(root, item.dataset.hmBsProxy);
  for (const item of items) {
    item.addEventListener("click", () => {
      item.closest("[popover]")?.hidePopover?.();
      target(item)?.click();
    });
  }
  items[0]?.closest("[popover]")?.addEventListener("toggle", () => items.forEach((i) => (i.disabled = !!target(i)?.disabled)));
}
