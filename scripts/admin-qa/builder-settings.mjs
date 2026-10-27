// Element settings (E6): anchors, link target/rel, image figure/link/lightbox with media width/height, linked container.
// A generated section is pasted onto QA_PAGE_NAME (default "About") through the clipboard and the page is saved with
// Update; then, on the public page (signed out, real CSP): an anchor link scrolls to its target (scrollY and :target),
// target=_blank links carry noopener noreferrer (and rel only the chosen tokens otherwise), a linked container is one
// link named by its aria-label or its text, CLS is exactly 0 for media images under a throttled cold-cache load (the
// same page with width/height stripped must shift, so the measurement can fail; an image still loading at first layout
// is required, else the run is invalid), the lightbox opens by keyboard, traps Tab, ArrowRight shows the next image, Esc
// closes and focus returns to the link, axe on the open dialog. In the builder: the inspector Link field lists pages
// and #anchors, "Open in new tab" and rel boxes write the props, the Advanced tab's Anchor lowercases, refuses a
// duplicate, a reserved id is refused by the save (422, message shown), and the Advanced tab fits at phone width.
// The last check deletes the pasted section, saves the page as found and compares documents.
// Needs the seed (seed.sh adds a 1200x675 media image). node scripts/admin-qa/builder-settings.mjs [chromium]
import fs from "node:fs";
import { launch, newSession, base } from "./lib.mjs";

const axe = fs.readFileSync(new URL("./vendor/axe.min.js", import.meta.url), "utf8");
const tags = ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"];
const NAME = process.env.QA_PAGE_NAME || "About";
const b = await launch("chromium");
let bad = 0;
const p = await newSession(b, { theme: "light", width: 1440, bypassCSP: true });
await p.context().grantPermissions(["clipboard-read", "clipboard-write"], { origin: base });
const errors = [];
p.on("pageerror", (e) => errors.push(e.message));
p.on("dialog", (d) => d.accept().catch(() => {}));

const TREE = '[data-hm-bs-tree] [role="treeitem"]';
const MENU = '.hm-menu[aria-label="Element actions"]';
const settle = (ms = 150) => p.waitForTimeout(ms);
const doc = () => p.evaluate(() => window.__hmBuilder.doc());
const flat = (ns) => ns.flatMap((n) => [n, ...flat(n.c ?? [])]);
// Pasted nodes get fresh ids: each carries its name as navlabel, and ID maps name -> id once the paste landed.
const ID = {};
const byId = async (name) => flat((await doc()).nodes).find((n) => n.p?.navlabel === name);
const cn = (name) => `.hm-n-${ID[name]}`;
const sig = async () => JSON.stringify((await doc()).nodes);
const row = (name) => p.locator(`${TREE}[data-id="${ID[name]}"]`);
const state = () => p.evaluate(() => document.querySelector("[data-hm-bs-state]")?.textContent ?? "");
async function check(name, fn) {
  if (process.env.QA_ONLY && !/^setup/.test(name) && !new RegExp(process.env.QA_ONLY, "i").test(name)) return;
  try { console.log(`ok   ${name}: ${await fn()}`); }
  catch (e) {
    bad++; console.log(`FAIL ${name}: ${String(e.message || e).split("\n").slice(0, process.env.QA_VERBOSE ? 30 : 1).join(" | ")}`);
    for (let i = 0; i < 2; i++) { await p.keyboard.press("Escape").catch(() => {}); await settle(300); }
  }
}
const noErrors = (from) => { if (errors.length > from) throw new Error("page error: " + errors[from]); };
const need = (cond, msg) => { if (!cond) throw new Error(msg); };

