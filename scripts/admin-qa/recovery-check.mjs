// Server-kept recovery copies (editor and builder), end to end in a real browser.
//  1. A copy planted in browser storage is never restored and is deleted on load.
//  2. Typing in a published post, then opening a second fresh session (own storage, own cookies)
//     offers "kept for you"; Restore applies it; Discard removes it.
//  3. The same for the page builder.
// Needs a published post ("QA Post 1") and a published page (QA_PAGE_NAME, default "About").
// Screenshots go to SHOTS (default shots-out next to this script).
// bash scripts/admin-qa/run-local.sh scripts/admin-qa/recovery-check.mjs   Exit 1 on any problem.
import fs from "node:fs";
import path from "node:path";
import { launch, newSession, resolvePath, base, here } from "./lib.mjs";

const shots = process.env.SHOTS || path.join(here, "shots-out");
fs.mkdirSync(shots, { recursive: true });
const shot = (page, name) => page.screenshot({ path: path.join(shots, name) });

const b = await launch("chromium");
let bad = 0;
async function check(name, fn) {
  try {
    await fn();
    console.log(`ok   ${name}`);
  } catch (e) {
    bad++;
    console.log(`FAIL ${name}: ${e.message}`);
  }
}
const ok = (cond, msg) => {
  if (!cond) throw new Error(msg);
};
const open = async (p, url) => {
  p.on("dialog", (d) => d.accept().catch(() => {}));
  await p.goto(base + url);
};

const first = await newSession(b);
const postPath = await resolvePath(first, "/admin/entities/{post}/edit");
const pagePath = await resolvePath(first, "/admin/entities/{page}/edit");
ok(postPath && pagePath, "could not resolve the seeded post and page");
const builderPath = `${pagePath}?qa=1`;
const TITLE = "Recovered by the server";

const banner = (p) => p.locator("[data-hm-be-recover]");
const bannerText = (p) => p.locator("[data-hm-be-recover-text]").textContent();
const writeSeen = (p, part) => p.waitForResponse((r) => r.url().includes(part) && !r.url().endsWith("/discard") && r.request().method() === "POST", { timeout: 10000 });

// ---- 1. planted browser copies are never restored -------------------------------------------
await check("blog editor: a planted browser copy is not offered and is deleted", async () => {
  await open(first, postPath);
  const { user, id } = await first.$eval("#hm-be", (f) => ({ user: f.dataset.hmUser, id: f.dataset.hmEntity }));
  const key = `hm:blog:${user}:${id}`;
  const planted = { content: "<p>PLANTED COPY</p>", version: 1, name: "Planted title", excerpt: "", at: Date.now() };
  await first.evaluate(([k, v]) => localStorage.setItem(k, v), [key, JSON.stringify(planted)]);
  await first.reload();
  await first.waitForSelector("#hm-be");
  await first.waitForTimeout(500);
  ok(await banner(first).isHidden(), "the recovery banner is showing");
  ok((await first.evaluate((k) => localStorage.getItem(k), key)) === null, "the planted key is still in storage");
  ok(!(await first.inputValue("#hm-be [name=content]")).includes("PLANTED COPY"), "the planted text reached the editor");
  ok((await first.inputValue("#hm-be [name=name]")) !== "Planted title", "the planted title reached the editor");
  await shot(first, "planted-copy-ignored.png");
});

await check("builder: a planted browser copy is not offered and is deleted", async () => {
  await open(first, builderPath);
  await first.waitForFunction(() => !!window.__hmBuilder, null, { timeout: 10000 });
  const boot = await first.evaluate(() => JSON.parse(document.getElementById("hm-builder-boot").textContent));
  const key = `hm:blog:${boot.user}:${boot.entity}`;
  await first.evaluate(([k, v]) => localStorage.setItem(k, v), [key, JSON.stringify({ content: JSON.stringify({ v: 2, nodes: [] }), version: boot.version, name: "Planted page", at: Date.now() })]);
  await first.reload();
  await first.waitForFunction(() => !!window.__hmBuilder, null, { timeout: 10000 });
  await first.waitForTimeout(800);
  ok((await first.evaluate((k) => localStorage.getItem(k), key)) === null, "the planted key is still in storage");
  ok((await first.locator("text=kept for you").count()) === 0, "a recovery banner is showing");
  ok((await first.evaluate(() => window.__hmBuilder.doc().nodes.length)) > 0, "the planted empty doc was restored");
  ok((await first.inputValue("[data-hm-bs-title]")) !== "Planted page", "the planted name reached the builder");
});

