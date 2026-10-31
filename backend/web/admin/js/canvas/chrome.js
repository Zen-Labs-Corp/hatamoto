// The canvas strip: breadcrumbs of the selected element, the zoom control and the device band
// label. A lazy chunk of the canvas host (loaded after the first layout, never preloaded): the
// host keeps geometry and state, this builds the DOM once and repaints it on `update()`.
// The strip is sticky over the scroller and takes no clicks itself; only its buttons do.
import { BP_NAME, BREAKPOINT_MAX } from "@hm/builder/bp";
import { sizeText, toolbarModel } from "@hm/canvas/chromemodel";
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
// The chip's icon by element kind (the Navigator's mapping); anything else reads as a puzzle piece.
const KIND_ICON = { section: "layers", heading: "heading", text: "file-text", button: "link", image: "image", divider: "minus", spacer: "maximize", container: "layout", component: "layers", ref: "copy", loop: "list", loopempty: "list", slot: "puzzle", code: "code", columns: "layout", gallery: "image", table: "list", stat: "layers", buttons: "link", icon: "image", video: "image", accordion: "list", tabs: "list", tabpanel: "layers", menu: "list", breadcrumbs: "list", html: "code", richtext: "file-text", form: "file-text" };

/**
 * mountToolbar(canvas, handlers) -> {update, destroy}
 * The floating dark toolbar of the selected element: built once from toolbarModel and repositioned, never rebuilt by the marks redraw.
 * It also carries the size badge under the selection.
 * canvas: the host (stage, scale, inset, selectionBox, editingId, editable, nodeChain); handlers: {nodeLabel, sectionLabel, toolbarState(id, k), isHidden(id), onAction(name), onCrumb(id, k), onMore(rect), onHandleDown}.
 */
