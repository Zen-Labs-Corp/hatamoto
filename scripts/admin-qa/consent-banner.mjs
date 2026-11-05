// Cookie consent banner: enable it in Settings > Privacy & tracking > Cookie consent, then check the public site as a visitor.
// The script is first in <head>, Consent Mode defaults come first, Reject all writes the cookie and fires
// hm:consent, a reload shows no banner, the preferences dialog is keyboard operable (trap, Esc, focus
// return), [data-hm-consent-open] reopens it, and axe finds nothing on the banner or the dialog.
// Run: scripts/admin-qa/run-local.sh consent-banner.mjs
import fs from "node:fs";
import { launch, newSession, base, admin } from "./lib.mjs";

const axeSrc = fs.readFileSync(new URL("./vendor/axe.min.js", import.meta.url), "utf8");
const b = await launch();
let bad = 0;
const ok = (c, m) => { console.log(`${c ? "ok  " : "FAIL"} ${m}`); if (!c) bad++; };

// Owner turns the banner on.
const p = await newSession(b, { theme: "light", width: 1440 });
await p.goto(`${base}${admin}/settings/privacy`);
await p.locator("label[for=privacy-enabled]").click();
await Promise.all([p.waitForURL((u) => u.pathname.endsWith("/settings/privacy")), p.click('form[action$="/settings/privacy"] button[type=submit]')]);
ok(await p.locator("#privacy-enabled").isChecked(), "the banner switch is saved on");

// A visitor with the real CSP.
const ctx = await b.newContext({ viewport: { width: 1440, height: 900 } });
const v = await ctx.newPage();
const errors = [];
v.on("pageerror", (e) => errors.push(String(e)));
v.on("console", (m) => { if (m.type() === "error") errors.push(m.text()); });
await v.goto(`${base}/`);
await v.waitForSelector('[aria-label="Cookie notice"]');

const first = await v.evaluate(() => { const e = document.head.firstElementChild; return e && e.tagName === "SCRIPT" ? e.getAttribute("src") : null; });
ok(/hm-consent\.js\?v=[0-9a-f]+$/.test(first || ""), `consent script is the first element in head (${first})`);
const def = await v.evaluate(() => Array.from(window.dataLayer[0]));
ok(def[0] === "consent" && def[1] === "default", "dataLayer[0] is consent default");
const keys = ["ad_storage", "ad_user_data", "ad_personalization", "analytics_storage", "functionality_storage", "personalization_storage"];
ok(keys.every((k) => def[2][k] === "denied") && def[2].security_storage === "granted", "defaults deny all six, security_storage granted");
ok(await v.evaluate(() => hm.consent.get().decided === false), "get() is undecided before a choice");

await v.evaluate(() => { window.__seen = []; document.addEventListener("hm:consent", (e) => window.__seen.push(e.detail)); });
await v.getByRole("button", { name: "Reject all" }).click();
const cookie = (await ctx.cookies()).find((c) => c.name === "hm_consent");
ok(!!cookie && cookie.sameSite === "Lax", "Reject all writes the hm_consent cookie");
const state = await v.evaluate(() => ({ get: hm.consent.get(), seen: window.__seen, ls: !!localStorage.getItem("hm_consent"), upd: Array.from(window.dataLayer).some((a) => a[0] === "consent" && a[1] === "update") }));
ok(state.get.analytics === false && state.get.decided === true && state.get.necessary === true, "get() reports every optional category off");
ok(state.seen.length === 1 && state.seen[0].decided === true, "hm:consent fired once with the same shape");
ok(state.ls && state.upd, "choice mirrored to localStorage and pushed as consent update");
await v.waitForSelector('[aria-label="Cookie notice"]', { state: "detached" });

await v.reload();
await v.waitForTimeout(300);
ok((await v.locator('[aria-label="Cookie notice"]').count()) === 0, "a reload shows no banner");
ok(await v.evaluate(() => hm.consent.get().decided === true && hm.consent.allows("analytics") === false), "the stored choice is read back");

// Keyboard: reopen from the floating button, trap, Esc, focus return.
const opener = v.locator(".hm-consent-reopen");
await opener.focus();
await v.keyboard.press("Enter");
const dlg = v.getByRole("dialog");
await dlg.waitFor();
ok(await dlg.getAttribute("aria-modal") === "true", "dialog is modal");
let inside = true;
for (let i = 0; i < 12; i++) {
  await v.keyboard.press("Tab");
  inside = inside && (await v.evaluate(() => !!document.activeElement.closest('[role="dialog"]')));
}
ok(inside, "Tab stays inside the dialog");
await v.keyboard.press("Shift+Tab");
ok(await v.evaluate(() => !!document.activeElement.closest('[role="dialog"]')), "Shift+Tab stays inside the dialog");
await v.keyboard.press("Escape");
await dlg.waitFor({ state: "detached" });
ok(await v.evaluate(() => document.activeElement.classList.contains("hm-consent-reopen")), "Esc closes and focus returns to the opener");

// A footer element with the hook reopens it too.
await v.evaluate(() => { const a = document.createElement("a"); a.href = "#"; a.id = "qa-open"; a.textContent = "Cookie settings"; a.setAttribute("data-hm-consent-open", ""); document.body.appendChild(a); });
await v.click("#qa-open");
await dlg.waitFor();
ok(true, "[data-hm-consent-open] reopens the dialog");
await v.getByRole("checkbox", { name: "Analytics" }).check();
await v.getByRole("button", { name: "Save choices" }).click();
ok(await v.evaluate(() => hm.consent.allows("analytics") === true && hm.consent.allows("marketing") === false), "Save choices stores the ticked categories only");
ok(errors.length === 0, `no console or page errors (${errors.join("; ")})`);

// axe on a fresh visitor, CSP bypassed only so axe can be injected.
const ax = await b.newContext({ viewport: { width: 1440, height: 900 }, bypassCSP: true });
const a = await ax.newPage();
await a.goto(`${base}/`);
await a.waitForSelector('[aria-label="Cookie notice"]');
const run = async (label) => {
  if (!(await a.evaluate(() => typeof axe !== "undefined"))) await a.addScriptTag({ content: axeSrc });
  const res = await a.evaluate(() => axe.run(document, { runOnly: { type: "tag", values: ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"] } }));
  ok(res.violations.length === 0, `axe: ${label} has ${res.violations.length} violations ${res.violations.map((x) => x.id).join(",")}`);
};
await run("the banner");
await a.getByRole("button", { name: "Preferences", exact: true }).click();
await a.getByRole("dialog").waitFor();
await run("the dialog");

await b.close();
process.exit(bad ? 1 : 0);
