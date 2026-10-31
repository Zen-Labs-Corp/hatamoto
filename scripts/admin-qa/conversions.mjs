// Analytics > Conversions on a seeded instance (QA_SEED_ANALYTICS=1): the five KPIs in one row, the
// leads-by-channel chart, the funnel, the give-vs-get table, Recent leads with the submitter email and the
// CSV link, the First touch | Last touch switch (a GET form with Apply) and no console errors.
// Screenshots go to SHOTS (default shots-out/).
// Run: QA_SEED_ANALYTICS=1 scripts/admin-qa/run-local.sh conversions.mjs
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

const res = await p.goto(`${base}/admin/analytics/conversions`);
ok(res && res.status() === 200, `conversions page loads (${res && res.status()})`);
await p.waitForFunction(() => !document.querySelector("[data-hm-aia-chart][aria-busy=true]"), null, { timeout: 15000 });

const kpis = p.locator(".hm-statgrid--strip > .hm-statcard");
const labels = (await kpis.locator(".hm-statcard__label").allInnerTexts()).map((s) => s.trim());
ok(labels.join("|") === "Leads|Conv. rate|Leads from AI assistants|Best channel|Top form", `five KPIs (${labels.join(", ")})`);
const tops = new Set(await kpis.evaluateAll((els) => els.map((e) => Math.round(e.getBoundingClientRect().top))));
ok(tops.size === 1, `the KPIs sit in one row at 1440px (${[...tops].join(",")})`);
const leads = (await kpis.first().locator(".hm-statcard__value").innerText()).replace(/[^\d]/g, "");
ok(Number(leads) > 0, `Leads is populated (${leads})`);

const card = (id) => p.locator(`#${id}`);
for (const id of ["w-c-leads", "w-c-funnel", "w-c-givegets", "w-c-recent"]) {
  ok((await card(id).count()) === 1, `card #${id} renders`);
}
ok((await card("w-c-funnel").locator("li").count()) === 5, "the funnel has five steps");
ok((await card("w-c-givegets").locator("tbody tr").count()) > 0, "give vs get has rows");
ok((await card("w-c-recent").locator("tbody tr").count()) > 0, "Recent leads has rows");
ok((await card("w-c-recent").innerText()).includes("@example.com"), "Recent leads shows the submitter email to an owner");
ok((await card("w-c-recent").locator("a", { hasText: "Export CSV" }).count()) === 1, "Export CSV is offered");

const csv = await p.evaluate(async () => {
  const r = await fetch(document.querySelector("#w-c-recent a[href*='export.csv']").href);
  return { status: r.status, text: await r.text() };
});
ok(csv.status === 200 && csv.text.includes("When (UTC),Form,Channel"), `the CSV downloads (${csv.status})`);

// First touch is the default; Last touch through the form (Apply) reloads with ?touch=last.
const radios = p.locator("fieldset.hm-segmented input[name=touch]");
ok((await radios.count()) === 2 && (await radios.first().isChecked()), "First touch is checked by default");
await p.locator("fieldset.hm-segmented label", { hasText: "Last touch" }).click();
await Promise.all([p.waitForURL(/touch=last/), p.getByRole("button", { name: "Apply" }).click()]);
ok((await p.locator("fieldset.hm-segmented input[value=last]").isChecked()), "Last touch stays chosen after Apply");
ok((await p.locator("#w-c-leads").innerText()).includes("Leads by last channel"), "the chart follows the last touch");

await p.screenshot({ path: path.join(out, "conversions.png"), fullPage: true });
const m = await newSession(b, { theme: "light", width: 390 });
await m.goto(`${base}/admin/analytics/conversions`);
const overflow = await m.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth);
ok(!overflow, "no horizontal page scroll at 390px");
await m.screenshot({ path: path.join(out, "conversions-mobile.png"), fullPage: true });

ok(errors.length === 0, `no console errors (${errors.join(" | ")})`);
await b.close();
process.exit(bad ? 1 : 0);
