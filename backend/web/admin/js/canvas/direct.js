// Direct manipulation on the canvas: padding handles on a selected section (drag or arrow keys), the padding hatch with its
// value badge, and the "Add section" strips between top-level elements. A lazy chunk of the canvas host, built once and
// repositioned on `update()`; its elements live in their own layer, never in the host's redrawn marks, so a hover redraw cannot
// end a drag. A drag only previews in the frame (setVar, no commit); pointer-up makes ONE commit through handlers.onPadCommit.
import { styleDecls } from "@hm/canvas/host";
import { toBridge } from "@hm/canvas/protocol";
import { boundaryIndex, hatchRects, keepInside, overlaps, pillPoints, pillShift } from "@hm/canvas/chromemodel";
import { OPPOSITE, SIDES, padAria, padLabel, padValue, padWrites, startPx } from "@hm/canvas/padding";

const SIDE_LIST = ["top", "right", "bottom", "left"]; // the order of a layout node's `pd`
const STEP = { ArrowUp: 1, ArrowRight: 1, ArrowDown: -1, ArrowLeft: -1 }; // slider keys: up/right grow
const VERTICAL = { top: true, bottom: true };

function el(tag, props = {}, ...kids) {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(props)) {
    if (v === false || v === null || v === undefined) continue;
    if (k === "text") e.textContent = v;
    else e.setAttribute(k, v === true ? "" : v);
  }
  e.append(...kids);
  return e;
}

/**
 * mountDirect(canvas, hd) -> {update, destroy}
 * canvas: the host (stage, scale, nodes, selectionBox, editable, editingId, apply).
 * hd: {isSection(id), padState(id) -> {bp}, styleEntry(key), refOf(id) -> {sel, key}, onPadCommit({id, writes, label, coalesce?}) -> false when refused, stamp?() -> the last history entry,
 *      onAddSection(index, el), indexOf(id), total(), authored?(id, side) -> raw style value, nameOf?(id)}.
 */
