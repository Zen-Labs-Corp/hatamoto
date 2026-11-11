// The Mail card on a plugin's Permissions tab: the owner saves a recipients-per-hour quota, a value
// outside 1..3600 is refused with a field error (HTTP 422) and nothing changes, and the page has no
// horizontal overflow at 1440 and 390, light and dark.
// Needs the frameplug fixture, which declares mail.send (run-local.sh loads it for this script).
// Run: SHOTS=<dir> bash scripts/admin-qa/run-local.sh mail-quota.mjs   (SHOTS: where screenshots go)   Exit 1 on any problem.
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

const url = `${base}/admin/plugins/frameplug/permissions`;
const b = await launch("chromium");
for (const theme of ["light", "dark"]) {
  for (const width of [1440, 390]) {
    const tag = `${theme}-${width}`;
    const p = await newSession(b, { theme, width });
    await p.goto(url, { waitUntil: "load" });
    check((await p.locator("#mail-card").count()) === 1, `${tag}: the Mail card is on the Permissions tab`);
    check((await p.inputValue("#mail-per-hour")) === "360", `${tag}: the default is 360`, await p.inputValue("#mail-per-hour"));
    check(/Sent this hour/i.test(await p.locator("#mail-card").innerText()), `${tag}: the card shows the sent count`);
    await shot(p, `mail-quota-${tag}.png`);

    // A value out of range is refused: 422, the field error, the typed value kept.
    await p.fill("#mail-per-hour", "3601");
    await p.evaluate(() => (document.querySelector("form[action$='/plugins/frameplug/mail']").noValidate = true)); // the server's check, not the browser's
    const [refused] = await Promise.all([p.waitForResponse((r) => r.request().method() === "POST" && r.url().endsWith("/plugins/frameplug/mail")), p.click("form[action$='/plugins/frameplug/mail'] button[type=submit]")]);
    check(refused.status() === 422, `${tag}: 3601 is refused with 422`, String(refused.status()));
    check(/1 to 3600/.test(await p.locator("body").innerText()), `${tag}: the field error names the range`);
    check((await p.inputValue("#mail-per-hour")) === "3601", `${tag}: the typed value is kept`);
    await shot(p, `mail-quota-${tag}-error.png`);

    // A valid value is saved and shown after the redirect.
    await p.fill("#mail-per-hour", "3600");
    await Promise.all([p.waitForURL((u) => u.pathname.endsWith("/plugins/frameplug/permissions")), p.click("form[action$='/plugins/frameplug/mail'] button[type=submit]")]);
    check(/Mail quota saved/.test(await p.locator("body").innerText()), `${tag}: the save shows a flash`);
    check((await p.inputValue("#mail-per-hour")) === "3600", `${tag}: the saved value is shown`, await p.inputValue("#mail-per-hour"));
    await shot(p, `mail-quota-${tag}-saved.png`);

    const over = await p.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth);
    check(!over, `${tag}: no horizontal overflow`);

    // Put the default back for the next pass.
    await p.fill("#mail-per-hour", "360");
    await Promise.all([p.waitForURL((u) => u.pathname.endsWith("/plugins/frameplug/permissions")), p.click("form[action$='/plugins/frameplug/mail'] button[type=submit]")]);
    await p.context().close();
  }
}
await b.close();
console.log(bad ? `\n${bad} problem(s)` : "\nok");
process.exit(bad ? 1 : 0);
