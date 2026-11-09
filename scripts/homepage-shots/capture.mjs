// Captures the homepage shots from a seeded local instance (see run.sh).
//   node capture.mjs [--out DIR] [--only slot,slot] [--shots shots.json]
// Env: BASE, CHROME, QA_EMAIL, QA_PASSWORD (see ../admin-qa/lib.mjs).
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { execSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import sharp from "sharp";

const here = path.dirname(fileURLToPath(import.meta.url));
process.env.OUT ||= fs.mkdtempSync(path.join(os.tmpdir(), "hm-shots-lib-")); // lib.mjs makes its results dir on import
const { launch, login, base } = await import("../admin-qa/lib.mjs");
const { frame, renderBar, W, INNER_H } = await import("./frame.mjs");

const arg = (n, d) => { const i = process.argv.indexOf(n); return i > 0 ? process.argv[i + 1] : d; };
const out = path.resolve(arg("--out", process.env.OUT_DIR || "scripts/homepage-shots/out"));
const only = arg("--only", "")?.split(",").filter(Boolean);
// A shots file is either a list of shots (shots.json, the homepage) or a set object
// { host, width, shots } (readme-shots.json): host replaces "hatamoto.dev" in the URL labels,
// width scales the framed image down, and an entry { "from": SLOT, ... } reuses that homepage
// shot with its own overrides.
const shotsPath = path.resolve(here, arg("--shots", "shots.json"));
const set = JSON.parse(fs.readFileSync(shotsPath, "utf8"));
const homepage = JSON.parse(fs.readFileSync(path.join(here, "shots.json"), "utf8"));
const shots = (Array.isArray(set) ? set : set.shots).map((s) => {
  if (!s.from) return s;
  const b = homepage.find((h) => h.slot === s.from);
  if (!b) throw new Error(`${shotsPath}: no homepage shot ${s.from}`);
  const { from, ...rest } = s;
  return { ...b, ...rest };
});
const host = Array.isArray(set) ? "" : set.host || "";
const outWidth = Array.isArray(set) ? 0 : set.width || 0;
// env expands $NAME from the environment (paths that only exist for one run).
const env = (v) => v.replace(/\$([A-Z_][A-Z0-9_]*)/g, (_, n) => process.env[n] ?? "");
fs.mkdirSync(out, { recursive: true });

// The admin ships Inter; the homepage uses Geist, so the shots alias the admin font tokens to it.
const fontCSS = `@font-face{font-family:"Geist";src:url(${base}/theme/assets/fonts/Geist-Variable.woff2);font-weight:100 900}
@font-face{font-family:"Geist Mono";src:url(${base}/theme/assets/fonts/GeistMono-Variable.woff2);font-weight:100 900}
:root{--font-ui:"Geist",sans-serif !important;--font-mono:"Geist Mono",monospace !important}`;
const FORBIDDEN = /qa\.test|\bQA [A-Z]|\bQA\b/;

async function resolveUrl(page, url) {
  const m = url.match(/\{(\w+)(?::([^}]*))?\}/);
  if (!m) return url;
  const [tok, kind, arg1] = m;
  const lists = {
    entity: [`/admin/entities?q=${encodeURIComponent(arg1 || "")}`, /\/admin\/entities\/([0-9a-f-]{36})/],
    type: ["/admin/settings/types", /\/admin\/settings\/types\/([0-9a-f-]{36})/],
    menu: ["/admin/theme/menus", /menus\/([0-9a-f-]{36})/],
    form: ["/admin/forms", /forms\/(?:edit|design)\/([0-9a-f-]{36})/],
  };
  const [list, re] = lists[kind];
  await page.goto(base + list);
  const links = await page.$$eval("a[href]", (as) => as.map((a) => ({ href: a.getAttribute("href"), text: a.textContent.trim() })));
  const href = links.find((l) => re.test(l.href) && (kind === "entity" || !arg1 || l.text.includes(arg1)))?.href;
  if (!href) throw new Error(`cannot resolve ${tok}`);
  return url.replace(tok, href.match(re)[1]);
}

async function runActions(page, actions = []) {
  for (const a of actions) {
    if (a.click) await page.click(a.click);
    else if (a.fill) await page.fill(a.fill[0], a.fill[1]);
    else if (a.press) await page.press(a.press[0], a.press[1]);
    else if (a.setInputFiles) await page.setInputFiles(a.setInputFiles[0], path.resolve(env(a.setInputFiles[1])));
    else if (a.waitFor) await page.waitForSelector(a.waitFor, { timeout: 30000 });
    else if (a.check) await page.check(a.check);
    else if (a.submit) await Promise.all([page.waitForNavigation({ waitUntil: "load" }), page.evaluate((s) => document.querySelector(s).requestSubmit(), a.submit)]);
    else if (a.wait) await page.waitForTimeout(a.wait);
    else if (a.blur) await page.evaluate(() => document.activeElement?.blur());
    else if (a.eval) await page.evaluate(a.eval);
    else throw new Error(`unknown action ${JSON.stringify(a)}`);
  }
}

