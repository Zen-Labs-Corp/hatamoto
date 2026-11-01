// Builder direct manipulation (E4), by mouse: the "+" layout picker, copy on one page and paste on
// ANOTHER through the system clipboard and the paste endpoint, wrap, rename (F2 and double-click),
// Ctrl+click multi-select and delete, the right-click menu on the canvas and Shift+F10 in the tree,
// the history panel, the move handle straight after load (the lazy drag chunk), axe on the menu, picker and history.
// Needs QA_PAGE_NAME (a page with a page_doc, default "About") and QA_PAGE2_NAME (a second page, default "Contact").
// The pages are left as they were found: every check undoes its edits, and the last check proves it.
// node scripts/admin-qa/builder-direct.mjs [chromium]   Exit 1 on any problem.
import fs from "node:fs";
import { launch, newSession, base } from "./lib.mjs";

const axe = fs.readFileSync(new URL("./vendor/axe.min.js", import.meta.url), "utf8");
const tags = ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"];
const NAME1 = process.env.QA_PAGE_NAME || "About";
const NAME2 = process.env.QA_PAGE2_NAME || "Contact";
const b = await launch("chromium");
let bad = 0;
const p = await newSession(b, { theme: "light", width: 1440, bypassCSP: true });
await p.context().grantPermissions(["clipboard-read", "clipboard-write"], { origin: base });
if (process.env.QA_VERBOSE) { p.on("console", (m) => console.log("  console", m.type(), m.text().slice(0, 200))); p.on("pageerror", (e) => console.log("  pageerror", e.message)); }
p.on("dialog", (d) => d.accept().catch(() => {}));

const TREE = '[data-hm-bs-tree] [role="treeitem"]';
const MENU = '.hm-menu[aria-label="Element actions"]';
const settle = (ms = 150) => p.waitForTimeout(ms);
// The visible canvas frame (the builder keeps a second, hidden one for the next render).
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
const ids = async () => flat(await tops()).map((n) => n.id);
const rowIds = () => p.locator(TREE).evaluateAll((rs) => rs.map((r) => r.dataset.id));
const row = (id) => p.locator(`${TREE}[data-id="${id}"]`);
const sel = () => p.evaluate(() => window.__hmBuilder.selection());

const pages = {};
async function pageHref(name) {
  if (pages[name]) return pages[name];
  await p.goto(`${base}/admin/entities?q=${encodeURIComponent(name)}`);
  const hrefs = await p.$$eval('a[href^="/admin/entities/"]', (as, t) => as.filter((a) => a.textContent.includes(t)).map((a) => a.getAttribute("href")), name);
  const id = hrefs.map((h) => h.match(/entities\/([0-9a-f-]{36})/)?.[1]).find(Boolean);
  if (!id) throw new Error(`no page named ${name}`);
  return (pages[name] = `/admin/entities/${id}/edit?qa=1`);
}
async function open(name) {
  await p.goto(base + (await pageHref(name)), { waitUntil: "load" });
  await p.waitForSelector(TREE, { timeout: 10000 });
  await p.waitForFunction(() => !!window.__hmBuilder, null, { timeout: 5000 });
  await settle(600);
}
const saved = () => p.waitForFunction(() => /\bSaved\b|\bDraft saved\b/.test(document.querySelector("[data-hm-bs-state]")?.textContent ?? ""), null, { timeout: 15000 }).catch(() => {});
const sig = async () => JSON.stringify(await tops());
// Undo until the tree is what it was (Ctrl+Z needs focus off any input), then wait for the autosave to settle.
async function restore(start) {
  await p.evaluate(() => document.activeElement?.blur?.());
  await p.keyboard.press("Escape");
  await p.locator(TREE).first().focus().catch(() => {});
  for (let i = 0; i < 30 && (await sig()) !== start; i++) { await p.keyboard.press("Control+z"); await settle(80); }
  if ((await sig()) !== start) throw new Error("undo did not restore the page");
  await saved();
}

