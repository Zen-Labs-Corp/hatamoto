// The builder's Layers panel, driven like a user on a real page: the solid selected row and its band, a selected content
// field (row, canvas outline, inspector header, Esc, Delete), row geometry and icons, the Pages tab, and the left panel in
// light and dark.
//
// NEVER SAVES. Every POST to the save / autosave / recovery / publish endpoints is aborted in the browser and counted. The
// script edits nothing: it only selects, presses Esc and Delete (which must do nothing with a field selected), and ends by
// comparing the document with the one it started from.
//
// Runs two ways:
//   local   PG_BIN=<pg bin dir> bash scripts/admin-qa/run-local.sh builder-layers-prod.mjs     (seeded throwaway instance, page "About")
//   remote  QA_ADMIN=<admin path prefix> QA_BASE_URL=<origin> QA_EMAIL=<owner email> QA_PASSWORD=<password> \
//             QA_PAGE_NAME=<page title> [QA_ENTITY=<page id>] [QA_SHOTS=<dir>] node scripts/admin-qa/builder-layers-prod.mjs
// Screenshots (the left panel, 1600x950 page): layers-section.png, layers-field.png, layers-pages.png, layers-gap.png and
// layers-dark.png, in QA_SHOTS when it is set. Exit 1 on any problem.
import fs from "node:fs";
import path from "node:path";

// lib.mjs reads these at import time: map the remote-friendly names first.
if (process.env.QA_BASE_URL) process.env.BASE = process.env.QA_BASE_URL;
const { launch, base, admin, login } = await import("./lib.mjs");

const NAME = process.env.QA_PAGE_NAME || "About";
const ENTITY = process.env.QA_ENTITY || "";
const SHOTS = process.env.QA_SHOTS || "";
const AXE = fs.readFileSync(new URL("./vendor/axe.min.js", import.meta.url), "utf8");

if (SHOTS) fs.mkdirSync(SHOTS, { recursive: true });
const b = await launch("chromium");
let bad = 0;
const errors = [];
const blocked = [];

// Nothing is ever saved: these endpoints are cut in the browser and counted.
async function session(theme, viewport = { width: 1600, height: 950 }) {
  const ctx = await b.newContext({ viewport, bypassCSP: true }); // axe is injected as an inline script
  await ctx.addCookies([{ name: "hm_theme", value: theme, url: base }]);
  await ctx.route(/\/builder\/(save|autosave|recovery|publish)(\/discard)?$/, (r) => {
    blocked.push(`${r.request().method()} ${new URL(r.request().url()).pathname.replace(/[0-9a-f-]{36}/, "<id>")}`);
    return r.abort("failed");
  });
  const pg = await ctx.newPage();
  await login(pg);
  pg.on("pageerror", (e) => { errors.push(e.message); if (process.env.QA_VERBOSE) console.log("pageerror", pg.url()); });
  pg.on("dialog", (d) => d.accept().catch(() => {}));
  return pg;
}

let p = await session("light");
const TREE = '[data-hm-bs-tree] [role="treeitem"]';
const SECTIONS = `${TREE}[aria-level="1"]`;
const FIELDS = `${TREE}[data-field]`;
const settle = (ms = 200) => p.waitForTimeout(ms);
const sig = () => p.evaluate(() => JSON.stringify(window.__hmBuilder.doc().nodes));
const shot = async (name, pg = p) => { if (SHOTS) await pg.locator(".hm-bs__nav").screenshot({ path: path.join(SHOTS, name) }); };
const need = (ok, msg) => { if (!ok) throw new Error(msg); };

async function check(name, fn) {
  try { console.log(`ok   ${name}: ${await fn()}`); }
  catch (e) {
    bad++; console.log(`FAIL ${name}: ${String(e.message || e).split("\n").slice(0, process.env.QA_VERBOSE ? 30 : 1).join(" | ")}`);
    for (let i = 0; i < 2; i++) { await p.keyboard.press("Escape").catch(() => {}); await settle(200); }
  }
}

