// Analytics > Traffic on a seeded instance (QA_SEED_ANALYTICS=1): the five KPIs in one row, the Channels
// table (AI assistants badged new, one decimal shares, the attribution toggle), Sources, Campaigns,
// Landing pages, Devices, the Countries empty state without a GeoIP file, Engagement and Measurement,
// and no console errors. Screenshots go to SHOTS (default shots-out/).
// Run: QA_SEED_ANALYTICS=1 scripts/admin-qa/run-local.sh traffic.mjs
import path from "node:path";
import fs from "node:fs";
import { launch, newSession, base, here } from "./lib.mjs";

const b = await launch();
let bad = 0;
const ok = (c, m) => { console.log(`${c ? "ok  " : "FAIL"} ${m}`); if (!c) bad++; };
const out = process.env.SHOTS || path.join(here, "shots-out");
fs.mkdirSync(out, { recursive: true });

const p = await newSession(b, { theme: "light", width: 1440 });
const errors = [];
p.on("console", (m) => { if (m.type() === "error") errors.push(m.text()); });
p.on("pageerror", (e) => errors.push(String(e)));

const res = await p.goto(`${base}/admin/analytics/traffic`);
ok(res && res.status() === 200, `traffic page loads (${res && res.status()})`);
await p.waitForFunction(() => !document.querySelector("[data-hm-aia-chart][aria-busy=true]"), null, { timeout: 15000 });

const kpis = p.locator(".hm-statgrid--strip > .hm-statcard");
const labels = (await kpis.locator(".hm-statcard__label").allInnerTexts()).map((s) => s.trim());
ok(labels.join("|") === "Visits|Pageviews|Leads|Conv. rate|Top channel", `five KPIs (${labels.join(", ")})`);
const tops = new Set(await kpis.evaluateAll((els) => els.map((e) => Math.round(e.getBoundingClientRect().top))));
ok(tops.size === 1, `the KPIs sit in one row at 1440px (${[...tops].join(",")})`);
const visits = (await kpis.first().locator(".hm-statcard__value").innerText()).replace(/[^\d]/g, "");
ok(Number(visits) > 0, `Visits is populated (${visits})`);

const card = (id) => p.locator(`#${id}`);
for (const id of ["w-t-channels", "w-t-sources", "w-t-campaigns", "w-t-landing", "w-t-devices", "w-t-countries", "w-t-engagement", "w-t-measurement"]) {
  ok((await card(id).count()) === 1, `card #${id} renders`);
}

const channels = card("w-t-channels");
const rows = (await channels.locator("tbody tr").allInnerTexts()).map((s) => s.replace(/\s+/g, " ").trim());
ok(rows.length === 7, `Channels lists the seven channels (${rows.length})`);
ok(rows.some((r) => /^AI assistants\s+new\b/.test(r)), "AI assistants carries the new badge");
const shares = await channels.locator("tbody td[data-label=Share]").allInnerTexts();
ok(shares.length > 0 && shares.every((s) => /^\d+\.\d%$/.test(s.trim())), `shares have one decimal (${shares.map((s) => s.trim()).join(", ")})`);
const touch = channels.locator("input[name=touch]");
ok((await touch.count()) === 2 && (await touch.evaluateAll((els) => els.every((e) => !e.disabled))), "First touch and Last touch are enabled");
ok(await channels.locator("input[name=touch][value=first]").isChecked(), "First touch is the default");
ok(/^\d[\d,. ]*$/.test((await kpis.nth(2).locator(".hm-statcard__value").innerText()).trim()), "Leads KPI shows a number, not a placeholder");

ok((await card("w-t-sources").locator("tbody tr").count()) > 0, "Sources has rows");
ok((await card("w-t-campaigns").locator("tbody tr").count()) > 0, "Campaigns has rows");
ok((await card("w-t-landing").locator("li").count()) > 0, "Landing pages has rows");
ok((await card("w-t-devices").locator("li").count()) > 0, "Devices has rows");
ok((await card("w-t-countries").innerText()).includes("Set up country lookup"), "Countries shows the set-up empty state without a GeoIP file");
ok((await card("w-t-engagement").innerText()).includes("Bounced"), "Engagement lists Bounced");
ok((await card("w-t-measurement").innerText()).includes("Country lookup"), "Measurement shows the country lookup state");

await p.screenshot({ path: path.join(out, "traffic.png"), fullPage: true });
const m = await newSession(b, { theme: "light", width: 390 });
await m.goto(`${base}/admin/analytics/traffic`);
const overflow = await m.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth);
ok(!overflow, "no horizontal page scroll at 390px");
await m.screenshot({ path: path.join(out, "traffic-mobile.png"), fullPage: true });

ok(errors.length === 0, `no console errors (${errors.join(" | ")})`);
await b.close();
process.exit(bad ? 1 : 0);