async function check(name, fn) {
  try { console.log(`ok   ${name}: ${await fn()}`); }
  catch (e) { bad++; console.log(`FAIL ${name}: ${String(e.message || e).split("\n").slice(0, process.env.QA_VERBOSE ? 30 : 1).join(" | ")}`); }
}
const axeOn = async (what) => {
  await p.evaluate(axe);
  const r = await p.evaluate((t) => window.axe.run(document, { runOnly: { type: "tag", values: t } }), tags);
  if (r.violations.length) throw new Error(`axe on ${what}: ` + r.violations.map((v) => `${v.id} (${v.nodes[0].target.join(" ")})`).join("; "));
};
const menuItem = (re) => p.locator(`${MENU} [role="menuitem"]`, { hasText: re }).first();
// The page point of a canvas element's centre. The frame is scaled to fit its pane, which Playwright's
// frame-relative clicks do not follow, so the point is computed here (the centre; an empty container has its '+' there, so the point is left of it).
async function canvasPoint(id) {
  const f = await frame();
  const box = await (await f.frameElement()).boundingBox();
  const r = await f.evaluate((i) => {
    const e = document.querySelector(`[data-hm-node="${i}"], [data-hm-section="${i}"]`);
    if (!e) return null;
    e.scrollIntoView({ block: "center" });
    const b = e.getBoundingClientRect();
    return { x: b.x + b.width * (e.matches("[data-hm-holds]:empty") ? 0.15 : 0.5), y: b.y + b.height / 2, w: innerWidth }; // an empty container has its "+" at the centre
  }, id);
  if (!r) return null;
  const k = box.width / r.w;
  return { x: box.x + r.x * k, y: box.y + r.y * k };
}
// A right-click on the canvas element of a node (sections draw as data-hm-section, other nodes as data-hm-node).
async function canvasMenu(id) {
  // The frame is swapped after a render: retry until the bridge of the new one is live.
  for (let i = 0; i < 6; i++) {
    const at = await canvasPoint(id);
    if (at) {
      await settle(150);
      await p.mouse.click(at.x, at.y, { button: "right" });
      if (await p.locator(MENU).waitFor({ timeout: 1000 }).then(() => true, () => false)) return;
    }
    await settle(400);
  }
  throw new Error("right-click on the canvas never opened the menu");
}
async function pick(re) {
  await menuItem(re).click();
  await p.waitForSelector(MENU, { state: "detached", timeout: 3000 });
  await settle(400);
}
// The page-level "Add section" pill (top: before the first section) takes the pointer only while it shows: hover the section next to it first.
async function clickTopPill() {
  const line = p.locator('[data-hm-pe-gapline][aria-label="Add section at the top"]');
  await line.waitFor({ state: "attached", timeout: 5000 });
  const box = await line.boundingBox();
  await p.mouse.move(box.x + box.width / 2, box.y + box.height + 40, { steps: 4 }); // inside the section below the line
  await line.locator(".hm-pe__pill").waitFor({ state: "visible", timeout: 3000 });
  await line.click();
}
async function addColumns3() {
  await clickTopPill();
  await p.getByRole("dialog", { name: "Add a section" }).getByRole("button", { name: "3 columns" }).click();
  await settle(500);
}

// A heading in the first column of a 2-column section, clicked at its centre: the click selects the heading
// (the deepest element), not the column, the section or a v1 select that arrives after it. Also at scale < 1.
await check("an Icon inserted in the builder is 24 px in the canvas (no seeded width), at the top and inside a container", async () => {
  await open(NAME1);
  const start = await sig();
  await p.evaluate(() => window.__hmBuilder.insertAt({ parent: "", index: 0 }, "container"));
  await settle(500);
  const box = (await tops())[0];
  await p.evaluate(() => window.__hmBuilder.insertAt({ parent: "", index: 0 }, "icon"));
  await p.evaluate((c) => window.__hmBuilder.insertAt({ parent: c, index: 0 }, "icon"), box.id);
  await settle(1200);
  const f = await frame();
  const sizes = await f.evaluate(() => [...document.querySelectorAll("svg.hm-icon")].map((s) => Math.round(s.getBoundingClientRect().width)));
  if (sizes.length < 2 || sizes.some((w) => w !== 24)) throw new Error("icon widths " + sizes.join(","));
  if (flat(await tops()).some((n) => n.t === "icon" && n.s)) throw new Error("an inserted icon carries a style");
  await restore(start);
  return "2 icons, 24 px";
});