// The page is published, so edits stay in the browser until Update; this is the explicit save.
async function update() {
  await p.evaluate(() => document.activeElement?.blur?.());
  const resp = p.waitForResponse((r) => /\/builder\/(save|autosave)$/.test(new URL(r.url()).pathname) && r.request().method() === "POST", { timeout: 15000 });
  await p.locator('[data-hm-bs-save="save"]').first().click();
  const r = await resp;
  if (r.status() !== 200) throw new Error("save answered " + r.status() + " " + (await r.text()).slice(0, 200));
  await settle(600);
}
let pageHref = "";
async function open() {
  if (!pageHref) {
    await p.goto(`${base}/admin/entities?q=${encodeURIComponent(NAME)}`);
    const hrefs = await p.$$eval('a[href^="/admin/entities/"]', (as, t) => as.filter((a) => a.textContent.includes(t)).map((a) => a.getAttribute("href")), NAME);
    const h = hrefs.find((x) => /entities\/[0-9a-f-]{36}/.test(x));
    if (!h) throw new Error(`no page named ${NAME}`);
    pageHref = h + (h.includes("?") ? "&" : "?") + "qa=1";
  }
  await p.goto(base + pageHref, { waitUntil: "load" });
  await p.waitForSelector(TREE, { timeout: 10000 });
  await p.waitForFunction(() => !!window.__hmBuilder, null, { timeout: 5000 });
  await settle(600);
}
const tab = (name) => p.getByRole("tab", { name, exact: true });
async function select(id, tabName = "Content") {
  await row(id).click();
  await settle(500);
  await tab(tabName).click();
  await settle(300);
}
const panel = () => p.locator(".hm-bs-panel");

// The public path of the page, and the media image the seed added (1200x675).
let publicPath = "", wide = "";
async function findWide() {
  // From inside the admin page: a bare request has no fetch metadata or token and gets the 404.
  const html = await p.evaluate((u) => fetch(u).then((r) => r.text()), `${base}/admin/media/picker`);
  for (const m of html.matchAll(/data-hm-pick="([^"]*)"/g)) {
    const png = Buffer.from(await (await p.request.get(base + m[1])).body());
    if (png.length > 24 && png.readUInt32BE(16) === 1200 && png.readUInt32BE(20) === 675) return m[1];
  }
  throw new Error("the seeded 1200x675 media image is not in the library (run seed.sh)");
}

const FILLERS = 30;
const section = () => {
  const img = (id, extra = {}) => ({ id, t: "image", p: { src: wide, alt: `Wide ${id}`, ...extra } });
  const named = (n) => { n.p = { ...n.p, navlabel: n.id }; n.c?.forEach(named); return n; };
  return [named({
    id: "qa-sec", t: "container", p: { tag: "section" }, c: [
      { id: "qa-gallery", t: "gallery", p: { lightbox: true }, c: [img("qa-g1"), img("qa-g2"), img("qa-g3")] },
      img("qa-one", { lightbox: true, caption: "QA caption" }),
      img("qa-plain"),
      { id: "qa-jump", t: "button", p: { text: "QA jump", href: "#qa-target" } },
      { id: "qa-ext", t: "button", p: { text: "QA external", href: "https://example.com/qa", target: "_blank", rel: "nofollow sponsored", aria: "QA external site", tip: "Opens example.com" } },
      { id: "qa-rel", t: "button", p: { text: "QA rel only", href: "https://example.com/rel", rel: "nofollow" } },
      { id: "qa-card", t: "container", p: { href: "/contact", aria: "QA card heading, read more", anchor: "qa-card" }, c: [{ id: "qa-ch", t: "heading", p: { text: "QA card heading", level: 3 } }] },
      { id: "qa-card2", t: "container", p: { href: "/blog" }, c: [{ id: "qa-ch2", t: "heading", p: { text: "QA unnamed card", level: 3 } }] },
      { id: "qa-wrap", t: "container", c: [{ id: "qa-wb", t: "button", p: { text: "QA inner button", href: "/x" } }] },
      ...Array.from({ length: FILLERS }, (_, i) => ({ id: `qa-f${i}`, t: "text", p: { text: `QA filler paragraph ${i}` } })),
      { id: "qa-target", t: "container", p: { anchor: "qa-target" }, c: [{ id: "qa-th", t: "heading", p: { text: "QA target heading", level: 2 } }] },
    ],
  })];
};

