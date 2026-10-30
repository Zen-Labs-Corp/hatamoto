// The AI analytics charts (js/aia/*) at runtime, on every section of a seeded
// instance (QA_SEED_ANALYTICS=1): each figure ends with an SVG, the legend
// toggles aria-pressed and never lets the last visible series go, ArrowRight
// moves the live region, a failing data request shows an alert with a working
// Retry, reduced motion leaves no running animation, and the page logs no CSP
// violation. Needs analytics rows so the charts have data.
// Run: QA_SEED_ANALYTICS=1 scripts/admin-qa/run-local.sh aia-states.mjs
import { launch, newSession, base } from "./lib.mjs";

const b = await launch();
let bad = 0;
const ok = (c, m) => { console.log(`${c ? "ok  " : "FAIL"} ${m}`); if (!c) bad++; };
const sections = ["", "/crawlers", "/content", "/referrals", "/health", "/agents/gptbot"];
const settled = (p) => p.waitForFunction(() => !document.querySelector("[data-hm-aia-chart][aria-busy=true]"), null, { timeout: 15000 });

const p = await newSession(b, { theme: "light", width: 1440 });
const violations = [];
p.on("console", (m) => { if (/content security policy|refused to/i.test(m.text())) violations.push(m.text()); });

// Every figure ends in an SVG (or the empty/error state the templates carry).
for (const s of sections) {
  await p.goto(`${base}/admin/analytics${s}`);
  await settled(p);
  const charts = await p.evaluate(() => [...document.querySelectorAll('[data-hm-aia-chart="time"]')].map((f) => ({
    id: f.id, svg: !!f.querySelector("[data-aia-plot] svg"), state: f.querySelector("[data-aia-state]")?.dataset.aiaState || "",
  })));
  ok(charts.length === 0 || charts.every((c) => c.svg || c.state), `/analytics${s || ""}: ${charts.length} time chart(s), each an SVG or an empty/error state`);
}

// The Crawlers hero must draw a chart for every date preset, with and without
// the comparison: an empty or error state there is a failure, not a pass.
const day = (d) => d.toISOString().slice(0, 10);
const utc = (y, m, d) => new Date(Date.UTC(y, m, d));
const now = new Date();
const t = utc(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate());
const presets = {
  today: [t, t],
  "7d": [utc(t.getUTCFullYear(), t.getUTCMonth(), t.getUTCDate() - 6), t],
  "30d": [utc(t.getUTCFullYear(), t.getUTCMonth(), t.getUTCDate() - 29), t],
  "3m": [utc(t.getUTCFullYear(), t.getUTCMonth() - 3, t.getUTCDate()), t],
  "6m": [utc(t.getUTCFullYear(), t.getUTCMonth() - 6, t.getUTCDate()), t],
  mtd: [utc(t.getUTCFullYear(), t.getUTCMonth(), 1), t],
  ytd: [utc(t.getUTCFullYear(), 0, 1), t],
};
for (const [name, [from, to]] of Object.entries(presets)) {
  for (const compare of [false, true]) {
    await p.goto(`${base}/admin/analytics/crawlers?from=${day(from)}&to=${day(to)}${compare ? "&compare=1" : ""}`);
    await settled(p);
    const hero = await p.evaluate(() => {
      const f = document.getElementById("w-cr-hero");
      return { found: !!f, svg: !!f?.querySelector("[data-aia-plot] svg"), state: f?.querySelector("[data-aia-state]")?.dataset.aiaState || "" };
    });
    ok(hero.found && hero.svg && hero.state === "", `crawlers hero, ${name}${compare ? " +compare" : ""}: SVG drawn, state "${hero.state}"`);
  }
}

// Legend toggle on the overview hero.
await p.goto(`${base}/admin/analytics`);
await settled(p);
const fig = p.locator('[data-hm-aia-chart="time"]').first();
const lg = fig.locator("button.hm-aia-lg");
const n = await lg.count();
ok(n >= 2, `hero legend has ${n} series buttons`);
await lg.first().click();
ok((await lg.first().getAttribute("aria-pressed")) === "false", "clicking a legend button turns the series off (aria-pressed=false)");
for (let i = 1; i < n; i++) await lg.nth(i).click({ force: true }); // the last one is aria-disabled
const pressed = await lg.evaluateAll((bs) => bs.filter((x) => x.getAttribute("aria-pressed") === "true").length);
ok(pressed === 1, `the last visible series cannot be hidden (${pressed} still on)`);
await lg.first().click();
ok((await lg.first().getAttribute("aria-pressed")) === "true", "a hidden series comes back");

// Keyboard: ArrowRight/Left move the cursor and the live region says where.
const svg = fig.locator("[data-aia-plot] svg").first();
await svg.focus();
const say = () => fig.locator("[aria-live]").first().textContent();
await p.keyboard.press("Home");
const first = await say();
await p.keyboard.press("ArrowRight");
const second = await say();
ok(first !== "" && second !== "" && first !== second, `ArrowRight changes the live region ("${first}" -> "${second}")`);

// A failing data request: one alert with Retry, and Retry recovers.
let fail = true;
await p.route("**/admin/analytics/data/**", (r) => (fail ? r.fulfill({ status: 500, contentType: "text/plain", body: "boom" }) : r.continue()));
await p.goto(`${base}/admin/analytics`);
await settled(p);
ok(await p.locator('[data-aia-state="error"][role="alert"] [data-aia-retry]').first().isVisible(), "failed fetch: an alert with a Retry button");
fail = false;
await p.locator("[data-aia-retry]").first().focus();
await p.keyboard.press("Enter");
await settled(p);
ok(await p.evaluate(() => !!document.activeElement && document.activeElement !== document.body), "after Retry focus is not on <body>");
ok(await p.locator('[data-hm-aia-chart="time"] [data-aia-plot] svg').count() > 0, "Retry draws the chart");
await p.unroute("**/admin/analytics/data/**");

// Reduced motion: nothing is animating once the charts have drawn.
await p.emulateMedia({ reducedMotion: "reduce" });
await p.goto(`${base}/admin/analytics`);
await settled(p);
const running = await p.evaluate(() => document.getAnimations().filter((a) => a.playState === "running" && a.effect?.getTiming().iterations === Infinity).length);
ok(running === 0, `reduced motion: ${running} infinite animation(s) running`);

ok(violations.length === 0, `no CSP violation in the console (${violations.join("; ")})`);
await b.close();
process.exit(bad ? 1 : 0);