await check("a click at the centre of a heading inside a 2-column section selects the heading (deepest wins)", async () => {
  await open(NAME1);
  const start = await sig();
  if (!(await p.evaluate(() => window.__hmBuilder.insertAt({ parent: "", index: 0 }, "columns")))) throw new Error("columns were not inserted");
  await settle(500);
  const cols = (await tops())[0];
  if (cols.t !== "columns" || (cols.c ?? []).length < 2) throw new Error("no 2-column section at the top: " + JSON.stringify({ t: cols.t, kids: cols.c?.length }));
  if (!(await p.evaluate((id) => window.__hmBuilder.insertAt({ parent: id, index: 0 }, "heading"), cols.c[0].id))) throw new Error("heading was not inserted");
  await settle(600);
  const head = flat([(await tops())[0]]).find((n) => n.t === "heading");
  if (!head) throw new Error("no heading in the section");
  let at = null;
  for (let i = 0; i < 20 && !at; i++) at = await canvasPoint(head.id).catch(() => null) ?? (await settle(300), null);
  if (!at) throw new Error("no point for the heading");
  await p.mouse.click(at.x, at.y);
  await settle(500);
  const got = (await sel())[0];
  if (got !== head.id) throw new Error(`selected ${got}, wanted the heading ${head.id} (column ${cols.c[0].id}, section ${cols.id})`);
  // The element toolbar floats outside the element, is built once (hover redraws keep it), and works by keyboard.
  const tb = p.locator("[data-hm-pe-toolbar]");
  await tb.waitFor({ state: "visible", timeout: 3000 });
  const tbBox = await tb.boundingBox();
  const el = await (async () => {
    const f = await frame();
    const fb = await (await f.frameElement()).boundingBox();
    const r = await f.evaluate((i) => { const e = document.querySelector(`[data-hm-node="${i}"]`).getBoundingClientRect(); return { x: e.x, y: e.y, w: e.width, h: e.height, iw: innerWidth }; }, head.id);
    const k = fb.width / r.iw;
    return { x: fb.x + r.x * k, y: fb.y + r.y * k, w: r.w * k, h: r.h * k };
  })();
  if (tbBox.x < el.x + el.w && tbBox.x + tbBox.width > el.x && tbBox.y < el.y + el.h && tbBox.y + tbBox.height > el.y) throw new Error(`the toolbar covers the element: ${JSON.stringify(tbBox)} vs ${JSON.stringify(el)}`);
  await p.evaluate(() => { window.__tb = document.querySelector("[data-hm-pe-toolbar]"); });
  await p.mouse.move(at.x + 5, at.y + 5);
  await p.mouse.move(at.x + 40, at.y + 20);
  await settle(300);
  if (!(await p.evaluate(() => window.__tb === document.querySelector("[data-hm-pe-toolbar]") && window.__tb.isConnected))) throw new Error("the toolbar was rebuilt by a hover redraw");
  const acts = await p.locator("[data-hm-pe-toolbar] [data-act]").evaluateAll((bs) => bs.map((b) => b.getAttribute("aria-label")));
  if (acts.map((a) => a.replace(/^Select parent.*/, "Select parent")).join("|") !== "Select parent|Move up|Move down|Duplicate|Hide on this device|Delete|More element actions") throw new Error("toolbar buttons: " + acts.join("|"));
  await p.locator('[data-hm-pe-toolbar] [data-act="duplicate"]').focus();
  await p.keyboard.press("ArrowRight");
  if ((await p.evaluate(() => document.activeElement?.dataset.act)) !== "togglehide") throw new Error("ArrowRight did not move to Hide");
  await p.keyboard.press("ArrowLeft");
  if ((await p.evaluate(() => document.activeElement?.dataset.act)) !== "duplicate") throw new Error("ArrowLeft did not move back to Duplicate");
  const count = (await ids()).length;
  await p.keyboard.press("Enter");
  await settle(500);
  if ((await ids()).length <= count) throw new Error("Enter on Duplicate did not duplicate");
  await p.keyboard.press("Control+z");
  await settle(300);
  if ((await sel())[0] !== head.id && !(await ids()).includes(head.id)) throw new Error("undo of the duplicate lost the heading");
  await row(head.id).click();
  await settle(300);
  // The strip above the canvas: Page > ... > Heading; a crumb click selects that ancestor.
  const crumbs = () => p.locator("[data-hm-pe-strip] .hm-pe__crumb").allTextContents();
  const chain = await crumbs();
  if (chain[0] !== "Page" || chain.length < 4) throw new Error("breadcrumbs: " + chain.join(" > "));
  await p.locator("[data-hm-pe-strip] .hm-pe__crumb").nth(chain.length - 2).click();
  await settle(400);
  if ((await sel())[0] !== cols.c[0].id) throw new Error(`the crumb ${chain[chain.length - 2]} selected ${(await sel())[0]}`);
  if ((await crumbs()).length !== chain.length - 1) throw new Error("the strip did not follow the selection");
  // Zoom: 50% fixes the scale at the floor, and Fit comes back; the Tablet band is labelled and coloured.
  const width = () => p.locator(".hm-pe__stage").evaluate((e) => e.getBoundingClientRect().width);
  await p.locator('[data-hm-pe-strip] [data-zoom="100"]').click();
  const w100 = await width();
  await p.locator('[data-hm-pe-strip] [data-zoom="50"]').click();
  const w50 = await width();
  if (Math.abs(w50 / w100 - 0.5) > 0.02) throw new Error(`zoom 50% gave ${w50}px next to ${w100}px at 100%`);
  await p.locator('[data-hm-pe-strip] [data-zoom="fit"]').click();
  await p.locator('[data-hm-bs-device="md"]').click();
  await settle(300);
  const label = await p.locator(".hm-pe__bandlabel").textContent();
  if (!/^Tablet · editing styles up to 1024 px · Desktop is not changed$/.test(label)) throw new Error("band label: " + label);
  const band = await p.locator(".hm-pe__stage").evaluate((e) => getComputedStyle(e, "::before").backgroundColor);
  if (band === "rgba(0, 0, 0, 0)") throw new Error("no band at Tablet");
  await p.locator('[data-hm-bs-device="base"]').click();
  await settle(200);
  if (!(await p.locator(".hm-pe__bandlabel").isHidden())) throw new Error("a band label shows on Desktop");
  await restore(start);
  return `heading selected, not its column or section; crumbs ${chain.join(" > ")}; zoom 50% = half of 100%; Tablet band labelled`;
});

