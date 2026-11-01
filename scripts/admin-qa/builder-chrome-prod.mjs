// Builder chrome, driven like a user on a real page: the top bar, the hint bar, the Insert tab and the page shortcuts
// (Esc, Alt+Up/Down, Ctrl/Cmd+D, Backspace, Delete, Up/Down, Enter, 1/2/3, /) with focus inside the canvas frame, plus
// in-place text edit, a padding drag and a device switch, each undone and redone.
//
// NEVER SAVES. Every POST to the save / autosave / recovery endpoints is aborted in the browser and counted (QA_ALLOW_REMOTE_SAVE
// is not read; this script does not need it). The page is left as found: the script inserts a scratch section, edits only that,
// undoes everything and ends by reloading and comparing the document and the status line with the values it started from.
//
// Runs two ways:
//   local   PG_BIN=<pg bin dir> bash scripts/admin-qa/run-local.sh builder-chrome-prod.mjs     (seeded throwaway instance, page "About")
//   remote  QA_ADMIN=<admin path prefix> QA_BASE_URL=<origin> QA_EMAIL=<owner email> QA_PASSWORD=<password> \
//             QA_PAGE_NAME=<page title> [QA_ENTITY=<page id>] [QA_SHOTS=<dir>] node scripts/admin-qa/builder-chrome-prod.mjs
// Screenshots (1600x950, light; chrome-phone.png at 390x844): chrome-topbar.png, chrome-hint.png, chrome-insert.png,
// chrome-section.png, chrome-block.png and chrome-phone.png, in QA_SHOTS when it is set. Exit 1 on any problem.
import fs from "node:fs";
import path from "node:path";

// lib.mjs reads these at import time: map the remote-friendly names first.
if (process.env.QA_BASE_URL) process.env.BASE = process.env.QA_BASE_URL;
const { launch, base, admin, login } = await import("./lib.mjs");

const NAME = process.env.QA_PAGE_NAME || "About";
const ENTITY = process.env.QA_ENTITY || "";
const SHOTS = process.env.QA_SHOTS || "";
const MOD = process.platform === "darwin" ? "Meta" : "Control";

if (SHOTS) fs.mkdirSync(SHOTS, { recursive: true });
const b = await launch("chromium");
let bad = 0;
const errors = [];
const blocked = [];
const popups = [];

// Nothing is ever saved: these endpoints are cut in the browser and counted.
async function guard(ctx) {
  await ctx.route(/\/builder\/(save|autosave|recovery)(\/discard)?$/, (r) => {
    blocked.push(`${r.request().method()} ${new URL(r.request().url()).pathname.replace(/[0-9a-f-]{36}/, "<id>")}`);
    return r.abort("failed");
  });
  ctx.on("page", (pg) => popups.push(pg.url()));
}
async function session() {
  const ctx = await b.newContext({ viewport: { width: 1600, height: 950 }, bypassCSP: false });
  await ctx.addCookies([{ name: "hm_theme", value: "light", url: base }]);
  await guard(ctx);
  const pg = await ctx.newPage();
  await login(pg);
  pg.on("pageerror", (e) => errors.push(e.message));
  pg.on("dialog", (d) => d.accept().catch(() => {})); // "leave the page?" when the draft is discarded
  return { ctx, pg };
}

const main = await session();
const p = main.pg;

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
async function open(href, pg = p) {
  await pg.goto(base + href, { waitUntil: "load" });
  await pg.waitForSelector(TREE, { timeout: 15000 });
  await pg.waitForFunction(() => !!window.__hmBuilder, null, { timeout: 8000 });
  await pg.waitForTimeout(800);
}

