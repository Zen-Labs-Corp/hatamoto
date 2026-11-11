// Plugin camera: the frameplug fixture asks ui.allow camera. Ungranted, its page has no capture dialog,
// the admin page keeps camera=() and hatamoto.camera.capture() is refused. After the owner ticks the
// permission the page sends camera=(self); the plugin's call opens core's dialog, Capture returns one JPEG
// data URL (longest side <= 1280px, <= ~1 MB) and the camera is stopped, Cancel returns "cancelled".
// Chrome runs with a fake camera and auto-accepted prompt.
// Run: SHOTS=<dir> bash scripts/admin-qa/run-local.sh camera.mjs   (SHOTS: where screenshots go)   Exit 1 on any problem.
import fs from "node:fs";
import path from "node:path";
import { launch, newSession, base } from "./lib.mjs";

const SHOTS = process.env.SHOTS || "";
if (SHOTS) fs.mkdirSync(SHOTS, { recursive: true });
const shot = (page, name) => (SHOTS ? page.screenshot({ path: path.join(SHOTS, name) }) : null);

let bad = 0;
const check = (ok, msg, detail = "") => {
  console.log(`${ok ? "PASS" : "FAIL"}  ${msg}${!ok && detail ? "  " + detail : ""}`);
  if (!ok) bad++;
};

const b = await launch("chromium", ["--use-fake-device-for-media-stream", "--use-fake-ui-for-media-stream"]);
const p = await newSession(b, { width: 1440 });
const pageUrl = `${base}/admin/plugins/frameplug/settings`;
const pframe = () => p.frames().find((f) => f.url().includes("/plugin-ui/frameplug/index.html"));

// The plugin page, loaded; returns the main response.
async function open() {
  const res = await p.goto(pageUrl, { waitUntil: "load" });
  await p.waitForFunction(() => document.querySelector("iframe.hm-pluginframe"), null, { timeout: 10000 });
  await p.waitForFunction(() => document.querySelector("iframe.hm-pluginframe")?.contentWindow, null, { timeout: 10000 });
  for (let i = 0; i < 50 && !(await pframe()?.locator("#state").textContent().catch(() => ""))?.startsWith("bridge ready"); i++) await p.waitForTimeout(200);
  return res;
}
// Press the plugin's button and read what it wrote.
const out = async () => JSON.parse((await pframe().locator("#out").textContent()) || "{}");
const waitOut = async (before = "") => {
  await pframe().waitForFunction((x) => document.getElementById("out").textContent !== x, before, { timeout: 15000 });
  return out();
};

// ---- (1) not granted ----
let res = await open();
check(!/camera=\(self\)/.test(res.headers()["permissions-policy"] || ""), "ungranted: the admin page does not allow the camera", res.headers()["permissions-policy"]);
check((await p.locator("#plugin-camera").count()) === 0, "ungranted: no capture dialog on the page");
check((await p.locator("[data-hm-pluginframe][data-camera]").count()) === 0, "ungranted: the frame host carries no camera flag");
await pframe().locator("#snap").click(); // a real click in the frame: the gesture the op needs
let r = await waitOut();
check(!!r.refused && !r.ok, "ungranted: hatamoto.camera.capture() is refused", JSON.stringify(r));
check(await pframe().evaluate(() => hatamoto.camera.available === false), "ungranted: hatamoto.camera.available is false");
check((await p.locator("dialog[open]").count()) === 0, "ungranted: no dialog opened");

// ---- (2) the owner ticks the permission ----
await p.goto(`${base}/admin/plugins/frameplug/permissions`, { waitUntil: "load" });
const box = p.locator('input[name="permission"][value="ui.allow:camera"]');
check((await box.count()) === 1, "the Permissions tab offers the camera");
check(!(await box.isChecked()), "the camera box starts unticked");
await box.check();
await Promise.all([p.waitForURL((u) => u.pathname.endsWith("/plugins/frameplug/permissions")), p.click('form[action$="/plugins/frameplug/grant"] button[type=submit]')]);
check(await p.locator('input[name="permission"][value="ui.allow:camera"]').isChecked(), "the camera grant is saved");

res = await open();
check(/camera=\(self\)/.test(res.headers()["permissions-policy"] || ""), "granted: the admin page sends camera=(self)", res.headers()["permissions-policy"]);
check((await p.locator("[data-hm-pluginframe][data-camera='1']").count()) === 1 && (await p.locator("#plugin-camera").count()) === 1, "granted: the frame host is flagged and the dialog is on the page");
const sandbox = await p.locator("iframe.hm-pluginframe").getAttribute("sandbox");
check(sandbox === "allow-scripts" && (await p.locator("iframe.hm-pluginframe").getAttribute("allow")) === null, "granted: the frame stays sandbox=allow-scripts with no allow attribute", String(sandbox));

check(await pframe().evaluate(() => hatamoto.camera.available === true), "granted: hatamoto.camera.available is true");
// A call from a timer, long after any click (the evaluate itself counts as a gesture, so wait it out), is refused (a script cannot start the camera).
const noClick = await pframe().evaluate(() => new Promise((r) => setTimeout(r, 6000)).then(() => hatamoto.camera.capture()).then(() => "resolved", (e) => String(e.message)));
check(/click/.test(noClick) && (await p.locator("dialog[open]").count()) === 0, "granted: no click, no dialog", noClick);

// Capture: the dialog opens with a live preview, Capture returns one JPEG.
await pframe().locator("#snap").click();
await p.waitForSelector("dialog#plugin-camera[open]", { timeout: 10000 });
await p.waitForFunction(() => document.querySelector("#plugin-camera video")?.videoWidth > 0, null, { timeout: 10000 });
await shot(p, "camera-dialog.png");
await p.click("#plugin-camera [data-hm-camera-capture]");
r = await waitOut();
check(r.ok === true && r.image === "data:image/jpeg;base64,", "capture: the plugin got a JPEG data URL", JSON.stringify(r));
check(r.width > 0 && r.height > 0 && Math.max(r.width, r.height) <= 1280, "capture: the longest side is at most 1280px", `${r.width}x${r.height}`);
check(r.length > 0 && r.length * 0.75 <= 1000000 + 4, "capture: at most about 1 MB", String(r.length));
check((await p.locator("dialog#plugin-camera[open]").count()) === 0, "capture: the dialog is closed");
check(await p.evaluate(() => document.querySelector("#plugin-camera video").srcObject === null), "capture: the camera stream was released");

// ---- (3) Cancel ----
const before = await pframe().locator("#out").textContent();
await pframe().locator("#snap").click();
await p.waitForSelector("dialog#plugin-camera[open]", { timeout: 10000 });
await p.waitForFunction(() => document.querySelector("#plugin-camera video")?.videoWidth > 0, null, { timeout: 10000 });
await p.locator("#plugin-camera").getByRole("button", { name: "Cancel" }).click();
r = await waitOut(before);
check(r.ok === false && r.error === "cancelled", "cancel: the plugin got cancelled", JSON.stringify(r));
check(await p.evaluate(() => document.querySelector("#plugin-camera video").srcObject === null), "cancel: the camera stream was released");

// A page that asks again right after a cancel gets no dialog (cooldown); the reply is the same
// "cancelled" text as before, so only the missing dialog is observable here.
await pframe().locator("#snap").click();
await p.waitForTimeout(1500);
check((await p.locator("dialog#plugin-camera[open]").count()) === 0, "cooldown: an immediate retry opens no dialog");

await b.close();
console.log(bad ? `\n${bad} problem(s)` : "\nok");
process.exit(bad ? 1 : 0);
