// Navigation-mode, speculation and scroll checks (no hx-boost):
//  ISC-19 a sidebar click is a real document navigation (navigation entry
//         type "navigate", no fetch/xhr for the page document)
//  ISC-21 the shell carries <script type="speculationrules">
//  ISC-22 scroll position is restored after row click + history.back()
//  ISC-23 the entities list loads no editor modules
// node scripts/admin-qa/nav.mjs      Exit 1 when a check fails.
import { launch, newSession, writeResult, base, role } from "./lib.mjs";

const legacy = process.env.QA_LEGACY === "1";
const b = await launch("chromium", ["--disable-features=Prerender2"]);
const p = await newSession(b, { theme: "light", width: 1440 });
const checks = {};
let bad = 0;
async function check(name, fn) {
  try { checks[name] = { ok: true, detail: await fn() }; }
  catch (e) { checks[name] = { ok: false, detail: String(e.message || e).split("\n")[0] }; bad++; }
  console.log(`${checks[name].ok ? "ok  " : "FAIL"} ${name}: ${checks[name].detail}`);
}

await check("ISC-19 sidebar click is a document navigation, not fetch/xhr", async () => {
  await p.goto(base + "/admin/entities");
  await p.evaluate(() => { window.__marker = 1; });
  const links = await p.$$eval(".hm-sidebar a.hm-nav-link", (as) => as.map((a) => a.getAttribute("href")));
  const target = links.find((h) => h !== "/admin/entities" && !h.startsWith("#"));
  if (!target) throw new Error("no second sidebar link");
  const xhr = [];
  const onReq = (r) => { if (["fetch", "xhr"].includes(r.resourceType()) && r.url().startsWith(base + target.split("?")[0])) xhr.push(r.url()); };
  p.on("request", onReq);
  await Promise.all([p.waitForLoadState("load"), p.click(`.hm-sidebar a[href="${target}"]`)]);
  p.off("request", onReq);
  const t = await p.evaluate(() => [performance.getEntriesByType("navigation")[0].type, window.__marker]);
  if (t[0] !== "navigate" || t[1] === 1 || xhr.length) throw new Error(JSON.stringify({ type: t[0], windowSurvived: t[1] === 1, xhr }));
  return `${target}: type=navigate, window replaced, ${xhr.length} fetch/xhr for the document`;
});
await check("ISC-21 speculationrules script present on the shell", async () => {
  await p.goto(base + "/admin/entities");
  const n = await p.locator('script[type="speculationrules"]').count();
  if (!n) throw new Error("no <script type=speculationrules>");
  const rules = JSON.parse(await p.locator('script[type="speculationrules"]').first().textContent());
  return `${n} script, rule kinds: ${Object.keys(rules).join(",")}`;
});
await check("ISC-22 scroll restored after row click and history.back()", async () => {
  await p.goto(base + "/admin/entities");
  const h = await p.evaluate(() => document.documentElement.scrollHeight - innerHeight);
  if (h < 200) throw new Error(`list not scrollable (${h}px of overflow); seed more entities`);
  await p.evaluate(() => (document.scrollingElement || document.documentElement).scrollTo(0, 400));
  // click() scrolls its target into view first, so the position to restore is the
  // one at the moment the page is left, not the one set above
  await p.evaluate(() => addEventListener("pagehide", () => { sessionStorage.hmLeftAt = String(scrollY); }));
  const row = p.locator('main a[href^="/admin/entities/"]:visible').last();
  // a link visible at this scroll offset, in the table body
  await Promise.all([p.waitForLoadState("load"), row.click()]);
  await p.goBack({ waitUntil: "load" });
  await p.waitForTimeout(300);
  const before = Number(await p.evaluate(() => sessionStorage.hmLeftAt));
  const after = await p.evaluate(() => scrollY);
  if (Math.abs(after - before) > 8) throw new Error(`scrollY ${before} -> ${after}`);
  return `scrollY ${before} -> ${after}`;
});
await check("ISC-23 entities list loads no editor modules", async () => {
  const q = await newSession(b, { theme: "light", width: 1440 });
  const js = [];
  q.on("request", (r) => { if (/\.js(\?|$)/.test(r.url())) js.push(new URL(r.url()).pathname); });
  await q.goto(base + "/admin/entities", { waitUntil: "networkidle" });
  await q.context().close();
  const editors = js.filter((u) => /pageeditor|blogeditor|\/editor\/|menutree|sortable/.test(u));
  if (editors.length) throw new Error("editor modules loaded: " + editors.join(", "));
  return `${js.length} scripts, none editor`;
});
await b.close();
writeResult("nav.json", { role, base, legacy, checks });
console.log(`\n${bad} failing`);
process.exit(bad ? 1 : 0);