// The page point of a canvas element (the frame is scaled to fit its pane).
async function canvasPoint(id, fx = 0.5, fy = 0.5) {
  const f = await frame();
  const box = await (await f.frameElement()).boundingBox();
  const r = await f.evaluate(([i, ax, ay]) => {
    const e = document.querySelector(`[data-hm-node="${i}"], [data-hm-section="${i}"]`);
    if (!e) return null;
    e.scrollIntoView({ block: "center" });
    const bb = e.getBoundingClientRect();
    return { x: bb.x + bb.width * ax, y: bb.y + bb.height * ay, w: innerWidth };
  }, [id, fx, fy]);
  if (!r) return null;
  const k = box.width / r.w;
  return { x: box.x + r.x * k, y: box.y + r.y * k };
}
async function pointOf(id, fx, fy) {
  for (let i = 0; i < 20; i++) {
    const at = await canvasPoint(id, fx, fy).catch(() => null);
    if (at) return at;
    await settle(300);
  }
  throw new Error(`no canvas element for ${id}`);
}
/**
 * Select an element (Navigator row) and leave keyboard focus inside the canvas frame, without a click: a click on the page
 * selects the section around a block first. The page click path is covered by the in-place text edit below.
 */
async function clickCanvas(id) {
  await navRow(id);
  await settle(300);
  await p.evaluate(() => document.activeElement?.blur?.());
  const f = await frame();
  await (await f.frameElement()).focus();
  await f.evaluate(() => window.focus());
  await settle(300);
  if ((await sel())[0] !== id) throw new Error(`selecting ${id} gave ${await sel()}`);
}
const undo = async () => { await p.evaluate(() => document.activeElement?.blur?.()); await p.locator("[data-hm-bs-undo]").first().click(); await settle(400); };
const redo = async () => { await p.evaluate(() => document.activeElement?.blur?.()); await p.locator("[data-hm-bs-redo]").first().click(); await settle(400); };
const device = async (d) => { await p.locator(`[data-hm-bs-device="${d}"]`).click(); await settle(500); };
const pressed = (d) => p.locator(`[data-hm-bs-device="${d}"]`).getAttribute("aria-pressed");
const padInput = (side) => p.locator(`.hm-bs-bm__p input[data-side="${side}"]`).first();
const authored = async (id, bp, k) => (await find(id))?.s?.[bp]?.[k] ?? null;
const px = (v) => (v === null ? null : String(v).replace(/px$/, ""));
const insertTab = async () => {
  if ((await p.locator('[data-hm-bs-ltab="add"]').getAttribute("aria-selected")) !== "true") await p.locator('[data-hm-bs-ltab="add"]').click();
  await settle(300);
};
const navRow = async (id) => {
  if ((await p.locator('[data-hm-bs-ltab="nav"]').getAttribute("aria-selected")) !== "true") await p.locator('[data-hm-bs-ltab="nav"]').click();
  await settle(200);
  await row(id).click();
};
const card = (key) => p.locator(`[data-hm-bs-palette] [data-pal="${key}"]`).first();

/** Press a key with focus in the frame; the change must be exactly one history step: undo once restores, redo reapplies. */
async function oneStep(label, key, clickId) {
  await clickCanvas(clickId);
  const before = await sig();
  await p.keyboard.press(key);
  await settle(500);
  const after = await sig();
  if (after === before) throw new Error(`${label}: ${key} changed nothing`);
  await undo();
  if ((await sig()) !== before) throw new Error(`${label}: one undo did not restore the document`);
  await redo();
  if ((await sig()) !== after) throw new Error(`${label}: redo did not reapply`);
  return after;
}

let href = "", startSig = "", startStatus = "";

await check("setup: open the page, remember its document and status", async () => {
  href = await pageHref();
  await open(href);
  startSig = await sig();
  startStatus = await status();
  if (!(await p.locator(TREE).count())) throw new Error("the Navigator has no rows");
  return `status "${startStatus}"`;
});

await check("A1: the device switch is centred in the bar; Navigator is an icon-only toggle with aria-expanded", async () => {
  const bar = await p.locator(".hm-editor-bar").first().boundingBox();
  const dev = await p.locator(".hm-bs__devices").first().boundingBox();
  const d = Math.abs(dev.x + dev.width / 2 - (bar.x + bar.width / 2));
  if (d > 24) throw new Error(`the device switch centre is ${d.toFixed(1)}px off the bar centre`);
  const nav = p.getByRole("button", { name: "Navigator" }).first();
  if ((await nav.innerText()).trim()) throw new Error(`Navigator shows text "${(await nav.innerText()).trim()}"`);
  const ex = await nav.getAttribute("aria-expanded");
  if (!["true", "false"].includes(ex)) throw new Error(`aria-expanded is ${ex}`);
  await shot("chrome-topbar.png");
  return `${d.toFixed(1)}px off centre, Navigator aria-expanded=${ex}`;
});

