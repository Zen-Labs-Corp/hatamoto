// AI analytics chart data model. Node cannot resolve the @hm import map, so a
// resolver hook maps @hm/aia/* onto the source files.
// Run: node --test scripts/admin-qa/unit/aia-model.test.mjs
import { register } from "node:module";
import { test } from "node:test";
import assert from "node:assert/strict";

const root = new URL("../../../backend/web/admin/js/", import.meta.url).href;
register(
  "data:text/javascript," +
    encodeURIComponent(`export async function resolve(s, c, next) {
      if (s.startsWith("@hm/aia/")) return { url: ${JSON.stringify(root)} + s.slice(4) + ".js", shortCircuit: true };
      return next(s, c);
    }`),
);
const m = await import(new URL("aia/model.js", root));

const days = ["2026-10-19", "2026-10-20", "2026-10-21", "2026-10-22"];
const spec = (extra = {}) => ({
  kind: "line",
  stacked: true,
  labels: days,
  datasets: [
    { label: "Training", data: [10, 20, 30, 40] },
    { label: "Search index", data: [1, 2, 3, 4] },
    { label: "Other", data: [5, 5, 5, 5] },
  ],
  ...extra,
});
const prev = { labels: ["2026-10-12", "2026-10-13", "2026-10-14", "2026-10-15"], datasets: [{ label: "Training", data: [8, 8, 8, 8] }, { label: "Search index", data: [2, 2, 2, 2] }] };

test("imports in Node with no document or window", () => {
  assert.equal(typeof document, "undefined");
  assert.equal(typeof window, "undefined");
});

test("series get the prototype order and Other is slot 6", () => {
  const p = m.buildModel(spec()).panels[0];
  assert.deepEqual(p.series.map((s) => s.style.n), [1, 2, 6]);
  assert.deepEqual(p.series.map((s) => s.cls), ["hm-viz--series-1", "hm-viz--series-2", "hm-viz--series-6"]);
  assert.equal(p.series[2].isOther, true);
});

test("override restyles one series", () => {
  const model = m.buildModel(spec(), { override: { "Search index": { cls: "hm-viz--danger", marker: "triangle" } } });
  assert.equal(model.panels[0].series[1].cls, "hm-viz--danger");
  assert.equal(model.panels[0].series[1].style.marker, "triangle");
});

test("hide: cannot hide the last visible series, per chart id", () => {
  const keys = ["Training", "Search index", "Other"];
  let r = m.toggleHidden(new Set(), "c1", "Training", keys);
  assert.equal(r.ok, true);
  r = m.toggleHidden(r.hidden, "c1", "Search index", keys);
  assert.equal(r.ok, true);
  const last = m.toggleHidden(r.hidden, "c1", "Other", keys);
  assert.equal(last.ok, false);
  assert.ok(!last.hidden.has("c1|Other"));
  const unhide = m.toggleHidden(r.hidden, "c1", "Training", keys);
  assert.equal(unhide.ok, true);
  assert.ok(!unhide.hidden.has("c1|Training"));
  // another chart with the same keys is untouched
  assert.equal(m.toggleHidden(r.hidden, "c2", "Other", keys).ok, true);
  // input is not mutated
  assert.equal(r.hidden.has("c1|Training"), true);
});

test("stacked total sums the visible series only", () => {
  const hidden = new Set(["c|Search index"]);
  const p = m.buildModel(spec(), { chartId: "c", hidden }).panels[0];
  assert.deepEqual(p.totals, [15, 25, 35, 45]);
  assert.equal(p.max, 45);
  assert.equal(p.series[1].visible, false);
  const all = m.buildModel(spec()).panels[0];
  assert.deepEqual(all.totals, [16, 27, 38, 49]);
});

test("unstacked max is the highest single value", () => {
  const p = m.buildModel(spec({ stacked: false })).panels[0];
  assert.equal(p.max, 40);
});

