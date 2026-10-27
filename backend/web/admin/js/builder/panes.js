// The builder's resizable, collapsible sidebars. The pure part (limits, clamp, storage)
// is DOM-free so it is unit-tested; mountPanes() wires the splitters and toggle buttons.
// Widths are CSSOM custom properties on the grid root (the admin CSP is nonce-only).
import { h, $ } from "@hm/builder/dom";

export const LIMITS = {
  nav: { min: 12, max: 40, def: 16 }, // rem
  insp: { min: 16, max: 48, def: 24 },
};
export const CANVAS_MIN = 480; // px the canvas always keeps
export const SPLIT = 8; // px, one splitter track
export const STEP = 16;
export const BIG_STEP = 64;

const KEY = "hm-builder-panes:";
const SIDE = { nav: "navOpen", insp: "inspOpen" };

export const defaults = (rem = 16) => ({ nav: LIMITS.nav.def * rem, insp: LIMITS.insp.def * rem, navOpen: true, inspOpen: true });

const clamp = (v, lo, hi) => Math.min(Math.max(v, lo), Math.max(lo, hi));
const track = (s, which) => (s[SIDE[which]] ? SPLIT : 0);

/** {min, max} px a pane may take with the other pane left as it is. */
export function limits(which, s, total, rem = 16) {
  const other = which === "nav" ? "insp" : "nav";
  const l = LIMITS[which];
  const room = total - CANVAS_MIN - track(s, "nav") - track(s, "insp") - (s[SIDE[other]] ? s[other] : 0);
  const min = l.min * rem;
  return { min, max: Math.max(min, Math.min(l.max * rem, room)) };
}

/** Fits both widths into the window: the Navigator pane gives way first (down to its minimum), the inspector keeps its room. */
export function clampPanes(s, total, rem = 16) {
  const out = { ...s };
  if (out.inspOpen) {
    const room = total - CANVAS_MIN - track(out, "nav") - track(out, "insp") - (out.navOpen ? LIMITS.nav.min * rem : 0);
    out.insp = clamp(out.insp, LIMITS.insp.min * rem, Math.min(LIMITS.insp.max * rem, room));
  }
  if (out.navOpen) out.nav = clamp(out.nav, LIMITS.nav.min * rem, limits("nav", out, total, rem).max);
  return out;
}

/** Stored state for a user, with anything unusable replaced by the default. */
export function loadPanes(storage, user = "", rem = 16) {
  const d = defaults(rem);
  let raw = null;
  try {
    raw = JSON.parse(storage?.getItem(KEY + user) ?? "null");
  } catch {
    return d;
  }
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) return d;
  const num = (v, fallback) => (typeof v === "number" && Number.isFinite(v) && v > 0 && v < 4000 ? v : fallback);
  const bool = (v) => v !== true; // only an explicit true collapses
  return { nav: num(raw.nav, d.nav), insp: num(raw.insp, d.insp), navOpen: bool(raw.navClosed), inspOpen: bool(raw.inspClosed) };
}

export function savePanes(storage, user, s) {
  try {
    storage?.setItem(KEY + user, JSON.stringify({ nav: Math.round(s.nav), insp: Math.round(s.insp), navClosed: !s.navOpen, inspClosed: !s.inspOpen }));
  } catch {
    // storage full or blocked: the layout just does not persist
  }
}

/**
 * Wires the splitters and toggles under root (the grid).
 * opts: {user, storage, announce}. Returns {toggle(which), refit()}.
 */
