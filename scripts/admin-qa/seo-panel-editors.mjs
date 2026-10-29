// One SEO panel everywhere (A2) and the builder drawer (B1). The same four tabs (Search, Social, Schema, Advanced) and the
// hm-seopanel root show on the post editor, the entity form of a page, a term's edit page and the builder's SEO drawer;
// the term page runs no body checks (no "Internal links" row); Escape in the drawer closes it and returns focus to the
// SEO button; a title typed in the drawer and saved with the page survives a reload.
// Needs the seed (a post named "QA Post 1", a page named QA_PAGE_NAME, default "About").
// node scripts/admin-qa/seo-panel-editors.mjs [chromium]   Exit 1 on any mismatch.
import { launch, newSession, resolvePath, base, writeResult } from "./lib.mjs";

const b = await launch("chromium");
const p = await newSession(b, { theme: "light", width: 1440 });
const errors = [];
p.on("pageerror", (e) => errors.push(e.message));
p.on("dialog", (d) => d.accept().catch(() => {}));
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
const goto = async (path) => {
  const url = await resolvePath(p, path);
  need(url, `no data for ${path} (run the seed)`);
  const res = await p.goto(base + url, { waitUntil: "load" });
  need(res?.ok(), `${url} answered ${res?.status()}`);
};
const labels = () => p.$$eval('[data-hm-seo-tabs] [role="tab"]', (ts) => ts.map((t) => t.textContent.trim()));
const panelCount = () => p.locator("[data-hm-seo]").count();
const WANT = ["Search", "Social", "Schema", "Advanced"];
const same = (got, where) => need(JSON.stringify(got) === JSON.stringify(WANT), `${where}: tabs ${JSON.stringify(got)}`);

await check("post editor", async () => {
  await goto("/admin/entities/{post}/edit");
  await p.click("#be-tab-seo");
  need((await panelCount()) === 1, "no hm-seopanel root");
  same(await labels(), "post editor");
  return "four tabs";
});

await check("entity form of a page", async () => {
  await goto("/admin/entities/{page}");
  need((await panelCount()) === 1, "no hm-seopanel root");
  same(await labels(), "entity form");
  return "four tabs";
});

await check("term edit page", async () => {
  await p.goto(base + "/admin/settings/taxonomies", { waitUntil: "load" });
  let href = await p.locator('a[href*="/settings/taxonomies/terms/"]', { hasText: "QA SEO term" }).first().getAttribute("href").catch(() => null);
  if (!href) {
    await p.goto(base + "/admin/settings/taxonomies/new", { waitUntil: "load" });
    await p.fill("#name", "QA SEO term");
    if (!(await p.locator("#kind option").count())) {
      await p.click("summary:has-text('More options')");
      await p.fill("#new_kind", "qa-topic");
    }
    await Promise.all([p.waitForURL((u) => !u.pathname.endsWith("/taxonomies/new")), p.click('button[type="submit"]:has-text("Add term")')]);
    await p.goto(base + "/admin/settings/taxonomies", { waitUntil: "load" });
    href = await p.locator('a[href*="/settings/taxonomies/terms/"]', { hasText: "QA SEO term" }).first().getAttribute("href");
  }
  await p.goto(base + href, { waitUntil: "load" });
  need((await panelCount()) === 1, "no hm-seopanel root");
  same(await labels(), "term page");
  need((await p.locator("#hm-termform [data-hm-seo]").count()) === 1, "the panel is not inside the term form");
  const body = await p.locator("[data-hm-seo]").innerText();
  need(!/Internal links/i.test(body), "the term panel shows body checks");
  return "four tabs, no body checks";
});

await check("builder drawer", async () => {
  await goto("/admin/entities/{page}/edit");
  await p.waitForSelector("[data-hm-bs-seo]:not([hidden])");
  const btn = p.locator("[data-hm-bs-seo]");
  await btn.click();
  await p.waitForSelector("#hm-bs-seo-drawer:not([hidden]) [data-hm-seo-tabs] [role=tab]");
  same(await labels(), "drawer");
  need((await p.locator("#hm-bs-seo-drawer [data-hm-seo]").count()) === 1, "no hm-seopanel root in the drawer");
  need((await btn.getAttribute("aria-expanded")) === "true", "aria-expanded is not true while open");
  await p.keyboard.press("Escape");
  need(await p.locator("#hm-bs-seo-drawer").isHidden(), "Escape did not close the drawer");
  need(await btn.evaluate((el) => el === document.activeElement), "focus did not return to the SEO button");
  return "four tabs, Escape closes, focus returns";
});

await check("drawer edit saves with the page (B1)", async () => {
  const title = "QA drawer title " + Date.now();
  await goto("/admin/entities/{page}/edit");
  await p.waitForSelector("[data-hm-bs-seo]:not([hidden])");
  await p.click("[data-hm-bs-seo]");
  await p.waitForSelector("#hm-bs-seo-drawer:not([hidden]) #seo_title");
  await p.fill("#seo_title", title);
  const save = p.locator("[data-hm-bs-save]:visible").first();
  await save.click();
  await p.waitForFunction(() => /Saved/i.test(document.querySelector("[data-hm-bs-state]")?.textContent ?? ""), null, { timeout: 15000 });
  await p.reload({ waitUntil: "load" });
  await p.click("[data-hm-bs-seo]");
  await p.waitForSelector("#hm-bs-seo-drawer:not([hidden]) #seo_title");
  need((await p.inputValue("#seo_title")) === title, "the saved title did not survive the reload");
  return "title persisted";
});

await b.close();
if (errors.length) console.log("page errors:", errors.join(" | "));
writeResult("seo-panel-editors.json", { results });
console.log(`\n${bad} failing checks`);
process.exit(bad ? 1 : 0);
