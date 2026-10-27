// The canvas strip: breadcrumbs of the selected element, the zoom control and the device band
// label. A lazy chunk of the canvas host (loaded after the first layout, never preloaded): the
// host keeps geometry and state, this builds the DOM once and repaints it on `update()`.
// The strip is sticky over the scroller and takes no clicks itself; only its buttons do.
import { BP_NAME, BREAKPOINT_MAX } from "@hm/builder/bp";
import { placeToolbar } from "@hm/canvas/layout";
import { insertionPoints } from "@hm/canvas/points";

const MIN_GAP = 26; // screen px: "+" buttons never sit closer than this
const ORDER = ["fit", "100", "75", "50"]; // the buttons, left to right (keys of ZOOMS in layout.js)

function h(tag, props = {}, ...kids) {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(props)) {
    if (v === false || v === null || v === undefined) continue;
    if (k === "text") e.textContent = v;
    else e.setAttribute(k, v === true ? "" : v);
  }
  e.append(...kids);
  return e;
}

const ZOOM_NAME = (m) => (m === "fit" ? "Fit" : `${m}%`);
/** The band label of a narrower device; Desktop shows none. */
export const bandText = (d) => (d === "md" || d === "sm" ? `${BP_NAME[d]} · editing styles up to ${BREAKPOINT_MAX[d]} px · Desktop is not changed` : "");

/**
 * mountStrip(canvas, handlers) -> {update, destroy}
 * canvas: the host (stage, zoom, device, selectedId, nodeChain, setZoom, setInset); handlers: {nodeLabel(id, k, t), sectionLabel(id), onCrumb(id|null, k), onZoom(mode)}.
 */
export function mountStrip(canvas, handlers) {
  const crumbs = h("ol", { class: "hm-pe__crumbs" });
  const nav = h("nav", { "aria-label": "Selected element path", class: "hm-pe__crumbnav" }, crumbs);
  // not a live region: the device change is announced once by the builder screen; a repaint on every hover would re-read this
  const band = h("span", { class: "hm-pe__bandlabel", hidden: true });
  const zoomBtns = ORDER.map((m) => {
    const b = h("button", { type: "button", class: "hm-segmented__btn", "data-zoom": m, "aria-label": m === "fit" ? "Zoom to fit" : `Zoom ${m}%`, text: ZOOM_NAME(m) });
    b.addEventListener("click", () => {
      canvas.setZoom(m === "fit" ? "fit" : Number(m));
      handlers.onZoom?.(m === "fit" ? "fit" : Number(m));
      update();
    });
    return b;
  });
  const zoomBox = h("div", { class: "hm-pe__zoom hm-segmented", role: "group", "aria-label": "Zoom" }, ...zoomBtns);
  const strip = h("div", { class: "hm-pe__strip", "data-hm-pe-strip": "" }, nav, band, zoomBox);
  canvas.stage.parentElement?.insertBefore(strip, canvas.stage);

  let shown = "";
  const nameOf = (c) => (c.k === "s" ? handlers.sectionLabel?.(c.id) : handlers.nodeLabel?.(c.id, c.k, c.t)) || c.t;

  function update() {
    const chain = canvas.selectedId ? canvas.nodeChain(canvas.selectedId) : [];
    const items = [{ id: null, k: "p", name: "Page" }, ...chain.map((c) => ({ id: c.id, k: c.k, name: nameOf(c) }))];
    const sig = items.map((i) => `${i.id}:${i.name}`).join("|");
    if (sig !== shown) {
      shown = sig;
      crumbs.replaceChildren(
        ...items.map((it, i) => {
          const last = i === items.length - 1;
          const b = h("button", { type: "button", class: "hm-pe__crumb", text: it.name, "aria-current": last ? "true" : false });
          if (!last) b.addEventListener("click", () => handlers.onCrumb?.(it.id, it.k));
          return h("li", { class: "hm-pe__crumbitem" }, b);
        }),
      );
    }
    const text = bandText(canvas.device);
    band.hidden = !text;
    if (band.textContent !== text) band.textContent = text;
    zoomBtns.forEach((b) => b.setAttribute("aria-pressed", String(String(canvas.zoom) === b.dataset.zoom)));
    canvas.setInset(strip.offsetHeight);
  }

  update();
  return {
    update,
    destroy: () => strip.remove(),
  };
}

const TEXT_KINDS = new Set(["heading", "text"]); // edited in place
// [action, visible text, accessible name]; the visible text is part of the name
const ACTIONS = [
  ["parent", "Parent", "Select parent"],
  ["duplicate", "Duplicate", "Duplicate"],
  ["wrap", "Wrap", "Wrap in container"],
  ["addafter", "Add after", "Add after"],
  ["togglehide", "Hide", "Hide on this device"],
  ["remove", "Delete", "Delete"],
  ["more", "More", "More element actions"],
];

/**
 * mountToolbar(canvas, handlers) -> {update, destroy}
 * The floating toolbar of the selected element: built once and repositioned, never rebuilt by the marks redraw.
 * canvas: the host (stage, scale, inset, selectionBox, editingId, editable, nodeChain); handlers: {nodeLabel, sectionLabel, isHidden(id), onAction(name), onCrumb(id, k), onMore(rect), onAddAfter(button), onHandleDown}.
 */
