// Entry module of the AI analytics pages ({{define "modules"}}@hm/aia{{end}}).
// Progressive enhancement: every chart is a server-rendered figure with its data
// table; this draws the SVG over it. Does nothing on a page without charts.
//
//   <figure data-hm-aia-chart="time" data-src="<admin>/analytics/data/overview.json?..." data-chart="fetches">
//     data-panels="fetches|referrals"   several keys of the JSON, drawn as stacked panels
//     data-endpoints="url1|url2"             one URL per panel when they differ (default data-src)
//     data-ratio="referrals,pageviews"  AI share: first key summed over the second
//     data-sum="AI fetches|AI visits"  per panel: add the datasets up into one series with that name
//     data-ratio-label="AI share"  data-title  data-hero  data-cols  data-panel-labels="A|B"
//     data-override='{"Spoofed":{"cls":"hm-viz--danger","marker":"triangle"}}'
//   <span data-hm-aia-chart="spark" data-src data-chart [data-minmax] [data-ratio] [data-dataset="label"]> the live hm/spark: sets d/points/label on its svg
//   <div data-hm-aia-tracker> around a server-rendered hm/tracker
//     [data-aia-plot]  template[data-aia-empty]  template[data-aia-error] (with a [data-aia-retry] button)
// See timechart.js for the legend, Show data and live region parts of the contract.
import { drawTimeChart } from "@hm/aia/timechart";
import { fetchJSON, forget } from "@hm/aia/load";
import { ratioSpec, sparkSummary, sparkValues, sumSpec } from "@hm/aia/model";
import { heightFor } from "@hm/aia/scale";
import { sparkPoints } from "@hm/aia/paths";
import { enhanceTracker } from "@hm/aia/tracker";

const handles = new WeakMap(); // figure -> drawn chart
const started = new WeakSet();

const isEmpty = (spec) => spec.labels.length === 0 || spec.datasets.every((d) => d.data.every((v) => v === 0));
const list = (v, sep) => (v ? v.split(sep) : []);

function plotOf(fig) {
  let plot = fig.querySelector("[data-aia-plot]");
  if (!plot) {
    plot = document.createElement("div");
    plot.setAttribute("data-aia-plot", "");
    fig.append(plot);
  }
  return plot;
}

// The URLs and the JSON keys this figure reads.
function sources(d) {
  if (d.ratio) {
    const [num, den] = d.ratio.split(",");
    return { urls: [d.src, d.src], keys: [num, den] }; // the cache makes it one request
  }
  const keys = list(d.panels, "|");
  if (!keys.length) return { urls: [d.src], keys: [d.chart] };
  const urls = list(d.endpoints, "|");
  return { urls: keys.map((_, i) => urls[i] || d.src), keys };
}

function specsFrom(d, jsons, keys) {
  const pick = (i) => {
    const spec = jsons[i].charts && jsons[i].charts[keys[i]];
    if (!spec) throw new Error("no chart " + keys[i]);
    return spec;
  };
  if (d.ratio) return ratioSpec(pick(0), pick(1), d.ratioLabel || "AI share");
  const sums = list(d.sum, "|");
  const specs = keys.map((_, i) => (sums[i] ? sumSpec(pick(i), sums[i]) : pick(i)));
  return specs.length > 1 ? specs : specs[0];
}

function clone(fig, selector) {
  const tpl = fig.querySelector(selector);
  return tpl && tpl.content.firstElementChild ? tpl.content.firstElementChild.cloneNode(true) : null;
}

// The plot keeps its height in every state so the table below never jumps.
function reserve(plot, d) {
  const phone = typeof matchMedia === "function" && matchMedia("(max-width: 599px)").matches;
  const panels = Math.max(1, list(d.panels, "|").length);
  plot.style.minHeight = heightFor({ hero: "hero" in d, panels, phone }) + "px"; // CSSOM, not a style attribute
}

function setData(fig, show) {
  const table = fig.querySelector("[data-aia-data]");
  const sd = fig.querySelector("[data-aia-sd]");
  if (!show) {
    if (table) table.hidden = true;
    if (sd) sd.hidden = true;
  }
}

function showError(fig, plot, retry) {
  const box = clone(fig, "template[data-aia-error]");
  if (!box) return;
  box.setAttribute("data-aia-state", "error");
  // One alert per page: five failing charts would read the same sentence five times.
  box.setAttribute("role", document.querySelector('[data-aia-state="error"][role="alert"]') ? "status" : "alert");
  const btn = box.querySelector("[data-aia-retry]");
  if (btn) btn.addEventListener("click", retry);
  plot.replaceChildren(box);
}

