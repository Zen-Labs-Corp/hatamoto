// Builder inspector, driven like a user: select a section, change its padding through the box model (typed value, arrow
// keys, a scrub drag), switch to Phone and change the padding there (the override lands on the Phone key only, Desktop
// is untouched), set a value in the Hover state, undo and redo every step, select a block ("Block in <section>") and
// take screenshots.
//
// Runs two ways:
//   local   PG_BIN=<pg bin dir> bash scripts/admin-qa/run-local.sh builder-inspector.mjs     (seeded throwaway instance)
//   remote  QA_ADMIN=<admin path prefix> QA_BASE_URL=<origin> QA_EMAIL=<owner email> QA_PASSWORD=<password> \
//             QA_PAGE_NAME=<page title> [QA_ENTITY=<page id>] node scripts/admin-qa/builder-inspector.mjs
// Screenshots (all 1600x950): inspector-section.png, inspector-scrub.png, inspector-phone.png and inspector-block.png.
// The page is left exactly as found: the script inserts a scratch section (and a heading in it), edits only that and undoes
// everything. It never saves or publishes (not even with QA_ALLOW_REMOTE_SAVE=1, which this script does not need): every POST
// to the save / autosave / recovery endpoints is aborted in the browser and counted, and the last check reloads and compares
// the document and status line. Exit 1 on any problem.
import fs from "node:fs";
import path from "node:path";

// lib.mjs reads these at import time: map the remote-friendly names first.
if (process.env.QA_BASE_URL) process.env.BASE = process.env.QA_BASE_URL;
const { launch, base, admin, login, pw } = await import("./lib.mjs");

const NAME = process.env.QA_PAGE_NAME || "About";
const ENTITY = process.env.QA_ENTITY || "";
const SHOTS = process.env.QA_SHOTS || "";

