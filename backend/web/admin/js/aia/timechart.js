// The AI analytics time chart: draws an SVG area/line/column chart from a
// server chartSpec, ported from the approved prototype's timeChart (geometry,
// fills, dashes, markers and compare lines are identical). Colour comes from
// CSS classes (hm-viz--series-N sets --viz), never from script, so the theme
// needs no redraw. Legend, tooltip, keyboard and Show data are added around
// this by the same module.
//
// Markup contract (rendered by Go, hm/aia-chart): inside the host
//   [data-aia-plot]                 where the SVG goes
//   [data-aia-legend-list]          legend container (optional)
//   template[data-aia-legend]       one legend <button aria-pressed> holding [data-aia-swatch] and [data-aia-name]
//   template[data-aia-legend-static] the same shape as a non-interactive item (Previous period)
//   button[data-aia-sd]             "Show data" (hidden until enhanced) controlling [data-aia-data]
//   [data-aia-data]                 the server-rendered data table wrapper (JS only toggles hidden)
//   [data-aia-live]                 aria-live="polite" region (created when missing)
// JS builds only SVG, the tooltip div and clones of those templates; values
// are always set with textContent, never as markup.
import { el } from "@hm/aia/svg";
import { announce, buildModel, axisLabel, stepIndex, summary, toggleHidden, tooltipRows } from "@hm/aia/model";
import { GEOM, heightFor, layout, nice, plotWidth, tickCount, xTickIndexes, xTickStep, yTicks } from "@hm/aia/scale";
import { areaPath, columns, COMPARE_DASH, COLUMN_RX, linePath, MARKER_R, markerIndexes, OPACITY, STROKE } from "@hm/aia/paths";
import { markerPath } from "@hm/aia/series";

const MIN_WIDTH = 50; // narrower than this (hidden, collapsed) is not drawn

// State that survives a redraw or a remount (Retry), keyed by chart id.
const sharedHidden = new Set(); // "<chartId>|<series>"
const dataOpen = new Set(); // chart ids whose data table is open

const marker = (kind, x, y, r, cls) => {
  const { tag, attrs } = markerPath(kind, x, y, r);
  return el(tag, { ...attrs, class: attrs.class + " " + cls });
};

/**
 * Paint the chart geometry for `model` into a new <svg> of width W and height H.
 * Returns {svg, geometry} where geometry has what the tooltip needs
 * ({n, xs, innerWidth, panels: [{panel, visible, y0, y}]}).
 */