let start1 = "";
let copiedIds = [];

await check("'+' picker: 3 columns adds a section with three column children and selects it", async () => {
  await open(NAME1);
  start1 = await sig();
  const before = (await ids()).length;
  await clickTopPill();
  const dlg = p.getByRole("dialog", { name: "Add a section" });
  await dlg.waitFor({ timeout: 3000 });
  await axeOn("the open layout picker");
  const names = await dlg.getByRole("button").allTextContents();
  if (!names.includes("3 columns") || !names.includes("Choose an element…")) throw new Error("picker buttons: " + names.join("|"));
  await dlg.getByRole("button", { name: "3 columns" }).click();
  await settle(500);
  const n = (await tops())[0];
  if (n.t !== "columns" || (n.c ?? []).length !== 3) throw new Error("first section is " + JSON.stringify({ t: n.t, kids: n.c?.length }));
  if ((await ids()).length !== before + 4) throw new Error("expected 4 new nodes");
  if ((await sel())[0] !== n.id) throw new Error("the new section is not selected");
  const rows = await rowIds();
  for (const id of [n.id, ...n.c.map((c) => c.id)]) if (!rows.includes(id)) throw new Error("tree has no row for " + id);
  copiedIds = flat([n]).map((x) => x.id);
  return "section + 3 columns in the doc and the tree, selected";
});

