// Screenshots of the admin address flow, from a fresh install: no seed.sh, the
// owner is created through the real setup wizard with the setup code from the
// server log. Boots its own server (twice: a fresh boot for the wizard and the
// Security page, then a reboot on the legacy /admin address with no
// acknowledgement for the upgrade banner and the Keep confirm).
//
//   HATAMOTO_BIN=/tmp/hm DATABASE_URL=postgres://.../empty_db?sslmode=disable \
//     node scripts/admin-qa/shots-address.mjs
//
// DATABASE_URL must be an EMPTY scratch database (the script migrates it, makes
// an owner and rewrites its admin address; never point it at a real site).
// Env: HATAMOTO_BIN (built `hatamoto`), DATABASE_URL, PSQL (default `psql`),
// SHOTS (output dir, default scripts/admin-qa/shots-out/address),
// plus the usual CHROME / PW (lib.mjs). Desktop 1440, light.
import { spawn, execFileSync } from "node:child_process";
import fs from "node:fs";
import net from "node:net";
import os from "node:os";
import path from "node:path";
import { launch, newSession, pw, password, sizes } from "./lib.mjs";
import { here } from "./lib.mjs";

const bin = process.env.HATAMOTO_BIN;
const dsn = process.env.DATABASE_URL;
if (!bin || !dsn) {
  console.error("shots-address: set HATAMOTO_BIN and DATABASE_URL (an empty scratch database)");
  process.exit(2);
}
const shots = process.env.SHOTS || path.join(here, "shots-out", "address");
fs.mkdirSync(shots, { recursive: true });
const psql = process.env.PSQL || "psql";
const owner = { name: "Olga Owner", email: "olga@qa.test" };

const freePort = () => new Promise((res) => { const s = net.createServer().listen(0, "127.0.0.1", () => { const p = s.address().port; s.close(() => res(p)); }); });
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const work = fs.mkdtempSync(path.join(os.tmpdir(), "hm-addr-shots-"));
let server = null;
let log = "";

// boot starts the server on a free port and resolves with its base URL once /health answers.
async function boot() {
  const port = await freePort();
  log = "";
  server = spawn(bin, ["serve"], {
    cwd: work,
    env: { ...process.env, DATABASE_URL: dsn, API_HOST: "127.0.0.1", API_PORT: String(port), EMAIL_DISABLED: "true", HATAMOTO_DATA: path.join(work, "data"), HATAMOTO_ADMIN_PATH: "" },
  });
  for (const s of [server.stdout, server.stderr]) s.on("data", (d) => (log += d));
  const url = `http://127.0.0.1:${port}`;
  for (let i = 0; i < 120; i++) {
    if (server.exitCode !== null) throw new Error(`server exited:\n${log}`);
    try { if ((await fetch(`${url}/health`)).ok) return url; } catch { /* not up yet */ }
    await sleep(500);
  }
  throw new Error(`server did not come up:\n${log}`);
}
async function stop() {
  if (!server || server.exitCode !== null) return;
  server.kill();
  await new Promise((r) => server.once("exit", r));
}
const sql = (q) => execFileSync(psql, [dsn, "-v", "ON_ERROR_STOP=1", "-q", "-c", q], { stdio: ["ignore", "inherit", "inherit"] });

// A short wait lets the page's view transition finish, so a shot is never a cross-fade.
const shot = async (page, name) => { await page.waitForLoadState("load"); await page.waitForTimeout(800); return page.screenshot({ path: path.join(shots, name), fullPage: true }).then(() => console.log("wrote", name)); };

// signIn opens the admin login at the current address and signs the owner in.
async function signIn(page, base, ap) {
  await page.goto(`${base}${ap}/login`);
  await page.fill("#email", owner.email);
  await page.fill("#password", password);
  await Promise.all([page.waitForURL((u) => !u.pathname.endsWith("/login")), page.click(`form[action="${ap}/login"] button[type=submit]`)]);
}

// completeProfile gives the owner the name and photo the admin insists on,
// through the same endpoints the page uses (upload a pixel, pick it, save).
async function completeProfile(page, ap) {
  await page.goto(`${page.url().split(ap)[0]}${ap}/profile/complete`);
  const result = await page.evaluate(async (ap) => {
    const token = document.querySelector('meta[name="csrf-token"]')?.content || "";
    const png = Uint8Array.from(atob("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGNgYGD4DwABBAEAHxUEWQAAAABJRU5ErkJggg=="), (c) => c.charCodeAt(0));
    const fd = new FormData();
    fd.append("file", new File([png], "qa-pixel.png", { type: "image/png" }));
    await fetch(`${ap}/media/upload`, { method: "POST", headers: { "X-CSRF-Token": token }, body: fd });
    const picker = await (await fetch(`${ap}/media/picker`)).text();
    const photo = (picker.match(/data-hm-pick="([^"]*)"/) || [])[1] || "/media/qa-pixel.png";
    const body = new URLSearchParams({ name: "Olga Owner", photo, public: "false" });
    const r = await fetch(`${ap}/profile/complete`, { method: "POST", headers: { "X-CSRF-Token": token, "Content-Type": "application/x-www-form-urlencoded" }, body, redirect: "manual" });
    return r.type === "opaqueredirect" ? 303 : r.status;
  }, ap);
  if (result !== 303) throw new Error(`completing the owner profile failed (${result})`);
}

