// The verification banner on Analytics > Crawlers on a seeded instance (QA_SEED_ANALYTICS=1): the
// default range is healthy (green), a range around the seeded bad day behind a CDN is red and names
// the header and the fix. Screenshots of both go to SHOTS (default shots-out/).
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
ok(/Client IPs are read from [A-Za-z-]+ behind/.test(green.text), "green banner names the header in use");
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

await b.close();
process.exit(bad ? 1 : 0);
