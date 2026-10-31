// Analytics > Crawlers and Google on a seeded instance (QA_SEED_ANALYTICS=1). Crawlers: the verification banner
// (the default range is healthy (green), a range around the seeded bad day behind a CDN is red and names
// the header and the fix), the Search engines group with its summary row and the AI only / AI + search hero
// toggle. Google: KPIs, the page table and its filters, the connect banner. Screenshots go to SHOTS (default shots-out/).
// Run: QA_SEED_ANALYTICS=1 scripts/admin-qa/run-local.sh crawlers-banner.mjs
import path from "node:path";
import fs from "node:fs";
import { launch, newSession, base, here } from "./lib.mjs";

const b = await launch();
let bad = 0;
const ok = (c, m) => { console.log(`${c ? "ok  " : "FAIL"} ${m}`); if (!c) bad++; };
const out = process.env.SHOTS || path.join(here, "shots-out");
fs.mkdirSync(out, { recursive: true });
const day = (offset) => new Date(Date.now() + offset * 864e5).toISOString().slice(0, 10);

const p = await newSession(b, { theme: "light", width: 1440 });
const banner = async (query) => {
  await p.goto(`${base}/admin/analytics/crawlers${query}`);
  await p.waitForFunction(() => !document.querySelector("[data-hm-aia-chart][aria-busy=true]"), null, { timeout: 15000 });
  const el = p.locator("#w-cr-verify");
  return { el, text: (await el.count()) ? (await el.innerText()).replace(/\s+/g, " ") : "", kind: (await el.count()) ? await el.getAttribute("class") : "" };
};

const green = await banner("");
ok(green.kind.includes("hm-callout--success") && green.text.includes("Verification is healthy."), `default range: green banner (${green.text})`);
ok(/Client IPs are read from the connection, or X-Forwarded-For/.test(green.text), "green banner names the header in use");
await green.el.screenshot({ path: path.join(out, "crawlers-banner-green.png") });
await p.screenshot({ path: path.join(out, "crawlers-banner-green-page.png") });

const red = await banner(`?from=${day(-111)}&to=${day(-109)}`);
ok(red.kind.includes("hm-callout--danger") && red.text.includes("Verification looks misconfigured."), `bad-day range: red banner (${red.text})`);
ok(/\d+% of fetches from agents with published IP ranges failed\. [\d,]+ spoofed fetches from \d+ IPs \(see them\)\./.test(red.text), "red banner counts the spoofed fetches and IPs");
ok(red.text.includes("Client IPs are read from") && red.text.includes("HATAMOTO_TRUSTED_CDN=cloudflare"), "red banner names the header and the fix");
const href = await red.el.locator("a").getAttribute("href");
ok(/\/admin\/analytics\/health\?.*#w-h-ver$/.test(href || ""), `see-them link goes to Health keeping the range (${href})`);
await red.el.screenshot({ path: path.join(out, "crawlers-banner-red.png") });
await p.screenshot({ path: path.join(out, "crawlers-banner-red-page.png") });

// Search engines group, summary row and the hero toggle (default: AI + search, grey search series).
await p.goto(`${base}/admin/analytics/crawlers`);
await p.waitForFunction(() => !document.querySelector("[data-hm-aia-chart][aria-busy=true]"), null, { timeout: 15000 });
const group = p.locator("tbody", { has: p.locator("h4.hm-table__group-title", { hasText: "Search engines" }) });
const groupText = (await group.innerText()).replace(/\s+/g, " ");
ok(/Googlebot/.test(groupText) && /Bingbot/.test(groupText) && /Applebot/.test(groupText), `search group lists the engines that fetched (${groupText.slice(0, 160)})`);
ok(/Google AI Overviews and Gemini also use what Googlebot fetches/.test(groupText), "search group says what else Googlebot feeds");
ok((await group.locator('input[name="purpose.search"]').count()) === 2, "search group carries its own Allow / Block inside the robots form");
const summary = (await p.locator("section.hm-aia-other", { hasText: "Social, SEO tools, archives" }).innerText()).replace(/\s+/g, " ");
ok(/facebookexternalhit [\d,]+/i.test(summary) && /ahrefsbot [\d,]+/i.test(summary), `summary row counts social and SEO agents (${summary})`);
const fig = p.locator("#w-cr-hero-fig");
const toggle = (key) => p.locator(`[data-chart-toggle="${key}"]`);
ok((await fig.getAttribute("data-chart")) === "fetches_all" && (await toggle("fetches_all").getAttribute("aria-pressed")) === "true", "hero defaults to AI + search");
ok((await p.locator("#w-cr-hero-fig .hm-aia-legend").innerText()).includes("Search engines"), "AI + search draws the Search engines series");
await p.screenshot({ path: path.join(out, "crawlers-search-page.png") });
await toggle("fetches").click();
await p.waitForFunction(() => !document.querySelector("[data-hm-aia-chart][aria-busy=true]"), null, { timeout: 15000 });
ok((await fig.getAttribute("data-chart")) === "fetches" && (await toggle("fetches").getAttribute("aria-pressed")) === "true", "AI only flips the chart key and the pressed button");
ok(!(await p.locator("#w-cr-hero-fig .hm-aia-legend").innerText()).includes("Search engines"), "AI only drops the Search engines series");

// The Google tab: KPIs, crawling chart, the page table (stale, error, never crawled, spoofed-only left out), filters, the banner.
await p.goto(`${base}/admin/analytics/google`);
await p.waitForFunction(() => !document.querySelector("[data-hm-aia-chart][aria-busy=true]"), null, { timeout: 15000 });
const g = (await p.locator("main .hm-stack").innerText()).replace(/\s+/g, " ");
ok(g.includes("Connect Search Console (plugin) to add impressions, clicks, position and index status."), "Google tab shows the connect banner without a provider");
ok(/Googlebot crawls [\d,]+/.test(g) && /Pages crawled \d+ of \d+ published/.test(g) && g.includes("Publish to first crawl"), `Google tab KPIs (${g.slice(0, 200)})`);
ok(!g.includes("Impressions") && !g.includes("Top query"), "no Search Console parts without a provider");
ok(/\b404\b/.test(g) && g.includes("stale"), "table shows an error path and a stale page");
ok(!g.includes("/qa-spoofed-only"), "a path only a forged Googlebot fetched is left out");
await p.screenshot({ path: path.join(out, "google-page.png") });
const filtered = async (value) => {
  await Promise.all([
    p.waitForURL(new RegExp(`filter=${value}`)),
    p.locator(`input[name="filter"][value="${value}"]`).click({ force: true }), // submits the form on change
  ]);
  await p.waitForLoadState("networkidle"); // the page script and its chart data are in: the next change submits too
  return (await p.locator("#w-g-pages").innerText()).replace(/\s+/g, " ");
};
ok(/\b404\b/.test(await filtered("errors")), "Errors filter keeps the 404 path");
ok((await filtered("stale")).includes("stale"), "Stale filter keeps the edited page");
const never = await filtered("not-crawled");
ok(!never.includes("stale") && !/\b404\b/.test(never), "Not crawled filter drops crawled and error rows");

await b.close();
process.exit(bad ? 1 : 0);
