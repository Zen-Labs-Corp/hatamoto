// Performance bars on the primary screens, Chrome, no throttling, prerender off,
// median of RUNS loads: CLS < 0.05, INP < 200 ms after scripted interactions,
// navigation (click to DOMContentLoaded, and responseEnd) < 300 ms.
// node scripts/admin-qa/perf.mjs [screen ...]   (RUNS=5 default)
// Exit 1 when a bar is missed. JSON in results/perf.json.
import { launch, newSession, visit, writeResult, median, base, role } from "./lib.mjs";
import { inventory, primaryScreens } from "./urls.mjs";

const RUNS = +(process.env.RUNS || 5);
const bars = { cls: 0.05, inp: 200, nav: 300 };
const only = process.argv.slice(2);
const screens = only.length ? inventory({ only }) : primaryScreens();
const b = await launch("chromium", ["--disable-features=Prerender2"]);

const observers = () => {
  window.__qa = { cls: 0, inp: 0 };
  new PerformanceObserver((l) => { for (const e of l.getEntries()) if (!e.hadRecentInput) window.__qa.cls += e.value; }).observe({ type: "layout-shift", buffered: true });
  new PerformanceObserver((l) => { for (const e of l.getEntries()) if (e.interactionId) window.__qa.inp = Math.max(window.__qa.inp, e.duration); }).observe({ type: "event", durationThreshold: 16, buffered: true });
};
const frames = (p) => p.evaluate(() => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(() => setTimeout(r, 50)))));

// scripted interactions: each runs only when its control exists
async function interact(p) {
  const done = [];
  const step = async (name, fn) => { try { if (await fn()) { done.push(name); await frames(p); } } catch { /* control absent or page moved on */ } };
  await step("row menu open", async () => { const t = p.locator('[popovertarget^="menu-"]').first(); if (!(await t.count())) return false; await t.click(); return true; });
  await step("row menu close", async () => { await p.keyboard.press("Escape"); return done.includes("row menu open"); });
  await step("filters open", async () => { const t = p.locator('[popovertarget*="filter" i]').first(); if (!(await t.count())) return false; await t.click(); return true; });
  await step("filters close", async () => { await p.keyboard.press("Escape"); return done.includes("filters open"); });
  await step("search type", async () => { const t = p.locator('main input[type="search"], main input[name="q"]').first(); if (!(await t.count())) return false; await t.click(); await t.pressSequentially("qa", { delay: 40 }); await p.waitForTimeout(500); return true; });
  await step("tab switch", async () => { const t = p.locator('[role="tab"]').nth(1); if (!(await t.count())) return false; await t.click(); return true; });
  await step("theme button hover", async () => { const t = p.locator("[data-hm-theme-toggle]").first(); if (!(await t.count())) return false; await t.hover(); return true; });
  return done;
}

const results = [];
let failed = 0;
for (const s of screens) {
  const row = { screen: s.name, cls: [], inp: [], responseEnd: [], clickDcl: [], interactions: [] };
  const p = await newSession(b, { theme: "light", width: 1440 });
  await p.context().addInitScript(observers);
  const probe = await visit(p, s);
  if (probe.skipped || !probe.ok) { console.log(`${s.name.padEnd(24)} ${probe.skipped ? "skipped (no data)" : "FAIL status " + probe.status}`); if (!probe.skipped) failed++; await p.context().close(); continue; }
  for (let i = 0; i < RUNS; i++) {
    await visit(p, s);
    await p.waitForTimeout(1200);
    row.interactions = await interact(p);
    const m = await p.evaluate(() => { const n = performance.getEntriesByType("navigation")[0]; return { ...window.__qa, responseEnd: n.responseEnd, dcl: n.domContentLoadedEventEnd }; });
    row.cls.push(m.cls); row.inp.push(m.inp); row.responseEnd.push(m.responseEnd);
    // click to DOMContentLoaded: from the entities screen through the sidebar link
    const startPath = probe.url.split("?")[0] === "/admin/entities" ? "/admin/profile" : "/admin/entities";
    await p.goto(base + startPath);
    const link = p.locator(`.hm-sidebar a[href="${probe.url.split("?")[0]}"]`).first();
    if (await link.count()) {
      await p.evaluate(() => { window.__marker = 1; });
      const t0 = await p.evaluate(() => performance.timeOrigin + performance.now());
      await Promise.all([p.waitForLoadState("domcontentloaded"), link.click()]);
      await p.waitForLoadState("load");
      // a boosted swap keeps the window (marker survives): that is not a document navigation, so no sample
      const r = await p.evaluate(() => ({ kept: window.__marker === 1, dcl: performance.timeOrigin + performance.getEntriesByType("navigation")[0].domContentLoadedEventEnd }));
      if (!r.kept) row.clickDcl.push(r.dcl - t0);
    }
  }
  const sum = { screen: s.name, cls: median(row.cls), inp: median(row.inp), responseEnd: median(row.responseEnd), clickDcl: median(row.clickDcl), interactions: row.interactions, runs: RUNS };
  sum.pass = sum.cls < bars.cls && sum.inp < bars.inp && (sum.clickDcl ?? sum.responseEnd) < bars.nav && sum.responseEnd < bars.nav;
  if (!sum.pass) failed++;
  results.push({ ...sum, raw: row });
  const f = (n, d = 0) => (n == null ? "n/a" : n.toFixed(d));
  console.log(`${s.name.padEnd(24)} CLS ${f(sum.cls, 4)}  INP ${f(sum.inp)} ms  responseEnd ${f(sum.responseEnd)} ms  click->DCL ${f(sum.clickDcl)} ms  ${sum.pass ? "ok" : "FAIL"}  [${row.interactions.join(", ") || "no interactions"}]`);
  await p.context().close();
}
writeResult("perf.json", { role, base, bars, results });
console.log(`\nmedian of ${RUNS}; bars CLS<${bars.cls} INP<${bars.inp} nav<${bars.nav} ms; ${failed} failing`);
await b.close();
process.exit(failed ? 1 : 0);