test("alignPrevious truncates and zero pads by index", () => {
  assert.deepEqual(m.alignPrevious([1, 2, 3, 4, 5], 4), [1, 2, 3, 4]);
  assert.deepEqual(m.alignPrevious([1, 2, 3], 4), [1, 2, 3, 0]);
  assert.deepEqual(m.alignPrevious(null, 2), [0, 0]);
  const longer = m.buildModel(spec({ previous: { labels: [...prev.labels, "2026-10-16"], datasets: prev.datasets.map((d) => ({ ...d, data: [...d.data, 99] })) } }));
  assert.equal(longer.panels[0].series[0].previous.length, 4);
  const shorter = m.buildModel(spec({ previous: { labels: prev.labels.slice(0, 3), datasets: prev.datasets.map((d) => ({ ...d, data: d.data.slice(0, 3) })) } }));
  assert.deepEqual(shorter.panels[0].series[0].previous, [8, 8, 8, 0]);
  assert.equal(shorter.previousLabels[3], "");
});

test("previous total counts visible series and lifts the axis max", () => {
  const big = { ...prev, datasets: [{ label: "Training", data: [100, 100, 100, 100] }, { label: "Search index", data: [1, 1, 1, 1] }] };
  const p = m.buildModel(spec({ previous: big })).panels[0];
  assert.deepEqual(p.previousTotal, [101, 101, 101, 101]);
  assert.equal(p.max, 101);
  assert.equal(p.series[2].previous.every((v) => v === 0), true); // Other has no previous series
  assert.equal(m.buildModel(spec()).panels[0].previousTotal, null);
});

test("ratio: zero denominator is zero, spec carries percent and previous", () => {
  assert.deepEqual(m.ratioSeries([1, 2, 3], [10, 0, 12]), [10, 0, 25]);
  const r = m.ratioSpec(
    { labels: days, bucket: "week", datasets: [{ label: "a", data: [1, 2, 3, 4] }, { label: "b", data: [1, 0, 0, 0] }], previous: { labels: days, datasets: [{ label: "a", data: [1, 1, 1, 1] }] } },
    { labels: days, datasets: [{ label: "Pageviews", data: [20, 0, 10, 40] }], previous: { labels: days, datasets: [{ label: "Pageviews", data: [10, 10, 10, 0] }] } },
    "AI share",
  );
  assert.equal(r.format, "percent");
  assert.equal(r.bucket, "week");
  assert.deepEqual(r.datasets[0].data, [10, 0, 30, 10]);
  assert.deepEqual(r.previous.datasets[0].data, [10, 10, 10, 0]);
  const p = m.buildModel(r).panels[0];
  assert.equal(p.fmt(3.84), "3.8%");
  assert.equal(p.axis(12), "12%");
  assert.equal(p.sum, false);
});

test("table rows: weekly heads, total, previous columns", () => {
  const wk = m.buildModel(spec({ bucket: "week", previous: prev }), { title: "Fetches" });
  assert.deepEqual(m.tableHead(wk), ["Date", "Training", "Search index", "Other", "Total", "Previous period"]);
  const rows = m.tableRows(wk);
  assert.equal(rows[0].head, "Week of 19 Oct");
  assert.deepEqual(rows[0].cells, ["10", "1", "5"]);
  assert.equal(rows[0].total, "16");
  assert.deepEqual(rows[0].prev, ["10"]);
  const day = m.tableRows(m.buildModel(spec()));
  assert.equal(day[0].head, "19 Oct 2026");
  assert.equal(day[0].prev, undefined);
  assert.equal(m.tableRows(m.buildModel(spec({ stacked: false })))[0].total, undefined);
  assert.equal(m.tableRows(m.buildModel(spec()), { weekly: true })[1].head, "Week of 19 Oct");
});

test("two panels: headings, per panel previous column", () => {
  const a = spec({ stacked: false, datasets: [{ label: "Verified", data: [1, 2, 3, 4] }] });
  const b = spec({ stacked: false, datasets: [{ label: "Spoofed", data: [0, 1, 0, 2] }] });
  const model = m.buildModel([a, b], { panelLabels: ["Verified", "Spoofed"], title: "Verification" });
  assert.equal(model.multi, true);
  assert.deepEqual(m.tableHead(model), ["Date", "Verified", "Spoofed"]);
  const t = m.tooltipRows(model, 1);
  assert.deepEqual(t.rows.map((r) => [r.kind, r.name, r.value]), [["heading", "Verified", undefined], ["series", "Verified", "2"], ["heading", "Spoofed", undefined], ["series", "Spoofed", "1"]]);
  assert.equal(m.announce(model, 1), "20 Oct 2026. Verified. Verified 2. Spoofed. Spoofed 1.");
});

