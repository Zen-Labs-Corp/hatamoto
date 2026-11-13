// The Visitor entry panel on Settings > Locales and the Countries field of Edit locale: a second locale is
// added, the panel is filled in (switch, two entry paths through Add path, an old cookie name), saved and read
// back, a rejected path shows its error and saves nothing, Edit locale saves a Countries list that the table
// then shows, and the page has no horizontal overflow at 1440 and 390, light and dark. The locale is removed
// again at the end, and the panel is switched off.
// Run: SHOTS=<dir> bash scripts/admin-qa/run-local.sh locale-entry.mjs   (SHOTS: where screenshots go)   Exit 1 on any problem.
import fs from "node:fs";
import path from "node:path";
import { launch, newSession, base } from "./lib.mjs";

const SHOTS = process.env.SHOTS || "";
if (SHOTS) fs.mkdirSync(SHOTS, { recursive: true });
const shot = (page, name) => (SHOTS ? page.screenshot({ path: path.join(SHOTS, name), fullPage: true }) : null);

let bad = 0;
const check = (ok, msg, detail = "") => {
  console.log(`${ok ? "PASS" : "FAIL"}  ${msg}${!ok && detail ? "  " + detail : ""}`);
  if (!ok) bad++;
};

const locales = `${base}/admin/settings/locales`;
const save = (p) => Promise.all([p.waitForLoadState("load"), p.click("#visitor-entry-form .hm-card__foot button[type=submit], #visitor-entry-form button.hm-btn--primary")]);
const b = await launch("chromium");
const p = await newSession(b, { theme: "light", width: 1440 });

// A second locale makes the panel appear.
await p.goto(locales);
check((await p.locator("#visitor-entry-form").count()) === 0, "single-locale: no Visitor entry panel");
const csrf = await p.locator("#add-locale-form input[name=_csrf]").inputValue();
const added = await p.evaluate(async ([t]) => (await fetch("/admin/settings/locales/add", { method: "POST", headers: { "Content-Type": "application/x-www-form-urlencoded" }, body: new URLSearchParams({ _csrf: t, tag: "fr", name: "French", public: "true" }) })).status, [csrf]);
check(added === 200, `locale fr added (${added})`);

await p.goto(locales);
check((await p.locator("#visitor-entry-form").count()) === 1, "two locales: the Visitor entry panel is shown");
check((await p.getByText("Keep visitors in their locale").count()) === 0, "the panel has no Keep visitors in their locale row");

// Fill it in: the switch, one path, Add path (no save, the typed path stays), a second path, the old cookie name.
await p.locator("#entry-enabled").check({ force: true });
await p.fill("#entry-path-0", "/contact-form");
await Promise.all([p.waitForLoadState("load"), p.click("button[name=add_entry_path]")]);
check((await p.inputValue("#entry-path-0")) === "/contact-form" && (await p.locator("input[name=entry_path]").count()) === 3, "Add path keeps the typed path and adds a row");
await p.fill("#entry-path-1", "/pricing");
await p.fill("#entry-cookie", "old_loc");
await save(p);
check(/Visitor entry saved/.test(await p.locator("body").innerText()), "the save shows a flash");
await p.goto(locales);
check((await p.locator("#entry-enabled").isChecked()) && (await p.inputValue("#entry-path-0")) === "/contact-form" && (await p.inputValue("#entry-path-1")) === "/pricing" && (await p.inputValue("#entry-cookie")) === "old_loc", "the saved panel reloads with its values");

// Remove drops a row without saving; a path that cannot be an entry path is refused and nothing is saved.
await Promise.all([p.waitForLoadState("load"), p.click("button[name=remove_entry_path][value='1']")]);
check((await p.locator("input[name=entry_path]").count()) === 2 && (await p.inputValue("#entry-path-0")) === "/contact-form", "Remove takes the row out");
await p.goto(locales);
check((await p.inputValue("#entry-path-1")) === "/pricing", "Remove did not save");
await p.fill("#entry-path-2", "/locale");
const [refused] = await Promise.all([p.waitForResponse((r) => r.request().method() === "POST" && r.url().endsWith("/settings/locales/entry")), save(p)]);
check(refused.status() === 422 && /switcher/.test(await p.locator("#hm-form-errors").innerText()), "/locale is refused with an error", String(refused.status()));
await shot(p, "locale-entry-error.png");
await p.goto(locales);
check((await p.locator("input[name=entry_path]").count()) === 3, "the refused path was not saved");

// Edit locale: Countries.
await p.goto(`${locales}/edit/fr`);
await p.fill("#locale-countries", "fr");
await Promise.all([p.waitForLoadState("load"), p.click("form[action$='/update/fr'] button[type=submit]")]);
await p.goto(`${locales}/edit/fr`);
check((await p.inputValue("#locale-countries")) === "FR", "Countries saves upper-case and reloads");
await shot(p, "locale-entry-edit-light-1440.png");
await p.goto(locales);
check(/\bFR\b/.test(await p.locator("#locales").innerText()), "the table shows the country");

for (const theme of ["light", "dark"]) {
  for (const width of [1440, 390]) {
    const tag = `${theme}-${width}`;
    const s = await newSession(b, { theme, width });
    await s.goto(locales);
    check((await s.locator("#visitor-entry-form").count()) === 1, `${tag}: the panel is shown`);
    check(!(await s.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth)), `${tag}: no horizontal overflow`);
    await shot(s, `locale-entry-${tag}.png`);
    await s.context().close();
  }
}

// Put the site back: panel off and no paths, then remove fr.
await p.goto(locales);
await p.locator("#entry-enabled").uncheck({ force: true });
for (let i = 2; i >= 0; i--) await p.fill(`#entry-path-${i}`, "");
await save(p);
await p.goto(`${locales}/remove/fr`);
const rm = p.locator("form[action$='/remove/fr'] button[type=submit]").first();
if (await rm.count()) await Promise.all([p.waitForLoadState("load"), rm.click()]);
await b.close();
console.log(bad ? `\n${bad} problem(s)` : "\nok");
process.exit(bad ? 1 : 0);
