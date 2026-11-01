// Draw model for the AI analytics time chart: turns the server's chartSpec
// ({kind, stacked, bucket, format, labels, datasets[{label,data}], previous?})
// into panels, visible series, totals, previous-period lines, table rows,
// the aria summary, keyboard steps and tooltip rows. Pure data in, plain data
// out: no DOM, so Node can import it. Ported from the prototype's timeChart.
import { compact, dayLabel, longDate, num, percent, weekLabel } from "@hm/aia/fmt";
import { seriesFor, vizClass } from "@hm/aia/series";

const OTHER = "Other";
// Series numbers of the neutral steps a neutral chart cycles through.
const NEUTRAL_STEPS = [2, 3, 4];

/** Hidden series are kept as "<chartId>|<seriesKey>" so charts do not share state. */
export const hiddenKey = (chartId, key) => chartId + "|" + key;

/** Previous values by index. The server buckets the previous period by the current range's weeks, so lengths match; a short list is zero-padded. */
export function alignPrevious(values, length) {
  return Array.from({ length }, (_, i) => (values && values[i]) || 0);
}

const alignLabels = (labels, length) => Array.from({ length }, (_, i) => (labels && labels[i]) || "");

/** Per bucket sum of a chartSpec's datasets (or of the one named `dataset`). */
export function sparkValues(spec, dataset) {
  const sets = dataset ? spec.datasets.filter((d) => d.label === dataset) : spec.datasets;
  return spec.labels.map((_, i) => sets.reduce((a, d) => a + (d.data[i] || 0), 0));
}

/** The text alternative of a sparkline: the shape only, as the server's ui.sparkSummary does. */
export function sparkSummary(vals, unit = "days") {
  const half = Math.floor(vals.length / 2);
  const sum = (a) => a.reduce((x, y) => x + y, 0);
  const first = sum(vals.slice(0, half));
  const second = sum(vals.slice(Math.ceil(vals.length / 2)));
  const lead = "Trend over " + vals.length + " " + unit + ": ";
  if (first === 0) return lead + (second > 0 ? "ending higher" : "steady");
  const change = ((second - first) / first) * 100;
  return lead + (change >= 0.5 ? "ending higher" : change <= -0.5 ? "ending lower" : "steady");
}

/** Per bucket num / den * 100; a zero denominator gives 0. */
export function ratioSeries(num_, den) {
  return num_.map((v, i) => (den[i] ? (v / den[i]) * 100 : 0));
}

/**
 * A spec with one series that is the ratio (as a percentage) of the summed
 * `numSpec` datasets over the first dataset of `denSpec`, previous included
 * when both carry one. Used for "AI share over time".
 */
export function ratioSpec(numSpec, denSpec, label) {
  const total = (datasets, n) => Array.from({ length: n }, (_, i) => datasets.reduce((a, d) => a + (d.data[i] || 0), 0));
  const den = (spec) => (spec.datasets[0] || { data: [] }).data;
  const out = {
    kind: "line",
    stacked: false,
    bucket: numSpec.bucket,
    format: "percent",
    labels: numSpec.labels,
    datasets: [{ label, data: ratioSeries(total(numSpec.datasets, numSpec.labels.length), den(denSpec)) }],
  };
  if (numSpec.previous && denSpec.previous) {
    out.previous = {
      labels: numSpec.previous.labels,
      datasets: [{ label, data: ratioSeries(total(numSpec.previous.datasets, numSpec.previous.labels.length), den(denSpec.previous)) }],
    };
  }
  return out;
}

/**
 * One series named `label` that is the sum of every dataset of `spec` per
 * bucket, previous included, drawn unstacked (the hero's "AI fetches" line).
 */
export function sumSpec(spec, label) {
  const total = (datasets, n) => Array.from({ length: n }, (_, i) => datasets.reduce((a, d) => a + (d.data[i] || 0), 0));
  const out = { ...spec, stacked: false, datasets: [{ label, data: total(spec.datasets, spec.labels.length) }] };
  if (spec.previous) out.previous = { ...spec.previous, datasets: [{ label, data: total(spec.previous.datasets, spec.previous.labels.length) }] };
  return out;
}

const formatters = (format) => {
  if (format === "percent") return { fmt: (v) => percent(v), axis: (v) => compact(v) + "%", sum: false };
  if (format === "compact") return { fmt: compact, axis: compact, sum: true };
  return { fmt: num, axis: compact, sum: true };
};

