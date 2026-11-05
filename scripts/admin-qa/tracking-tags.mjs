// Core tag manager: save GA4 and Meta in Settings > Privacy & tracking > Tracking, then check the public site as a visitor.
// The tag script is linked, the page CSP names only the enabled vendors' hosts with no console CSP errors,
// nothing is requested from a vendor host before consent (banner on) and requests follow Accept, one form
// submit dispatches exactly one hm:conversion with the submission id, and axe finds nothing on the page.
// Needs the cookie banner on (Settings > Privacy & tracking > Cookie consent) so consent gates the tags. A form page is read from
// QA_FORM_PATH (default /contact); without a form there the conversion check is skipped.
// Run: scripts/admin-qa/run-local.sh tracking-tags.mjs
import fs from "node:fs";
import { launch, newSession, base, admin } from "./lib.mjs";

const axeSrc = fs.readFileSync(new URL("./vendor/axe.min.js", import.meta.url), "utf8");
const b = await launch();
let bad = 0;
const ok = (c, m) => { console.log(`${c ? "ok  " : "FAIL"} ${m}`); if (!c) bad++; };

// Owner turns GA4 and Meta on.
const p = await newSession(b, { theme: "light", width: 1440 });
await p.goto(`${base}${admin}/settings/tracking`);
await p.locator("label[for=tracking-ga4-enabled]").click();
await p.fill("#tracking-ga4-id", "G-QA1234567");
await p.locator("label[for=tracking-meta-enabled]").click();
await p.fill("#tracking-meta-id", "123456789012345");
await Promise.all([p.waitForURL((u) => u.pathname.endsWith("/settings/tracking")), p.click('form[action$="/settings/tracking"] button[type=submit]')]);
ok(await p.locator("#tracking-ga4-enabled").isChecked() && (await p.inputValue("#tracking-meta-id")) === "123456789012345", "GA4 and Meta are saved on");

// A bad id shows an inline error and saves nothing.
await p.fill("#tracking-ga4-id", "not-an-id");
await Promise.all([p.waitForLoadState("load"), p.click('form[action$="/settings/tracking"] button[type=submit]')]);
ok((await p.locator("#tracking-ga4-id-error").count()) === 1 && (await p.locator("#tracking-ga4-id").getAttribute("aria-invalid")) === "true", "a bad id shows an inline error");
await p.goto(`${base}${admin}/settings/tracking`);
ok((await p.inputValue("#tracking-ga4-id")) === "G-QA1234567", "the bad id was not saved");

// axe on the Tracking page.
await p.addScriptTag({ content: axeSrc });
const res = await p.evaluate(() => axe.run(document, { runOnly: { type: "tag", values: ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"] } }));
ok(res.violations.length === 0, `axe: Settings > Privacy & tracking > Tracking has ${res.violations.length} violations ${res.violations.map((x) => x.id).join(",")}`);

// A visitor with the real CSP; vendor hosts are counted, not fetched.
const ctx = await b.newContext({ viewport: { width: 1440, height: 900 } });
const vendorHost = /(googletagmanager|google-analytics|analytics\.google|facebook)\.(com|net)/;
const hits = [];
await ctx.route(vendorHost, (r) => { hits.push(r.request().url()); return r.fulfill({ status: 200, contentType: "text/javascript", body: "" }); });
const v = await ctx.newPage();
const errors = [];
v.on("pageerror", (e) => errors.push(String(e)));
v.on("console", (m) => { if (m.type() === "error") errors.push(m.text()); });
const resp = await v.goto(`${base}/`);
const csp = resp.headers()["content-security-policy"] || resp.headers()["content-security-policy-report-only"] || "";
ok(await v.locator('script[src*="/theme/assets/hm-tags.js?v="]').count() === 1, "the page links hm-tags.js with a hash");
ok(csp.includes("https://www.googletagmanager.com") && csp.includes("https://connect.facebook.net") && !/hubspot|tiktok|clarity|crisp|tawk/.test(csp), "the CSP names the enabled vendors only");
ok(await v.evaluate(() => !Array.from(document.scripts).some((s) => !s.src && s.type !== "application/ld+json" && s.type !== "application/json")), "no inline script on the page");
await v.waitForTimeout(500);
if (await v.locator('[aria-label="Cookie notice"]').count()) {
  ok(hits.length === 0, `no vendor request before consent (${hits.join(", ")})`);
  await v.getByRole("button", { name: "Accept all" }).click();
  await v.waitForTimeout(800);
  ok(hits.some((u) => u.includes("googletagmanager.com")) && hits.some((u) => u.includes("facebook.net")), "vendor scripts are requested after Accept");
} else {
  ok(false, "turn the cookie banner on in Settings > Privacy & tracking > Cookie consent for the consent check");
}
ok(errors.length === 0, `no console or page errors (${errors.join("; ")})`);

// One submit, one hm:conversion.
const formPath = process.env.QA_FORM_PATH || "/contact";
await v.goto(`${base}${formPath}`);
const form = v.locator("form[data-hm-form]").first();
if (await form.count()) {
  await v.evaluate(() => { window.__conv = []; document.addEventListener("hm:conversion", (e) => window.__conv.push(e.detail)); });
  for (const f of await form.locator("input[required], textarea[required]").all()) {
    const type = (await f.getAttribute("type")) || "text";
    await f.fill(type === "email" ? "qa@example.com" : "QA test");
  }
  await form.locator("button[type=submit]").click();
  await v.waitForSelector(".hm-form__success");
  const conv = await v.evaluate(() => window.__conv);
  ok(conv.length === 1 && /^[0-9a-f-]{36}$/.test(conv[0].id), `exactly one hm:conversion with the submission id (${JSON.stringify(conv)})`);
} else {
  console.log(`skip conversion check: no form at ${formPath} (set QA_FORM_PATH)`);
}

await b.close();
process.exit(bad ? 1 : 0);