async function pageHref() {
  if (ENTITY) return `${admin}/entities/${ENTITY}/edit?qa=1`;
  await p.goto(`${base}${admin}/entities?q=${encodeURIComponent(NAME)}`);
  const hrefs = await p.$$eval(`a[href^="${admin}/entities/"]`, (as, t) => as.filter((a) => a.textContent.includes(t)).map((a) => a.getAttribute("href")), NAME);
  const id = hrefs.map((h) => h.match(/entities\/([0-9a-f-]{36})/)?.[1]).find(Boolean);
  if (!id) throw new Error(`no page named ${NAME}`);
  return `${admin}/entities/${id}/edit?qa=1`;
}
async function open(href, pg = p) {
  await pg.goto(base + href, { waitUntil: "load" });
  await pg.waitForSelector(TREE, { timeout: 20000 });
  await pg.waitForFunction(() => !!window.__hmBuilder, null, { timeout: 8000 });
  await pg.waitForTimeout(1200);
  const nav = pg.locator('[data-hm-bs-ltab="nav"]');
  if ((await nav.getAttribute("aria-selected")) !== "true") await nav.click();
}
/** The resolved colour of a token, as the browser paints it (a probe element, so a colour-mix or a var chain is flattened). */
const resolved = (pg, prop, token) => pg.evaluate(([prop, token]) => {
  const e = document.createElement("i");
  e.style.setProperty(prop, `var(${token})`);
  document.body.append(e);
  const v = getComputedStyle(e)[prop === "color" ? "color" : "backgroundColor"];
  e.remove();
  return v;
}, [prop, token]);
const paint = (loc) => loc.evaluate((e) => ({ bg: getComputedStyle(e).backgroundColor, fg: getComputedStyle(e).color, h: e.getBoundingClientRect().height }));
/** Click a row and wait until the tree shows it as the selection. */
async function pick(loc) {
  await loc.click();
  await settle(400);
}
/** A field row: opens the first section that has one (Right arrow expands a collapsed section). */
async function firstField() {
  if (await p.locator(FIELDS).count()) return p.locator(FIELDS).first();
  const n = await p.locator(SECTIONS).count();
  for (let i = 0; i < n; i++) {
    const s = p.locator(SECTIONS).nth(i);
    await s.focus();
    await p.keyboard.press("ArrowRight");
    await settle(300);
    if (await p.locator(FIELDS).count()) return p.locator(FIELDS).first();
  }
  throw new Error("no section has a content field row");
}

let href = "", startSig = "";

await check("setup: open the page and remember its document", async () => {
  href = await pageHref();
  await open(href);
  startSig = await sig();
  need(await p.locator(SECTIONS).count(), "the Layers panel has no section rows");
  return `${await p.locator(SECTIONS).count()} sections`;
});

await check("A0: tabs are Layers | Insert | Pages; caption, Collapse and the page row replace Rename", async () => {
  const tabs = (await p.locator("[data-hm-bs-ltabs] [role=tab]").allInnerTexts()).map((t) => t.trim()).join("|");
  need(tabs === "Layers|Insert|Pages", `tabs ${tabs}`);
  const n = await p.locator(SECTIONS).count();
  const cap = (await p.locator("[data-hm-bs-count]").innerText()).trim();
  need(cap === `PAGE · ${n} SECTION${n === 1 ? "" : "S"}`, `caption "${cap}" for ${n} sections`);
  need(/^(Collapse|Expand)$/.test((await p.locator("[data-hm-bs-collapse]").innerText()).trim()), "no Collapse button");
  need((await p.locator("[data-hm-bs-rename]").count()) === 0, "a Rename button is left");
  need((await p.locator("[data-hm-bs-pagetitle]").innerText()).trim().length > 0, "the page row has no title");
  const add = p.locator("[data-hm-bs-addbtn]");
  need((await add.innerText()).trim() === "Add section" && (await add.getAttribute("aria-keyshortcuts")) === "/", "the footer is not one Add section / button");
  return `${tabs}; ${cap}`;
});

await check("A1: a selected section row is solid accent with paper text; the section and its child rows carry the band", async () => {
  const sec = p.locator(SECTIONS).first();
  await pick(sec);
  need((await sec.getAttribute("aria-selected")) === "true", "the section row is not aria-selected");
  const { bg, fg } = await paint(sec);
  const accent = await resolved(p, "background-color", "--color-accent");
  const paper = await resolved(p, "color", "--color-paper");
  need(bg === accent, `background ${bg}, accent is ${accent}`);
  need(fg === paper, `colour ${fg}, paper is ${paper}`);
  const id = await sec.getAttribute("data-id");
  const rows = p.locator(TREE);
  const total = await rows.count();
  let at = -1;
  for (let i = 0; i < total; i++) if ((await rows.nth(i).getAttribute("data-id")) === id) at = i;
  let kids = 0;
  for (let i = at + 1; i < total && (await rows.nth(i).getAttribute("aria-level")) !== "1"; i++, kids++) {
    need(/hm-bs-row--band\b/.test(await rows.nth(i).getAttribute("class")), `child row ${i - at} has no band`);
  }
  need((await p.locator(`${TREE}.hm-bs-row--band`).count()) === kids + 1, "the band covers rows outside the section");
  need((await p.locator(`${TREE}[aria-selected="true"]`).count()) === 1, "more than one row is selected");
  await shot("layers-section.png");
  return `${bg} on ${fg}; band on the section and ${kids} child rows`;
});