let start = "";
await check("setup: paste the settings section onto the page, save, and find the public path", async () => {
  await open();
  // a failed earlier run may have left its section behind: remove it first (the page is then "as found")
  for (const left of (await doc()).nodes.filter((n) => n.p?.navlabel === "qa-sec")) {
    await p.locator(`${TREE}[data-id="${left.id}"]`).click({ button: "right" });
    await p.waitForSelector(MENU, { timeout: 3000 });
    await p.locator(`${MENU} [role="menuitem"]`, { hasText: /^Delete/ }).first().click();
    await settle(600);
    await update();
  }
  start = await sig();
  wide = await findWide();
  const env = { hm: "nodes", v: 2, nodes: section(), site: base };
  await p.evaluate((t) => navigator.clipboard.writeText(t), JSON.stringify(env));
  const first = (await doc()).nodes[0];
  await p.locator(`${TREE}[data-id="${first.id}"]`).click({ button: "right" });
  await p.waitForSelector(MENU, { timeout: 3000 });
  const resp = p.waitForResponse((r) => r.url().endsWith("/builder/paste") && r.request().method() === "POST", { timeout: 5000 });
  await p.locator(`${MENU} [role="menuitem"]`, { hasText: /^Paste after/ }).first().click();
  const r = await resp;
  if (r.status() !== 200) throw new Error("paste endpoint answered " + r.status() + " " + (await r.text()).slice(0, 200));
  await settle(1200);
  for (const n of flat((await doc()).nodes)) if (n.p?.navlabel?.startsWith("qa-")) ID[n.p.navlabel] = n.id;
  need(ID["qa-target"] && ID["qa-sec"], "the pasted section is not in the doc");
  await update();
  const view = await p.evaluate(() => JSON.parse(document.getElementById("hm-builder-boot").textContent).urls.view);
  need(view, "the page has no public address (is it published?)");
  publicPath = new URL(view, base).pathname;
  return `public ${publicPath}, image ${wide.split("/").pop()}`;
});

// A signed-out visitor with the real CSP.
const visitor = await b.newContext({ viewport: { width: 1440, height: 900 } });
const pub = await visitor.newPage();
const pubErrors = [];
pub.on("pageerror", (e) => pubErrors.push(e.message));
pub.on("console", (m) => { if (/Content Security Policy/i.test(m.text())) pubErrors.push(m.text()); });
const goPublic = (pg = pub) => pg.goto(base + publicPath, { waitUntil: "load" });

await check("anchor link: clicking #qa-target scrolls to it, the target is :target and carries the id", async () => {
  await goPublic();
  const y0 = await pub.evaluate(() => scrollY);
  const link = pub.locator(`a${cn("qa-jump")}`);
  await link.scrollIntoViewIfNeeded();
  const y1 = await pub.evaluate(() => scrollY);
  await link.click();
  await pub.waitForFunction(() => location.hash === "#qa-target");
  await settle(900);
  const r = await pub.evaluate(() => {
    const t = document.getElementById("qa-target");
    return { y: scrollY, target: document.querySelector(":target")?.id, top: t?.getBoundingClientRect().top, vh: innerHeight, cls: t?.className };
  });
  need(r.target === "qa-target", `:target is "${r.target}"`);
  need(r.y > y1 + 200, `the page did not scroll: ${y1} -> ${r.y}`);
  need(r.top >= -2 && r.top < r.vh, `target not in view: top ${r.top}`);
  need(new RegExp(ID["qa-target"]).test(r.cls), "the target lost its node class");
  return `scrollY ${y0} -> ${r.y}, target top ${Math.round(r.top)}px`;
});

await check("link attributes: target=_blank gets noopener noreferrer with the chosen tokens in order; rel without a tab only the chosen tokens; no target on an internal link", async () => {
  await goPublic();
  const attrs = (sel) => pub.locator(sel).evaluate((a) => ({ target: a.getAttribute("target"), rel: a.getAttribute("rel"), label: a.getAttribute("aria-label"), title: a.getAttribute("title"), href: a.getAttribute("href") }));
  const ext = await attrs(`a${cn("qa-ext")}`);
  need(ext.target === "_blank", `target ${ext.target}`);
  need(ext.rel === "nofollow sponsored noopener noreferrer", `rel "${ext.rel}"`);
  need(ext.label === "QA external site" && ext.title === "Opens example.com", `aria/tip: ${JSON.stringify(ext)}`);
  const rel = await attrs(`a${cn("qa-rel")}`);
  need(rel.target === null && rel.rel === "nofollow", `rel-only: ${JSON.stringify(rel)}`);
  const jump = await attrs(`a${cn("qa-jump")}`);
  need(jump.target === null && jump.rel === null, `internal link: ${JSON.stringify(jump)}`);
  const unsafe = await pub.$$eval('a[target="_blank"]', (as) => as.filter((a) => !/\bnoopener\b/.test(a.rel)).map((a) => a.outerHTML.slice(0, 80)));
  need(!unsafe.length, "target=_blank without noopener: " + unsafe.join(" "));
  return `${ext.rel} | ${rel.rel}`;
});