test("summary names the range, totals, peaks and the arrow keys", () => {
  const s = m.summary(m.buildModel(spec(), { title: "Fetches by purpose" }));
  assert.match(s, /^Fetches by purpose, 19 Oct to 22 Oct\./);
  assert.match(s, /Training: 100 in total, peak 40 on 22 Oct 2026/);
  assert.match(s, /left and right arrow keys to read each day\.$/);
  const w = m.summary(m.buildModel(spec({ bucket: "week" }), { title: "t" }));
  assert.match(w, /on the week of 22 Oct 2026/);
  assert.match(w, /each week\.$/);
  const ratio = m.summary(m.buildModel(m.ratioSpec(spec(), spec(), "Share"), { title: "t" }));
  assert.match(ratio, /Share: peak /);
  assert.doesNotMatch(ratio, /in total/);
  const noHidden = m.summary(m.buildModel(spec(), { chartId: "c", title: "t", hidden: new Set(["c|Other"]) }));
  assert.doesNotMatch(noHidden, /Other:/);
});

test("stepIndex: arrows, Home, End clamp; Escape hides; others are ignored", () => {
  assert.equal(m.stepIndex(null, "ArrowLeft", 10), 8); // starts at the last point
  assert.equal(m.stepIndex(null, "ArrowRight", 10), 9);
  assert.equal(m.stepIndex(0, "ArrowLeft", 10), 0);
  assert.equal(m.stepIndex(9, "ArrowRight", 10), 9);
  assert.equal(m.stepIndex(4, "ArrowRight", 10), 5);
  assert.equal(m.stepIndex(4, "Home", 10), 0);
  assert.equal(m.stepIndex(4, "End", 10), 9);
  assert.equal(m.stepIndex(4, "Escape", 10), null);
  assert.equal(m.stepIndex(4, "Tab", 10), undefined);
});

test("tooltip: Total for stacked, Previous row only with compare", () => {
  const plain = m.tooltipRows(m.buildModel(spec()), 1);
  assert.equal(plain.head, "20 Oct 2026");
  assert.deepEqual(plain.rows.map((r) => r.kind), ["series", "series", "series", "total"]);
  assert.deepEqual(plain.rows.at(-1), { kind: "total", name: "Total", value: "27" });
  assert.equal(plain.rows.some((r) => r.kind === "previous"), false);
  const cmp = m.tooltipRows(m.buildModel(spec({ previous: prev })), 1);
  assert.deepEqual(cmp.rows.at(-1), { kind: "previous", name: "Previous, 13 Oct", value: "10" });
  const wk = m.tooltipRows(m.buildModel(spec({ bucket: "week", previous: prev })), 0);
  assert.equal(wk.head, "Week of 19 Oct");
  assert.equal(wk.rows.at(-1).name, "Previous, week of 12 Oct");
  const one = m.tooltipRows(m.buildModel(spec({ datasets: [{ label: "Fetches", data: [1, 2, 3, 4] }] })), 0);
  assert.equal(one.rows.some((r) => r.kind === "total"), false);
  const cols = m.tooltipRows(m.buildModel(spec({ stacked: false, previous: prev }), { cols: true }), 1);
  assert.equal(cols.rows.some((r) => r.kind === "previous"), false);
});

test("hidden series drop out of the tooltip and the summary, not the table", () => {
  const model = m.buildModel(spec(), { chartId: "c", hidden: new Set(["c|Training"]) });
  assert.deepEqual(m.tooltipRows(model, 0).rows.map((r) => r.name), ["Search index", "Other", "Total"]);
  assert.deepEqual(m.tableRows(model)[0].cells, ["10", "1", "5"]);
});

