// Settings > Branding: save a branding with a good accent, then check the sign-in page and the form in light,
// dark and auto (OS dark): axe finds no violation, the nonce'd override applies (the managed-by link is the accent
// in light and the derived shade in dark and auto), and a colour that fails contrast is refused. Resets at the end.
// BASE=http://127.0.0.1:8090 node scripts/admin-qa/branding.mjs     Exit 1 on a failed check.
import fs from "node:fs";
import { launch, newSession, writeResult, base, admin, role } from "./lib.mjs";

const axe = fs.readFileSync(new URL("./vendor/axe.min.js", import.meta.url), "utf8");
const tags = ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"];
const ACCENT = "#1f4e9a";
const results = [];
const check = (name, ok, detail = "") => {
  results.push({ name, ok, detail });
  console.log(`${ok ? "ok  " : "FAIL"} ${name}${detail ? ": " + detail : ""}`);
};

const rgb = (hex) => `rgb(${[1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16)).join(", ")})`;
async function violations(p) {
  if (!(await p.evaluate(() => typeof axe !== "undefined"))) await p.addScriptTag({ content: axe });
  return p.evaluate(async (tags) => (await axe.run(document, { runOnly: { type: "tag", values: tags } })).violations.map((v) => `${v.id} x${v.nodes.length}`), tags);
}

const b = await launch("chromium");
const owner = await newSession(b, { theme: "light", width: 1440, bypassCSP: true });
try {
  await owner.goto(`${base}${admin}/settings/branding`, { waitUntil: "load" });
  await owner.fill("#name", "QA Console");
  await owner.fill("#accent", "#f5e642");
  await owner.fill("#managed_text", "Managed by QA Studio");
  await owner.fill("#managed_url", "https://example.test/qa");
  await owner.click('form[action$="/settings/branding"] button[type=submit]');
  check("a failing accent is refused with the contrast message", (await owner.locator("#accent-error, .hm-field__error").allTextContents()).join(" ").includes("too light to read"));
  await owner.fill("#accent", ACCENT);
  await Promise.all([owner.waitForURL((u) => u.pathname.endsWith("/settings/branding")), owner.click('form[action$="/settings/branding"] button[type=submit]')]);
  check("a good accent saves", (await owner.locator("#accent").inputValue()) === ACCENT);

  for (const mode of ["light", "dark", "auto"]) {
    const ctx = await b.newContext({ viewport: { width: 1440, height: 900 }, colorScheme: mode === "light" ? "light" : "dark", bypassCSP: true });
    if (mode !== "auto") await ctx.addCookies([{ name: "hm_theme", value: mode, url: base }]);
    const p = await ctx.newPage();
    await p.goto(`${base}${admin}/login`, { waitUntil: "load" });
    check(`login ${mode}: brand name and managed-by line show`, (await p.locator(".hm-bare__brand").innerText()).includes("QA Console") && (await p.locator(".hm-bare__managed a").count()) === 1);
    const css = await p.locator("head style").first().textContent();
    const dark = /data-admin-theme="dark"\]\{--color-accent:(#[0-9a-f]{6})/.exec(css)?.[1];
    const want = mode === "light" ? rgb(ACCENT) : rgb(dark || "#000000");
    const got = await p.locator(".hm-bare__managed a").evaluate((a) => getComputedStyle(a).color);
    check(`login ${mode}: the override applies (${want})`, got === want, got);
    check(`login ${mode}: axe finds no violation`, (await violations(p)).length === 0, (await violations(p)).join(", "));
    await ctx.close();
  }

  await owner.goto(`${base}${admin}/settings/branding`, { waitUntil: "load" });
  const v = await violations(owner);
  check("the Branding page has no axe violation", v.length === 0, v.join(", "));
} finally {
  await owner.goto(`${base}${admin}/settings/branding`, { waitUntil: "load" });
  if (await owner.locator('form[action$="/settings/branding/reset"] button').count()) {
    await Promise.all([owner.waitForURL((u) => u.pathname.endsWith("/settings/branding")), owner.click('form[action$="/settings/branding/reset"] button[type=submit]')]);
  }
  await b.close();
}
writeResult("branding.json", { role, base, results });
process.exit(results.every((r) => r.ok) ? 0 : 1);