// ---- 2. blog editor: kept on the server, offered in a second session -----------------------------
await check("blog editor: typing is kept on the server and offered in a second session", async () => {
  await open(first, postPath);
  await first.waitForSelector("#hm-be");
  const original = await first.inputValue("#hm-be [name=name]");
  const seen = writeSeen(first, "/edit/recovery");
  await first.fill("#hm-be [name=name]", TITLE);
  const res = await seen;
  ok(res.status() === 204, `recovery write answered ${res.status()}`);
  ok((await first.evaluate(() => Object.keys(localStorage).filter((k) => k.startsWith("hm:blog:")).length)) === 0, "a copy was written to browser storage");

  const second = await newSession(b);
  await open(second, postPath);
  await second.waitForSelector("#hm-be");
  await banner(second).waitFor({ state: "visible", timeout: 5000 });
  const text = await bannerText(second);
  ok(/^Unsaved changes from .+ were kept for you\.$/.test(text.trim()), `banner text was "${text}"`);
  await shot(second, "editor-recovery-banner.png");
  await second.click("[data-hm-be-recover-use]");
  ok((await second.inputValue("#hm-be [name=name]")) === TITLE, "Restore did not apply the title");
  ok(await banner(second).isHidden(), "the banner stayed after Restore");
  await shot(second, "editor-restored.png");

  // Discard: reopen, the offer is there again (nothing was saved), discard it, reopen: gone
  await second.reload();
  await banner(second).waitFor({ state: "visible", timeout: 5000 });
  await second.click("[data-hm-be-recover-drop]");
  ok(await banner(second).isHidden(), "the banner stayed after Discard");
  await second.reload();
  await second.waitForSelector("#hm-be");
  await second.waitForTimeout(500);
  ok(await banner(second).isHidden(), "the copy came back after Discard");
  ok((await second.inputValue("#hm-be [name=name]")) === original, "the post title changed");
  await second.context().close();
});

// ---- 3. builder -----------------------------------------------------------------------------
await check("builder: edits are kept on the server and offered in a second session", async () => {
  await open(first, builderPath);
  await first.waitForFunction(() => !!window.__hmBuilder, null, { timeout: 10000 });
  const status = await first.evaluate(() => JSON.parse(document.getElementById("hm-builder-boot").textContent).status);
  ok(status === "published", `the page is ${status}: a draft keeps its autosave instead of a recovery copy`);
  const original = await first.inputValue("[data-hm-bs-title]");
  const docBefore = await first.evaluate(() => JSON.stringify(window.__hmBuilder.doc()));
  const seen = writeSeen(first, "/builder/recovery");
  await first.fill("[data-hm-bs-title]", "Builder recovered");
  const res = await seen;
  ok(res.status() === 204, `recovery write answered ${res.status()}`);
  ok((await first.evaluate(() => Object.keys(localStorage).filter((k) => k.startsWith("hm:blog:")).length)) === 0, "a copy was written to browser storage");
  await first.waitForFunction(() => /kept for you/.test(document.body.innerText), null, { timeout: 5000 });

  const second = await newSession(b);
  await open(second, builderPath);
  await second.waitForFunction(() => !!window.__hmBuilder, null, { timeout: 10000 });
  const flash = second.locator("text=Unsaved changes from your last visit were kept for you.");
  await flash.waitFor({ state: "visible", timeout: 5000 });
  await shot(second, "builder-recovery-banner.png");
  await second.click("button:has-text('Restore my unsaved changes')");
  ok((await second.inputValue("[data-hm-bs-title]")) === "Builder recovered", "Restore did not apply the name");
  ok((await second.evaluate(() => JSON.stringify(window.__hmBuilder.doc()))) === docBefore, "Restore changed the doc");
  ok((await flash.count()) === 0, "the banner stayed after Restore");
  await shot(second, "builder-restored.png");

  await second.reload();
  await second.waitForFunction(() => !!window.__hmBuilder, null, { timeout: 10000 });
  await flash.waitFor({ state: "visible", timeout: 5000 });
  await second.click("button:has-text('Discard them')");
  await second.reload();
  await second.waitForFunction(() => !!window.__hmBuilder, null, { timeout: 10000 });
  await second.waitForTimeout(800);
  ok((await flash.count()) === 0, "the copy came back after Discard");
  ok((await second.inputValue("[data-hm-bs-title]")) === original, "the page name changed");
  await second.context().close();
});

await b.close();
if (bad) {
  console.log(`${bad} check(s) failed`);
  process.exit(1);
}
console.log("recovery-check: all ok");