/**
 * @param spec one chartSpec per panel (an array draws stacked panels)
 * @param opts {chartId, hidden: Set, title, cols, panelLabels[], override{label: {n, marker, dash, cls}}}
 *   cols draws columns instead of lines; override restyles a series by label
 *   (for example the spoofed series in danger colour).
 *   neutral {accent} colours the series named accent (else the first named one) in the
 *   accent and every other named series in neutral steps; "Other" and override keep theirs.
 */
export function buildModel(spec, { chartId = "", hidden = new Set(), title = "", cols = false, panelLabels = [], override = {}, neutral = null } = {}) {
  const specs = Array.isArray(spec) ? spec : [spec];
  const labels = specs[0].labels.map((l) => String(l).slice(0, 10));
  const n = labels.length;
  const bucket = specs[0].bucket || "";
  const cmp = specs.some((s) => s.previous);
  const previousLabels = cmp ? alignLabels((specs.find((s) => s.previous).previous.labels || []).map((l) => String(l).slice(0, 10)), n) : null;

  const panels = specs.map((s, pi) => {
    const f = formatters(s.format);
    let named = 0;
    const names = s.datasets.filter((d) => d.label !== OTHER).map((d) => d.label);
    const accent = neutral && names.includes(neutral.accent) ? neutral.accent : names[0];
    let step = 0;
    const series = s.datasets.map((d) => {
      const isOther = d.label === OTHER;
      let base = seriesFor(isOther ? 0 : named, isOther);
      if (neutral && !isOther) base = { ...base, n: d.label === accent ? 1 : NEUTRAL_STEPS[step++ % NEUTRAL_STEPS.length] };
      if (!isOther) named++;
      const style = { ...base, ...(override[d.label] || {}) };
      const prev = s.previous && s.previous.datasets.find((p) => p.label === d.label);
      return {
        key: d.label,
        name: d.label,
        values: alignPrevious(d.data, n),
        previous: s.previous ? alignPrevious(prev && prev.data, n) : null,
        isOther,
        style,
        cls: style.cls || vizClass(style),
        seriesIdx: style.n,
        visible: !hidden.has(hiddenKey(chartId, d.label)),
      };
    });
    const vs = series.filter((x) => x.visible);
    const totals = Array.from({ length: n }, (_, i) => vs.reduce((a, x) => a + x.values[i], 0));
    const previousTotal = cmp && s.previous ? Array.from({ length: n }, (_, i) => vs.reduce((a, x) => a + x.previous[i], 0)) : null;
    const drawn = (s.stacked ? totals : vs.flatMap((x) => x.values)).concat(previousTotal && !cols ? (s.stacked ? previousTotal : vs.flatMap((x) => x.previous)) : [0]);
    return {
      label: panelLabels[pi] || "",
      stacked: !!s.stacked,
      cols,
      series,
      active: vs.length > 0,
      totals,
      previousTotal,
      max: Math.max(0, ...drawn),
      int: (s.format || "number") !== "percent",
      ...f,
    };
  });

  return { chartId, title, labels, bucket, weekly: bucket === "week", previousLabels, compare: cmp, multi: panels.length > 1, panels };
}

/**
 * Toggle a series. The last visible series cannot be hidden.
 * `allKeys` are the keys of every series of the chart. Returns {ok, hidden}
 * with a NEW Set.
 */
export function toggleHidden(hidden, chartId, key, allKeys) {
  const next = new Set(hidden);
  const k = hiddenKey(chartId, key);
  if (next.has(k)) {
    next.delete(k);
    return { ok: true, hidden: next };
  }
  const visible = allKeys.filter((x) => !next.has(hiddenKey(chartId, x)));
  if (visible.length <= 1) return { ok: false, hidden: next };
  next.add(k);
  return { ok: true, hidden: next };
}

const slotLabel = (model, i) => (model.weekly ? weekLabel(model.labels[i]) : longDate(model.labels[i]));
const prevLabel = (model, i) => {
  const l = model.previousLabels && model.previousLabels[i];
  if (!l) return "";
  return model.weekly ? "week of " + dayLabel(l) : dayLabel(l);
};

/** "Week of 20 Oct" or "23 Oct 2026". */
export const slotName = slotLabel;
/** x-axis label for a slot: "23 Oct". */
export const axisLabel = (model, i) => dayLabel(model.labels[i]);