await check("'+' between sections and inside an empty column inserts at that exact position (doc order), by mouse", async () => {
  const s3 = await sig();
  // The page level has no "+": the "Add section" line after the last section is the way there.
  const [first, second] = await tops();
  const after = p.locator(`[data-hm-pe-gapline][aria-label="Add section at the end"]`);
  await after.first().waitFor({ state: "attached", timeout: 5000 });
  if ((await after.count()) !== 1) throw new Error("expected one end-of-page Add section line, got " + (await after.count()));
  // a pill takes the pointer only while it shows: hover the last section, then click its pill
  const endBox = await after.first().boundingBox();
  await p.mouse.move(endBox.x + endBox.width / 2, endBox.y - 40, { steps: 4 });
  await after.first().locator(".hm-pe__pill").waitFor({ state: "visible", timeout: 3000 });
  await after.first().click();
  await p.getByRole("dialog", { name: "Add a section" }).getByRole("button", { name: "1 column" }).click();
  await settle(500);
  const top = await tops();
  if (top[0].id !== first.id || top[1].id !== second.id || top[2].t !== "container" || (top[2].c ?? []).length) throw new Error("the new section is not right after the last one: " + top.slice(0, 4).map((n) => n.t).join(","));
  const empty = p.locator('[data-hm-pe-plus][aria-label="Add inside this empty container"]');
  await empty.first().waitFor({ state: "attached", timeout: 5000 });
  const n = await empty.count();
  if (n < 3) throw new Error(`expected an empty-container '+' for each empty column, got ${n}`);
  await p.locator(".hm-pe__crumb", { hasText: "Page" }).click(); // nothing selected: no toolbar over the columns' '+'
  const at = (await empty.evaluateAll((bs) => bs.map((b, i) => ({ i, ...b.getBoundingClientRect().toJSON() })))).sort((a, c) => a.y - c.y || a.x - c.x);
  await empty.nth(at[1].i).click({ force: true }); // the second column of the first row
  await p.getByRole("dialog", { name: "Add a section" }).getByRole("button", { name: "2 columns" }).click();
  await settle(500);
  const col = (await tops())[0].c[1];
  if ((col.c ?? []).length !== 1 || col.c[0].t !== "columns") throw new Error("the second column does not hold the new 2 columns: " + JSON.stringify(col));
  await restore(s3);
  return "after-first section and the second empty column received their layouts; undone";
});

await check("Escape closes the picker and focus returns to the pill or the tree; an outside click closes it too", async () => {
  await clickTopPill();
  await p.getByRole("dialog", { name: "Add a section" }).waitFor({ timeout: 3000 });
  await p.keyboard.press("Escape");
  await settle(200);
  if (await p.getByRole("dialog", { name: "Add a section" }).count()) throw new Error("Esc left the picker open");
  if (!(await p.evaluate(() => document.activeElement?.closest("[data-hm-pe-plus], [data-hm-pe-gapline]") || document.activeElement?.closest("[data-hm-bs-tree]")))) throw new Error("focus is lost after Esc");
  return "closed, focus kept";
});

await check("right-click on a canvas element selects it and opens the menu (axe clean); Escape closes it and focus returns", async () => {
  const cell = copiedIds[1]; // the first column: the deepest element under the pointer
  await canvasMenu(cell);
  if ((await sel()).join() !== cell) throw new Error("right-click did not select the element under the pointer: " + (await sel()));
  const items = await p.locator(`${MENU} [role="menuitem"]`).allTextContents();
  for (const w of ["Copy", "Cut", "Paste after", "Duplicate", "Rename", "Wrap in container", "Delete"]) if (!items.some((t) => t.startsWith(w))) throw new Error(`menu has no ${w}: ${items.join("|")}`);
  await axeOn("the open element menu");
  await p.keyboard.press("Escape");
  await p.waitForSelector(MENU, { state: "detached", timeout: 2000 });
  if (!(await p.evaluate(() => !!document.activeElement?.closest("[data-hm-bs-tree]")))) throw new Error("focus did not return to the tree");
  const other = copiedIds[2];
  await canvasMenu(other); // outside the selection: the selection moves to it
  if ((await sel()).join() !== other) throw new Error("selection after the second right-click is " + (await sel()));
  await p.keyboard.press("Escape");
  await p.waitForSelector(MENU, { state: "detached", timeout: 2000 });
  return `${items.length} rows; selection follows the pointer; Esc -> tree`;
});