await check("A4: a fresh browser shows the hint bar; Dismiss hints removes it for good (stored as bar)", async () => {
  const fresh = await session();
  try {
    await open(href, fresh.pg);
    const hint = fresh.pg.locator(".hm-bs-hint");
    await hint.first().waitFor({ state: "visible", timeout: 5000 });
    if (p === fresh.pg) throw new Error("not a fresh page");
    await fresh.pg.screenshot({ path: SHOTS ? path.join(SHOTS, "chrome-hint.png") : undefined });
    await fresh.pg.getByRole("button", { name: "Dismiss hints" }).click();
    await fresh.pg.waitForTimeout(300);
    await fresh.pg.reload({ waitUntil: "load" });
    await fresh.pg.waitForSelector(TREE, { timeout: 15000 });
    await fresh.pg.waitForTimeout(1200);
    if (await hint.count()) throw new Error("the hint bar is back after the reload");
    const kept = await fresh.pg.evaluate(() => Object.entries(localStorage).filter(([k]) => k.startsWith("hm-builder-hints:")).map(([k, v]) => v));
    if (!kept.some((v) => /"bar"/.test(v))) throw new Error(`localStorage hints: ${JSON.stringify(kept)}`);
    return `hint shown, dismissed, gone after reload, stored ${kept[0]}`;
  } finally {
    await fresh.ctx.close();
  }
});

let sec = null, head = null;

await check('A5: "/" opens the Insert tab with the search focused; the tab is named Insert', async () => {
  await p.locator(TREE).first().click();
  await settle(300);
  await p.locator(TREE).first().focus();
  await p.keyboard.press("/");
  await settle(400);
  const tab = p.getByRole("tab", { name: "Insert", exact: true });
  if ((await tab.getAttribute("aria-selected")) !== "true") throw new Error("the Insert tab is not selected after /");
  const ok = await p.evaluate(() => document.activeElement?.id === "hm-bs-pal-q");
  if (!ok) throw new Error("#hm-bs-pal-q is not focused");
  await shot("chrome-insert.png");
  await p.keyboard.press("Escape");
  return "Insert tab open, #hm-bs-pal-q focused";
});

await check("insert a scratch section after the selection from the Insert tab, and a heading inside it", async () => {
  const first = (await tops())[0];
  await navRow(first.id);
  await settle(400);
  const known = new Set(flat(await tops()).map((n) => n.id));
  await insertTab();
  await card("container").click();
  await settle(900);
  sec = (await tops()).find((n) => !known.has(n.id));
  if (!sec || sec.t !== "container") throw new Error("no new top-level container after the Insert card");
  await navRow(sec.id);
  await settle(400);
  await insertTab();
  await card("heading").click();
  await settle(900);
  head = flat([(await tops()).find((n) => n.id === sec.id)]).find((n) => n.t === "heading");
  if (!head) throw new Error("no heading inside the scratch section");
  await clickCanvas(sec.id);
  await shot("chrome-section.png");
  return `section ${sec.id}, heading ${head.id}`;
});

await check("Esc selects the parent (no history step)", async () => {
  await clickCanvas(head.id);
  const before = await sig();
  await p.keyboard.press("Escape");
  await settle(400);
  if ((await sel())[0] !== sec.id) throw new Error(`Esc selected ${await sel()}, wanted the section ${sec.id}`);
  if ((await sig()) !== before) throw new Error("Esc changed the document");
  return "heading -> section";
});

await check("Enter on the section selects its first child (page focus); Enter on the heading in the frame edits its text", async () => {
  await navRow(sec.id);
  await settle(300);
  await p.evaluate(() => document.activeElement?.blur?.()); // focus on the page, outside the frame and the Navigator
  await p.keyboard.press("Enter");
  await settle(400);
  if ((await sel())[0] !== head.id) throw new Error(`Enter selected ${await sel()}, wanted ${head.id}`);
  await shot("chrome-block.png");
  await clickCanvas(head.id);
  await p.keyboard.press("Enter");
  await settle(400);
  const f = await frame();
  const editing = await f.evaluate(() => !!document.querySelector("[contenteditable]"));
  if (!editing) throw new Error("Enter on the heading did not start a text edit");
  await p.keyboard.press("Escape");
  await settle(300);
  return "first child selected, then text edit";
});

