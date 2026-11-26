// Plugin screen tab: /admin/plugins/frameplug lands on the Frames screen (/app), where the plugin frame
// fills the page and follows its content height. Owner: tabs Frames (current), Settings, Permissions;
// editor: no tabs, no "Turn off". The Settings tab carries no frame. Checks at 1440 light.
// Run: PG_BIN=<pg bin dir> [QA_ROLE=editor] [QA_SHOTS=<dir>] bash scripts/admin-qa/run-local.sh plugin-screen.mjs
// Exit 1 on any failed check. Not in the default run list (needs the frameplug fixture, loaded by run-local.sh).
import fs from "node:fs";
import path from "node:path";
import { launch, newSession, writeResult, base, role } from "./lib.mjs";

const SHOTS = process.env.QA_SHOTS || "";
if (SHOTS) fs.mkdirSync(SHOTS, { recursive: true });
const results = [];
let bad = 0;
const check = (ok, msg, detail = "") => {
  console.log(`${ok ? "PASS" : "FAIL"}  ${msg}${!ok && detail ? "  " + detail : ""}`);
  results.push({ ok, msg, detail });
  if (!ok) bad++;
};

const b = await launch("chromium");
const p = await newSession(b, { theme: "light", width: 1440 });
const shot = (name) => (SHOTS ? p.screenshot({ path: path.join(SHOTS, `${name}-${role}.png`), fullPage: true }) : null);
const pframe = () => p.frames().find((f) => f.url().includes("/plugin-ui/frameplug/index.html"));
const frameHeight = () => p.locator("iframe.hm-pluginframe").evaluate((el) => el.offsetHeight);

// 1. The plugin's address lands on the screen.
await p.goto(`${base}/admin/plugins/frameplug`, { waitUntil: "load" });
check(new URL(p.url()).pathname.endsWith("/plugins/frameplug/app"), "/admin/plugins/frameplug lands on /app", p.url());

// 2. Tabs: owner has three, editor none.
if (role === "owner") {
  const tabs = (await p.locator(".hm-tabs__tab").allTextContents()).map((t) => t.trim());
  check(tabs[0] === "Frames" && tabs[1] === "Settings" && tabs[2] === "Permissions", "owner: tabs Frames, Settings, Permissions", JSON.stringify(tabs));
  check((await p.locator(".hm-tabs__tab").first().getAttribute("aria-current")) === "page", "owner: Frames is the current tab");
} else {
  check((await p.locator(".hm-tabs").count()) === 0, "editor: no tabs");
  check((await p.getByText("Turn off").count()) === 0, "editor: no Turn off");
  check((await p.locator("iframe.hm-pluginframe").count()) === 1, "editor: the frame is present");
  await p.goto(`${base}/admin/plugins/frameplug/settings`, { waitUntil: "load" });
  check(new URL(p.url()).pathname.endsWith("/plugins/frameplug/app"), "editor: /settings ends on /app", p.url());
  await p.goto(`${base}/admin/plugins/frameplug/app`, { waitUntil: "load" });
}

// 3. Bridge ready, no card title.
await p.waitForFunction(() => document.querySelector("iframe.hm-pluginframe")?.contentWindow, null, { timeout: 10000 });
for (let i = 0; i < 50 && !(await pframe()?.locator("#state").textContent().catch(() => ""))?.startsWith("bridge ready"); i++) await p.waitForTimeout(200);
check(((await pframe()?.locator("#state").textContent().catch(() => "")) || "").startsWith("bridge ready"), "the bridge is ready");
check((await p.locator("#plugin-page").count()) === 0, "no card title element (#plugin-page)");

// 4. Short page: the frame follows the content, not the CSS fallback (70vh = 630px).
await p.waitForTimeout(500);
const short = await frameHeight();
check(short >= 320 && short < 630, "short page: the frame height follows the content (>= 320, < 630)", String(short));
await shot("plugin-screen-short");

// 5. Tall page: grows past the viewport, no inner scrollbar, capped.
await pframe().locator("#grow").click();
let tall = 0;
for (let i = 0; i < 25 && tall <= 900; i++) {
  await p.waitForTimeout(200);
  tall = await frameHeight();
}
check(tall > 900, "after Add rows: the frame is taller than the viewport", String(tall));
const inner = await pframe().evaluate(() => document.documentElement.scrollHeight - document.documentElement.clientHeight);
check(inner <= 1, "no inner scrollbar in the frame", String(inner));
check(tall <= 20002, "the frame height is capped at 20000px", String(tall));
await shot("plugin-screen-tall");

// 6. The Settings tab has no frame (owner).
if (role === "owner") {
  await p.goto(`${base}/admin/plugins/frameplug/settings`, { waitUntil: "load" });
  check((await p.locator("[data-hm-pluginframe]").count()) === 0, "owner: the Settings tab has no frame");
  await shot("settings");
}

await b.close();
writeResult(`plugin-screen-${role}.json`, { role, base, results });
console.log(bad ? `\n${bad} problem(s)` : "\nok");
process.exit(bad ? 1 : 0);
