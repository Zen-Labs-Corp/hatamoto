// The inline SEO view of the entity list: type a title, Tab away, the row saves and the value survives a reload;
// filtering and paging keep view=seo; a second session that changed the same field shows the conflict state.
// Needs the seed (posts named "QA Post ...").
// node scripts/admin-qa/seo-bulk.mjs [chromium]   Exit 1 on any mismatch.
import { launch, newSession, base, writeResult } from "./lib.mjs";

const b = await launch("chromium");
const p = await newSession(b, { theme: "light", width: 1440 });
const other = await newSession(b, { theme: "light", width: 1440 });
const errors = [];
p.on("pageerror", (e) => errors.push(e.message));
let bad = 0;
const results = [];
async function check(name, fn) {
  try {
    const detail = await fn();
    results.push({ name, ok: true, detail });
    console.log(`ok   ${name}: ${detail ?? ""}`);
  } catch (e) {
    bad++;
    results.push({ name, ok: false, detail: String(e.message || e) });
    console.log(`FAIL ${name}: ${String(e.message || e).split("\n")[0]}`);
  }
}
const need = (cond, msg) => {
  if (!cond) throw new Error(msg);
};
const LIST = "/admin/entities?view=seo";
const state = (page) => page.locator("tbody tr").first().locator(".hm-savestate").getAttribute("data-state");
const titleBox = (page) => page.locator('tbody tr').first().locator('input[name="title"]');

await check("blur saves and the value survives a reload", async () => {
  const title = "QA seo title " + Date.now();
  await p.goto(base + LIST, { waitUntil: "load" });
  await titleBox(p).fill(title);
  await p.keyboard.press("Tab");
  await p.waitForFunction(() => document.querySelector("tbody tr .hm-savestate")?.dataset.state === "saved", null, { timeout: 15000 });
  need(/Saved/.test((await p.locator("tbody tr").first().locator(".hm-savestate__text").textContent()) ?? ""), "no Saved text");
  await p.reload({ waitUntil: "load" });
  need((await titleBox(p).inputValue()) === title, "title did not persist");
  return title;
});

await check("filter and paging keep view=seo", async () => {
  await p.goto(base + LIST + "&status=published", { waitUntil: "load" });
  const hrefs = await p.$$eval(".hm-table__sort, .hm-pagination a", (as) => as.map((a) => a.getAttribute("href")));
  need(hrefs.length > 0, "no sort or page links");
  need(hrefs.every((h) => h.includes("view=seo")), "a link lost view=seo: " + hrefs.find((h) => !h.includes("view=seo")));
  return `${hrefs.length} links`;
});

await check("a second session's change shows the conflict state", async () => {
  await p.goto(base + LIST, { waitUntil: "load" });
  await other.goto(base + LIST, { waitUntil: "load" });
  await titleBox(other).fill("QA other " + Date.now());
  await other.keyboard.press("Tab");
  await other.waitForFunction(() => document.querySelector("tbody tr .hm-savestate")?.dataset.state === "saved", null, { timeout: 15000 });
  await titleBox(p).fill("QA mine " + Date.now());
  await p.keyboard.press("Tab");
  await p.waitForFunction(() => document.querySelector("tbody tr .hm-savestate")?.dataset.state === "conflict", null, { timeout: 15000 });
  need((await titleBox(p).inputValue()).startsWith("QA other"), "the current value is not shown");
  return "conflict";
});

await check("Default tab leaves the remembered SEO view", async () => {
  await p.goto(base + LIST, { waitUntil: "load" });
  await p.getByRole("link", { name: "Default", exact: true }).click();
  await p.waitForURL(/view=default/);
  await p.waitForLoadState("load");
  need((await p.locator(".hm-cell-edit").count()) === 0, "the SEO columns are still shown after Default");
  await p.goto(base + "/admin/entities", { waitUntil: "load" });
  need(!p.url().includes("view=seo"), "the plain list bounced back to the SEO view");
  return "default stays";
});

await check("a failed save offers Retry and a change made while saving is saved after it", async () => {
  await p.goto(base + LIST, { waitUntil: "load" });
  const row = p.locator("tbody tr").first();
  const save = row.locator("[data-hm-seobulk-save]");
  need(await save.isHidden(), "Save is visible before any failure");
  let fail = true;
  await p.route("**/seo", async (route) => (fail ? route.abort() : route.continue()));
  await titleBox(p).fill("QA retry " + Date.now());
  await p.keyboard.press("Tab");
  await p.waitForFunction(() => document.querySelector("tbody tr .hm-savestate")?.dataset.state === "error", null, { timeout: 15000 });
  need(await save.isVisible(), "no Retry after a failed save");
  need(/Retry/.test((await save.textContent()) ?? ""), "the button is not labelled Retry");
  fail = false;
  await save.focus();
  await p.keyboard.press("Enter");
  await p.waitForFunction(() => document.querySelector("tbody tr .hm-savestate")?.dataset.state === "saved", null, { timeout: 15000 });
  need(await save.isHidden(), "Retry stays after a good save");
  // a second edit arriving while the first save is held back
  let release;
  const held = new Promise((r) => (release = r));
  await p.unroute("**/seo");
  await p.route("**/seo", async (route) => {
    await held;
    await route.continue();
  });
  const t1 = "QA one " + Date.now();
  const t2 = "QA two " + Date.now();
  await titleBox(p).fill(t1);
  await p.keyboard.press("Tab");
  await row.locator('textarea[name="description"]').fill(t2);
  await p.keyboard.press("Tab");
  release();
  await p.waitForFunction(() => document.querySelector("tbody tr .hm-savestate")?.dataset.state === "saved", null, { timeout: 15000 });
  await p.unroute("**/seo");
  await p.reload({ waitUntil: "load" });
  need((await titleBox(p).inputValue()) === t1, "first edit lost");
  need((await row.locator('textarea[name="description"]').inputValue()) === t2, "the edit made while saving was dropped");
  return "retry + queued";
});

await b.close();
if (errors.length) console.log("page errors:", errors.join(" | "));
writeResult("seo-bulk.json", { results });
console.log(`\n${bad} failing checks`);
process.exit(bad ? 1 : 0);