test("sumSpec: datasets add up into one unstacked series, previous included", () => {
  const s = m.sumSpec(
    { labels: days, stacked: true, bucket: "week", datasets: [{ label: "a", data: [1, 2, 3, 4] }, { label: "b", data: [1, 0, 0, 5] }], previous: { labels: days, datasets: [{ label: "a", data: [1, 1, 1, 1] }, { label: "b", data: [2, 0, 0, 0] }] } },
    "AI fetches",
  );
  assert.equal(s.stacked, false);
  assert.equal(s.bucket, "week");
  assert.deepEqual(s.datasets, [{ label: "AI fetches", data: [2, 2, 3, 9] }]);
  assert.deepEqual(s.previous.datasets, [{ label: "AI fetches", data: [3, 1, 1, 1] }]);
  assert.equal(m.sumSpec({ labels: days, datasets: [{ label: "a", data: [1, 1, 1, 1] }] }, "x").previous, undefined);
});

test("sparkValues sums the datasets or picks one; sparkSummary mirrors the server's wording", async () => {
  const { sparkValues, sparkSummary } = await import(new URL("aia/model.js", root));
  const spec = { labels: ["a", "b", "c"], datasets: [{ label: "X", data: [1, 2, 3] }, { label: "Y", data: [10, 0, 5] }] };
  assert.deepEqual(sparkValues(spec), [11, 2, 8]);
  assert.deepEqual(sparkValues(spec, "Y"), [10, 0, 5]);
  assert.equal(sparkSummary([1, 2, 3, 6], "days"), "Trend over 4 days: ending higher");
  assert.equal(sparkSummary([6, 3, 2, 1], "weeks"), "Trend over 4 weeks: ending lower");
  assert.equal(sparkSummary([5, 5, 5, 5]), "Trend over 4 days: steady");
  assert.equal(sparkSummary([0, 0, 0, 4]), "Trend over 4 days: ending higher");
  assert.equal(sparkSummary([0, 0, 0, 0]), "Trend over 4 days: steady");
});

test("neutral: the named accent gets series 1 and the others cycle the neutral steps", () => {
  const p = m.buildModel(spec(), { neutral: { accent: "Search index" } }).panels[0];
  assert.deepEqual(p.series.map((s) => s.seriesIdx), [2, 1, 6]);
  assert.equal(p.series[1].cls, "hm-viz--series-1");
});

test("neutral: an accent that is not in the data falls back to the first named series", () => {
  for (const neutral of [{ accent: "" }, { accent: "Missing" }]) {
    const p = m.buildModel(spec(), { neutral }).panels[0];
    assert.deepEqual(p.series.map((s) => s.seriesIdx), [1, 2, 6]);
  }
});

test("neutral: Other keeps chart-6 and is never the accent", () => {
  const p = m.buildModel(spec(), { neutral: { accent: "Other" } }).panels[0];
  assert.deepEqual(p.series.map((s) => s.seriesIdx), [1, 2, 6]);
  assert.equal(p.series[2].isOther, true);
  const only = m.buildModel(spec({ datasets: [{ label: "Other", data: [1, 2, 3, 4] }] }), { neutral: { accent: "Other" } }).panels[0];
  assert.deepEqual(only.series.map((s) => s.seriesIdx), [6]);
});

test("neutral: an explicit override wins over the accent and the neutral step", () => {
  const override = { "Search index": { n: 6 }, Training: { cls: "hm-viz--danger" } };
  const p = m.buildModel(spec(), { neutral: { accent: "Training" }, override }).panels[0];
  assert.equal(p.series[0].cls, "hm-viz--danger");
  assert.equal(p.series[1].seriesIdx, 6);
});

test("neutral: more named series than steps repeat the steps after the accent", () => {
  const datasets = ["A", "B", "C", "D", "E", "F"].map((label) => ({ label, data: [1, 1, 1, 1] }));
  const p = m.buildModel(spec({ datasets }), { neutral: { accent: "A" } }).panels[0];
  assert.deepEqual(p.series.map((s) => s.seriesIdx), [1, 2, 3, 4, 2, 3]);
});

test("without neutral the series keep the prototype order", () => {
  const p = m.buildModel(spec({ datasets: [{ label: "A", data: [1, 1, 1, 1] }, { label: "B", data: [1, 1, 1, 1] }] })).panels[0];
  assert.deepEqual(p.series.map((s) => s.seriesIdx), [1, 2]);
});