await check("linked container: one link named by its aria-label (and by its text without one), no nested links, the anchor id on the link", async () => {
  await goPublic();
  const named = pub.getByRole("link", { name: "QA card heading, read more", exact: true });
  need((await named.count()) === 1, `links named by the card aria-label: ${await named.count()}`);
  need((await named.getAttribute("href")) === "/contact", "href");
  need((await named.getAttribute("id")) === "qa-card", "anchor id on the link element");
  need((await named.evaluate((a) => a.tagName)) === "A", "not an <a>");
  const byText = pub.getByRole("link", { name: /QA unnamed card/ });
  need((await byText.count()) === 1 && (await byText.getAttribute("href")) === "/blog", "text-named link");
  const nested = await pub.$$eval("a a", (x) => x.length);
  need(nested === 0, `${nested} nested links`);
  return "named by aria-label and by text";
});

// The public image page: media images known to the library carry width and height; nothing shifts while they load.
async function cls(pg, { strip }) {
  const cdp = await pg.context().newCDPSession(pg);
  await cdp.send("Network.enable");
  await cdp.send("Network.setCacheDisabled", { cacheDisabled: true });
  await cdp.send("Network.emulateNetworkConditions", { offline: false, latency: 400, downloadThroughput: 150 * 1024, uploadThroughput: 150 * 1024 });
  await pg.addInitScript(() => {
    window.__cls = 0;
    window.__shifts = [];
    new PerformanceObserver((l) => {
      for (const e of l.getEntries()) {
        if (e.hadRecentInput) continue;
        window.__cls += e.value;
        window.__shifts.push(`${e.value.toFixed(5)} ${e.sources.map((s) => `${s.node?.nodeName}.${s.node?.className ?? ""} ${JSON.stringify(s.previousRect)}->${JSON.stringify(s.currentRect)}`).join("; ")}`);
      }
    }).observe({ type: "layout-shift", buffered: true });
  });
  if (strip) {
    await pg.route((u) => u.pathname === publicPath, async (route) => {
      const res = await route.fetch();
      const html = (await res.text()).replace(/(<img\b[^>]*?) width="\d+" height="\d+"/g, "$1");
      await route.fulfill({ response: res, body: html });
    });
  }
  await pg.goto(base + publicPath, { waitUntil: "domcontentloaded" });
  const early = await pg.evaluate(() => [...document.querySelectorAll("main img[src^='/media/']")].map((i) => ({ done: i.complete && i.naturalWidth > 0, w: i.getAttribute("width"), h: i.getAttribute("height") })));
  await pg.waitForLoadState("load");
  await pg.waitForFunction(() => [...document.querySelectorAll("main img[src^='/media/']")].every((i) => i.complete && i.naturalWidth > 0), null, { timeout: 60000 });
  await pg.evaluate(() => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(() => setTimeout(r, 300)))));
  const value = await pg.evaluate(() => window.__cls);
  const shifts = await pg.evaluate(() => window.__shifts);
  await cdp.detach();
  return { value, early, shifts };
}
await check("CLS is exactly 0 on the media image page (cold cache, throttled), and the same page without width/height shifts", async () => {
  const ctx = await b.newContext({ viewport: { width: 1440, height: 900 } });
  try {
    const good = await cls(await ctx.newPage(), { strip: false });
    need(good.early.length >= 5, `only ${good.early.length} media images on the page`);
    need(good.early.some((i) => !i.done), "every image had loaded at first layout: the run is invalid (no throttled load)");
    need(good.early.every((i) => i.w === "1200" && i.h === "675"), "an image has no width/height from the media row: " + JSON.stringify(good.early[0]));
    need(good.value === 0, `CLS ${good.value}: ${good.shifts.join(" | ")}`);
    const ctl = await b.newContext({ viewport: { width: 1440, height: 900 } });
    try {
      const control = await cls(await ctl.newPage(), { strip: true });
      need(control.early.every((i) => i.w === null), "the control still has width/height");
      need(control.value > 0, `the control (no width/height) measured CLS ${control.value}: the measurement cannot fail`);
      return `CLS ${good.value} with ${good.early.length} images (control without width/height: ${control.value.toFixed(4)})`;
    } finally { await ctl.close(); }
  } finally { await ctx.close(); }
});