export function mountToolbar(canvas, handlers) {
  const handle = h("span", { class: "hm-pe__handle", "data-id": "", "aria-hidden": "true", title: "Drag to move", text: "\u283f" });
  const name = h("span", { class: "hm-pe__tname" });
  const buttons = ACTIONS.map(([act, text, label], i) => h("button", { type: "button", class: "hm-pe__tbtn", "data-act": act, "aria-label": label, tabindex: i === 0 ? "0" : "-1", text }));
  const actions = h("div", { class: "hm-pe__tactions" }, ...buttons);
  const hint = h("span", { class: "hm-pe__thint" });
  const bar = h("div", { class: "hm-pe__toolbar", role: "toolbar", "aria-label": "Element toolbar", "data-hm-pe-toolbar": "", hidden: true }, handle, name, actions, hint);
  canvas.stage.append(bar);

  const enabled = () => buttons.filter((b) => !b.hasAttribute("disabled"));
  const rove = (to) => {
    buttons.forEach((b) => b.setAttribute("tabindex", b === to ? "0" : "-1"));
    to.focus();
  };
  bar.addEventListener("keydown", (e) => {
    const list = enabled();
    const i = list.indexOf(document.activeElement);
    const to = { ArrowRight: i + 1, ArrowLeft: i - 1, Home: 0, End: list.length - 1 }[e.key];
    if (to === undefined || i < 0) return;
    e.preventDefault();
    rove(list[(to + list.length) % list.length]);
  });
  bar.addEventListener("click", (e) => {
    const b = e.target.closest?.("[data-act]");
    if (!b || b.hasAttribute("disabled")) return;
    buttons.forEach((x) => x.setAttribute("tabindex", x === b ? "0" : "-1"));
    const act = b.dataset.act;
    const sel = canvas.selectionBox;
    if (act === "parent") {
      const chain = sel ? canvas.nodeChain(sel.id) : [];
      const up = chain[chain.length - 2];
      if (up) handlers.onCrumb?.(up.id, up.k);
    } else if (act === "more") handlers.onMore?.(b.getBoundingClientRect());
    else if (act === "addafter") handlers.onAddAfter?.(b);
    else handlers.onAction?.(act);
  });

  let shown = "";
  let retried = "";
  // The formatting bar grows when its link row opens (Ctrl/Cmd+K): the hint under it moves down with it.
  const grown = typeof ResizeObserver === "function" ? new ResizeObserver(() => update()) : null;
  let watched = null;
  function update() {
    const sel = canvas.selectionBox;
    const on = !!sel && canvas.editable;
    bar.hidden = !on;
    if (!on) return;
    const editing = canvas.editingId === sel.id;
    const nm = (sel.k === "s" ? handlers.sectionLabel?.(sel.id) : handlers.nodeLabel?.(sel.id, sel.k, sel.t)) || sel.t;
    const text = editing ? "Editing text \u00b7 Enter to finish \u00b7 Esc to cancel" : TEXT_KINDS.has(sel.t) ? "Double-click or Enter to edit text" : "";
    const hid = !!handlers.isHidden?.(sel.id); // hidden on this device: the button shows it again
    const sig = `${sel.id}|${nm}|${editing}|${text}|${hid}`;
    if (sig !== shown) {
      shown = sig;
      name.textContent = nm;
      hint.textContent = text;
      hint.hidden = !text;
      actions.hidden = editing;
      handle.hidden = editing || !handlers.onHandleDown;
      handle.setAttribute("data-id", sel.id);
      const chain = canvas.nodeChain(sel.id);
      const parent = buttons[0];
      if (chain.length < 2) parent.setAttribute("disabled", "");
      else parent.removeAttribute("disabled");
      if (document.activeElement === parent && parent.hasAttribute("disabled")) rove(buttons[1]);
      // the one tab stop must be a button Tab can reach: a disabled one (Parent, on a top-level element) would skip the toolbar
      const stop = buttons.find((b) => b.getAttribute("tabindex") === "0");
      if (!stop || stop.hasAttribute("disabled")) {
        const first = enabled()[0];
        buttons.forEach((b) => b.setAttribute("tabindex", b === first ? "0" : "-1"));
      }
      const hide = buttons.find((b) => b.getAttribute("data-act") === "togglehide");
      hide.textContent = hid ? "Show" : "Hide";
      hide.setAttribute("aria-label", hid ? "Show on this device" : "Hide on this device");
    }
    const scroller = canvas.stage.parentElement;
    const sc = canvas.scale;
    const inset = canvas.inset ?? 0;
    const top = scroller ? scroller.scrollTop - canvas.stage.offsetTop + inset : 0;
    const left = scroller ? scroller.scrollLeft - canvas.stage.offsetLeft : 0;
    const view = scroller ? { top, left, right: left + scroller.clientWidth, bottom: top + scroller.clientHeight - inset } : { top: 0, left: 0, right: Infinity, bottom: Infinity };
    // While editing, the text formatting bar owns the space it chose (above the element, or below when there is no room above):
    // the toolbar, now only a hint, goes under the element, and under the formatting bar when that is the one below.
    // a bar wider than the view (a phone) drops its hint, then the rest scrolls inside it
    bar.style.maxWidth = `${Math.max(0, view.right - view.left)}px`; // the view, not the (scaled) stage
    hint.hidden = !text;
    if (!hint.hidden && actions.scrollWidth > actions.clientWidth) hint.hidden = true; // the buttons are squeezed: the hint goes first
    let box = { x: sel.x * sc, y: sel.y * sc, w: sel.w * sc, h: sel.h * sc };
    const fmt = editing ? canvas.stage.querySelector(".hm-pe__fmt:not([hidden])") : null;
    if (fmt && fmt.offsetTop >= box.y) box = { ...box, h: fmt.offsetTop + fmt.offsetHeight - box.y };
    if (fmt && fmt !== watched) grown?.observe((watched = fmt));
    const { x, y } = placeToolbar({ box, bar: { w: bar.offsetWidth, h: bar.offsetHeight }, view, below: editing });
    bar.style.left = `${x}px`;
    bar.style.top = `${y}px`;
    // the formatting bar places itself after the selection report: look again once, next frame
    if (editing && retried !== sig) {
      retried = sig;
      requestAnimationFrame?.(update);
    }
  }
  canvas.stage.parentElement?.addEventListener("scroll", update, { passive: true });
  update();
  return {
    update,
    destroy: () => (grown?.disconnect(), bar.remove()),
  };
}

