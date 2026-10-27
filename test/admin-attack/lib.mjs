// Helpers for the admin attack harness (test tooling only, never shipped).
// Self-contained on purpose: scripts/admin-qa/lib.mjs and seed.sh hardcode /admin,
// and this suite also runs against a random admin address.
// Env: PW (path to playwright-core, required), CHROME, BASE, ADMIN_ADDR (default /admin),
// QA_EMAIL, QA_PASSWORD.
import { createRequire } from "node:module";
import http from "node:http";

const require = createRequire(import.meta.url);
if (!process.env.PW) throw new Error("set PW to the playwright-core directory");
export const pw = require(process.env.PW);
export const base = (process.env.BASE || "http://127.0.0.1:8192").replace(/\/$/, "");
export const addr = process.env.ADMIN_ADDR || "/admin";
export const email = process.env.QA_EMAIL || "owner@qa.test";
export const password = process.env.QA_PASSWORD || "qa-password-123";

export const launch = () => pw.chromium.launch({ executablePath: process.env.CHROME || "/usr/bin/google-chrome" });

// A browser context holding a real owner session, the way the owner browses their own public site.
export async function newOwner(browser, options = {}) {
  const context = await browser.newContext(options);
  const page = await context.newPage();
  await login(page);
  return { context, page };
}

// Sign in through the admin login form at the admin address.
export async function login(page) {
  await page.goto(`${base}${addr}/login`);
  await page.fill("#email", email);
  await page.fill("#password", password);
  await Promise.all([
    page.waitForURL((u) => !u.pathname.endsWith("/login")),
    page.click(`form[action="${addr}/login"] button[type=submit]`),
  ]);
  if (page.url().includes("/login")) throw new Error(`login failed for ${email} at ${addr}`);
}

// A bearer session token for the JSON API (the API login does not depend on the admin address).
export async function bearer() {
  const r = await fetch(`${base}/api/v1/auth/login`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ email, password }),
  });
  if (!r.ok) throw new Error(`api login failed: ${r.status}`);
  return (await r.json()).session_token;
}

// Grant permissions to an installed plugin (owner only). Throws on any non-2xx.
export async function grant(name, permissions) {
  const token = await bearer();
  const r = await fetch(`${base}/api/v1/plugins/${name}/grant`, {
    method: "POST",
    headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
    body: JSON.stringify({ permissions }),
  });
  if (!r.ok) throw new Error(`grant ${name} failed: ${r.status} ${await r.text()}`);
}

// A raw HTTP request that keeps header order and bytes (fetch loses both).
export const raw = (path, { method = "GET", headers = {}, body } = {}) =>
  new Promise((resolve, reject) => {
    const req = http.request(new URL(path, base), { method, headers }, (res) => {
      const chunks = [];
      res.on("data", (c) => chunks.push(c));
      res.on("end", () => resolve({ status: res.statusCode, raw: res.rawHeaders, body: Buffer.concat(chunks).toString() }));
    });
    req.on("error", reject);
    req.end(body);
  });

// Result lines: PASS when the attack failed and its positive control succeeded,
// HOLE when the attack got through, INCONCLUSIVE when the control failed (a blind probe proves nothing).
const results = [];
export function record(id, name, { ok, control, detail = "" }) {
  const state = !control ? "INCONCLUSIVE" : ok ? "PASS" : "HOLE";
  console.log(`${state} ${id} ${name}${detail ? " :: " + detail : ""}`);
  results.push(state);
}
export const finish = () => process.exit(results.length > 0 && results.every((s) => s === "PASS") ? 0 : 1);
