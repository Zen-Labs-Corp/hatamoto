// The media picker keeps its first page per type until the page reloads: a second open sends no request,
// search inside it still asks the server, an upload drops the kept page, and focus moves in and back to the opener. Not covered here:
// the document type (pdf-widget.mjs opens it).
// node scripts/admin-qa/panels-picker.mjs   (run-local.sh panels-picker.mjs)  Exit 1 on a failed check or error.
import { launch, newSession, base, admin } from "./lib.mjs";

const b = await launch();
let bad = 0;
const ok = (c, m) => {
  console.log(`${c ? "ok  " : "FAIL"} ${m}`);
  if (!c) bad++;
};

const page = await newSession(b, { width: 1440 });
const errors = [];
page.on("pageerror", (e) => errors.push(e.message));
page.on("console", (m) => {
  if (m.type() === "error") errors.push(m.text());
});
const reqs = [];
page.on("request", (r) => {
  if (new URL(r.url()).pathname.endsWith("/media/picker")) reqs.push(r.url());
});
const isOpen = () => page.waitForSelector("#hm-media-picker[open] #hm-picker");

await page.goto(`${base}${admin}/settings/admin-branding`);
const opener = page.locator("[data-hm-media-picker]:visible").first();
await opener.click();
await isOpen();
ok(await page.evaluate(() => document.getElementById("hm-media-picker").contains(document.activeElement)), "focus is inside the picker");
await page.keyboard.press("Escape");
await page.waitForFunction(() => !document.getElementById("hm-media-picker").open);
ok(await opener.evaluate((el) => el === document.activeElement), "Esc returns focus to the opener");
await opener.click();
await isOpen();
ok(reqs.length === 1, `second open of the same type sends no request (${reqs.length} request)`);
await page.fill("#picker-q", "qa");
for (let i = 0; i < 40 && reqs.length < 2; i++) await page.waitForTimeout(100); // the search waits 300ms
ok(reqs.length === 2 && reqs[1].includes("q=qa"), "search inside the picker still asks the server");
await page.click("#hm-media-picker [data-hm-picker-close]"); // Esc in a search box first clears it
await page.waitForFunction(() => !document.getElementById("hm-media-picker").open);

// An upload from inside the picker drops the kept page: the reopen asks the server and lists the file.
const name = `qa-picker-${Date.now()}.png`;
const png = Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==", "base64");
await opener.click();
await isOpen();
await page.setInputFiles("#hm-picker input[type=file]", { name, mimeType: "image/png", buffer: png });
await page.click("#hm-picker .hm-picker__upload button[type=submit]");
await page.waitForFunction((n) => document.getElementById("hm-picker")?.textContent.includes(n), name, { timeout: 15000 }).catch(() => {});
await page.click("#hm-media-picker [data-hm-picker-close]");
await page.waitForFunction(() => !document.getElementById("hm-media-picker").open);
const before = reqs.length;
await opener.click();
await isOpen();
ok(reqs.length > before, "reopen after an upload sends a request (kept page dropped)");
ok(await page.evaluate((n) => document.getElementById("hm-picker").textContent.includes(n), name), "reopened picker lists the uploaded file");
await page.click("#hm-media-picker [data-hm-picker-close]");
await page.waitForFunction(() => !document.getElementById("hm-media-picker").open);

ok(errors.length === 0, `no console or page errors${errors.length ? ": " + errors.slice(0, 3).join(" | ") : ""}`);
await b.close();
console.log(bad ? `${bad} check(s) failed` : "all checks passed");
process.exit(bad ? 1 : 0);