export function mountToolbar(canvas, handlers) {
  const tpl = canvas.stage.closest?.("[data-hm-builder]")?.querySelector("template[data-hm-bs-ico]"); // the server-rendered icons
  const icon = (name) => tpl?.content?.querySelector(`[data-ico="${name}"]`)?.cloneNode(true) ?? h("span", { "data-ico": name });
  const initial = toolbarModel({ kind: "", name: "", parentId: "x" });
  const handle = h("span", { class: "hm-pe__handle", "data-id": "", "aria-hidden": "true", title: "Drag to move", text: "\u283f" });
  const chipIcon = h("span", { "aria-hidden": "true" });
  const name = h("span", { class: "hm-pe__tname" });
  const chip = h("span", { class: "hm-pe__tchip" }, chipIcon, name);
  const up = h("button", { type: "button", class: "hm-pe__tup", "data-act": "parent", tabindex: "-1" });
  const sep = h("span", { class: "hm-pe__tsep", "aria-hidden": "true" });
  const acts = initial.buttons.map((m) => h("button", { type: "button", class: "hm-pe__tbtn", "data-act": m.act, tabindex: "-1" }, icon(m.icon)));
  const buttons = [up, ...acts];
  const actions = h("div", { class: "hm-pe__tactions" }, ...acts);
  const hint = h("span", { class: "hm-pe__thint" });
  const bar = h("div", { class: "hm-pe__toolbar", role: "toolbar", "aria-label": "Element toolbar", "data-hm-pe-toolbar": "", hidden: true }, handle, chip, up, sep, actions, hint);
  const size = h("span", { class: "hm-pe__size", "data-hm-pe-size": "", "aria-hidden": "true", hidden: true });
  canvas.stage.append(bar, size);

  const enabled = () => buttons.filter((b) => !b.hidden && !b.hasAttribute("disabled"));
  const stopAt = (to) => buttons.forEach((b) => b.setAttribute("tabindex", b === to ? "0" : "-1"));
  const rove = (to) => {
    stopAt(to);
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
  let upId = null;
  bar.addEventListener("click", (e) => {
    const b = e.target.closest?.("[data-act]");
    if (!b || b.hasAttribute("disabled")) return;
    stopAt(b);
    const act = b.getAttribute("data-act");
    if (act === "parent") {
      if (upId) handlers.onCrumb?.(upId, canvas.nodes?.find((n) => n.id === upId)?.k);
    } else if (act === "more") handlers.onMore?.(b.getBoundingClientRect());
    else handlers.onAction?.(act);
  });

  let shown = "";
  let retried = "";
  let padSeen = "";
  // The formatting bar grows when its link row opens (Ctrl/Cmd+K): the hint under it moves down with it.
  const grown = typeof ResizeObserver === "function" ? new ResizeObserver(() => update()) : null;
  let watched = null;
  /** What the toolbar shows for the selection: the screen's toolbarState when it supplies one, else what the host alone knows. */
  function stateOf(sel, nm) {
    const given = handlers.toolbarState?.(sel.id, sel.k);
    if (given) return { kind: sel.t, ...given, name: given.name || nm };
    const chain = canvas.nodeChain(sel.id);
    const parent = chain[chain.length - 2];
    return { name: nm, kind: sel.t, parentId: parent?.id ?? "", parentName: parent ? (parent.k === "s" ? handlers.sectionLabel?.(parent.id) : handlers.nodeLabel?.(parent.id, parent.k, parent.t)) || parent.t : "", canUp: true, canDown: true, hidden: !!handlers.isHidden?.(sel.id) };
  }
  function update() {
    const sel = canvas.selectionBox;
    const on = !!sel && canvas.editable;
    bar.hidden = !on;
    const editing = on && canvas.editingId === sel.id;
    size.hidden = !on || editing;
    if (!on) return;
    const nm = (sel.k === "s" ? handlers.sectionLabel?.(sel.id) : handlers.nodeLabel?.(sel.id, sel.k, sel.t)) || sel.t;
    const text = editing ? "Editing text \u00b7 Enter to finish \u00b7 Esc to cancel" : TEXT_KINDS.has(sel.t) ? "Double-click or Enter to edit text" : "";
    const info = { ...stateOf(sel, nm), editing };
    const model = toolbarModel(info);
    const sig = [sel.id, text, info.name, info.kind, info.parentId, info.parentName, info.canUp, info.canDown, info.hidden, editing].join("|");
    if (sig !== shown) {
      shown = sig;
      name.textContent = model.title.name;
      chipIcon.replaceChildren(icon(KIND_ICON[model.title.icon] ?? "puzzle"));
      hint.textContent = text;
      hint.hidden = !text;
      actions.hidden = editing;
      handle.hidden = editing || !handlers.onHandleDown;
      handle.setAttribute("data-id", sel.id);
      upId = model.up?.id ?? null;
      up.hidden = !model.up || editing;
      sep.hidden = up.hidden;
      if (model.up) {
        up.textContent = model.up.text;
        up.setAttribute("aria-label", model.up.aria);
      }
      for (const m of model.buttons) {
        const b = acts.find((x) => x.getAttribute("data-act") === m.act);
        b.setAttribute("aria-label", m.aria);
        b.setAttribute("title", m.aria); // icon-only: the name shows on hover
        if (m.disabled) b.setAttribute("disabled", "");
        else b.removeAttribute("disabled");
        if (m.act === "togglehide") b.replaceChildren(icon(m.icon));
      }
      if (document.activeElement && buttons.includes(document.activeElement) && !enabled().includes(document.activeElement) && enabled()[0]) rove(enabled()[0]);
      // the one tab stop must be a button Tab can reach: a disabled or hidden one would skip the toolbar
      const stop = buttons.find((b) => b.getAttribute("tabindex") === "0");
      if (!stop || stop.hidden || stop.hasAttribute("disabled")) stopAt(enabled()[0]);
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
    const label = sizeText(sel); // CSS px, whatever the zoom
    if (size.textContent !== label) size.textContent = label;
    size.style.left = `${box.x + box.w / 2}px`;
    size.style.top = `calc(${box.y + box.h}px + var(--space-1))`;
    const fmt = editing ? canvas.stage.querySelector(".hm-pe__fmt:not([hidden])") : null;
    if (fmt && fmt.offsetTop >= box.y) box = { ...box, h: fmt.offsetTop + fmt.offsetHeight - box.y };
    if (fmt && fmt !== watched) grown?.observe((watched = fmt));
    // the top padding handle (canvas/direct.js) hangs inward from the section's padding edge: the inside toolbar keeps off it
    const ph = canvas.stage.querySelector('[data-hm-pe-padh][data-side="top"]:not([hidden])');
    const pw = ph?.offsetWidth || 44;
    const avoid = ph ? { x: parseFloat(ph.style.left) - pw / 2, y: parseFloat(ph.style.top), w: pw, h: ph.offsetHeight || 24 } : null;
    const { x, y, inside } = placeToolbar({ box, bar: { w: bar.offsetWidth, h: bar.offsetHeight }, view, below: editing, avoid: avoid && avoid.y === avoid.y ? avoid : null });
    const padSig = avoid ? `${avoid.x},${avoid.y}` : "";
    if (inside && padSig !== padSeen) {
      padSeen = padSig; // the direct layer paints after the toolbar: look again once it has moved the handle
      requestAnimationFrame?.(update);
    }
    bar.style.left = `${x}px`;
    bar.style.top = `${y}px`;
    if (inside) bar.setAttribute("data-inside", "");
    else bar.removeAttribute("data-inside");
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
    destroy: () => (grown?.disconnect(), bar.remove(), size.remove()),
  };
}

/**
 * mountDirectLayer(canvas, handlers) -> {update, destroy}
 * The padding handles and "Add section" lines (canvas/direct.js), loaded on their own so the toolbar does not wait for them.
 * Mounted only when the screen supplies the handlers they need (refOf).
 */
export function mountDirectLayer(canvas, handlers) {
  let part = null;
  let gone = false;
  if (handlers.refOf)
    import("@hm/canvas/direct")
      .then((m) => {
        if (!gone) part = m.mountDirect(canvas, handlers);
      })
      .catch((err) => console.error("hatamoto canvas: direct", err));
  return { update: () => part?.update(), destroy: () => ((gone = true), part?.destroy()) };
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
      if (pt.parent === "") continue; // the page level has the "Add section" lines
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