/**
 * mountHandles(canvas, handlers) -> {update, destroy}
 * The round "+" buttons between siblings and inside empty containers, with an insertion line on hover.
 * Built from a pool keyed by position (a button, and the layout picker anchored to it, survives the marks redraw);
 * only the buttons take clicks. The page level, the selection's container and its parent, and the container last
 * hovered get points (all visible, bold by the selection, fainter by the hover); an empty container always shows its own.
 * Points closer than MIN_GAP on screen are dropped, and the layer is hidden while text is edited.
 * canvas: the host (stage, scale, nodes, selectedId, hoverId); handlers: {nodeLabel, sectionLabel, onAdd(point, button)}.
 */
export function mountHandles(canvas, handlers) {
  const layer = h("div", { class: "hm-pe__pluses", "data-hm-pe-pluses": "" });
  canvas.stage.append(layer);
  const pool = new Map();
  let lastHover = null;
  // leaving the canvas drops the hovered scope; a "+" is inside the stage, so reaching one keeps it
  canvas.stage.parentElement?.addEventListener("mouseleave", () => {
    if (!lastHover) return;
    lastHover = null;
    update();
  });

  const nameOf = (id) => {
    const n = canvas.nodes.find((x) => x.id === id);
    return (n && (n.k === "s" ? handlers.sectionLabel?.(n.id) : handlers.nodeLabel?.(n.id, n.k, n.t))) || n?.t || "";
  };
  const labelOf = (pt) => (pt.empty ? "Add inside this empty container" : pt.after ? `Add after ${nameOf(pt.after)}` : `Add before ${nameOf(pt.before)}`);

  function scope(id, into) {
    const n = id && canvas.nodes.find((x) => x.id === id);
    if (!n) return;
    if (n.c === 1) into.add(n.id);
    into.add(n.p);
  }

  function update() {
    const sel = new Set([""]);
    scope(canvas.selectedId, sel);
    const parents = new Set(sel);
    if (canvas.hoverId) lastHover = canvas.hoverId;
    scope(lastHover, parents);
    const sc = canvas.scale;
    layer.hidden = canvas.editingId != null; // no "+" over the text being typed or over its formatting bar
    const points = layer.hidden ? [] : insertionPoints(canvas.nodes, parents, { gap: MIN_GAP / sc });
    const keep = new Set();
    for (const pt of points) {
      keep.add(pt.key);
      let b = pool.get(pt.key);
      if (!b) {
        b = h("button", { type: "button", class: "hm-pe__plus", "data-hm-pe-plus": "", tabindex: "-1" }, h("span", { class: "hm-pe__plusline", "aria-hidden": "true" }), h("span", { class: "hm-pe__plusmark", "aria-hidden": "true", text: "+" }));
        b.addEventListener("click", () => handlers.onAdd?.(b._pt, b));
        pool.set(pt.key, b);
        layer.append(b);
      }
      b._pt = pt;
      b.setAttribute("aria-label", labelOf(pt));
      b.dataset.dir = pt.dir;
      b.setAttribute("data-show", pt.empty || sel.has(pt.parent) ? "sel" : "hover"); // every button here is visible, so every one takes clicks
      b.style.left = `${pt.x * sc}px`;
      b.style.top = `${pt.y * sc}px`;
      b.firstChild.style.cssText = pt.dir === "row" ? `height:${pt.len * sc}px` : `width:${pt.len * sc}px`;
    }
    for (const [k, b] of pool)
      if (!keep.has(k)) {
        b.remove();
        pool.delete(k);
      }
  }
  update();
  return { update, destroy: () => layer.remove() };
}