// clipFor picks the CSS-pixel rectangle to shoot. A region is cropped tight (plus padding) and only
// grows taller to reach the frame's aspect; a region narrower than the aspect keeps its width and
// frame.mjs centres it on the page colour, so the sidebar never leaks in as a sliver.
async function clipFor(page, s) {
  const dims = await page.evaluate(() => ({ w: document.documentElement.scrollWidth, h: document.documentElement.scrollHeight }));
  const pageH = Math.max(dims.h, s.viewport[1]);
  let box = { x: 0, y: 0, width: s.viewport[0], height: s.viewport[1] };
  let bg = null;
  if (s.region) {
    const loc = page.locator(s.region).first();
    await loc.scrollIntoViewIfNeeded();
    const b = await loc.evaluate((el) => {
      const r = el.getBoundingClientRect();
      let bg = "", e = el;
      while (e && (!bg || /rgba\(0, 0, 0, 0\)|transparent/.test(bg))) { bg = getComputedStyle(e).backgroundColor; e = e.parentElement; }
      return { x: r.x + scrollX, y: r.y + scrollY, width: r.width, height: r.height, bg };
    });
    bg = b.bg;
    if (s.regionHeight) b.height = Math.min(b.height, s.regionHeight); // the top part of a tall card
    const pad = s.pad ?? 24;
    box = { x: b.x - pad, y: b.y - pad, width: b.width + 2 * pad, height: b.height + 2 * pad };
  }
  const aspect = W / INNER_H;
  let { width: cw, height: ch } = box;
  if (cw / ch > aspect) ch = cw / aspect;
  else if (!s.region) cw = ch * aspect;
  cw = Math.min(cw, dims.w); ch = Math.min(ch, pageH);
  const x = Math.max(0, Math.min(box.x + box.width / 2 - cw / 2, dims.w - cw));
  // A region that had to grow taller keeps its top edge, so the card above never peeks in.
  const y = Math.max(0, Math.min(s.region ? box.y : box.y + box.height / 2 - ch / 2, pageH - ch));
  return { clip: { x, y, width: cw, height: ch }, bg };
}

const browser = await launch();
const results = [];
for (const s of shots) {
  if (only.length && !only.includes(s.slot)) continue;
  const [vw, vh] = s.viewport;
  const theme = s.theme === "dark" ? "dark" : "light";
  const ctx = await browser.newContext({ viewport: { width: vw, height: vh }, deviceScaleFactor: s.dpr, bypassCSP: true, colorScheme: theme });
  await ctx.addCookies([{ name: "hm_theme", value: theme, url: base }]);
  const page = await ctx.newPage();
  try {
    await login(page);
    if (s.pre) execSync(s.pre, { cwd: here, stdio: "inherit" });
    if (s.html) await page.goto("file://" + path.resolve(env(s.html)), { waitUntil: "load" }); // a local page (mcp-demo.mjs)
    else await page.goto(base + (await resolveUrl(page, s.url)), { waitUntil: "load" });
    await page.addStyleTag({ content: fontCSS });
    await runActions(page, s.actions);
    await page.addStyleTag({ content: fontCSS }); // an action may have loaded a new document
    if (s.waitFor) await page.waitForSelector(s.waitFor);
    // The rolled-back banner (seeded for the updates shot) would sit on top of every other page.
    if (!s.keepBanner) await page.evaluate(() => document.querySelectorAll(".hm-callout").forEach((e) => /Update rolled back/.test(e.textContent) && e.remove()));
    // The install stats question is a first-run prompt, not part of any screen being shown.
    await page.evaluate(() => document.querySelectorAll("[data-hm-stats-banner]").forEach((e) => e.remove()));
    // Per-shot polish, applied to the page only: css restyles, hide removes whatever should not be framed.
    if (s.css) await page.addStyleTag({ content: s.css });
    if (s.hide) await page.evaluate((sel) => document.querySelectorAll(sel).forEach((e) => e.remove()), s.hide);
    await page.evaluate(() => document.fonts.ready);
    if (await page.$("canvas")) await page.waitForTimeout(1200);
    await page.waitForTimeout(s.settle ?? 300);
    const text = await page.evaluate(() => document.body.innerText + "\n" + [...document.querySelectorAll("input,textarea")].map((i) => i.value).join("\n"));
    const hit = text.match(FORBIDDEN);
    if (hit) throw new Error(`forbidden text on page: "${hit[0]}"`);
    const geist = await page.evaluate(() => document.fonts.check('14px "Geist"') && getComputedStyle(document.body).fontFamily.startsWith("Geist"));
    if (!geist) throw new Error("Geist is not applied");
    const { clip, bg: regionBg } = await clipFor(page, s);
    if (process.env.SHOTS_DEBUG) console.log("     clip", JSON.stringify(clip));
    const png = await page.screenshot({ clip, fullPage: true });
    // bg "corner": the letterbox takes the clip's top-left pixel, for regions whose ancestors' colour is not the one on screen.
    let bg = regionBg || (await page.evaluate(() => getComputedStyle(document.body).backgroundColor));
    if (s.bg === "corner") { const { data } = await sharp(png).raw().toBuffer({ resolveWithObject: true }); bg = { r: data[0], g: data[1], b: data[2] }; }
    const label = host ? s.urlLabel.replace(/^hatamoto\.dev/, host) : s.urlLabel;
    const bar = await renderBar(browser, label, theme);
    await frame(bar, png, path.join(out, s.file), bg, outWidth);
    results.push({ slot: s.slot, file: s.file, ok: true });
    console.log(`ok   ${s.file}`);
  } catch (e) {
    results.push({ slot: s.slot, file: s.file, ok: false, error: String(e.message || e) });
    console.error(`FAIL ${s.file}: ${e.message || e}`);
    await page.screenshot({ path: path.join(out, `_fail-${s.file}.png`) }).catch(() => {});
  }
  await ctx.close();
}
await browser.close();
if (results.some((r) => !r.ok)) process.exit(1);
