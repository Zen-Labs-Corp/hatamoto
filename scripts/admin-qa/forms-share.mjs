// A form's Share tab and hosted page: the copy fields carry the shortcode, link, endpoint and
// markup, a Paused form says so and its /f/ page is not found. The form is deleted at the end.
// node scripts/admin-qa/forms-share.mjs   Exit 1 on a failed check, a console error or a page error.
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
const csrf = () => page.$eval('meta[name="csrf-token"]', (m) => m.content);

await page.goto(`${base}${admin}/forms`);
const name = "QA share " + Math.random().toString(36).slice(2, 7);
const made = await page.evaluate(async ({ admin, token, name }) => {
  const res = await fetch(`${admin}/forms`, { method: "POST", body: new URLSearchParams({ name, template: "contact", _csrf: token }), credentials: "same-origin", headers: { "X-CSRF-Token": token } });
  return res.url;
}, { admin, token: await csrf(), name });
const formId = made.match(/\/forms\/design\/([0-9a-f-]{36})/)?.[1];
ok(!!formId, "a form is made");
await page.goto(`${base}${admin}/forms/design/${formId}/share`);
const val = (id) => page.locator(`#${id}`).inputValue();
const shortcode = await val("share-shortcode");
const slug = shortcode.match(/slug="([^"]+)"/)?.[1];
ok(!!slug, `shortcode ${shortcode}`);
ok((await val("share-link")).endsWith(`/f/${slug}`), "the link points at the hosted page");
ok(/<form[^>]+action="[^"]+\/forms\/[^"]+\/submit"/.test(await val("share-html")), "the markup posts to the endpoint");
ok(/This form is paused/.test(await page.locator("main").innerText()), "a new form is Paused and the tab says so");
ok((await page.request.get(`${base}/f/${slug}`)).status() === 404, "the hosted page is not found while paused");
await page.goto(`${base}${admin}/forms`);
const token = await csrf();
await page.evaluate(async ({ admin, id, token }) => {
  await fetch(`${admin}/forms/delete/${id}`, { method: "POST", body: new URLSearchParams({ _csrf: token }), credentials: "same-origin", headers: { "X-CSRF-Token": token } });
}, { admin, id: formId, token });
ok(errors.length === 0, `no console or page errors${errors.length ? ": " + errors.slice(0, 3).join(" | ") : ""}`);
await b.close();
console.log(bad ? `${bad} check(s) failed` : "all checks passed");
process.exit(bad ? 1 : 0);