await check("edit text in place; undo and redo", async () => {
  const before = await sig();
  const f = await frame();
  const at = await pointOf(head.id);
  await p.mouse.dblclick(at.x, at.y);
  await settle(400);
  if (!(await f.evaluate(() => !!document.querySelector("[contenteditable]")))) throw new Error("double-click did not start a text edit");
  await p.keyboard.press("End");
  await p.keyboard.type(" QA");
  await settle(200);
  await p.locator(TREE).first().focus();
  await settle(500);
  const after = await sig();
  if (after === before) throw new Error("typing changed nothing");
  await undo();
  if ((await sig()) !== before) throw new Error("undo did not restore the text");
  await redo();
  if ((await sig()) !== after) throw new Error("redo did not reapply the text");
  await undo();
  return "typed, undone, redone, undone";
});

const sigBeforeMoves = await sig();
await check("Alt+Up then Alt+Down move the section, one undo step each", async () => {
  const idx = async () => (await tops()).findIndex((n) => n.id === sec.id);
  const i0 = await idx();
  if (i0 < 1) throw new Error("the scratch section is first: nothing above it");
  await oneStep("Alt+Up", "Alt+ArrowUp", sec.id);
  const i1 = await idx();
  if (i1 !== i0 - 1) throw new Error(`Alt+Up: index ${i0} -> ${i1}`);
  await oneStep("Alt+Down", "Alt+ArrowDown", sec.id);
  const i2 = await idx();
  if (i2 !== i0) throw new Error(`Alt+Down: index ${i1} -> ${i2}`);
  await undo();
  await undo();
  if ((await idx()) !== i0 || (await sig()) !== sigBeforeMoves) throw new Error("two undos did not bring the document back");
  return `index ${i0} -> ${i1} -> ${i0}`;
});

let dupId = null;
await check("Ctrl/Cmd+Z in the frame undoes a step, and Ctrl/Cmd+D duplicates in one step with no dialog or popup", async () => {
  const n0 = (await tops()).length;
  const pages0 = popups.length;
  const known = new Set((await tops()).map((n) => n.id));
  await oneStep("Ctrl+D", `${MOD}+d`, sec.id);
  if ((await tops()).length !== n0 + 1) throw new Error(`Ctrl+D: ${n0} -> ${(await tops()).length} sections`);
  dupId = (await tops()).find((n) => !known.has(n.id))?.id ?? null;
  if (popups.length !== pages0) throw new Error("Ctrl+D opened a new page");
  if (await p.locator("dialog[open]").count()) throw new Error("a dialog is open after Ctrl+D");
  // the frame's own Ctrl+Z
  const after = await sig();
  await clickCanvas(sec.id);
  await p.keyboard.press(`${MOD}+z`);
  await settle(500);
  if ((await tops()).length !== n0) throw new Error("Ctrl+Z in the frame did not undo the duplicate");
  await p.keyboard.press(`${MOD}+Shift+z`);
  await settle(500);
  if ((await sig()) !== after) throw new Error("Ctrl+Shift+Z in the frame did not redo");
  return `${n0} -> ${n0 + 1} sections; undone and redone from the frame`;
});

await check("Backspace and Delete remove the selected duplicate, one undo step each", async () => {
  if (!dupId) throw new Error("no duplicate to remove");
  const n0 = (await tops()).length;
  await oneStep("Backspace", "Backspace", dupId);
  if ((await tops()).length !== n0 - 1) throw new Error("Backspace did not remove the section");
  await undo();
  await oneStep("Delete", "Delete", dupId);
  if ((await tops()).length !== n0 - 1) throw new Error("Delete did not remove the section");
  await undo();
  await clickCanvas(dupId);
  await p.keyboard.press("Delete"); // leave only the original scratch section behind
  await settle(500);
  if ((await tops()).some((n) => n.id === dupId)) throw new Error("the duplicate is still there");
  return `${n0} -> ${n0 - 1} sections by each key`;
});