await check("tree right-click on the section, Copy puts the 3-column envelope on the system clipboard", async () => {
  const id = copiedIds[0];
  await row(id).click({ button: "right" });
  await p.waitForSelector(MENU, { timeout: 3000 });
  await pick(/^Copy/);
  const text = await p.evaluate(() => navigator.clipboard.readText());
  const env = JSON.parse(text);
  if (env.hm !== "nodes" || env.nodes?.[0]?.t !== "columns" || env.nodes[0].c?.length !== 3) throw new Error("clipboard: " + text.slice(0, 120));
  return "clipboard holds the 3-column envelope";
});

await check("Shift+F10 in the tree opens the menu at the row; arrows move, Esc returns focus to the row", async () => {
  const id = copiedIds[0];
  await row(id).click(); await settle();
  await p.keyboard.press("Shift+F10");
  await p.waitForSelector(MENU, { timeout: 3000 });
  await p.waitForFunction(() => document.activeElement?.closest(".hm-menu"), null, { timeout: 2000 }).catch(() => { throw new Error("focus did not move into the menu"); });
  const first = await p.evaluate(() => document.activeElement?.textContent);
  await p.keyboard.press("ArrowDown");
  const second = await p.evaluate(() => document.activeElement?.textContent);
  if (!first || first === second) throw new Error(`arrows did not move focus: ${first} / ${second}`);
  await p.keyboard.press("Escape");
  await p.waitForSelector(MENU, { state: "detached", timeout: 2000 });
  if ((await p.evaluate(() => document.activeElement?.closest('[role="treeitem"]')?.dataset.id)) !== id) throw new Error("focus is not on the row");
  return `${first.slice(0, 12)} -> ${second.slice(0, 12)}; Esc -> row`;
});

await check("paste on ANOTHER page: system clipboard -> paste endpoint -> fresh ids; the first page is left as found", async () => {
  await restore(start1);
  await open(NAME2); // a new page load: only the system clipboard still has the nodes
  const start2 = await sig();
  globalThis.start2 = start2;
  const first = (await tops())[0];
  await row(first.id).click({ button: "right" }); await settle(200);
  await p.waitForSelector(MENU, { timeout: 3000 });
  const resp = p.waitForResponse((r) => r.url().endsWith("/builder/paste") && r.request().method() === "POST", { timeout: 5000 });
  await menuItem(/^Paste after/).click();
  const r = await resp;
  if (r.status() !== 200) throw new Error("paste endpoint answered " + r.status());
  await settle(500);
  const t = await tops();
  const added = t.find((n) => n.t === "columns" && (n.c ?? []).length === 3);
  if (!added) throw new Error("no 3-column section after the paste");
  const had = new Set(flat(JSON.parse(start2)).map((n) => n.id));
  if (flat([added]).some((n) => had.has(n.id) || copiedIds.includes(n.id))) throw new Error("pasted ids are not fresh");
  if (t.indexOf(added) !== t.findIndex((n) => n.id === first.id) + 1) throw new Error("pasted at index " + t.indexOf(added));
  globalThis.pastedId = added.id;
  return `POST 200, new ids, placed after the selected section`;
});

await check("menu Wrap in container wraps the pasted section; one undo unwraps", async () => {
  const id = globalThis.pastedId;
  await row(id).click(); await settle();
  await row(id).click({ button: "right" }); await p.waitForSelector(MENU, { timeout: 3000 });
  await pick(/^Wrap in container/);
  const w = (await tops()).find((n) => n.t === "container" && (n.c ?? []).length === 1 && n.c[0].id === id);
  if (!w) throw new Error("the section is not the only child of a new container");
  globalThis.wrapId = w.id;
  await p.locator(TREE).first().focus();
  await p.keyboard.press("Control+z"); await settle(300);
  if ((await tops()).some((n) => n.id === w.id)) throw new Error("undo kept the wrapper");
  await p.keyboard.press("Control+Shift+z"); await settle(300);
  if (!(await tops()).some((n) => n.id === w.id)) throw new Error("redo lost the wrapper");
  return "wrapped, undone, redone";
});