if (SHOTS) fs.mkdirSync(SHOTS, { recursive: true });
const b = await launch("chromium");
const ctx = await b.newContext({ viewport: { width: 1600, height: 950 }, bypassCSP: false });
await ctx.addCookies([{ name: "hm_theme", value: "light", url: base }]);
const p = await ctx.newPage();
await login(p);
let bad = 0;
const errors = [];
p.on("pageerror", (e) => errors.push(e.message));
p.on("dialog", (d) => d.accept().catch(() => {})); // "leave the page?" when the draft is discarded
// Nothing is ever saved: these endpoints are cut in the browser and counted.
const blocked = [];
await ctx.route(/\/builder\/(save|autosave|recovery)(\/discard)?$/, (r) => {
  blocked.push(`${r.request().method()} ${new URL(r.request().url()).pathname.replace(/[0-9a-f-]{36}/, "<id>")}`);
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
const doc = () => p.evaluate(() => window.__hmBuilder.doc());
const tops = async () => (await doc()).nodes;
const flat = (ns) => ns.flatMap((n) => [n, ...flat(n.c ?? [])]);
const find = async (id) => flat(await tops()).find((n) => n.id === id);
const sel = () => p.evaluate(() => window.__hmBuilder.selection());
const sig = async () => JSON.stringify(await tops());
const status = () => p.locator("[data-hm-bs-state]").first().innerText().then((t) => t.replace(/\s+/g, " ").trim());
const row = (id) => p.locator(`${TREE}[data-id="${id}"]`);
const shot = async (name) => { if (SHOTS) await p.screenshot({ path: path.join(SHOTS, name) }); };

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


const authored = async (id, bp, k) => (await find(id))?.s?.[bp]?.[k] ?? null;
const px = (v) => (v === null ? null : String(v).replace(/px$/, ""));

const undo = async () => { await p.evaluate(() => document.activeElement?.blur?.()); await p.locator("[data-hm-bs-undo]").first().click(); await settle(400); };
const redo = async () => { await p.evaluate(() => document.activeElement?.blur?.()); await p.locator("[data-hm-bs-redo]").first().click(); await settle(400); };
const device = async (d) => { await p.locator(`[data-hm-bs-device="${d}"]`).click(); await settle(500); };
const padInput = (side) => p.locator(`.hm-bs-bm__p input[data-side="${side}"]`).first();
const styleTab = async () => { await p.locator('[role="tab"][data-tab="style"]').first().click().catch(() => {}); await settle(400); };
async function select(id) {
  await row(id).click();
  await settle(500);
  if ((await sel())[0] !== id) throw new Error(`could not select ${id} (selected ${await sel()})`);
  await styleTab();
  await padInput("top").waitFor({ state: "visible", timeout: 4000 });
}
async function typePad(side, text) {
  const f = padInput(side);
  await f.click();
  await f.fill(text);
  await f.press("Enter");
  await settle(500);
}
// A change undone and redone: the authored value goes back to `before`, then to `after` again.
async function undoRedo(id, bp, k, before, after) {
  await undo();
  if ((await authored(id, bp, k)) !== before) throw new Error(`undo: ${bp}.${k} is ${await authored(id, bp, k)}, wanted ${before}`);
  await redo();
  if ((await authored(id, bp, k)) !== after) throw new Error(`redo: ${bp}.${k} is ${await authored(id, bp, k)}, wanted ${after}`);
}

let href = "", startSig = "", startStatus = "";
let sec = null, head = null; // the scratch section and its heading

await check("setup: open the page, remember its document and status, insert a scratch section with a heading", async () => {
  href = await pageHref();
  await open(href);
  startSig = await sig();
  startStatus = await status();
  await p.evaluate(() => window.__hmBuilder.insertAt({ parent: "", index: 0 }, "container"));
  await settle(700);
  sec = (await tops())[0];
  if (sec.t !== "container") throw new Error("no container at the top after the insert: " + sec.t);
  await p.evaluate((id) => window.__hmBuilder.insertAt({ parent: id, index: 0 }, "heading"), sec.id);
  await settle(900);
  head = flat([(await tops())[0]]).find((n) => n.t === "heading");
  if (!head) throw new Error("no heading in the scratch section");
  return `scratch section ${sec.id}, heading ${head.id}; status "${startStatus}"`;
});

await check("select the section: header, context line, groups and box model show", async () => {
  await select(sec.id);
  const ctx = p.locator(".hm-bs-ctx");
  await ctx.waitFor({ state: "visible", timeout: 4000 });
  const pill = (await ctx.locator(".hm-bs-ctx__bp").innerText()).trim();
  if (!/Desktop/i.test(pill)) throw new Error(`width pill reads "${pill}"`);
  if (!(await p.locator('select[aria-label="State"]').first().isVisible())) throw new Error("no State dropdown");
  if (!(await p.locator('.hm-bs-bm__p input[data-side]').count() === 4)) throw new Error("the box model does not show four padding numbers");
  await shot("inspector-section.png");
  return `pill "${pill}", State dropdown, four padding numbers`;
});

let top0 = null;
await check("Desktop: type 48 in padding top; undo and redo", async () => {
  top0 = await authored(sec.id, "base", "pt");
  await typePad("top", "48");
  const v = await authored(sec.id, "base", "pt");
  if (px(v) !== "48") throw new Error(`pt is ${v}`);
  await undoRedo(sec.id, "base", "pt", top0, v);
  return `pt ${top0} -> ${v}`;
});

await check("Desktop: ArrowUp (+1) and Shift+ArrowUp (+10) on the padding top number, one undo step each", async () => {
  await select(sec.id);
  const f = padInput("top");
  await f.focus();
  await f.press("ArrowUp");
  await settle(400);
  if (px(await authored(sec.id, "base", "pt")) !== "49") throw new Error("ArrowUp gave " + (await authored(sec.id, "base", "pt")));
  await padInput("top").focus();
  await padInput("top").press("Shift+ArrowUp");
  await settle(400);
  const v = await authored(sec.id, "base", "pt");
  if (px(v) !== "59") throw new Error("Shift+ArrowUp gave " + v);
  await undo();
  if (px(await authored(sec.id, "base", "pt")) !== "49") throw new Error("one undo did not step back by the Shift+Arrow press");
  await redo();
  if (px(await authored(sec.id, "base", "pt")) !== "59") throw new Error("redo did not re-apply");
  await undo(); await undo();
  if (px(await authored(sec.id, "base", "pt")) !== "48") throw new Error("two undos did not return to 48");
  await redo(); await redo();
  return "48 -> 49 -> 59, each press its own step";
});

await check("Desktop: scrub the padding top number by dragging; one undo entry for the whole drag", async () => {
  await select(sec.id);
  const before = await authored(sec.id, "base", "pt");
  await padInput("top").scrollIntoViewIfNeeded();
  const hb = await padInput("top").boundingBox();
  const x = hb.x + hb.width / 2, y = hb.y + hb.height / 2;
  await p.mouse.move(x, y);
  await p.mouse.down();
  for (let i = 1; i <= 10; i++) await p.mouse.move(x, y - 2 * i); // up is bigger
  await settle(250);
  await shot("inspector-scrub.png");
  await p.mouse.up();
  await settle(500);
  const after = await authored(sec.id, "base", "pt");
  if (!(Number(px(after)) > Number(px(before)))) throw new Error(`the drag did not raise pt: ${before} -> ${after}`);
  await undoRedo(sec.id, "base", "pt", before, after);
  return `pt ${before} -> ${after} in one undo step`;
});

let deskPt = null;
await check("Phone: set padding top; it lands on sm only, Desktop and Tablet keys stay; undo and redo", async () => {
  deskPt = await authored(sec.id, "base", "pt");
  await device("sm");
  const pill = (await p.locator(".hm-bs-ctx__bp").innerText()).trim();
  if (!/Phone/i.test(pill)) throw new Error(`width pill reads "${pill}"`);
  await styleTab();
  await typePad("top", "24");
  const v = await authored(sec.id, "sm", "pt");
  if (px(v) !== "24") throw new Error("sm.pt is " + v);
  if ((await authored(sec.id, "base", "pt")) !== deskPt) throw new Error("the Desktop pt changed");
  if ((await authored(sec.id, "md", "pt")) !== null) throw new Error("the Tablet pt was written");
  await shot("inspector-phone.png");
  await undoRedo(sec.id, "sm", "pt", null, v);
  if ((await authored(sec.id, "base", "pt")) !== deskPt) throw new Error("the Desktop pt changed after undo/redo");
  return `sm.pt ${v}; base.pt still ${deskPt}`;
});

await check("Phone: the Hover state writes the sm:hover key (a theme colour pill in Fill); Normal goes back; undo and redo", async () => {
  await p.locator('select[aria-label="State"]').first().selectOption("hover");
  await settle(500);
  const note = (await p.locator(".hm-bs-ctx__note").innerText()).trim();
  if (!/Hover/.test(note)) throw new Error(`the context note reads "${note}"`);
  if (await padInput("top").count()) throw new Error("Spacing is offered in a state (the table gives it no states)");
  const fill = p.locator(".hm-bs-grp__t", { hasText: /^Fill/ }).first();
  if ((await fill.getAttribute("aria-expanded")) !== "true") await fill.click();
  const pill = p.locator(".hm-bs-swatches .hm-bs-pill").first();
  await pill.scrollIntoViewIfNeeded();
  await pill.click();
  await settle(500);
  const k = "sm:hover";
  const v = await authored(sec.id, k, "bg");
  if (!v) throw new Error(`${k}.bg is not set`);
  if ((await authored(sec.id, "sm", "bg")) !== null || (await authored(sec.id, "base", "bg")) !== null) throw new Error("the colour leaked into a stateless key");
  await undoRedo(sec.id, k, "bg", null, v);
  await p.locator('select[aria-label="State"]').first().selectOption("");
  await settle(300);
  return `${k}.bg ${v}`;
});

await check("back on Desktop, select the heading: header reads Block in <section>", async () => {
  await device("base");
  await row(head.id).click();
  await settle(600);
  await styleTab();
  const sub = (await p.locator(".hm-bs-sub").first().innerText()).replace(/\s+/g, " ").trim();
  if (!/^Block in /.test(sub)) throw new Error(`header says "${sub}"`);
  await shot("inspector-block.png");
  return `"${sub}"`;
});

await check("narrowest inspector (16rem): ring captions clear of the top numbers, the size text inside its box", async () => {
  await select(sec.id); // the section's Spacing group is open
  await p.locator('[data-hm-bs-split="insp"]').focus();
  await p.keyboard.press("Home");
  await settle(400);
  const r = await p.evaluate(() => {
    const out = [];
    for (const ring of document.querySelectorAll(".hm-bs-bm__m, .hm-bs-bm__p")) {
      const t = ring.querySelector(".hm-bs-bm__t")?.getBoundingClientRect();
      const n = ring.querySelector(".hm-bs-bm__n--top")?.getBoundingClientRect();
      if (t && n && t.bottom > n.top + 0.5 && t.right > n.left && t.left < n.right) out.push(`${ring.className} caption overlaps the top number`);
    }
    const el = document.querySelector(".hm-bs-bm__el");
    if (el) {
      const e = el.parentElement.getBoundingClientRect(); // the centre cell clips the size box
      for (const side of ["left", "right"]) {
        const n = el.closest(".hm-bs-bm__p, .hm-bs-bm__m").querySelector(`.hm-bs-bm__n--${side}`).getBoundingClientRect();
        if (e.right > n.left + 0.5 && e.left < n.right - 0.5 && e.bottom > n.top && e.top < n.bottom) out.push(`size box overlaps the ${side} number`);
      }
    }
    return { out, w: document.querySelector('[data-hm-bs-pane="insp"]').getBoundingClientRect().width };
  });
  if (SHOTS) await p.locator(".hm-bs-bm:visible").first().screenshot({ path: path.join(SHOTS, "inspector-narrow.png") });
  await p.locator('[data-hm-bs-split="insp"]').press("Enter"); // back to the default width
  await settle(300);
  if (r.out.length) throw new Error(r.out.join("; "));
  return `inspector ${Math.round(r.w)}px wide, no overlap, no overflow`;
});

await check("the page is left as found: undo everything, then reload discarding the unsaved state", async () => {
  await p.evaluate(() => document.activeElement?.blur?.());
  await p.keyboard.press("Escape");
  await p.locator(TREE).first().focus().catch(() => {});
  for (let i = 0; i < 40 && (await sig()) !== startSig; i++) { await p.keyboard.press("Control+z"); await settle(120); }
  if ((await sig()) !== startSig) throw new Error("undo did not restore the document");
  await open(href); // the unsaved state is discarded (the dialog is accepted)
  if ((await sig()) !== startSig) throw new Error("the document differs after the reload");
  const st = await status();
  if (st !== startStatus) throw new Error(`status "${st}", started as "${startStatus}"`);
  return `document identical, status "${st}"; ${blocked.length} save/autosave/recovery request(s) aborted in the browser: ${[...new Set(blocked)].join(", ") || "none"}`;
});

if (errors.length) { bad++; console.log("FAIL page errors: " + errors.slice(0, 3).join(" | ")); }
await b.close();
if (bad) { console.log(`${bad} check(s) failed`); process.exit(1); }
console.log("builder-inspector: all checks passed");
