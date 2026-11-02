// Settings > General: with the Identity card top at the viewport top (1440x900) its Save button
// is on screen (the card footer is sticky), and the "On this page" strip marks exactly one tab
// current (Identity first, then the one clicked).
// node scripts/admin-qa/general-sticky.mjs     Exit 1 on a failed check.
// QA_SHOT=<file.png> also saves a screenshot of that scrolled view.
import { launch, newSession, writeResult, base, admin, role } from "./lib.mjs";

const b = await launch("chromium");
const p = await newSession(b, { theme: "light", width: 1440 });
await p.goto(`${base}${admin}/settings/general`, { waitUntil: "load" });
const results = [];
const check = (name, ok, detail = "") => {
  results.push({ name, ok, detail });
  console.log(`${ok ? "ok  " : "FAIL"} ${name}${detail ? ": " + detail : ""}`);
};
const current = () => p.$$eval('nav.hm-tabs [aria-current]', (els) => els.map((e) => e.textContent.trim()));

check("Identity is current on load", JSON.stringify(await current()) === '["Identity"]', JSON.stringify(await current()));

await p.evaluate(() => { document.documentElement.style.scrollBehavior = "auto"; window.scrollTo(0, document.querySelector("#identity-card").getBoundingClientRect().top + window.scrollY); });
await p.waitForTimeout(200);
const box = await p.locator("#identity-card").getByRole("button", { name: "Save identity" }).boundingBox();
check("Save identity is within the 900px viewport", !!box && box.y >= 0 && box.y + box.height <= 900, JSON.stringify(box));
if (process.env.QA_SHOT) await p.screenshot({ path: process.env.QA_SHOT });

await p.locator("nav.hm-tabs").getByRole("link", { name: "Homepage" }).click();
await p.waitForTimeout(300);
const after = await current();
check("clicking Homepage moves the current tab", JSON.stringify(after) === '["Homepage"]', JSON.stringify(after));

await b.close();
writeResult("general-sticky.json", { role, base, results });
process.exit(results.every((r) => r.ok) ? 0 : 1);