await check("Up and Down step between sections on the page (no history step)", async () => {
  const ids = (await tops()).map((n) => n.id);
  const i = ids.indexOf(sec.id);
  const [firstKey, other, back] = i > 0 ? ["ArrowUp", ids[i - 1], "ArrowDown"] : ["ArrowDown", ids[i + 1], "ArrowUp"];
  await clickCanvas(sec.id);
  const before = await sig();
  await p.keyboard.press(firstKey);
  await settle(400);
  if ((await sel())[0] !== other) throw new Error(`${firstKey} selected ${await sel()}, wanted ${other}`);
  await p.keyboard.press(back);
  await settle(400);
  if ((await sel())[0] !== sec.id) throw new Error(`${back} selected ${await sel()}, wanted ${sec.id}`);
  if ((await sig()) !== before) throw new Error("the arrows changed the document");
  return `section ${i} -> ${ids.indexOf(other)} -> ${i}`;
});

await check("1, 2, 3 switch the device and the pressed state; / opens Insert", async () => {
  await clickCanvas(sec.id);
  const seen = [];
  for (const [k, d] of [["2", "md"], ["3", "sm"], ["1", "base"]]) {
    await p.keyboard.press(k);
    await settle(500);
    if ((await pressed(d)) !== "true") throw new Error(`${k} did not press ${d}`);
    seen.push(d);
    await clickCanvas(sec.id);
  }
  await p.keyboard.press("/");
  await settle(400);
  if (!(await p.evaluate(() => document.activeElement?.id === "hm-bs-pal-q"))) throw new Error('"/" in the canvas did not focus the Insert search');
  await p.keyboard.press("Escape");
  return `${seen.join(" ")}, then / focused the Insert search`;
});

await check("padding drag on Phone lands on sm only; undo and redo; device switch by the bar", async () => {
  await device("sm");
  await navRow(sec.id);
  await settle(400);
  await p.locator('[role="tab"][data-tab="style"]').first().click().catch(() => {});
  await padInput("top").waitFor({ state: "visible", timeout: 4000 });
  const deskPt = await authored(sec.id, "base", "pt");
  await padInput("top").scrollIntoViewIfNeeded();
  const hb = await padInput("top").boundingBox();
  const x = hb.x + hb.width / 2, y = hb.y + hb.height / 2;
  await p.mouse.move(x, y);
  await p.mouse.down();
  for (let i = 1; i <= 10; i++) await p.mouse.move(x, y - 2 * i);
  await p.mouse.up();
  await settle(500);
  const v = await authored(sec.id, "sm", "pt");
  if (!(Number(px(v)) > 0)) throw new Error(`the drag did not set sm.pt: ${v}`);
  if ((await authored(sec.id, "base", "pt")) !== deskPt) throw new Error("the Desktop pt changed");
  if ((await authored(sec.id, "md", "pt")) !== null) throw new Error("the Tablet pt was written");
  await undo();
  if ((await authored(sec.id, "sm", "pt")) !== null) throw new Error("undo did not clear sm.pt");
  await redo();
  if ((await authored(sec.id, "sm", "pt")) !== v) throw new Error("redo did not reapply sm.pt");
  await undo();
  await p.setViewportSize({ width: 390, height: 844 });
  await settle(600);
  await shot("chrome-phone.png");
  await p.setViewportSize({ width: 1600, height: 950 });
  await settle(400);
  await device("base");
  return `sm.pt ${v}; base.pt still ${deskPt}`;
});

await check("the page is left as found: undo everything, reload discarding the unsaved state, compare document and status", async () => {
  await p.evaluate(() => document.activeElement?.blur?.());
  await p.keyboard.press("Escape");
  await p.locator(TREE).first().focus().catch(() => {});
  for (let i = 0; i < 40 && (await sig()) !== startSig; i++) { await p.keyboard.press(`${MOD}+z`); await settle(120); }
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
console.log("builder-chrome-prod: all checks passed");