export function mountDirect(canvas, hd) {
  const HANDLE_HALF = 12; // CSS px: half the thickness of a padding handle (pageeditor.css .hm-pe__padh)
  const HANDLE_LONG = 24;
  const layer = el("div", { class: "hm-pe__pad", "data-hm-pe-pad": "" });
  const strips = el("div", { class: "hm-pe__strips", "data-hm-pe-strips": "" });
  const hatches = {};
  const handles = {};
  const badge = el("span", { class: "hm-pe__padbadge", "data-hm-pe-padbadge": "", "aria-hidden": "true", hidden: true });
  for (const side of SIDE_LIST) {
    hatches[side] = el("div", { class: "hm-pe__hatch", "data-side": side, "data-hm-pe-hatch": "", "aria-hidden": "true", hidden: true });
    handles[side] = el("div", { class: "hm-pe__padh", "data-side": side, "data-hm-pe-padh": "", role: "slider", tabindex: "0", "aria-valuemin": "0", "aria-valuemax": "1000", "aria-orientation": VERTICAL[side] ? "vertical" : "horizontal", hidden: true });
  }
  layer.append(...SIDE_LIST.map((s) => hatches[s]), badge, ...SIDE_LIST.map((s) => handles[s]));
  canvas.stage.append(layer, strips);

  let drag = null; // {id, side, alt, bp, ref, start, x0, y0, live: {side: px}, previewed, entries}
  let pend = { id: null, vals: {} }; // keyboard writes the frame has not reported back yet: side -> px
  let seq = 0; // the arrow-key session: presses within one coalesce into one history entry
  let stamp; // hd.stamp() after our last key commit: when it moves, an undo, redo or other edit happened
  const endSession = () => {
    pend = { id: null, vals: {} };
    seq++;
  };
  let hoverSide = null; // from the inspector's padding fields
  let overSide = null; // the handle under the pointer: its band shows too
  let shownId = null; // the section the handles were last drawn for
  const hot = () => overSide ?? hoverSide;
  let current = null; // {id, box, pd}: what the handles show now
  const pills = [];
  let lastHover = null; // the pill is reached by crossing the stage, which ends the frame's hover: it stays until the pointer leaves the canvas
  // A pointer leaving a frame that holds it gives the pane no mouseleave, so the page's own pointer events end it: any over something outside the canvas pane.
  const onOutside = (e) => {
    const pane = canvas.stage.parentElement;
    if (!lastHover || !pane || pane.contains(e.target)) return;
    lastHover = null;
    paintStrips();
  };
  document.addEventListener("mouseover", onOutside);
  // The pane scrolling moves the toolbar (pinned to the view's top) and the view's bounds under a selected section's pills: they are placed again, after the toolbar's own listener.
  const onScroll = () => pills.some((q) => q.hasAttribute("data-sel")) && paintStrips();
  canvas.stage.parentElement?.addEventListener("scroll", onScroll, { passive: true });

  // The section whose handles show: the selected one, when it is a section, editable, and not being typed in.
  function target() {
    const box = canvas.selectionBox;
    if (!box || !canvas.editable || canvas.editingId != null || !hd.isSection(box.id)) return null;
    const n = canvas.nodes.find((x) => x.id === box.id);
    const measured = SIDE_LIST.map((s) => startPx(n?.pd, s, hd.authored?.(box.id, s)));
    return { id: box.id, box, pd: measured, measuredReal: Array.isArray(n?.pd) };
  }

  function valueOf(t, side) {
    if (drag && drag.id === t.id && side in drag.live) return drag.live[side];
    if (pend.id === t.id && side in pend.vals) return pend.vals[side];
    return t.pd[SIDE_LIST.indexOf(side)];
  }

  function paint() {
    const t = (current = target());
    if (t?.id !== shownId) overSide = null; // the selection moved under a still pointer: no pointerleave comes, so the old hover is dropped
    shownId = t?.id ?? null;
    layer.hidden = !t;
    if (!t) {
      for (const s of SIDE_LIST) hatches[s].hidden = handles[s].hidden = true;
      badge.hidden = true;
    } else {
      const sc = canvas.scale;
      const pd = SIDE_LIST.map((s) => valueOf(t, s));
      const { x, y, w, h } = t.box;
      for (const r of hatchRects({ w, h }, pd)) {
        const hatch = hatches[r.side];
        const on = drag ? r.side === drag.side || (drag.alt && r.side === OPPOSITE[drag.side]) : r.side === hot();
        hatch.hidden = !on;
        hatch.style.left = `${(x + r.x) * sc}px`;
        hatch.style.top = `${(y + r.y) * sc}px`;
        hatch.style.width = `${r.w * sc}px`;
        hatch.style.height = `${r.h * sc}px`;
      }
      // Each handle sits in the middle of its padding band, kept 8px inside the section so a section with no padding still shows it (and a short one never stacks them)
      const EDGE = 8;
      const y0 = y * sc;
      const y1 = (y + h) * sc;
      const topC = Math.max(y0 + EDGE, (y + pd[0] / 2) * sc);
      const botC = Math.min(y1 - EDGE, (y + h - pd[2] / 2) * sc);
      const at = { top: [x + w / 2, topC], right: [x + w - pd[1] / 2, y + h / 2], bottom: [x + w / 2, botC], left: [x + pd[3] / 2, y + h / 2] };
      SIDE_LIST.forEach((s, i) => {
        const hnd = handles[s];
        hnd.hidden = false;
        const room = canvas.stage.offsetWidth || Infinity; // the frame: a handle on its edge (padding 0) must not be cut off
        let left = VERTICAL[s] ? at[s][0] * sc : keepInside(at[s][0] * sc, HANDLE_HALF, room);
        let top = VERTICAL[s] ? at[s][1] : at[s][1] * sc;
        if (s === "top") top = Math.min(topC, botC - HANDLE_LONG) - HANDLE_HALF; // its box hangs from the top (the toolbar's avoid reads it); a short section: it ends where the bottom one starts
        if (drag && s === drag.side) {
          // The band's middle moves at half the pointer's speed: the grabbed handle follows the pointer instead, or it would be left behind
          // and the release would land on the frame, which never reports it to the host.
          left = drag.h0.left + (VERTICAL[s] ? 0 : drag.dx);
          top = drag.h0.top + (VERTICAL[s] ? drag.dy : 0);
        }
        hnd.style.left = `${left}px`;
        hnd.style.top = `${top}px`;
        hnd.setAttribute("aria-valuenow", String(pd[i]));
        hnd.setAttribute("aria-label", padAria(s, pd[i]));
      });
      const shown = drag ? drag.side : hot();
      badge.hidden = !shown;
      if (shown) {
        const i = SIDE_LIST.indexOf(shown);
        const r = hatchRects({ w, h }, pd)[i];
        badge.textContent = String(pd[i]);
        // off to the side of the handle, so the value is not under the pointer
        badge.style.left = `${(x + r.x + r.w / 2) * sc + (VERTICAL[shown] ? 40 : 0)}px`;
        badge.style.top = `${(y + r.y + r.h / 2) * sc + (VERTICAL[shown] ? 0 : -30)}px`;
      }
    }
    paintStrips();
  }

  const topOf = (id) => {
    let n = canvas.nodes.find((x) => x.id === id);
    while (n && n.p !== "") n = canvas.nodes.find((x) => x.id === n.p);
    return n?.id ?? null;
  };
  // A pill shows (and takes the pointer) around the hovered section and alone on an empty page. At rest the selected section's edges show none:
  // its padding handles and W x H badge own them. Hovering the selected section shows its two pills ("sel"), drawn at the right end of the row
  // (data-sel, pageeditor.css) clear of the centred handle and badge. The boundary after the selected section is also the one tab stop.
  // The rest are inert, so no invisible element swallows a click.
  function showOf(b, selTop, hoverTop) {
    if (b.before === null && b.after === null) return "page";
    if (hoverTop && (b.before === hoverTop || b.after === hoverTop)) return hoverTop === selTop ? "sel" : "hover";
    return null;
  }

  // A selected section's pill sits at the row's right end. On a narrow stage (a phone at 50%) or a short section that is under the centred handle, the badge, the toolbar or
  // the next boundary's pill: measured against their rects, it drops to its own row (below a bottom edge, above a top one) instead, never out of the visible stage (under the
  // sticky bar). `outward` is 0 for a pill off the selected section's edges. With no free spot it keeps its row and the colliding pill yields (one pill per boundary stays clickable).
  const rectOf = (e) => {
    const r = e.getBoundingClientRect();
    return { x: r.left, y: r.top, w: r.width, h: r.height };
  };
  function placePill(p, outward) {
    const pill = p.children[0];
    if (!outward || typeof pill.getBoundingClientRect !== "function") return;
    const stage = canvas.stage;
    const others = [...stage.querySelectorAll("[data-hm-pe-padh], [data-hm-pe-size], [data-hm-pe-toolbar], .hm-pe__tag--sel")].filter((e) => !e.hidden).map(rectOf);
    const near = pills.filter((q) => q !== p && !q.hasAttribute("data-yield"));
    const scroller = stage.parentElement;
    const sr = scroller?.getBoundingClientRect?.();
    const bounds = sr ? { x: sr.left, y: sr.top + (canvas.inset ?? 0), w: sr.width, h: sr.height - (canvas.inset ?? 0) } : null;
    const dy = pillShift(rectOf(pill), [...others, ...near.map((q) => rectOf(q.children[0]))], outward, 4, bounds);
    if (dy) pill.style.top = `${dy}px`;
    if (dy === null) {
      const stop = (e) => e.getAttribute("tabindex") === "0"; // a tab stop is never hidden: the other pill yields
      near.forEach((q) => {
        if (!overlaps(rectOf(pill), rectOf(q.children[0]))) return;
        if (!stop(q)) q.setAttribute("data-yield", "");
        else if (!stop(p)) p.setAttribute("data-yield", "");
      });
    }
  }

  function paintStrips() {
    const points = canvas.editingId != null ? [] : pillPoints(canvas.nodes);
    if (canvas.hoverId) lastHover = canvas.hoverId;
    const selTop = canvas.selectionBox ? topOf(canvas.selectionBox.id) : null;
    const hoverTop = lastHover ? topOf(lastHover) : null;
    strips.hidden = canvas.editingId != null;
    while (pills.length > points.length) pills.pop().remove();
    pills.forEach((q) => q.removeAttribute?.("data-yield"));
    points.forEach((b, i) => {
      let p = pills[i];
      if (!p) {
        p = el("button", { type: "button", class: "hm-pe__gapline", "data-hm-pe-gapline": "" }, el("span", { class: "hm-pe__pill", "aria-hidden": "true", text: "Add section" }));
        p.addEventListener("click", () => {
          const at = boundaryIndex(p._b, hd.indexOf, hd.total());
          if (at !== null) hd.onAddSection(at, p);
        });
        pills[i] = p;
        strips.append(p);
      }
      p._b = b;
      p.setAttribute("aria-label", b.before === null ? "Add section at the top" : b.after === null ? "Add section at the end" : `Add section after ${hd.nameOf?.(b.before) || "this one"}`);
      const show = showOf(b, selTop, hoverTop);
      if (show) p.setAttribute("data-show", show);
      else p.removeAttribute("data-show");
      if (selTop && (b.before === selTop || b.after === selTop)) p.setAttribute("data-sel", "");
      else p.removeAttribute("data-sel");
      p.setAttribute("tabindex", (selTop && b.before === selTop) || show === "page" ? "0" : "-1");
      p.style.top = `${b.y * canvas.scale}px`;
      p.children[0].style.top = "";
    });
    // once every row is in place, so each pill measures its neighbours where they really are; for every state: the pill must not jump while the pointer is on its way to it
    points.forEach((b, i) => placePill(pills[i], selTop && (b.before === selTop || b.after === selTop) ? (b.before === selTop ? 1 : -1) : 0));
  }

  /** Previews (or, with null, restores) one side in the frame. Nothing is committed. */
  function preview(d, side, px) {
    const entry = d.entries[side];
    const decls = entry && styleDecls(entry, px === null ? null : `${px}px`);
    if (!decls) return;
    if (canvas.apply([toBridge.setVar({ sel: d.ref.sel, key: d.ref.key, decls })])) d.previewed = true;
  }
  function previewAll(d, px) {
    preview(d, d.side, px);
    if (d.alt) preview(d, OPPOSITE[d.side], px);
  }

  function end(commitIt, e) {
    const d = drag;
    if (!d) return;
    document.removeEventListener("keydown", onEsc, true);
    drag = null;
    try {
      handles[d.side].releasePointerCapture?.(e?.pointerId);
    } catch {
      /* the capture is already gone */
    }
    const px = d.live[d.side];
    if (commitIt && px !== d.start) {
      const ok = hd.onPadCommit({ id: d.id, writes: padWrites({ id: d.id, bp: d.bp, side: d.side, px, alt: d.alt }), label: padLabel(d.side, d.bp) });
      if (ok === false) previewAll(d, null);
    } else if (d.previewed) previewAll(d, null);
    paint();
  }
  const onEsc = (e) => {
    if (e.key !== "Escape") return;
    e.preventDefault?.();
    e.stopPropagation?.();
    end(false);
  };

  SIDE_LIST.forEach((side) => {
    const hnd = handles[side];
    hnd.addEventListener("pointerdown", (e) => {
      if (drag || e.button > 0 || !current) return;
      e.preventDefault?.();
      e.stopPropagation?.();
      const id = current.id;
      const keys = [side, OPPOSITE[side]];
      const start = valueOf(current, side);
      drag = {
        id,
        side,
        alt: !!e.altKey, // decided now: the opposite side is part of this gesture or it is not
        bp: hd.padState(id).bp,
        ref: hd.refOf(id),
        start,
        x0: e.clientX,
        y0: e.clientY,
        dx: 0,
        dy: 0,
        h0: { left: parseFloat(hnd.style.left) || 0, top: parseFloat(hnd.style.top) || 0 }, // where the handle was grabbed
        live: { [side]: start, [OPPOSITE[side]]: valueOf(current, OPPOSITE[side]) },
        previewed: false,
        entries: Object.fromEntries(keys.map((s) => [s, hd.styleEntry(SIDES[s])])),
      };
      hnd.focus?.({ preventScroll: true }); // preventDefault above stops the browser doing it, and Esc only reaches the host when focus is here, not in the frame
      hnd.setPointerCapture?.(e.pointerId);
      document.addEventListener("keydown", onEsc, true);
      paint();
    });
    hnd.addEventListener("pointerenter", () => {
      overSide = side;
      if (!drag) paint();
    });
    hnd.addEventListener("pointerleave", () => {
      overSide = null;
      if (!drag) paint();
    });
    hnd.addEventListener("pointermove", (e) => {
      if (!drag || drag.side !== side) return;
      drag.dx = e.clientX - drag.x0;
      drag.dy = e.clientY - drag.y0;
      const d = VERTICAL[side] ? drag.dy : drag.dx;
      const px = padValue({ start: drag.start, d, scale: canvas.scale, side, shift: e.shiftKey });
      drag.live[side] = px;
      if (drag.alt) drag.live[OPPOSITE[side]] = px;
      previewAll(drag, px);
      paint();
    });
    hnd.addEventListener("pointerup", (e) => end(true, e));
    hnd.addEventListener("pointercancel", (e) => end(false, e));
    hnd.addEventListener("lostpointercapture", (e) => end(false, e)); // the capture ended without a pointer-up (after a pointer-up, the drag is already over)
    hnd.addEventListener("blur", endSession); // after focus leaves, an undo or the inspector may change the value
    hnd.addEventListener("keydown", (e) => {
      const dir = STEP[e.key];
      if (!dir || drag || !current) return;
      e.preventDefault?.();
      const id = current.id;
      if (pend.id !== id || hd.stamp?.() !== stamp) {
        endSession(); // another node, or the history moved under us: a new session, and the old pending values are stale
        pend.id = id;
      }
      const was = valueOf(current, side);
      const px = padValue({ start: was, d: dir * (e.shiftKey ? 8 : 1), scale: 1, side: "top" }); // a step is already in page px: no zoom, and no side flips it
      if (px === was) return;
      const alt = !!e.altKey;
      const bp = hd.padState(id).bp;
      if (hd.onPadCommit({ id, writes: padWrites({ id, bp, side, px, alt }), label: padLabel(side, bp), coalesce: `pad${seq}:${id}:${side}:${alt ? "a" : ""}:${bp}` }) === false) return;
      pend.id = id; // a synchronous update() during the commit may have ended the session
      stamp = hd.stamp?.();
      pend.vals[side] = px;
      if (alt) pend.vals[OPPOSITE[side]] = px;
      paint();
    });
  });

  // The inspector's padding fields report hover/focus: show that side's hatch and value.
  const onHover = (e) => {
    hoverSide = e.detail?.side ?? null;
    if (!drag) paint();
  };
  document.addEventListener("hm-pad-hover", onHover);

  function update() {
    if (drag) return; // the drag repaints itself from the pointer; the frame's report lags it
    if (pend.id !== null && hd.stamp?.() !== stamp) endSession();
    if (current && pend.id === current.id) {
      // keyboard values the frame has now reported are no longer pending
      const n = canvas.nodes.find((x) => x.id === current.id);
      for (const s of Object.keys(pend.vals)) if (n?.pd?.[SIDE_LIST.indexOf(s)] === pend.vals[s]) delete pend.vals[s];
    }
    paint();
  }

  paint();
  return {
    update,
    destroy() {
      document.removeEventListener("hm-pad-hover", onHover);
      document.removeEventListener("mouseover", onOutside);
      canvas.stage.parentElement?.removeEventListener("scroll", onScroll);
      document.removeEventListener("keydown", onEsc, true);
      layer.remove();
      strips.remove();
    },
  };
}