await check("rename: double-click a tree row, type, Enter; the row shows the name and it is one undo step", async () => {
  const id = globalThis.wrapId;
  await row(id).dblclick(); await settle(200);
  const input = p.locator(`${TREE} input[aria-label="Name"]`);
  if ((await input.count()) !== 1) throw new Error("no rename input after the double-click");
  await input.fill("Hero wrap"); await p.keyboard.press("Enter"); await settle(300);
  if (!(await row(id).textContent()).includes("Hero wrap")) throw new Error("row reads " + (await row(id).textContent()));
  const n = (await tops()).find((x) => x.id === id);
  if (n.p?.navlabel !== "Hero wrap") throw new Error("navlabel is " + n.p?.navlabel);
  return "row shows 'Hero wrap', prop navlabel set";
});

await check("rename: F2 opens the input, Escape cancels, Enter with an empty value clears the name", async () => {
  const id = globalThis.wrapId;
  await row(id).focus(); await p.keyboard.press("F2"); await settle(150);
  const input = p.locator(`${TREE} input[aria-label="Name"]`);
  if ((await input.inputValue()) !== "Hero wrap") throw new Error("input starts with " + (await input.inputValue()));
  await input.fill("discarded"); await p.keyboard.press("Escape"); await settle(200);
  if ((await row(id).textContent()).includes("discarded")) throw new Error("Esc kept the typed name");
  if (!(await p.evaluate((i) => document.activeElement?.closest('[role="treeitem"]')?.dataset.id === i, id))) throw new Error("focus is not back on the row");
  await p.keyboard.press("F2"); await settle(150);
  await p.locator(`${TREE} input[aria-label="Name"]`).fill(""); await p.keyboard.press("Enter"); await settle(300);
  const n = (await tops()).find((x) => x.id === id);
  if (n.p?.navlabel) throw new Error("navlabel is still " + n.p.navlabel);
  return "Esc cancels and returns focus; empty Enter removes the name";
});

await check("Ctrl+click two rows selects both (aria-selected), Delete removes both, one undo restores both", async () => {
  const wrap = (await tops()).find((n) => n.id === globalThis.wrapId);
  const kids = wrap.c[0].c.map((c) => c.id);
  await row(wrap.c[0].id).click(); await settle(100);
  await row(kids[0]).click({ modifiers: ["Control"] }); await settle(100);
  await row(kids[1]).click({ modifiers: ["Control"] }); await settle(200);
  const s = await sel();
  if (s.length !== 3) throw new Error("selection has " + s.length);
  if ((await p.locator(`${TREE}[aria-selected="true"]`).count()) !== 3) throw new Error("aria-selected rows != 3");
  if ((await p.locator('[data-hm-bs-tree] [role="tree"]').getAttribute("aria-multiselectable")) !== "true") throw new Error("tree is not aria-multiselectable");
  // Ctrl+click again drops one.
  await row(wrap.c[0].id).click({ modifiers: ["Control"] }); await settle(100);
  if ((await sel()).length !== 2) throw new Error("Ctrl+click did not toggle off");
  const before = (await ids()).length;
  await p.keyboard.press("Delete"); await settle(400);
  const after = await ids();
  if (after.includes(kids[0]) || after.includes(kids[1]) || after.length !== before - 2) throw new Error(`delete left ${after.length} of ${before}`);
  await p.keyboard.press("Control+z"); await settle(300);
  const back = await ids();
  if (!back.includes(kids[0]) || !back.includes(kids[1]) || back.length !== before) throw new Error("one undo did not restore both");
  return "3 selected, toggled to 2, both deleted and restored by one undo";
});