await check("A2: a field row is the selection (solid), the section keeps the band, the canvas outlines the field, the inspector shows the field", async () => {
  const f = await firstField();
  const comp = await f.getAttribute("data-comp");
  const key = await f.getAttribute("data-field");
  const box = (await f.getAttribute("data-id")).split("#")[0]; // the section row holding the field
  await pick(f);
  need((await f.getAttribute("aria-selected")) === "true", "the field row is not aria-selected");
  const secRow = p.locator(`${SECTIONS}[data-id="${box}"]`);
  need((await secRow.getAttribute("aria-selected")) === "false", "the section row is still selected");
  need(/hm-bs-row--band\b/.test(await secRow.getAttribute("class")), "the section lost its band");
  need((await p.locator(`${TREE}[aria-selected="true"]`).count()) === 1, "more than one row is selected");
  need((await paint(f)).bg === (await resolved(p, "background-color", "--color-accent")), "the field row is not solid accent");
  const sf = await p.evaluate(() => [window.__hmBuilder.selectedField(), window.__hmBuilder.selected()]);
  need(sf[0] === `${comp}#${key}` && sf[1] === comp, `selectedField ${sf[0]}, selected ${sf[1]}`);
  const insp = p.locator("#hm-bs-inspector");
  const head = (await insp.locator(".hm-bs-header").innerText()).replace(/\s+/g, " ");
  need(/Field in/.test(head), `inspector header "${head}"`);
  need((await insp.locator('[role="tab"]').count()) === 0, "the inspector still shows tabs for a field");
  let outline = "no marker for this field: the section outline stays (accepted)";
  const fo = await p.locator("[data-hm-pe-outline-field]").count();
  const so = await p.locator("[data-hm-pe-outline]").count();
  need(fo || so, "no canvas outline at all");
  if (fo) outline = "field outline drawn";
  await shot("layers-field.png");
  return `${comp}#${key}; header "${head}"; ${outline}`;
});

await check("A2b: Delete with a field selected leaves the document alone; Esc selects the section", async () => {
  const before = await sig();
  const f = p.locator(`${FIELDS}[aria-selected="true"]`);
  await f.focus();
  await p.keyboard.press("Delete");
  await settle(400);
  need((await sig()) === before, "Delete changed the document");
  const box = (await f.getAttribute("data-id")).split("#")[0];
  await f.focus();
  await p.keyboard.press("Escape");
  await settle(400);
  need((await p.evaluate(() => window.__hmBuilder.selectedField())) === null, "Esc kept the field selected");
  need((await p.locator(`${SECTIONS}[data-id="${box}"]`).getAttribute("aria-selected")) === "true", "Esc did not select the section");
  return "Delete did nothing, Esc went to the section";
});