const dialogState = () => pub.evaluate(() => {
  const d = document.querySelector("dialog.hm-lb");
  return { open: !!d?.open, active: document.activeElement?.className + ":" + document.activeElement?.textContent, counter: d?.querySelector(".hm-lb__n")?.textContent, src: d?.querySelector(".hm-lb__img")?.getAttribute("src"), cap: d?.querySelector(".hm-lb__cap")?.textContent, say: d?.querySelector(".hm-lb__sr")?.textContent, described: d?.getAttribute("aria-describedby") === d?.querySelector(".hm-lb__sr")?.id, locked: getComputedStyle(document.documentElement).overflow };
});
await check("lightbox, single image: Enter opens a dialog focused on Close, Tab stays inside, Esc closes and focus returns to the link", async () => {
  await goPublic();
  const link = pub.locator(`figure${cn("qa-one")} a[data-hm-lightbox]`);
  await link.focus();
  await pub.keyboard.press("Enter");
  await pub.locator("dialog.hm-lb[open]").waitFor({ timeout: 3000 });
  let s = await dialogState();
  need(/Close$/.test(s.active), "focus is not on Close: " + s.active);
  need(/QA caption/.test(s.say), `the caption is not announced: "${s.say}"`);
  need(s.cap === "QA caption", `caption "${s.cap}"`);
  need(s.src === wide, `viewer src ${s.src}`);
  for (let i = 0; i < 4; i++) {
    await pub.keyboard.press("Tab");
    s = await dialogState();
    need(s.open && /Close$/.test(s.active), `Tab ${i + 1} left the dialog or moved off the only stop: ${s.active}`);
  }
  await pub.keyboard.press("Escape");
  await settle(200);
  s = await dialogState();
  need(!s.open, "Esc did not close the dialog");
  need(await link.evaluate((a) => a === document.activeElement), "focus did not return to the link");
  return "open, trapped, closed, focus back";
});
await check("lightbox, gallery: ArrowRight shows the next image and the counter; Tab wraps over Close/Previous/Next both ways; Esc returns focus to the opener", async () => {
  await goPublic();
  const link = pub.locator(`${cn("qa-gallery")} a[data-hm-lightbox]`).nth(1);
  await link.focus();
  await pub.keyboard.press("Enter");
  await pub.locator("dialog.hm-lb[open]").waitFor({ timeout: 3000 });
  let s = await dialogState();
  need(s.counter === "2 / 3", `counter "${s.counter}"`);
  need(s.locked === "hidden", `the page behind the dialog can scroll (overflow ${s.locked})`);
  need(s.described && /^Image 2 of 3\. Wide qa-g2/.test(s.say), `the dialog is not described by its position and alt: "${s.say}"`);
  await pub.keyboard.press("ArrowRight");
  s = await dialogState();
  need(s.counter === "3 / 3", `after ArrowRight "${s.counter}"`);
  need(/^Image 3 of 3\. Wide qa-g3/.test(s.say), `the live region was not updated: "${s.say}"`);
  await pub.keyboard.press("ArrowRight");
  need((await dialogState()).counter === "1 / 3", "ArrowRight did not wrap to the first image");
  await pub.keyboard.press("ArrowLeft");
  need((await dialogState()).counter === "3 / 3", "ArrowLeft did not go back");
  const seen = [];
  for (let i = 0; i < 3; i++) { await pub.keyboard.press("Tab"); seen.push((await dialogState()).active.replace(/^.*:/, "")); }
  need(seen.join() === "Previous,Next,Close", `Tab order ${seen}`);
  await pub.keyboard.press("Shift+Tab");
  need(/Next$/.test((await dialogState()).active), "Shift+Tab from Close did not wrap to Next");
  await pub.keyboard.press("Escape");
  await settle(200);
  need(await link.evaluate((a) => a === document.activeElement), "focus did not return to the opener (second image)");
  return `tab order ${seen}`;
});
await check("lightbox: no script on a page without a lightbox is checked by the Go budget; here no CSP violation or page error occurred", async () => {
  need(!pubErrors.length, pubErrors.join(" | "));
  return "no CSP violation, no page error";
});
await check("axe on the public page and on the open lightbox dialog", async () => {
  const ctx = await b.newContext({ viewport: { width: 1440, height: 900 }, bypassCSP: true });
  try {
    const pg = await ctx.newPage();
    await goPublic(pg);
    const run = async (what) => {
      await pg.evaluate(axe);
      const r = await pg.evaluate((t) => window.axe.run(document, { runOnly: { type: "tag", values: t } }), tags);
      const v = r.violations.filter((x) => !(what === "page" && /^(region|landmark-one-main|page-has-heading-one)$/.test(x.id)));
      need(!v.length, `${what}: ` + v.map((x) => `${x.id} (${x.nodes.length}) ${x.nodes[0].target}`).join("; "));
    };
    await run("page");
    const link = pg.locator(`${cn("qa-gallery")} a[data-hm-lightbox]`).first();
    await link.focus();
    await pg.keyboard.press("Enter");
    await pg.locator("dialog.hm-lb[open]").waitFor();
    await settle(500); // the dialog fades in over 150 ms: axe reads the colours once it is opaque
    await run("open dialog");
    return "page and open dialog clean";
  } finally { await ctx.close(); }
});

