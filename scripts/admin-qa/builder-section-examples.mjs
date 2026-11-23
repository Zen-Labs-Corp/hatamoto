// Theme section examples in the builder, driven like a user. A section card shows its description (A5); inserting Hero stores
// the example (A1: a visible band with the headline and two buttons), opens its Content tab and focuses the first field (A3), and
// is ONE undo step with no "Change section content" entry behind it; a drag from the Insert tab does the same. An emptied section
// shows the placeholder hint in the editor.
//
// Runs two ways:
//   local   PG_BIN=<pg bin dir> bash scripts/admin-qa/run-local.sh builder-section-examples.mjs     (seeded throwaway instance)
//   remote  QA_ADMIN=<admin path prefix> QA_BASE_URL=<origin> QA_EMAIL=<owner email> QA_PASSWORD=<password> \
//             QA_PAGE_NAME=<page title> [QA_ENTITY=<page id>] node scripts/admin-qa/builder-section-examples.mjs
// Screenshots (QA_SHOTS=<dir>, 1600x950): hero-insert.png (the inserted Hero, Content tab focused) and empty-placeholder.png.
// Nothing is saved: every POST to the save / autosave / recovery endpoints is aborted in the browser, and the page is left as
// found (the last check compares the document after a reload). Exit 1 on any problem.
import fs from "node:fs";
import path from "node:path";

if (process.env.QA_BASE_URL) process.env.BASE = process.env.QA_BASE_URL;
const { launch, base, admin, login } = await import("./lib.mjs");

const NAME = process.env.QA_PAGE_NAME || "About";
const ENTITY = process.env.QA_ENTITY || "";
const SHOTS = process.env.QA_SHOTS || "";
const BLURB = "Page opener with headline, subheadline and call-to-action buttons";

if (SHOTS) fs.mkdirSync(SHOTS, { recursive: true });
const b = await launch("chromium");
const ctx = await b.newContext({ viewport: { width: 1600, height: 950 } });
await ctx.addCookies([{ name: "hm_theme", value: "light", url: base }]);
const p = await ctx.newPage();
await login(p);
let bad = 0;
const errors = [];
p.on("pageerror", (e) => errors.push(e.message));
p.on("dialog", (d) => d.accept().catch(() => {}));
const blocked = [];
await ctx.route(/\/builder\/(save|autosave|recovery)(\/discard)?$/, (r) => {
  blocked.push(r.request().method());
  return r.abort("failed");
});

const TREE = '[data-hm-bs-tree] [role="treeitem"]';
const settle = (ms = 150) => p.waitForTimeout(ms);
async function frame() {
  for (const f of p.frames()) {
    if (f === p.mainFrame() || f.url() === "about:blank") continue;
    if (await (await f.frameElement()).isVisible()) return f;
  }
  throw new Error("no visible canvas frame");
}
const tops = () => p.evaluate(() => window.__hmBuilder.doc().nodes);
const sig = async () => JSON.stringify(await tops());
const shot = async (name) => { if (SHOTS) await p.screenshot({ path: path.join(SHOTS, name) }); };
const heroes = async () => (await tops()).filter((n) => n.t === "component" && n.p?.type === "hero");
const tile = () => p.locator('[data-hm-bs-palette] [data-pal="component:hero"]').first();
const showAdd = async () => {
  if ((await p.locator('[data-hm-bs-ltab="add"]').getAttribute("aria-selected")) !== "true") await p.locator('[data-hm-bs-ltab="add"]').click();
  await tile().waitFor({ state: "visible", timeout: 8000 });
};
const undoByKey = async () => { await p.evaluate(() => document.activeElement?.blur?.()); await p.keyboard.press("Control+z"); await settle(500); };
const contentTab = () => p.locator('[role="tab"][data-tab="content"]').first();