export function paint(model, W, H) {
  const n = model.labels.length;
  const iw = plotWidth(W);
  const xs = (i) => GEOM.L + (n < 2 ? iw / 2 : (i * iw) / (n - 1));
  const step = xTickStep(n, iw);
  const active = model.panels.filter((p) => p.active);
  const visCount = model.panels.reduce((a, p) => a + p.series.filter((s) => s.visible).length, 0);
  const places = layout({ height: H, panels: active.length, labelled: model.multi });
  const svg = el("svg", { class: "hm-aia-plot", height: H, tabindex: 0, role: "group" });
  const geometry = { n, xs, innerWidth: iw, step, W, H, panels: [] };

  active.forEach((p, pi) => {
    const { top: y0, innerH: ph } = places[pi];
    const vs = p.series.filter((s) => s.visible);
    const nz = nice(p.max, p.int, tickCount(ph));
    const y = (v) => y0 + ph - (v / nz.max) * ph;
    const ys = (values) => values.map(y);
    const allX = Array.from({ length: n }, (_, i) => xs(i));
    if (model.multi) svg.append(el("text", { class: "hm-aia-panel-label", x: GEOM.L, y: y0 - 7 }, p.label));
    for (const t of yTicks(nz.max, nz.step)) {
      svg.append(el("line", { class: "hm-aia-grid", x1: GEOM.L, x2: W - GEOM.RT, y1: y(t), y2: y(t) }));
      svg.append(el("text", { class: "hm-aia-axis", x: GEOM.L - 8, y: y(t), "text-anchor": "end", dy: ".32em" }, p.axis(t)));
    }

    // stacked series sit on the one below; the others rise from zero
    let base = Array(n).fill(0);
    const tops = {};
    for (const s of vs) {
      const top = p.stacked ? base.map((b, i) => b + s.values[i]) : s.values;
      tops[s.key] = top;
      if (p.cols) {
        for (const c of columns(allX, ys(top), y(0), iw, n)) svg.append(el("rect", { class: "hm-aia-col " + s.cls, x: c.x, y: c.y, width: c.w, height: c.h, rx: COLUMN_RX }));
      } else if (n > 1) {
        svg.append(el("path", { class: "hm-aia-area " + s.cls, d: areaPath(allX, ys(top), ys(p.stacked ? base : Array(n).fill(0))), "fill-opacity": p.stacked ? OPACITY.stacked : OPACITY.single }));
        svg.append(el("path", { class: "hm-aia-line " + s.cls, d: linePath(allX, ys(top)), "stroke-width": STROKE.line, "stroke-linejoin": "round", "stroke-dasharray": s.style.dash || "none" }));
      } else {
        svg.append(marker(s.style.marker, xs(0), y(top[0]), MARKER_R.single, s.cls));
      }
      if (!p.cols && n > 1) for (const i of markerIndexes(n, visCount, step)) svg.append(marker(s.style.marker, xs(i), y(top[i]), MARKER_R.point, s.cls));
      if (p.stacked) base = top;
    }

    // previous period: one dashed total when stacked, one dashed line per series otherwise
    if (p.previousTotal && n > 1 && !p.cols) {
      const lines = p.stacked
        ? [{ values: p.previousTotal, cls: "hm-aia-line--muted", opacity: OPACITY.compareTotal }]
        : vs.map((s) => ({ values: s.previous, cls: s.cls, opacity: OPACITY.compareSeries }));
      for (const l of lines) svg.append(el("path", { class: "hm-aia-line hm-aia-line--compare " + l.cls, d: linePath(allX, ys(l.values)), "stroke-opacity": l.opacity, "stroke-width": STROKE.compare, "stroke-dasharray": COMPARE_DASH }));
    }
    geometry.panels.push({ panel: p, visible: vs, y0, y, tops });
  });

  for (const i of xTickIndexes(n, step)) svg.append(el("text", { class: "hm-aia-axis", x: xs(i), y: H - 6, "text-anchor": "middle" }, axisLabel(model, i)));
  svg.append(el("g", { class: "hm-aia-hover" }));
  return { svg, geometry };
}

const clone = (host, selector) => {
  const tpl = host.querySelector(selector);
  return tpl && tpl.content.firstElementChild ? tpl.content.firstElementChild.cloneNode(true) : null;
};

// The legend swatch: a short line with the series marker, as in the prototype.
function legendIcon(style, cls, dash) {
  const svg = el("svg", { class: "hm-aia-lgi", width: 22, height: 10, "aria-hidden": "true" });
  svg.append(el("line", { class: "hm-aia-line " + cls, x1: 0, x2: 22, y1: 5, y2: 5, "stroke-width": 2, "stroke-dasharray": dash || "none" }));
  if (style && style.marker) svg.append(marker(style.marker, 11, 5, 3.5, cls));
  return svg;
}

const div = (cls, text) => {
  const d = document.createElement("div");
  d.className = cls;
  if (text !== undefined) d.textContent = text;
  return d;
};