await check("inspector: the Link field lists the pages and this page's #anchors; Open in new tab and the rel boxes write target and rel; clearing the link removes them", async () => {
  const from = errors.length;
  await open();
  await select("qa-rel");
  const input = panel().getByLabel("Link", { exact: true }).first();
  await input.waitFor({ timeout: 5000 });
  const listId = await input.getAttribute("list");
  need(listId, "the Link field has no datalist");
  await input.focus();
  const options = await p.$$eval(`#${listId} option`, (os) => os.map((o) => o.value));
  need(options.includes("#qa-target") && options.includes("#qa-card"), `anchors missing: ${options}`);
  need(options.some((o) => o.startsWith("/")), `no page path in: ${options}`);
  need(options.includes(publicPath), `this page ${publicPath} not offered: ${options.slice(0, 8)}`);
  const newTab = panel().getByLabel("Open in new tab");
  await newTab.waitFor({ timeout: 3000 });
  need(!(await newTab.isChecked()), "new tab starts checked");
  await newTab.check();
  await panel().getByLabel("Sponsored").check();
  await settle(300);
  let n = await byId("qa-rel");
  need(n.p.target === "_blank" && n.p.rel === "nofollow sponsored", `props ${JSON.stringify(n.p)}`);
  await input.fill("");
  await settle(500);
  n = await byId("qa-rel");
  need(n.p.href === undefined && n.p.target === undefined && n.p.rel === undefined, `link cleared but ${JSON.stringify(n.p)}`);
  need(!(await panel().getByLabel("Open in new tab").isVisible()), "link options stay visible without a link");
  await input.blur(); // Ctrl+Z inside a text field is the browser's own undo
  await p.keyboard.press("Control+z");
  await settle(500);
  n = await byId("qa-rel");
  need(n.p.target === "_blank" && n.p.rel === "nofollow sponsored" && typeof n.p.href === "string" && n.p.href !== "", `one undo should restore href, target and rel, got ${JSON.stringify(n.p)}`);
  need(await panel().getByLabel("Open in new tab").isChecked(), "new tab box not checked after undo");
  need(await panel().getByLabel("Sponsored").isChecked(), "sponsored box not checked after undo");
  await settle(500);
  n = await byId("qa-rel");
  need(n.p.target === "_blank", `selecting the node changed it: ${JSON.stringify(n.p)}`);
  noErrors(from);
  return `${options.length} choices; target/rel written, cleared, restored by one undo`;
});

