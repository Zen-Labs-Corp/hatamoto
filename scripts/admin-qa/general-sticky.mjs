// Settings > General: with the long Business details card top at the viewport top (1440x900) its footer is sticky at the
// viewport bottom and its Save button is on screen, and the "On this page" strip marks exactly one tab
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

// The long Business details card is taller than the viewport, so only a sticky footer keeps Save on screen.
await p.evaluate(() => { document.documentElement.style.scrollBehavior = "auto"; window.scrollTo(0, document.querySelector("#contact-card").getBoundingClientRect().top + window.scrollY); });
await p.waitForTimeout(200);
const m = await p.evaluate(() => {
  const card = document.querySelector("#contact-card");
  const foot = card.querySelector(".hm-card__foot");
  return { vh: innerHeight, cardH: card.offsetHeight, cardBottom: card.getBoundingClientRect().bottom, footBottom: foot.getBoundingClientRect().bottom, position: getComputedStyle(foot).position };
});
check("Business details card is taller than the viewport", m.cardH > m.vh, JSON.stringify(m));
check("card footer is position: sticky", m.position === "sticky", m.position);
check("footer sits at the viewport bottom while the card runs below it", Math.abs(m.footBottom - m.vh) <= 2 && m.cardBottom > m.vh, JSON.stringify(m));
const box = await p.locator("#contact-card .hm-card__foot").getByRole("button", { name: "Save business details" }).boundingBox();
check("the card Save button is within the 900px viewport", !!box && box.y >= 0 && box.y + box.height <= 900, JSON.stringify(box));
if (process.env.QA_SHOT) await p.screenshot({ path: process.env.QA_SHOT });

await p.locator("nav.hm-tabs").getByRole("link", { name: "Homepage" }).click();
await p.waitForTimeout(300);
const after = await current();
check("clicking Homepage moves the current tab", JSON.stringify(after) === '["Homepage"]', JSON.stringify(after));

await b.close();
writeResult("general-sticky.json", { role, base, results });
process.exit(results.every((r) => r.ok) ? 0 : 1);