function tooltipNode(content) {
  const box = document.createDocumentFragment();
  box.append(div("hm-aia-tt__date", content.head));
  for (const r of content.rows) {
    if (r.kind === "heading") {
      box.append(div("hm-aia-tt__head", r.name));
      continue;
    }
    const row = div("hm-aia-tt__row" + (r.kind === "total" ? " hm-aia-tt__row--total" : r.kind === "previous" ? " hm-aia-tt__row--muted" : ""));
    if (r.kind === "series" || r.kind === "previous") {
      const sq = document.createElement("i");
      sq.className = "hm-aia-sq " + (r.kind === "series" ? r.cls : "hm-aia-sq--previous");
      row.append(sq);
    }
    const name = document.createElement("span");
    name.textContent = r.name;
    const value = document.createElement("b");
    value.textContent = r.value;
    row.append(name, value);
    box.append(row);
  }
  return box;
}

/**
 * Draw a time chart into host's [data-aia-plot] (appended to the host when absent).
 * `spec` is one chartSpec or an array (one panel each). opts: chartId, title,
 * hero, cols, panelLabels, override. Series visibility and the open data table
 * are kept per chartId across redraws and remounts.
 * Returns {redraw, destroy, model}.
 */
export function drawTimeChart(host, spec, opts = {}) {
  let plot = host.querySelector("[data-aia-plot]");
  if (!plot) {
    plot = document.createElement("div");
    plot.setAttribute("data-aia-plot", "");
    host.append(plot);
  }
  const id = opts.chartId || "";
  const state = { model: null, geometry: null, hi: null, tapped: false };
  const phone = typeof matchMedia === "function" ? matchMedia("(max-width: 599px)") : null;
  const build = () => buildModel(spec, { chartId: id, hidden: sharedHidden, title: opts.title, cols: opts.cols, panelLabels: opts.panelLabels, override: opts.override });

  const live = host.querySelector("[data-aia-live]") || plot.insertAdjacentElement("afterend", div("hm-aia-live hm-visually-hidden")) || plot;
  live.setAttribute("data-aia-live", "");
  if (!live.hasAttribute("aria-live")) live.setAttribute("aria-live", "polite");
  const tt = div("hm-aia-tt");
  tt.hidden = true;
  plot.append(tt);

  // --- legend: one button per series, cloned from the server's template
  const keys = build().panels.flatMap((p) => p.series.map((s) => s.key));
  const legend = host.querySelector("[data-aia-legend-list]");
  const buttons = new Map();
  if (legend) {
    legend.replaceChildren();
    for (const p of build().panels)
      for (const s of p.series) {
        const item = clone(host, "template[data-aia-legend]");
        if (!item) continue;
        item.querySelector("[data-aia-swatch]").replaceChildren(legendIcon(s.style, s.cls, s.style.dash));
        item.querySelector("[data-aia-name]").textContent = s.name;
        item.addEventListener("click", () => {
          const r = toggleHidden(sharedHidden, id, s.key, keys);
          if (!r.ok) return;
          sharedHidden.clear();
          r.hidden.forEach((k) => sharedHidden.add(k));
          syncLegend();
          redraw();
        });
        buttons.set(s.key, item);
        legend.append(item);
      }
    if (build().compare) {
      const item = clone(host, "template[data-aia-legend-static]");
      if (item) {
        item.querySelector("[data-aia-swatch]").replaceChildren(legendIcon(null, "hm-aia-line--muted", COMPARE_DASH));
        item.querySelector("[data-aia-name]").textContent = "Previous period";
        legend.append(item);
      }
    }
  }
  function syncLegend() {
    const visible = keys.filter((k) => !sharedHidden.has(id + "|" + k));
    for (const [key, btn] of buttons) {
      const on = !sharedHidden.has(id + "|" + key);
      btn.setAttribute("aria-pressed", String(on));
      btn.setAttribute("aria-disabled", String(on && visible.length <= 1));
    }
  }

  // --- Show data: the table is server-rendered, JS only toggles hidden
  const sd = host.querySelector("[data-aia-sd]");
  const table = host.querySelector("[data-aia-data]");
  const onSd = () => {
    if (dataOpen.has(id)) dataOpen.delete(id);
    else dataOpen.add(id);
    syncData();
  };
  function syncData() {
    if (!sd || !table) return;
    const open = dataOpen.has(id);
    table.hidden = !open;
    sd.setAttribute("aria-expanded", String(open));
  }
  if (sd && table) {
    sd.hidden = false;
    sd.addEventListener("click", onSd);
    syncData();
  }

  // --- tooltip, cursor and keyboard
  function show(i, say) {
    const { geometry, model } = state;
    if (!geometry) return;
    state.hi = i;
    const g = plot.querySelector(".hm-aia-hover");
    g.replaceChildren();
    for (const q of geometry.panels) {
      g.append(el("line", { class: "hm-aia-cursor", x1: geometry.xs(i), x2: geometry.xs(i), y1: q.y0, y2: q.y(0) }));
      for (const s of q.visible) g.append(marker(s.style.marker, geometry.xs(i), q.y(q.tops[s.key][i]), MARKER_R.hover, s.cls));
    }
    tt.replaceChildren(tooltipNode(tooltipRows(model, i)));
    tt.hidden = false;
    const x = geometry.xs(i);
    const w = tt.offsetWidth;
    tt.style.left = (x + 16 + w > plot.clientWidth ? Math.max(0, x - 16 - w) : x + 16) + "px"; // CSSOM, not a style attribute
    if (say) live.textContent = announce(model, i);
  }
  function hide() {
    state.hi = null;
    tt.hidden = true;
    const g = plot.querySelector(".hm-aia-hover");
    if (g) g.replaceChildren();
  }
  function wire(svg) {
    const n = state.geometry.n;
    const idxAt = (e) => {
      const r = svg.getBoundingClientRect();
      return Math.max(0, Math.min(n - 1, Math.round(((e.clientX - r.left - GEOM.L) / state.geometry.innerWidth) * (n - 1)) || 0));
    };
    svg.addEventListener("pointermove", (e) => show(idxAt(e)));
    svg.addEventListener("pointerdown", (e) => {
      state.tapped = true;
      show(idxAt(e), true);
    });
    svg.addEventListener("pointerleave", (e) => {
      if (e.pointerType !== "touch" && document.activeElement !== svg) hide();
    });
    svg.addEventListener("focus", () => {
      if (!state.tapped && state.hi === null) show(n - 1, true);
      state.tapped = false;
    });
    svg.addEventListener("blur", hide);
    svg.addEventListener("keydown", (e) => {
      const next = stepIndex(state.hi, e.key, n);
      if (next === undefined) return;
      if (next === null) {
        e.stopPropagation(); // Escape closes the tooltip, not a dialog around the chart
        hide();
        return;
      }
      e.preventDefault();
      show(next, true);
    });
  }

  function redraw() {
    const W = plot.clientWidth;
    if (W < MIN_WIDTH) return;
    state.model = build();
    const panels = Array.isArray(spec) ? spec.length : 1;
    const H = heightFor({ hero: !!opts.hero, panels, phone: !!(phone && phone.matches) });
    const focused = plot.contains(document.activeElement) && document.activeElement.tagName.toLowerCase() === "svg";
    const { svg, geometry } = paint(state.model, W, H);
    svg.setAttribute("aria-label", summary(state.model));
    state.geometry = geometry;
    plot.replaceChildren(svg, tt);
    wire(svg);
    if (focused) svg.focus({ preventScroll: true });
    if (state.hi !== null) show(Math.min(state.hi, geometry.n - 1));
  }

  const observer = typeof ResizeObserver === "function" ? new ResizeObserver(() => redraw()) : null;
  if (observer) observer.observe(plot);
  syncLegend();
  redraw();
  return {
    redraw,
    get model() {
      return state.model;
    },
    destroy() {
      if (observer) observer.disconnect();
      if (sd) sd.removeEventListener("click", onSd);
      plot.replaceChildren();
      if (legend) legend.replaceChildren();
    },
  };
}