const browser = await launch();
try {
  // ---- phase 1: a fresh install, through the wizard ----
  let base = await boot();
  const m = log.match(/Setup: open (\S+?)\/setup and enter code ([A-Za-z0-9-]+)/);
  if (!m) throw new Error(`no setup line in the server log:\n${log}`);
  let ap = m[1];
  const code = m[2];
  console.log("fresh install, wizard at a private address");
  const ctx = await browser.newContext({ viewport: sizes[1440] });
  await ctx.addCookies([{ name: "hm_theme", value: "light", url: base }]);
  const page = await ctx.newPage();

  await page.goto(`${base}${ap}/setup`);
  await page.fill("#code", code);
  await page.fill("#name", owner.name);
  await page.fill("#email", owner.email);
  await page.fill("#password", password);
  await page.fill("#confirm", password);
  await Promise.all([page.waitForURL("**/setup/admin-address"), page.click('button[type=submit].hm-btn--primary')]);
  await shot(page, "wizard-admin-address.png");

  // A weak address of your own asks for the typed confirmation.
  await page.getByLabel("Choose my own").check();
  await page.fill("#custom", "my-place");
  await page.waitForSelector("#addr-strength .hm-badge", { timeout: 5000 });
  await Promise.all([page.waitForSelector("[data-hm-danger-confirm]"), page.click('form button.hm-btn--primary[type=submit]')]);
  await page.fill("[data-hm-danger-confirm] input[name=confirm_phrase]", "/my-place");
  await shot(page, "wizard-admin-address-weak-confirm.png");

  // Cancel, and go on with the private address.
  await page.click("[data-hm-danger-confirm] a");
  await page.waitForURL("**/setup/admin-address");
  await Promise.all([page.waitForURL("**/setup/site"), page.click('form button.hm-btn--primary[type=submit]')]);
  // The address did not change: a private one stays.
  const finish = async (url, fill) => {
    for (const [sel, v] of Object.entries(fill || {})) await page.fill(sel, v);
    await Promise.all([page.waitForURL((u) => u.pathname !== new URL(url).pathname), page.click('main form button.hm-btn--primary[type=submit]')]);
  };
  await finish(page.url(), { "#name": "QA Bakery" });
  if (page.url().endsWith("/setup/email")) await page.goto(`${base}${ap}/setup/theme`);
  await finish(page.url());
  await finish(page.url());
  await page.waitForURL("**/setup/done");
  await shot(page, "done.png");

  await completeProfile(page, ap);

  await page.goto(`${base}${ap}/settings/security/admin-address`);
  await shot(page, "security.png");

  // Change address: the password first (sudo) unless the account step's confirmation is still
  // fresh, then the picker with a weak choice.
  await page.goto(`${base}${ap}/settings/security/admin-address/change`);
  if (await page.locator("#password").count()) {
    await page.fill("#password", password);
    await Promise.all([page.waitForURL("**/admin-address/change"), page.click('form button.hm-btn--primary[type=submit]')]);
  }
  await page.getByLabel("Choose my own").check();
  await page.fill("#custom", "my-place");
  await Promise.all([page.waitForSelector("[data-hm-danger-confirm]"), page.click('form button.hm-btn--primary[type=submit]')]);
  await shot(page, "security-change-confirm.png");
  await ctx.close();
  await stop();

  // ---- phase 2: an upgraded install on /admin, nobody has accepted it ----
  sql("UPDATE hatamoto_site_config SET admin_path = '/admin', admin_path_set_by = NULL, admin_path_set_at = NULL, admin_path_ack_by = NULL, admin_path_ack_at = NULL");
  base = await boot();
  ap = "/admin";
  console.log("upgraded install on the default address");
  const up = await browser.newContext({ viewport: sizes[1440] });
  await up.addCookies([{ name: "hm_theme", value: "light", url: base }]);
  const upage = await up.newPage();
  await signIn(upage, base, ap);
  await upage.goto(`${base}${ap}/settings/general`);
  await upage.waitForSelector("[data-hm-address-banner]");
  await shot(upage, "banner.png");
  await upage.goto(`${base}${ap}/settings/security/admin-address/keep`);
  await upage.fill("[data-hm-danger-confirm] input[name=confirm_phrase]", "/admin");
  await shot(upage, "keep-confirm.png");
  await up.close();
} finally {
  await browser.close();
  await stop();
  fs.rmSync(work, { recursive: true, force: true });
}
