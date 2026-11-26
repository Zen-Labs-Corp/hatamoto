// The Overview's launch checklist: an owner sees #dash-launch above the KPI row with a progress bar, two
// groups and 1 to 10 rows whose links stay under the admin path; a row that needs attention carries the
// only primary buttons; the card is shot in light and dark ($SHOTS, default shots-out/launch-checklist);
// "Hide checklist" removes it for good; an editor never sees it. The hide step runs last and changes the
// QA site, so run this against the throwaway harness database only.
// scripts/admin-qa/run-local.sh launch-checklist.mjs
import path from "node:path";
import fs from "node:fs";
import { launch, newSession, base, admin, here, password } from "./lib.mjs";

const shots = process.env.SHOTS || path.join(here, "shots-out", "launch-checklist");
fs.mkdirSync(shots, { recursive: true });

const b = await launch();
let bad = 0;
const ok = (c, m) => {
  console.log(`${c ? "ok  " : "FAIL"} ${m}`);
  if (!c) bad++;
};
const errors = [];
const watch = (page) => {
  page.on("pageerror", (e) => errors.push(e.message));
  page.on("console", (m) => m.type() === "error" && errors.push(m.text()));
};
const overview = `${base}${admin}`;
const card = "#dash-launch";

const page = await newSession(b, { theme: "light", width: 1440 });
watch(page);
await page.goto(overview);
ok((await page.locator(card).count()) === 1, "the owner sees the launch checklist");
ok((await page.locator(`${card} [role=progressbar]`).count()) === 1, "it has a progress bar");
ok((await page.locator(`${card} h3.hm-dash__group`).count()) === 2, "it has two groups");
const rows = await page.locator(`${card} .hm-dash__launch`).count();
ok(rows >= 1 && rows <= 10, `it has ${rows} rows (1 to 10)`);
const hrefs = await page.locator(`${card} .hm-dash__launch a`).evaluateAll((as) => as.map((a) => a.getAttribute("href")));
ok(hrefs.length > 0 && hrefs.every((h) => h.startsWith(`${admin}/`)), "every row link stays under the admin path");
const above = await page.evaluate((sel) => {
  const c = document.querySelector(sel), k = document.querySelector(".hm-dash__kpis");
  return !k || !!(c.compareDocumentPosition(k) & Node.DOCUMENT_POSITION_FOLLOWING);
}, card);
ok(above, "the card sits above the KPI row");
const warn = page.locator(`${card} .hm-dash__launch:has(.hm-badge--warning)`);
const primaryRows = await page.locator(`${card} .hm-dash__launch:has(.hm-btn--primary)`).count();
ok((await warn.count()) === primaryRows, "primary buttons are on the rows that need attention, and only there");
const progress = (await page.locator(`${card} .hm-meter__value`).innerText()).trim();
ok(/^\d+ of \d+$/.test(progress), `the progress reads "${progress}"`);
await page.locator(card).screenshot({ path: path.join(shots, "overview-launch-checklist.png") });

const dark = await newSession(b, { theme: "dark", width: 1440 });
watch(dark);
await dark.goto(overview);
await dark.locator(card).screenshot({ path: path.join(shots, "overview-launch-checklist-dark.png") });

// An editor has no settings access: no card.
const editor = await b.newContext({ viewport: { width: 1440, height: 900 } }).then((c) => c.newPage());
watch(editor);
await editor.goto(`${base}${admin}/login`);
await editor.fill("#email", process.env.QA_EDITOR_EMAIL || "editor@qa.test");
await editor.fill("#password", password);
await Promise.all([editor.waitForURL((u) => !u.pathname.endsWith(`${admin}/login`)), editor.click(`form[action="${admin}/login"] button[type=submit]`)]);
await editor.goto(overview);
ok((await editor.locator(card).count()) === 0, "an editor does not see the launch checklist");

// Last: hide it.
await page.click(`${card} form[action$="/launch-checklist/hide"] button[type=submit]`);
await page.waitForLoadState("load");
await page.goto(overview);
ok((await page.locator(card).count()) === 0, "after Hide checklist the card is gone");

ok(errors.length === 0, `no console or page errors${errors.length ? ": " + errors.slice(0, 3).join(" | ") : ""}`);
await b.close();
console.log(bad ? `${bad} check(s) failed` : "all checks passed");
process.exit(bad ? 1 : 0);