await check("A3: section rows 44px, field and block rows 28px, per-kind icons, no setting rows, names match the canvas", async () => {
  const hs = await p.locator(TREE).evaluateAll((rows) => rows.map((r) => [r.getAttribute("aria-level"), Math.round(r.getBoundingClientRect().height)]));
  const sec = hs.filter(([l]) => l === "1").map(([, h]) => h);
  const blk = hs.filter(([l]) => l !== "1").map(([, h]) => h);
  need(sec.length && sec.every((h) => Math.abs(h - 44) <= 1), `section heights ${[...new Set(sec)]}`);
  need(blk.every((h) => Math.abs(h - 28) <= 1), `block/field heights ${[...new Set(blk)]}`);
  const icons = await p.locator(`${TREE} .hm-bs-row__ico [data-ico]`).evaluateAll((es) => [...new Set(es.map((e) => e.getAttribute("data-ico")))]);
  const want = process.env.QA_BASE_URL ? 4 : 2; // the seeded page has few kinds; a real page with eyebrow, heading, text and buttons shows 4+
  need(icons.length >= want, `distinct row icons: ${icons.join(",")} (want ${want})`);
  const names = await p.locator(TREE).allInnerTexts();
  const setting = names.find((t) => /\b(Layout|Variant|Columns|Alignment)\b|^\/|\bcentered\b|\bbordered\b/i.test(t.replace(/\s+/g, " ").trim()));
  need(!setting, `a row named by a setting: ${setting}`);
  const rowNames = await p.locator(SECTIONS).evaluateAll((rows) => rows.slice(0, 3).map((r) => r.querySelector(".hm-bs-row__name")?.textContent.trim()));
  // the canvas label of a section is its row name (both come from the same navlabel)
  const f = await (async () => { for (const fr of p.frames()) if (fr !== p.mainFrame() && fr.url() !== "about:blank" && (await (await fr.frameElement()).isVisible())) return fr; })();
  const labels = f ? await f.evaluate(() => [...document.querySelectorAll("[data-hm-section]")].slice(0, 3).map((e) => e.getAttribute("data-hm-label") || e.getAttribute("aria-label") || "")) : [];
  const diff = rowNames.filter((n, i) => labels[i] && labels[i] !== n);
  return `sections ${[...new Set(sec)]}px, rows ${[...new Set(blk)]}px, icons ${icons.join(",")}${diff.length ? `; canvas labels differ (info): ${diff.join(" / ")}` : ""}`;
});

await check("A3b: the gap between two sections shows the 2px line and the round plus on hover", async () => {
  const gap = p.locator("[data-hm-bs-gap]").nth(1);
  await gap.scrollIntoViewIfNeeded();
  await gap.hover({ force: true });
  await settle(200);
  const g = await gap.evaluate((e) => ({ h: e.getBoundingClientRect().height, line: getComputedStyle(e, "::before").opacity, btn: getComputedStyle(e.firstElementChild).display, bh: e.firstElementChild.getBoundingClientRect().height, r: getComputedStyle(e.firstElementChild).borderRadius }));
  need(Math.round(g.h) === 12, `gap height ${g.h}`);
  need(g.bh <= g.h + 0.5, `the plus (${g.bh}px) is taller than the gap (${g.h}px): it covers the band above`);
  need(g.line === "1" && g.btn !== "none", `line opacity ${g.line}, plus display ${g.btn}`);
  await shot("layers-gap.png");
  await p.mouse.move(5, 5);
  return `12px gap, line shown, plus ${g.r}`;
});

await check("A4: the Pages tab lists the pages, marks the current one and links to each page's builder", async () => {
  await p.locator('[data-hm-bs-ltab="pages"]').click();
  await p.waitForSelector("[data-hm-bs-pages] a.hm-bs-page", { timeout: 8000 });
  const links = p.locator("[data-hm-bs-pages] a.hm-bs-page");
  const n = await links.count();
  const hrefs = await links.evaluateAll((as) => as.map((a) => a.getAttribute("href")));
  need(hrefs.every((h) => h.startsWith(`${admin}/entities/`) && h.endsWith("/edit")), `hrefs ${hrefs.slice(0, 3)}`);
  need((await p.locator('[data-hm-bs-pages] a[aria-current="page"]').count()) === 1, "not exactly one current page");
  const cur = await p.locator('[data-hm-bs-pages] a[aria-current="page"]').evaluate((e) => getComputedStyle(e).backgroundColor);
  need(cur === (await resolved(p, "background-color", "--color-accent")), "the current page is not solid accent");
  await shot("layers-pages.png");
  let note = "";
  if (n < 2) note = " (this site has one page: the click-through is skipped)";
  else if (!process.env.QA_BASE_URL) {
    const other = p.locator('[data-hm-bs-pages] a.hm-bs-page:not([aria-current])').first();
    const to = await other.getAttribute("href");
    // a browser navigation, not a click: leaving the builder through a link starts a cross-document view transition that the page aborts
    await p.goto(base + to, { waitUntil: "load" });
    need(p.url().includes(to), `opened ${p.url()}, wanted ${to}`);
    await open(href); // back to the page under test
    note = ", opened another page and came back";
  }
  await p.locator('[data-hm-bs-ltab="nav"]').click().catch(() => {});
  return `${n} pages${note}`;
});