await check("inspector Advanced: Anchor lowercases and is written; a duplicate is refused in the field; Copy #anchor shows; Accessible name hidden for a bare image, shown for a lightbox image", async () => {
  const from = errors.length;
  await open();
  await select("qa-wrap", "Advanced");
  const anchor = panel().getByLabel("Anchor", { exact: true });
  await anchor.waitFor({ timeout: 5000 });
  await anchor.fill("QA-Upper");
  await settle(300);
  need((await anchor.inputValue()) === "qa-upper", "not lowercased: " + (await anchor.inputValue()));
  need((await byId("qa-wrap")).p.anchor === "qa-upper", "anchor not in the doc");
  need(await panel().getByRole("button", { name: "Copy #anchor" }).isVisible(), "no Copy #anchor button");
  await anchor.fill("qa-target");
  await settle(300);
  need((await anchor.getAttribute("aria-invalid")) === "true", "a duplicate anchor is not flagged");
  need((await byId("qa-wrap")).p.anchor === "qa-upper", "the duplicate reached the doc");
  await anchor.fill("1bad");
  need((await anchor.getAttribute("aria-invalid")) === "true", "a bad anchor is not flagged");
  await anchor.fill("");
  await settle(300);
  need((await byId("qa-wrap")).p.anchor === undefined, "clearing did not remove the anchor");
  await select("qa-plain", "Advanced");
  await panel().getByLabel("Tooltip").waitFor({ timeout: 3000 });
  need((await panel().getByLabel("Accessible name").count()) === 0, "a bare image offers Accessible name");
  await select("qa-one", "Advanced");
  await panel().getByLabel("Accessible name").waitFor({ timeout: 3000 });
  await p.keyboard.press("Control+z");
  await p.keyboard.press("Control+z");
  await settle(300);
  noErrors(from);
  return "lowercase, duplicate and bad refused, clear removes";
});

await check("a lightbox image with no name is warned about in the editor (decorative needs a caption), and Link/Lightbox explain each other in text", async () => {
  const from = errors.length;
  await open();
  await select("qa-plain", "Content");
  const lightbox = panel().getByLabel("Lightbox", { exact: true });
  await lightbox.waitFor({ timeout: 5000 });
  await lightbox.check();
  await settle(300);
  const warn = panel().locator(".hm-callout:visible", { hasText: /name for screen readers|decorative/i });
  need((await warn.count()) === 0, "a lightbox image with alt text is warned about");
  const link = panel().getByLabel("Link", { exact: true }).first();
  need(await link.isDisabled(), "Link stays enabled with a lightbox");
  const why = await panel().locator(".hm-muted:visible", { hasText: /cannot also be a link/ }).count();
  need(why === 1, "no visible text says why Link is off");
  need(/-n\b/.test((await link.getAttribute("aria-describedby")) ?? ""), "Link is not described by that text");
  await panel().getByLabel(/Decorative/).check();
  await settle(300);
  need((await panel().locator(".hm-callout:visible", { hasText: /decorative.*caption/i }).count()) === 1, "no warning for a decorative lightbox image");
  await p.evaluate(() => document.activeElement?.blur?.());
  await p.keyboard.press("Control+z");
  await p.keyboard.press("Control+z");
  await settle(300);
  noErrors(from);
  return "warned; Link off with visible reason";
});

await check("a reserved anchor is explained in the field before any save, is not stored, and the error is gone from the description once fixed", async () => {
  await open();
  await select("qa-wrap", "Advanced");
  const anchor = panel().getByLabel("Anchor", { exact: true });
  for (const bad of ["main-content", "hm-thing", "intro-strip-label"]) {
    await anchor.fill(bad);
    await settle(200);
    need((await anchor.getAttribute("aria-invalid")) === "true", `${bad} is not flagged`);
    need((await byId("qa-wrap")).p.anchor !== bad, `${bad} reached the doc`);
    const err = await panel().locator(".hm-bs-err:visible").first().innerText();
    need(/page itself|reserved/i.test(err), `${bad}: the field gives no reason: "${err}"`);
  }
  await anchor.fill("qa-fine");
  await settle(200);
  need((await anchor.getAttribute("aria-invalid")) === null, "a valid anchor stays flagged");
  const described = (await anchor.getAttribute("aria-describedby")) ?? "";
  need(!/-e\b/.test(described), `the fixed field is still described by its error: ${described}`);
  need(/saving stopped|reserved/i.test(await state()) === false, `the save state shows a refusal: "${await state()}"`);
  await anchor.fill("");
  await settle(300);
  return "reserved names refused inline; error leaves the description once fixed";
});