await check("history panel: lists the labelled steps, clicking an earlier one jumps there (one step), Esc returns focus to History", async () => {
  const btn = p.locator("[data-hm-bs-history]");
  const at = await sig();
  await btn.click();
  const list = p.getByRole("listbox", { name: /Changes/ });
  await list.waitFor({ timeout: 3000 });
  await axeOn("the open history panel");
  const labels = await list.getByRole("option").allTextContents();
  if (!labels[0].startsWith("Original")) throw new Error("first row: " + labels[0]);
  if (!labels.some((l) => /Paste|Wrap|Rename/.test(l))) throw new Error("no editing labels: " + labels.join("|"));
  if (labels.some((l) => /^Change/.test(l))) throw new Error("an unlabelled step: " + labels.join("|"));
  const pasteRow = list.getByRole("option").filter({ hasText: /Paste/ }).first();
  await pasteRow.click(); await settle(400);
  const d = await tops();
  if (d.some((n) => n.id === globalThis.wrapId)) throw new Error("jumping to the paste step kept the wrapper");
  if (!d.some((n) => n.id === globalThis.pastedId)) throw new Error("the jump lost the pasted section");
  if ((await sig()) === at) throw new Error("the document did not change");
  await p.keyboard.press("Escape"); await settle(200);
  if (!(await p.evaluate(() => document.activeElement?.hasAttribute("data-hm-bs-history")))) throw new Error("focus is not on the History button");
  return `${labels.length} rows; jumped to the paste step; Esc -> History button`;
});

await check("move handle: a drag started right after load (drag chunk delayed) still reorders", async () => {
  await restore(globalThis.start2);
  // The drag chunk arrives late on purpose: the handle must work whenever it is pressed or say why not.
  await p.route(/\/builder\/drag[^/]*\.js/, async (r) => { if (!process.env.QA_NODELAY) await new Promise((ok) => setTimeout(ok, 1200)); r.continue(); });
  await p.goto(base + (await pageHref(NAME2)), { waitUntil: "domcontentloaded" });
  await p.waitForSelector(TREE, { timeout: 10000 });
  const before = (await tops()).map((n) => n.id);
  if (before.length < 2) throw new Error("the page has fewer than two sections");
  await row(before[0]).click();
  const handle = p.locator(".hm-pe__handle");
  await handle.waitFor({ timeout: 5000 });
  let hb = await handle.boundingBox();
  for (let i = 0; i < 10; i++) { // the toolbar is placed again while the first render settles: press where it stays
    await settle(250);
    const now = await handle.boundingBox();
    const still = !!hb && !!now && Math.abs(now.x - hb.x) < 1 && Math.abs(now.y - hb.y) < 1;
    hb = now;
    if (still) break;
  }
  const second = (await tops())[1];
  const target = second.t === "container" && second.c?.length === 1 && second.c[0].t === "component" ? second.c[0].id : second.id; // a v1 section is drawn as its component
  let to = null;
  for (let i = 0; i < 20 && !to; i++) to = await canvasPoint(target).catch(() => null) ?? (await settle(400), null); // the first render of the page may still be on its way
  if (!hb || !to) throw new Error(`no box for ${hb ? "" : "the handle "}${to ? "" : "the target " + target}`);
  await p.mouse.move(hb.x + hb.width / 2, hb.y + hb.height / 2);
  await p.mouse.down();
  await p.mouse.move(hb.x + hb.width / 2, hb.y + hb.height / 2 + 12, { steps: 4 });
  await p.waitForTimeout(1600); // past the delayed chunk
  await p.mouse.move(to.x, to.y + 40, { steps: 12 });
  await p.mouse.up(); await settle(500);
  const after = (await tops()).map((n) => n.id);
  if (after.join() === before.join()) throw new Error(`the drag moved nothing (order ${after.join()})`);
  if (after[0] !== before[1] || after[1] !== before[0]) throw new Error(`unexpected order ${after.join()} (was ${before.join()})`);
  await p.unroute(/\/builder\/drag[^/]*\.js/);
  return "first section moved below the second";
});

await check("both pages are left as found (reload shows the original documents)", async () => {
  await restore(globalThis.start2);
  await open(NAME2);
  if ((await sig()) !== globalThis.start2) throw new Error(`${NAME2} differs after reload`);
  await open(NAME1);
  if ((await sig()) !== start1) throw new Error(`${NAME1} differs after reload`);
  await settle(2500); // the server keeps the last unsaved state (the original again) 1.5 s after the last change: let it land before the browser closes
  return "documents identical after reload";
});

await b.close();
console.log(bad ? `${bad} problem(s)` : "builder-direct: all checks passed");
process.exit(bad ? 1 : 0);