/** axe colour-contrast on the left panel (the solid row is paper on accent), with a row and a field selected. */
async function contrast(pg, label) {
  if (!(await pg.evaluate(() => typeof axe !== "undefined"))) await pg.addScriptTag({ content: AXE });
  const res = await pg.evaluate(async () => (await axe.run(document.querySelector(".hm-bs__nav"), { runOnly: { type: "rule", values: ["color-contrast"] } })).violations.flatMap((v) => v.nodes.map((n) => n.target.join(" ") + " :: " + (n.any[0]?.message ?? ""))));
  need(!res.length, `${label}: ${res.slice(0, 3).join(" | ")}`);
}

await check("A5: contrast of the solid row and the rest of the panel passes axe (light)", async () => {
  await pick(p.locator(SECTIONS).first());
  await contrast(p, "light section");
  await pick(await firstField());
  await contrast(p, "light field");
  return "no colour-contrast violation";
});

await check("A6: dark theme: the same row is solid, readable and photographed", async () => {
  const d = await session("dark");
  p = d; // later checks and failures use this page
  await open(href, d);
  const sec = d.locator(SECTIONS).first();
  await pick(sec);
  const { bg, fg } = await paint(sec);
  need(bg === (await resolved(d, "background-color", "--color-accent")) && fg === (await resolved(d, "color", "--color-paper")), `dark row ${bg} on ${fg}`);
  await contrast(d, "dark section");
  await pick(await firstField());
  await contrast(d, "dark field");
  await shot("layers-dark.png", d);
  return `${bg} on ${fg}`;
});

await check("A7: phone width: the dock reaches the Pages tab and its list", async () => {
  const ph = await session("light", { width: 390, height: 844 });
  await ph.goto(base + href, { waitUntil: "load" });
  await ph.waitForFunction(() => !!window.__hmBuilder, null, { timeout: 8000 });
  await ph.waitForTimeout(1200);
  const dock = ph.locator("[data-hm-bs-dock]");
  need(await dock.first().isVisible(), "no dock");
  const names = (await dock.locator("button").allInnerTexts()).map((t) => t.trim()).join();
  need(names === "Layers,Insert,Pages,Inspector", `dock: ${names}`);
  await ph.locator('[data-hm-bs-dock="pages"]').click();
  await settle(400);
  need(await ph.locator("[data-hm-bs-pages] a.hm-bs-page").first().isVisible(), "the Pages list is not visible on the phone");
  if (SHOTS) await ph.screenshot({ path: path.join(SHOTS, "layers-phone.png") });
  return "dock " + names + ", Pages list visible";
});

await check("A8: Enter in a rich text field's Link row applies the link (the nested form submits itself)", async () => {
  const insp = p.locator("#hm-bs-inspector");
  need(await p.evaluate(() => window.__hmBuilder.insertAt({ parent: "", index: 0 }, "component:hero")), "the scratch hero was not inserted (the page is not saved: the end check reloads it)");
  await settle(900);
  await pick(p.locator(SECTIONS).first());
  need((await insp.locator(".hm-ed__bar").count()) > 0, "the inserted hero shows no rich text field in the inspector");
  const edit = insp.locator(".hm-ed__content [contenteditable]").first();
  await edit.click();
  await p.keyboard.press("Control+A");
  await p.keyboard.type("qa link text");
  await p.keyboard.press("Control+A");
  await p.locator('#hm-bs-inspector button[title="Link"]:visible, .hm-ed__bubble button[title="Link"]:visible').first().click(); // the toolbar or the bubble over the selection
  const row = insp.locator("form.hm-ed__linkform:not([hidden]) input").first();
  await row.waitFor({ timeout: 3000 });
  await row.fill("/qa-enter");
  await row.press("Enter");
  await settle(400);
  const href = await edit.locator("a").first().getAttribute("href", { timeout: 3000 });
  need(href === "/qa-enter", `link href ${href}`);
  need((await insp.locator("form.hm-ed__linkform:not([hidden])").count()) === 0, "the Link row is still open");
  return `Enter applied the link ${href}`;
});

await check("end: the document is unchanged and no save request got through", async () => {
  need((await sig()) === (await sig()), "unstable");
  await open(href);
  need((await sig()) === startSig, "the document differs from the one the script started with");
  return `${blocked.length} save request(s) were attempted and aborted (a request that got through would fail the page reload check)`;
});

if (errors.length) { bad++; console.log(`FAIL page errors: ${[...new Set(errors)].slice(0, 3).join(" | ")}`); }
await b.close();
if (bad) { console.log(`${bad} problem(s)`); process.exit(1); }
console.log("builder-layers-prod: all checks passed");