await check("a link on a container with a button inside is explained in the Link field (no 422), not stored, and leaves when the field is emptied", async () => {
  await open();
  await select("qa-wrap", "Content");
  const link = panel().getByLabel("Link", { exact: true }).first();
  await link.waitFor({ timeout: 5000 });
  await link.fill("/refused");
  await settle(300);
  need((await link.getAttribute("aria-invalid")) === "true", "the Link field is not flagged");
  const msg = await panel().locator(".hm-bs-err:visible").first().innerText();
  need(/interactive|button|div tag/i.test(msg), "the field gives no reason: " + msg);
  need(!/\bn[0-9a-f]{8,}\b/.test(msg), "the reason shows a raw node id: " + msg);
  need((await byId("qa-wrap")).p.href === undefined, "the refused link reached the doc");
  await link.fill("");
  await settle(300);
  need((await link.getAttribute("aria-invalid")) === null, "the flag stays after emptying");
  return msg.slice(0, 100);
});

await check("phone width: the Inspector sheet's Advanced fields are reachable without sideways scrolling", async () => {
  await p.setViewportSize({ width: 390, height: 844 });
  try {
    await p.goto(base + pageHref, { waitUntil: "load" });
    await p.waitForFunction(() => !!window.__hmBuilder, null, { timeout: 8000 });
    await settle(800);
    await p.locator('[data-hm-bs-dock="nav"]').click(); // the phone dock opens the tree as a sheet
    await settle(300);
    await row("qa-card").click(); // a tap on a row opens the Inspector sheet
    await settle(500);
    if ((await p.evaluate(() => document.querySelector("[data-hm-builder]").dataset.hmBsTab)) !== "insp") { await p.locator('[data-hm-bs-dock="insp"]').click(); await settle(500); }
    await tab("Advanced").click({ timeout: 5000 });
    const anchor = panel().getByLabel("Anchor", { exact: true });
    await anchor.scrollIntoViewIfNeeded({ timeout: 5000 });
    const box = await anchor.boundingBox();
    need(box && box.x >= 0 && box.x + box.width <= 390, `Anchor field box ${JSON.stringify(box)}`);
    const over = await p.evaluate(() => document.documentElement.scrollWidth - innerWidth);
    need(over <= 1, `page scrolls sideways by ${over}px`);
    need((await anchor.inputValue()) === "qa-card", "the field does not show the stored anchor");
    return `field ${Math.round(box.width)}px wide`;
  } finally { await p.setViewportSize({ width: 1440, height: 900 }); }
});

await check("axe on the builder with the Advanced tab (Anchor, Accessible name, Tooltip) and the Link options open", async () => {
  await open();
  const run = async (what) => {
    await p.evaluate(axe);
    const r = await p.evaluate((t) => window.axe.run(document.querySelector(".hm-bs-panel"), { runOnly: { type: "tag", values: t } }), tags);
    need(!r.violations.length, `${what}: ` + r.violations.map((x) => `${x.id} (${x.nodes.length}) ${x.nodes[0].target}`).join("; "));
  };
  await select("qa-ext", "Content");
  await panel().getByLabel("Open in new tab").waitFor({ timeout: 3000 });
  await run("Content with link options");
  await select("qa-ext", "Advanced");
  await panel().getByLabel("Anchor").waitFor({ timeout: 3000 });
  await run("Advanced");
  return "clean";
});

await check("the page is left as found: the pasted section is deleted, the page saved as it was", async () => {
  await open();
  await p.evaluate(() => document.activeElement?.blur?.());
  await row("qa-sec").click({ button: "right" });
  await p.waitForSelector(MENU, { timeout: 3000 });
  await p.locator(`${MENU} [role="menuitem"]`, { hasText: /^Delete/ }).first().click();
  await settle(600);
  need((await sig()) === start, "deleting the pasted section did not give back the page found");
  await update();
  await open();
  need((await sig()) === start, "the reloaded page differs from the one found");
  return "restored, saved and reloaded";
});

await visitor.close();
await b.close();
if (bad) { console.log(`${bad} check(s) failed`); process.exit(1); }
console.log("builder-settings: all checks passed");
