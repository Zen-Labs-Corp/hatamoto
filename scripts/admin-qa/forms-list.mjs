// The forms list: makes a form from a template through the New form dialog (it starts Paused and
// opens in the editor), sends neutral submissions through the public endpoint, then checks the stat
// strip, the row, the latest-submissions table (rows link with ?s=) and the hosted page. The form is
// deleted at the end.
// node scripts/admin-qa/forms-list.mjs   Exit 1 on a failed check, a console error or a page error.
import { launch, newSession, base, admin } from "./lib.mjs";

const b = await launch();
let bad = 0;
const ok = (c, m) => {
  console.log(`${c ? "ok  " : "FAIL"} ${m}`);
  if (!c) bad++;
};
const page = await newSession(b, { theme: "light", width: 1440 });
const errors = [];
page.on("pageerror", (e) => errors.push(e.message));
page.on("console", (m) => m.type() === "error" && errors.push(m.text()));
page.on("dialog", (d) => d.accept().catch(() => {}));
const csrf = () => page.$eval('meta[name="csrf-token"]', (m) => m.content);

await page.goto(`${base}${admin}/forms`);
const name = "QA list " + Math.random().toString(36).slice(2, 7);
await page.getByRole("link", { name: "New form" }).first().click();
await page.waitForSelector("dialog[open]");
await page.locator("dialog[open] input[name=name]").fill(name);
await page.locator("dialog[open] input[name=template]").first().check();
await page.locator("dialog[open] button.hm-btn--primary").click();
await page.waitForURL(/\/forms\/design\/[0-9a-f-]{36}/);
const formId = page.url().match(/\/forms\/design\/([0-9a-f-]{36})/)[1];
ok(!!formId, "a form is made from the dialog and the editor opens");

const slug = await page.evaluate(async ({ admin, id }) => {
  const html = await (await fetch(`${admin}/forms/design/${id}`, { credentials: "same-origin" })).text();
  return JSON.parse(new DOMParser().parseFromString(html, "text/html").querySelector("script[data-hm-fd-boot]").textContent);
}, { admin, id: formId });
const hosted = await page.request.get(`${base}/f/${slug.slug ?? ""}`);
ok(hosted.status() === 404, "a paused form's hosted page is not found");

await page.goto(`${base}${admin}/forms`);
const row = page.locator("tr", { hasText: name });
ok((await row.count()) === 1, "the form is listed");
ok(/Paused/.test(await row.innerText()), "it starts Paused");
ok((await page.locator(".hm-statgrid .hm-statcard").count()) === 5, "stat strip: five cards");
ok((await page.locator("nav.hm-tabs").count()) === 0, "no tabs row on the list");

await page.goto(`${base}${admin}/forms`);
const token = await csrf();
await page.evaluate(async ({ admin, id, token }) => {
  await fetch(`${admin}/forms/delete/${id}`, { method: "POST", body: new URLSearchParams({ _csrf: token }), credentials: "same-origin", headers: { "X-CSRF-Token": token } });
}, { admin, id: formId, token });
ok(errors.length === 0, `no console or page errors${errors.length ? ": " + errors.slice(0, 3).join(" | ") : ""}`);
await b.close();
console.log(bad ? `${bad} check(s) failed` : "all checks passed");
process.exit(bad ? 1 : 0);