const hasTotal = (model) => model.panels.some((p) => p.stacked && p.series.length > 1);

/** Column headings of the data table. */
export function tableHead(model) {
  const all = model.panels.flatMap((p) => p.series.map((x) => x.name));
  const prev = model.compare ? model.panels.map((p) => (model.multi ? "Previous: " + p.label : "Previous period")) : [];
  return ["Date", ...all, ...(hasTotal(model) ? ["Total"] : []), ...prev];
}

/**
 * Rows of the data table (every series, hidden or not):
 * [{head, cells, total?, prev?}] with values formatted.
 */
export function tableRows(model, { weekly = model.weekly } = {}) {
  const totalCol = hasTotal(model);
  return model.labels.map((l, i) => {
    const row = {
      head: weekly ? weekLabel(l) : longDate(l),
      cells: model.panels.flatMap((p) => p.series.map((x) => p.fmt(x.values[i]))),
    };
    if (totalCol) row.total = model.panels[0].fmt(model.panels[0].series.reduce((a, x) => a + x.values[i], 0));
    if (model.compare) row.prev = model.panels.map((p) => p.fmt(p.series.reduce((a, x) => a + (x.previous ? x.previous[i] : 0), 0)));
    return row;
  });
}

const peak = (values) => values.indexOf(Math.max(...values));

/** aria-label of the chart: range, per series total and peak, and how to use the keys. */
export function summary(model) {
  const n = model.labels.length;
  const when = (i) => (model.weekly ? "the week of " : "") + longDate(model.labels[i]);
  const parts = model.panels
    .filter((p) => p.active)
    .flatMap((p) =>
      p.series
        .filter((x) => x.visible)
        .map((x) => {
          const pk = peak(x.values);
          const total = p.sum ? `${x.name}: ${p.fmt(x.values.reduce((a, b) => a + b, 0))} in total, peak` : `${x.name}: peak`;
          return `${total} ${p.fmt(x.values[pk])} on ${when(pk)}`;
        }),
    )
    .join(". ");
  const unit = model.weekly ? "week" : "day";
  return `${model.title}, ${dayLabel(model.labels[0])} to ${dayLabel(model.labels[n - 1])}. ${parts}. Use the left and right arrow keys to read each ${unit}.`;
}

/**
 * Keyboard: the next index for a key, clamped to 0..n-1. `cur` null means
 * nothing is shown yet (the last point is the start). Escape gives null (hide
 * the tooltip); keys the chart does not use give undefined.
 */
export function stepIndex(cur, key, n) {
  if (key === "Escape") return null;
  let i = cur === null || cur === undefined ? n - 1 : cur;
  if (key === "ArrowLeft") i--;
  else if (key === "ArrowRight") i++;
  else if (key === "Home") i = 0;
  else if (key === "End") i = n - 1;
  else return undefined;
  return Math.max(0, Math.min(n - 1, i));
}

/**
 * Tooltip content for slot i: {head, rows}. Rows are
 * {kind: "heading"|"series"|"total"|"previous", name, value?, seriesIdx?, cls?}
 * for the visible series only, a Total row for stacked panels with several
 * series, and a "Previous, <date>" row per panel with compare on (not for columns).
 */
export function tooltipRows(model, i) {
  const rows = [];
  for (const p of model.panels) {
    if (!p.active) continue;
    if (model.multi) rows.push({ kind: "heading", name: p.label });
    const vs = p.series.filter((x) => x.visible);
    for (const x of vs) rows.push({ kind: "series", name: x.name, value: p.fmt(x.values[i]), seriesIdx: x.seriesIdx, cls: x.cls });
    if (p.stacked && vs.length > 1) rows.push({ kind: "total", name: "Total", value: p.fmt(p.totals[i]) });
    if (p.previousTotal && !p.cols) {
      const d = prevLabel(model, i);
      rows.push({ kind: "previous", name: d ? "Previous, " + d : "Previous", value: p.fmt(p.previousTotal[i]) });
    }
  }
  return { head: slotLabel(model, i), rows };
}

/** The sentence the live region reads for slot i. */
export function announce(model, i) {
  const { head, rows } = tooltipRows(model, i);
  const parts = rows.map((r) => (r.kind === "heading" ? r.name : r.kind === "previous" ? "Previous " + r.value : r.name + " " + r.value));
  return [head, ...parts].join(". ") + ".";
}