export function mountPanes(root, opts) {
  const storage = opts.storage ?? globalThis.localStorage;
  const rem = () => parseFloat(getComputedStyle(document.documentElement).fontSize) || 16;
  const stackedQuery = globalThis.matchMedia?.("(max-width: 959.98px)");
  const stacked = () => !!stackedQuery?.matches;
  // s holds the preferred widths: only the user's own actions (drag, keys, toggle) change it.
  // What is painted is eff(), the preference fitted to the current window.
  let s = loadPanes(storage, opts.user, rem());
  const eff = (r = rem(), total = root.clientWidth) => (stacked() ? s : clampPanes(s, total, r));

  const part = {};
  for (const which of ["nav", "insp"]) {
    part[which] = {
      pane: $(root, `[data-hm-bs-pane="${which}"]`),
      split: $(root, `[data-hm-bs-split="${which}"]`),
      toggle: $(document, `[data-hm-bs-toggle="${which}"]`),
      name: which === "nav" ? "Navigator" : "Inspector",
    };
  }

  function paint() {
    // reads first (rem, width), then the writes: one layout per paint
    const r = rem();
    const total = root.clientWidth;
    const e = eff(r, total);
    const lims = { nav: limits("nav", e, total, r), insp: limits("insp", e, total, r) };
    for (const which of ["nav", "insp"]) {
      const { pane, split, toggle, name } = part[which];
      const open = stacked() || e[SIDE[which]]; // stacked: the phone tabs choose the pane, a saved collapse is ignored
      const now = Math.round(e[which]);
      root.style.setProperty(`--hm-bs-${which}`, open ? `${now}px` : "0px");
      root.style.setProperty(`--hm-bs-${which}-split`, open ? `${SPLIT}px` : "0px");
      pane.hidden = !open;
      split.hidden = !open;
      toggle?.setAttribute("aria-expanded", String(open));
      split.setAttribute("aria-valuemin", String(Math.round(lims[which].min)));
      split.setAttribute("aria-valuemax", String(Math.round(lims[which].max)));
      split.setAttribute("aria-valuenow", String(now));
      split.setAttribute("aria-valuetext", `${name} pane ${now} pixels wide`);
    }
  }

  /** Sets the user's preferred width, held within what the window allows right now. */
  function setWidth(which, px, persist = true) {
    const lim = limits(which, eff(), root.clientWidth, rem());
    s = { ...s, [which]: clamp(px, lim.min, lim.max) };
    paint();
    if (persist) savePanes(storage, opts.user, s);
  }

  function toggle(which, force) {
    const open = force ?? !s[SIDE[which]];
    if (open === s[SIDE[which]]) return;
    const p = part[which];
    const a = document.activeElement;
    const hadFocus = p.pane.contains(a) || p.split === a;
    s = { ...s, [SIDE[which]]: open };
    paint();
    savePanes(storage, opts.user, s);
    if (!open && hadFocus) p.toggle?.focus();
    opts.announce?.(`${p.name} pane ${open ? "shown" : "hidden"}`);
  }

  for (const which of ["nav", "insp"]) {
    const { split, toggle: btn } = part[which];
    const sign = which === "nav" ? 1 : -1; // growing direction along x
    btn?.addEventListener("click", () => toggle(which));
    split.addEventListener("dblclick", () => setWidth(which, defaults(rem())[which]));
    split.addEventListener("keydown", (e) => {
      if (e.ctrlKey || e.metaKey || e.altKey) return;
      const step = e.shiftKey ? BIG_STEP : STEP;
      const cur = eff()[which];
      const lim = limits(which, eff(), root.clientWidth, rem());
      const to = { ArrowRight: cur + sign * step, ArrowLeft: cur - sign * step, Home: lim.min, End: lim.max, Enter: defaults(rem())[which] }[e.key];
      if (to === undefined) return;
      e.preventDefault();
      e.stopPropagation();
      setWidth(which, to);
    });
    split.addEventListener("pointerdown", (e) => {
      if (e.button !== 0) return;
      e.preventDefault();
      split.focus();
      const shield = h("div", { class: "hm-bs-shield hm-bs-shield--col" });
      document.body.append(shield);
      split.classList.add("is-dragging");
      const startState = s;
      const edge = root.getBoundingClientRect();
      let x = e.clientX;
      let frame = 0;
      const apply = () => {
        frame = 0;
        setWidth(which, which === "nav" ? x - edge.left : edge.right - x, false);
      };
      const move = (ev) => {
        x = ev.clientX;
        frame ||= requestAnimationFrame(apply);
      };
      let ended = false;
      const end = (cancel) => {
        if (ended) return;
        ended = true;
        if (frame) cancelAnimationFrame(frame);
        split.removeEventListener("pointermove", move);
        split.removeEventListener("pointerup", end);
        split.removeEventListener("pointercancel", end);
        split.removeEventListener("lostpointercapture", end);
        window.removeEventListener("blur", end);
        window.removeEventListener("keydown", onKey, true);
        split.classList.remove("is-dragging");
        shield.remove();
        if (cancel === true) {
          s = startState;
          paint();
        } else {
          if (frame) apply();
          savePanes(storage, opts.user, s);
        }
      };
      const onKey = (ev) => {
        if (ev.key !== "Escape") return;
        ev.preventDefault();
        ev.stopPropagation();
        end(true);
      };
      split.addEventListener("pointermove", move);
      split.addEventListener("pointerup", end);
      split.addEventListener("pointercancel", end);
      split.addEventListener("lostpointercapture", end);
      window.addEventListener("blur", end);
      window.addEventListener("keydown", onKey, true);
      try {
        split.setPointerCapture(e.pointerId);
      } catch {
        end(true);
      }
    });
  }

  window.addEventListener("resize", paint);
  stackedQuery?.addEventListener?.("change", paint);
  paint();
  return { toggle, refit: paint };
}

/**
 * The left pane's tabs (Navigator | Add): roving tabindex, arrows/Home/End move and show the panel.
 * onPick(name) runs when a tab is shown. Returns {pick(name, focus)}.
 */
export function mountLeftTabs(root, onPick = () => {}) {
  const list = $(root, "[data-hm-bs-ltabs]");
  const tabs = list ? Array.from(list.querySelectorAll("[data-hm-bs-ltab]")) : [];
  const panels = Array.from(root.querySelectorAll("[data-hm-bs-lpanel]"));
  const pick = (name, focus = false) => {
    for (const t of tabs) {
      const on = t.dataset.hmBsLtab === name;
      t.setAttribute("aria-selected", String(on));
      t.tabIndex = on ? 0 : -1;
      if (on && focus) t.focus();
    }
    for (const p of panels) p.hidden = p.dataset.hmBsLpanel !== name;
    onPick(name);
  };
  list?.addEventListener("keydown", (e) => {
    const i = tabs.indexOf(document.activeElement);
    const to = { ArrowRight: i + 1, ArrowLeft: i - 1, Home: 0, End: tabs.length - 1 }[e.key];
    if (i < 0 || to === undefined) return;
    e.preventDefault();
    pick(tabs[(to + tabs.length) % tabs.length].dataset.hmBsLtab, true);
  });
  for (const t of tabs) t.addEventListener("click", () => pick(t.dataset.hmBsLtab));
  return { pick };
}
