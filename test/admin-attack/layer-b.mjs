// B-1 to B-3: Layer B. A page carrying community script runs in an opaque origin: the sandbox CSP
// is enforced without allow-same-origin, self.origin is "null", document.cookie and localStorage
// are refused, and its fetch to the admin reaches the server with no Cookie header.
// Needs the server built with -tags hatamoto_qa, started with HATAMOTO_QA_SANDBOX_ALL=1 and
// pinned at /admin (the capture /__qa/seen only records paths that start with /admin).
// The page is /__qa/opaque, served through the real public render path (writeHTML).
import { launch, newOwner, bearer, base, addr, record, finish } from "./lib.mjs";

if (addr !== "/admin") throw new Error("layer-b needs the server pinned at /admin");

// A published form for the QA page to embed, created through the API.
const slug = `atk-opaque-${Date.now().toString(36)}`;
const headers = { Authorization: `Bearer ${await bearer()}`, "Content-Type": "application/json" };
const cf = await fetch(`${base}/api/v1/forms`, {
  method: "POST",
  headers,
  body: JSON.stringify({ name: "Attack opaque", slug, fields: [{ name: "email", label: "Email", type: "email", required: true, position: 1 }], settings: { store_submissions: true } }),
});
if (!cf.ok) throw new Error(`create form failed: ${cf.status} ${await cf.text()}`);

const browser = await launch();
const { context, page } = await newOwner(browser);
const holds = (await context.cookies(base)).some((c) => c.name === "s" || c.name === "__Host-s");

// Control: a normal same-origin fetch reaches /admin WITH the cookie, so the capture can see one.
const marker = Date.now().toString(36);
await page.evaluate((m) => fetch(`/admin/qa-control-${m}`, { credentials: "include" }), marker);

const csp = [];
page.on("response", (r) => { if (r.url().includes("/__qa/opaque")) csp.push(r.headers()["content-security-policy"] ?? ""); });
await page.goto(`${base}/__qa/opaque?form=${slug}`);
await page.waitForFunction(() => document.getElementById("admin-fetch")?.textContent !== "pending");

const origin = await page.evaluate(() => self.origin);
const access = await page.evaluate(() => {
  const r = {};
  try { void document.cookie; r.cookie = "readable"; } catch (e) { r.cookie = e.name; }
  try { void localStorage.length; r.storage = "readable"; } catch (e) { r.storage = e.name; }
  return r;
});
const seen = await (await fetch(`${base}/__qa/seen`)).json();
const control = seen.find((s) => s.path === `/admin/qa-control-${marker}`);
const probe = seen.find((s) => s.path === "/admin/qa-probe");

record("B-1", "sandboxed page: enforced sandbox CSP without allow-same-origin, origin is null", {
  ok: csp.some((h) => /(^|[;\s,])sandbox\b/.test(h) && !h.includes("allow-same-origin")) && origin === "null",
  control: holds && !!control,
  detail: `origin=${JSON.stringify(origin)} csp=${csp.map((h) => h.slice(0, 60)).join("|")}`,
});
record("B-2", "sandboxed page cannot read document.cookie or localStorage", {
  ok: access.cookie === "SecurityError" && access.storage === "SecurityError",
  control: origin === "null" && holds,
  detail: JSON.stringify(access),
});
record("B-3", "sandboxed fetch to the admin reaches the server with no Cookie", {
  ok: !!probe && probe.cookie === false,
  control: !!control && control.cookie === true,
  detail: probe ? `origin=${probe.origin} site=${probe.fetchSite} cookie=${probe.cookie}` : "probe never reached the server",
});

await browser.close();
finish();