async function mount(fig) {
  const d = fig.dataset;
  const plot = plotOf(fig);
  const { urls, keys } = sources(d);
  // Retry removes the button that has focus: put focus back on what replaces it.
  const regain = !!document.activeElement && fig.contains(document.activeElement) && !!document.activeElement.closest('[data-aia-state="error"]');
  const old = handles.get(fig);
  if (old) old.destroy();
  handles.delete(fig);

  reserve(plot, d);
  fig.setAttribute("aria-busy", "true");
  const skeleton = document.createElement("div");
  skeleton.className = "hm-aia-skeleton";
  skeleton.setAttribute("role", "status");
  skeleton.setAttribute("aria-label", "Loading");
  plot.replaceChildren(skeleton);

  const retry = () => {
    for (const u of urls) forget(u);
    mount(fig);
  };
  let spec;
  try {
    const jsons = await Promise.all(urls.map(fetchJSON));
    spec = specsFrom(d, jsons, keys);
  } catch {
    showError(fig, plot, retry); // the server-rendered table stays: it holds the numbers the chart could not draw
    fig.removeAttribute("aria-busy");
    if (regain) (plot.querySelector("[data-aia-retry]") || plot).focus();
    return;
  }
  fig.removeAttribute("aria-busy");

  const specs = Array.isArray(spec) ? spec : [spec];
  if (specs.every(isEmpty)) {
    const box = clone(fig, "template[data-aia-empty]");
    if (box) {
      box.setAttribute("data-aia-state", "empty");
      box.tabIndex = -1; // focus can land here after a Retry
      plot.replaceChildren(box);
    } else plot.replaceChildren();
    setData(fig, false);
    if (regain && box) box.focus();
    return;
  }

  let override;
  try {
    override = d.override ? JSON.parse(d.override) : undefined;
  } catch {
    override = undefined;
  }
  const title = d.title || (fig.querySelector("h2, h3") || {}).textContent || "";
  plot.replaceChildren();
  plot.style.minHeight = ""; // the chart now sets its own height
  handles.set(
    fig,
    drawTimeChart(fig, spec, {
      chartId: fig.id || keys.join("+"),
      title: title.trim(),
      hero: "hero" in d,
      cols: "cols" in d,
      panelLabels: list(d.panelLabels, "|"),
      override,
    }),
  );
  if (regain) {
    const target = fig.querySelector(".hm-aia-plot") || fig.querySelector("h2, h3") || plot;
    if (target === plot) plot.tabIndex = -1;
    target.focus({ preventScroll: true });
  }
}

// A sparkline reads one chart of the JSON and sets the paths of the server-rendered hm/spark
// (empty until now); a failed request leaves it empty and marked.
async function mountSpark(host) {
  const d = host.dataset;
  const { urls, keys } = sources(d);
  try {
    const spec = specsFrom(d, await Promise.all(urls.map(fetchJSON)), keys);
    const one = Array.isArray(spec) ? spec[0] : spec;
    const values = sparkValues(one, d.dataset);
    const { line, area } = sparkPoints(values, { minmax: "minmax" in d });
    if (!line) return;
    host.querySelector(".hm-spark__area").setAttribute("d", area);
    host.querySelector(".hm-spark__line").setAttribute("points", line);
    const summary = sparkSummary(values, one.bucket === "week" ? "weeks" : "days");
    host.setAttribute("role", "img");
    host.tabIndex = 0;
    host.setAttribute("aria-label", summary);
    host.setAttribute("data-tooltip", summary);
  } catch {
    host.setAttribute("data-aia-state", "error");
  }
}

const KINDS = { time: mount, spark: mountSpark };

export function mountAll(root = document) {
  for (const fig of root.querySelectorAll("[data-hm-aia-chart]")) {
    const kind = KINDS[fig.dataset.hmAiaChart];
    if (!kind || started.has(fig)) continue;
    started.add(fig);
    kind(fig);
  }
  for (const t of root.querySelectorAll("[data-hm-aia-tracker]")) {
    if (started.has(t)) continue;
    started.add(t);
    enhanceTracker(t);
  }
}

mountAll();
document.addEventListener("htmx:load", (e) => mountAll(e.target));
