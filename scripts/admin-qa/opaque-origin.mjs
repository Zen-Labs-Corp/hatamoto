// Browser acceptance for the opaque-origin sandbox (ISC-12..14). Needs a server built with
// QA_TAGS=hatamoto_qa and started with HATAMOTO_QA_SANDBOX_ALL=1 (run-local.sh does both
// when told to; see README). It serves /__qa/opaque (sandboxed through the real writeHTML
// path) and /__qa/seen (what reached /admin, as the server saw it).
//
// Checks, in a context that holds a signed-in admin session:
//   1. the sandboxed page reports self.origin === "null"
//   2. its fetch('/admin...', {credentials:'include'}) arrived with no Cookie header
//      (server-side capture; a same-origin control fetch from a normal page does send it)
//   3. a public form on the page submits through hm.js (Origin: null plus the hm_ts token)
//   4. document.cookie and localStorage are refused to the sandboxed page
//   5. hm.js's pageview reaches /api/v1/track with Origin: null and no Cookie
import { pw, launch, base, email, password, login } from "./lib.mjs";

void pw;
const failures = [];
const check = (ok, msg, detail = "") => {
  console.log(`${ok ? "PASS" : "FAIL"} ${msg}${detail ? `: ${detail}` : ""}`);
  if (!ok) failures.push(msg);
};

// A published form for the page to submit, created through the API with a bearer token.
const slug = `qa-opaque-${Date.now().toString(36)}`;
const lr = await fetch(`${base}/api/v1/auth/login`, {
  method: "POST",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify({ email, password }),
});
if (!lr.ok) throw new Error(`api login failed: ${lr.status}`);
const bearer = { Authorization: `Bearer ${(await lr.json()).session_token}`, "Content-Type": "application/json" };
const cf = await fetch(`${base}/api/v1/forms`, {
  method: "POST",
  headers: bearer,
  body: JSON.stringify({
    name: "QA opaque",
    slug,
    fields: [{ name: "email", label: "Email", type: "email", required: true, position: 1 }],
    settings: { store_submissions: true },
  }),
});
if (!cf.ok) throw new Error(`create form failed: ${cf.status} ${await cf.text()}`);

const browser = await launch();
try {
  const ctx = await browser.newContext();
  const page = await ctx.newPage();
  await login(page);
  const cookies = await ctx.cookies(base);
  check(cookies.some((c) => c.name === "s" || c.name === "__Host-s"), "context holds the admin session cookie", cookies.map((c) => c.name).join(","));

  // Control: a normal same-origin fetch reaches /admin WITH the cookie, so the capture can see one.
  await page.evaluate(() => fetch("/admin/qa-control", { credentials: "include" }));

  const seen = async () => (await (await fetch(`${base}/__qa/seen`)).json());
  const csp = [];
  const submits = [];
  page.on("response", (r) => { if (r.url().endsWith("/__qa/opaque") || r.url().includes("/__qa/opaque?")) csp.push(r.headers()["content-security-policy"]); });
  const trackStatus = [];
  page.on("response", (r) => { if (r.url().endsWith("/api/v1/track")) trackStatus.push(r.status()); });
  page.on("request", (r) => { if (r.method() === "POST" && r.url().includes(`/forms/${slug}/submit`)) submits.push(r); });

  await page.goto(`${base}/__qa/opaque?form=${slug}`);
  await page.waitForFunction(() => document.getElementById("admin-fetch")?.textContent !== "pending");

  check(csp.some((h) => h && h.includes("sandbox") && !h.includes("allow-same-origin")), "page served with an enforced sandbox CSP without allow-same-origin", csp.join(" | "));
  const origin = await page.evaluate(() => self.origin);
  check(origin === "null", 'self.origin === "null"', JSON.stringify(origin));

  const all = await seen();
  const control = all.find((s) => s.path === "/admin/qa-control");
  const probe = all.find((s) => s.path === "/admin/qa-probe");
  console.log("server-side capture, /admin/qa-* requests:", JSON.stringify(all.filter((s) => s.path.startsWith("/admin/qa-"))));
  check(!!control && control.cookie === true, "control: same-origin fetch arrived with a Cookie header");
  check(!!probe, "sandboxed fetch('/admin/qa-probe', {credentials:'include'}) reached the server");
  check(!!probe && probe.cookie === false, "sandboxed fetch arrived with no Cookie header", probe ? `origin=${probe.origin} site=${probe.fetchSite}` : "");

  // The pageview fires on load; give the request a moment to land.
  let track;
  for (let i = 0; i < 40 && !track; i++) {
    track = (await seen()).find((s) => s.path === "/api/v1/track");
    if (!track) await new Promise((r) => setTimeout(r, 250));
  }
  check(!!track, "pageview reached /api/v1/track from the sandboxed page");
  for (let i = 0; i < 20 && !trackStatus.length; i++) await new Promise((r) => setTimeout(r, 250));
  check(trackStatus.includes(202), "pageview was accepted (202)", JSON.stringify(trackStatus));
  check(!!track && track.origin === "null" && track.cookie === false, "pageview carried Origin: null and no Cookie", track ? `origin=${track.origin} cookie=${track.cookie}` : "");

  const jsAccess = await page.evaluate(() => {
    const r = {};
    try { void document.cookie; r.cookie = "readable"; } catch (e) { r.cookie = e.name; }
    try { void localStorage.length; r.storage = "readable"; } catch (e) { r.storage = e.name; }
    return r;
  });
  check(jsAccess.cookie === "SecurityError", "document.cookie refused", jsAccess.cookie);
  check(jsAccess.storage === "SecurityError", "localStorage refused", jsAccess.storage);

  // The page's hm.js fetches the timing token with Origin: null; wait for it, then submit.
  await page.waitForFunction(() => !!document.querySelector('form.hm-form input[name="hm_ts"]')?.value, null, { timeout: 10000 }).catch(() => {});
  const hasToken = await page.evaluate(() => !!document.querySelector('form.hm-form input[name="hm_ts"]')?.value);
  check(hasToken, "form got an hm_ts token across the opaque origin");
  await page.click('form.hm-form button[type="submit"]');
  const ok = await page.waitForSelector(".hm-form__success", { timeout: 10000 }).then(() => true, () => false);
  check(ok, "public form submitted from the sandboxed page (.hm-form__success shown)");
  const req = submits[0];
  check(!!req && (await req.allHeaders()).origin === "null", "submit carried Origin: null", req ? (await req.allHeaders()).origin : "no submit seen");
  const list = await (await fetch(`${base}/api/v1/forms`, { headers: bearer })).json();
  const form = (list.forms || list.data || list).find?.((f) => f.slug === slug);
  const subs = form ? await (await fetch(`${base}/api/v1/forms/${form.id}/submissions`, { headers: bearer })).json() : null;
  const n = subs ? (subs.total ?? subs.submissions?.length ?? subs.data?.length ?? 0) : 0;
  check(n === 1, "exactly one stored submission", String(n));
} finally {
  await browser.close();
}
if (failures.length) {
  console.error(`\nopaque-origin: ${failures.length} check(s) failed`);
  process.exit(1);
}
console.log("\nopaque-origin: all checks passed");