async function check(name, fn) {
  try { console.log(`ok   ${name}: ${await fn()}`); }
  catch (e) {
    bad++; console.log(`FAIL ${name}: ${String(e.message || e).split("\n").slice(0, process.env.QA_VERBOSE ? 30 : 1).join(" | ")}`);
    await p.mouse.up().catch(() => {});
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
async function open(href) {
  await p.goto(base + href, { waitUntil: "load" });
  await p.waitForSelector(TREE, { timeout: 15000 });
  await p.waitForFunction(() => !!window.__hmBuilder, null, { timeout: 8000 });
  await settle(800);
}

// After an insert: the section is selected, its Content tab is open and the first field of the form has the focus.
async function contentFocused() {
  await p.waitForFunction(() => document.querySelector('[role="tab"][data-tab="content"]')?.getAttribute("aria-selected") === "true", null, { timeout: 5000 });
  const panel = p.locator('[role="tabpanel"]').first();
  await panel.locator('form:not([aria-busy="true"]), [aria-busy="false"]').first().waitFor({ state: "attached", timeout: 8000 }).catch(() => {});
  await p.waitForFunction(() => { const a = document.activeElement; return !!a && !!a.closest('[role="tabpanel"]') && a !== document.querySelector('[role="tabpanel"]'); }, null, { timeout: 8000 });
  return p.evaluate(() => {
    const a = document.activeElement, panel = a.closest('[role="tabpanel"]');
    const first = (a.closest("form") || panel).querySelector('[contenteditable="true"], input:not([readonly]):not([type="hidden"]), select, textarea');
    return { same: a === first || first?.contains(a), first: first && first.outerHTML.slice(0, 100), tag: a.tagName, label: a.getAttribute("aria-label") || a.name || a.id };
  });
}

let href = "", startSig = "";

await check("setup: open the page and remember its document", async () => {
  href = await pageHref();
  await open(href);
  startSig = await sig();
  await showAdd();
  return `${(await tops()).length} top-level nodes`;
});

await check("A5: the Hero card in the Insert tab reads the section description", async () => {
  const text = (await tile().locator(".hm-bs-pal__h").innerText()).trim();
  if (text !== BLURB) throw new Error(`card text is "${text}"`);
  return `"${text}"`;
});

await check("A1: inserting Hero shows a band over 150px with the headline and two buttons", async () => {
  const had = (await heroes()).length;
  await tile().click();
  await settle(900);
  const hs = await heroes();
  if (hs.length !== had + 1) throw new Error(`${hs.length} heroes, wanted ${had + 1}`);
  const node = hs.find((h) => h.p.content?.headline);
  if (!node) throw new Error("the new hero has no stored content");
  const f = await frame();
  const sec = f.locator(`[data-hm-id="${node.id}"], [data-id="${node.id}"]`).first();
  const box = (await sec.count()) ? await sec.boundingBox() : null;
  const m = await f.evaluate(() => {
    const h = document.querySelector(".hm-hero, section.hm-hero") || [...document.querySelectorAll("section")].find((s) => /A clear headline goes here/.test(s.textContent));
    const r = h.getBoundingClientRect();
    return { height: Math.round(r.height), text: /A clear headline goes here/.test(h.textContent), buttons: h.querySelectorAll(".hm-btn").length, empty: !!h.querySelector("[data-hm-empty]") };
  });
  if (m.height <= 150) throw new Error(`the hero is ${m.height}px high`);
  if (!m.text) throw new Error("the headline is not on the canvas");
  if (m.buttons !== 2) throw new Error(`${m.buttons} buttons`);
  if (m.empty) throw new Error("the placeholder shows");
  return `${m.height}px high, headline, ${m.buttons} buttons${box ? "" : ""}`;
});

await check("A3: the Content tab is selected and the first field has the focus", async () => {
  const sel = await contentTab().getAttribute("aria-selected");
  if (sel !== "true") throw new Error("the Content tab is not selected");
  const r = await contentFocused();
  if (!r.same) throw new Error(`the focus is on ${r.tag} "${r.label}", not the first field (${r.first})`);
  await shot("hero-insert.png");
  return `focus on ${r.tag} "${r.label}"`;
});

await check("one undo step: a single Ctrl+Z removes the hero, History lists no 'Change section content'", async () => {
  await p.evaluate(() => document.activeElement?.blur?.());
  await p.locator("[data-hm-bs-history]").first().click();
  await settle(300);
  const rows = (await p.locator(".hm-bs-history__row span:first-child").allInnerTexts()).map((t) => t.trim());
  await p.keyboard.press("Escape");
  await settle(200);
  if (rows.some((t) => /Change section content/i.test(t))) throw new Error("History: " + rows.join(" | "));
  if (rows.length !== 2) throw new Error("History has " + rows.length + " rows: " + rows.join(" | "));
  await undoByKey();
  if ((await sig()) !== startSig) throw new Error("one Ctrl+Z did not bring the page back to its start");
  return `History ${rows.join(" > ")}; one Ctrl+Z restores the page`;
});

await check("insert by drag also opens the Content tab with the first field focused", async () => {
  await showAdd();
  const had = (await heroes()).length;
  const t = await tile().boundingBox();
  const fr = await (await frame()).frameElement();
  const c = await fr.boundingBox();
  await p.mouse.move(t.x + t.width / 2, t.y + t.height / 2);
  await p.mouse.down();
  await p.mouse.move(t.x + t.width / 2 + 30, t.y + t.height / 2 + 10, { steps: 4 });
  await p.mouse.move(c.x + c.width / 2, c.y + 120, { steps: 12 });
  await settle(300);
  await p.mouse.move(c.x + c.width / 2, c.y + 126, { steps: 3 });
  await p.mouse.up();
  await settle(900);
  if ((await heroes()).length !== had + 1) throw new Error("the drag did not insert a hero");
  const sel = await contentTab().getAttribute("aria-selected");
  if (sel !== "true") throw new Error("the Content tab is not selected after the drag");
  const r = await contentFocused();
  if (!r.same) throw new Error(`the focus is on ${r.tag} "${r.label}", not the first field`);
  return `focus on ${r.tag} "${r.label}"`;
});

await check("an emptied section shows the placeholder hint in the editor", async () => {
  const form = p.locator('[data-hm-bs-pane="insp"] [role="tabpanel"] form').first();
  const texts = form.locator('input[type="text"], input[type="url"]:not([readonly]), input:not([type]), textarea:not([hidden])');
  const rich = form.locator('[contenteditable="true"]');
  let n = 0;
  for (const loc of [texts, rich]) {
    for (let i = 0, c = await loc.count(); i < c; i++) {
      const el = loc.nth(i);
      if (!(await el.isVisible())) continue;
      if (loc === rich) { await el.click(); await el.press("Control+a"); await el.press("Backspace"); await settle(600); }
      else { await el.fill(""); await el.dispatchEvent("change"); }
      n++;
    }
  }
  await p.evaluate(() => document.activeElement?.blur?.());
  await settle(1200);
  const f = await frame();
  const hint = f.locator("[data-hm-empty]").first();
  await hint.waitFor({ state: "attached", timeout: 5000 });
  await hint.scrollIntoViewIfNeeded().catch(() => {});
  const text = (await hint.innerText()).replace(/\s+/g, " ").trim();
  if (!/This section is empty/.test(text)) throw new Error(`hint reads "${text}"`);
  await shot("empty-placeholder.png");
  return `"${text}" (${n} fields cleared)`;
});

await check("the page is left as found: nothing was saved, the document is unchanged after a reload", async () => {
  await open(href);
  if ((await sig()) !== startSig) throw new Error("the reloaded document differs from the start");
  return `${blocked.length} save requests aborted`;
});

if (errors.length) { bad++; console.log("FAIL page errors: " + errors.slice(0, 3).join(" | ")); }
await b.close();
console.log(bad ? `\n${bad} check(s) failed` : "\nall checks passed");
process.exit(bad ? 1 : 0);
