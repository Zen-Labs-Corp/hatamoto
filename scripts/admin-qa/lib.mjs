// Shared helpers for the admin QA harness (test tooling only, never shipped).
// Env: BASE (default http://127.0.0.1:8090), PW (playwright-core path), CHROME,
// QA_EMAIL/QA_PASSWORD (owner), QA_EDITOR_EMAIL (editor, same password),
// QA_ROLE=owner|editor, QA_LEGACY=1 (use pre-v1-23a paths), OUT (results dir),
// QA_ADMIN (admin path prefix, default /admin), QA_BASE_URL (alias of BASE).
import { createRequire } from "node:module";
import fs from "node:fs";
import { fileURLToPath } from "node:url";
import path from "node:path";

const require = createRequire(import.meta.url);
// PW wins; else this directory's node_modules (npm ci).
const here0 = path.dirname(fileURLToPath(import.meta.url));
export const pw = require(process.env.PW || path.join(here0, "node_modules", "playwright-core"));
export const base = (process.env.QA_BASE_URL || process.env.BASE || "http://127.0.0.1:8090").replace(/\/$/, "");
export const admin = "/" + (process.env.QA_ADMIN || "/admin").replace(/^\/+|\/+$/g, "");
export const role = process.env.QA_ROLE || "owner";
export const password = process.env.QA_PASSWORD || "qa-password-123";
export const email = role === "editor" ? process.env.QA_EDITOR_EMAIL || "editor@qa.test" : process.env.QA_EMAIL || "owner@qa.test";
export const here = path.dirname(fileURLToPath(import.meta.url));
export const outDir = process.env.OUT || path.join(here, "results");
fs.mkdirSync(outDir, { recursive: true });

export function launch(engine = "chromium", args = []) {
  const opts = engine === "chromium" ? { executablePath: process.env.CHROME || "/usr/bin/google-chrome", args } : {};
  return pw[engine].launch(opts);
}

export const sizes = { 1440: { width: 1440, height: 900 }, 390: { width: 390, height: 844 } };

// bypassCSP lets axe be injected as an inline script (a11y only; perf and
// navigation runs keep the real CSP).
// newSession returns a signed-in page. The theme is set through the hm_theme
// cookie the server reads ("light" or "dark"), which is what POST /admin/theme
// stores, so the first paint already has data-admin-theme without a toggle.
// javaScriptEnabled: false is the no-JS browser (the pages' plain-form path).
export async function newSession(browser, { theme = "light", width = 1440, signedIn = true, bypassCSP = false, javaScriptEnabled = true } = {}) {
  const ctx = await browser.newContext({ viewport: sizes[width], deviceScaleFactor: width === 390 ? 2 : 1, bypassCSP, javaScriptEnabled });
  await ctx.addCookies([{ name: "hm_theme", value: theme, url: base }]);
  const page = await ctx.newPage();
  if (signedIn) await login(page);
  return page;
}

export async function login(page) {
  await page.goto(`${base}${admin}/login`);
  await page.fill("#email", email);
  await page.fill("#password", password);
  await Promise.all([page.waitForURL((u) => !u.pathname.endsWith(`${admin}/login`)), page.click(`form[action="${admin}/login"] button[type=submit]`)]);
  if (page.url().includes(`${admin}/login`)) throw new Error(`login failed for ${email}`);
}

// Tokens in url paths are resolved by finding a link on a list screen, so the
// harness needs no database access. Resolved once per run and cached.
const tokenSources = {
  // The entity list is paged, so search for the seeded title instead of hoping it is on page 1.
  post: [`${admin}/entities?q=QA+Post+1`, `a[href^="${admin}/entities/"]`, "QA Post 1"],
  page: [`${admin}/entities?q=` + encodeURIComponent(process.env.QA_PAGE_NAME || "About"), `a[href^="${admin}/entities/"]`, process.env.QA_PAGE_NAME || "About"],
  media: [`${admin}/settings/media`, `a[href^="${admin}/media/"]`, ""],
  form: [`${admin}/settings/forms`, 'a[href*="/forms/"]', ""],
  menu: [`${admin}/theme/menus`, 'a[href*="/menus/"]', ""],
  section: [`${admin}/theme/templates`, 'a[href*="/templates/"]', ""],
  type: [`${admin}/settings/types`, `a[href^="${admin}/settings/types/"]`, ""],
  user: [`${admin}/settings/users`, 'a[href*="/users/"]', ""],
};
const cache = new Map();
export async function resolvePath(page, urlPath, legacy = false) {
  const m = urlPath.match(/\{(\w+)\}/);
  if (!m) return urlPath;
  const key = m[1];
  if (!cache.has(key)) {
    const [list, sel, text] = tokenSources[key];
    const listPath = legacy ? legacyList[key] || list : list;
    await page.goto(base + listPath);
    const hrefs = await page.$$eval(sel, (as, t) => as.filter((a) => !t || a.textContent.includes(t)).map((a) => a.getAttribute("href")), text);
    const id = hrefs.map((h) => h.match(/\/(?:entities|media|forms|menus|templates|sections|users|types)\/([0-9a-f-]{36})/)?.[1]).find(Boolean);
    cache.set(key, id || null);
  }
  const id = cache.get(key);
  return id ? urlPath.replace(m[0], id) : null;
}
const legacyList = { media: `${admin}/media`, form: `${admin}/forms`, menu: `${admin}/menus`, section: `${admin}/sections`, user: `${admin}/users` };

// Writes a result file under outDir and returns its path.
export function writeResult(name, data) {
  const p = path.join(outDir, name);
  fs.writeFileSync(p, typeof data === "string" ? data : JSON.stringify(data, null, 1));
  return p;
}

export const median = (xs) => { const s = [...xs].sort((a, b) => a - b); return s.length ? s[Math.floor(s.length / 2)] : null; };

export const strict = process.env.QA_STRICT === "1";
export const legacyMode = process.env.QA_LEGACY === "1";

// visit opens a screen from the inventory. Returns { ok, skipped, status, url }.
// skipped: a path token had no data (nothing seeded); a screen that answers
// anything other than its expected status (200, or `status`) is not ok.
export async function visit(page, s) {
  const p = await resolvePath(page, s.path, legacyMode);
  if (!p) return { ok: !strict, skipped: true, status: 0, url: s.path };
  const res = await page.goto(base + p, { waitUntil: "load" });
  let status = res?.status() ?? 0;
  if (s.submit && status === 200) {
    for (const [sel, value] of Object.entries(s.submit.fill || {})) await page.fill(sel, value);
    if (s.submit.check) await page.check(s.submit.check);
    const [after] = await Promise.all([page.waitForNavigation({ waitUntil: "load" }), page.click(s.submit.button)]);
    status = after?.status() ?? 200;
  }
  const missing = s.expect && !(await page.$(s.expect));
  return { ok: status === (s.status || 200) && !missing, skipped: false, status, url: p };
}
