// Screenshots of the builder canvas and inspector (UX-2): the element toolbar and breadcrumbs, the "+" handles and the
// layout picker, the zoom control, the device band (Tablet, Phone), the inspector header and Style tab (State, groups,
// Show on, source chips), style groups, the box model, the colour control, the Content tab of a Button, Image, Columns
// and Tabs, the element restore dialog, the link panel on a heading, dark mode and the phone. Scratch pages are made through
// the Create menu route and deleted.
// Run: scripts/admin-qa/run-local.sh builder-ux2-shots.mjs   Output: SHOTS_OUT (default results/shots-ux2)
import fs from "node:fs";
import path from "node:path";
import { launch, newSession, base, admin, here } from "./lib.mjs";

const OUT = process.env.SHOTS_OUT || path.join(here, "results", "shots-ux2");
fs.mkdirSync(OUT, { recursive: true });
const b = await launch("chromium");
const made = [];
const wait = (p, ms = 400) => p.waitForTimeout(ms);
const shot = async (p, name, o = {}) => { await p.screenshot({ path: path.join(OUT, name + ".png"), ...o }); console.log("shot", name); };
const flat = (ns) => ns.flatMap((n) => [n, ...flat(n.c ?? [])]);
const nodes = async (p) => flat((await p.evaluate(() => window.__hmBuilder.doc())).nodes);
const TREE = '[data-hm-bs-tree] [role="treeitem"]';

async function csrf(p) {
  if (!p.url().endsWith(`${admin}/entities`)) await p.goto(`${base}${admin}/entities`);
  return p.evaluate(() => document.querySelector("#create-menu input[name=_csrf]").value);
}
async function draft(p) {
  const t = await csrf(p);
  const url = await p.evaluate(async ([a, x]) => (await fetch(a + "/entities/drafts", { method: "POST", headers: { "Content-Type": "application/x-www-form-urlencoded" }, body: new URLSearchParams({ _csrf: x, type: "page" }) })).url, [admin, t]);
  const id = url.match(/entities\/([0-9a-f-]{36})/)[1];
  made.push(id);
  return id;
}
async function open(p, id) {
  await p.goto(`${base}${admin}/entities/${id}/edit?qa=1`);
  await p.waitForFunction(() => !!window.__hmBuilder && document.querySelector("[data-hm-bs-tree]"));
  await wait(p, 800);
  for (const g of await p.getByRole("button", { name: "Dismiss hints" }).all()) await g.click().catch(() => {}); // the hint bar covers the canvas
}
const saved = (p) => p.waitForFunction(() => /Saved/.test(document.querySelector("[data-hm-bs-state]")?.textContent ?? ""), null, { timeout: 20000 });
async function drop(p, id) {
  await p.goto(`${base}${admin}/entities`);
  const t = await csrf(p);
  await p.evaluate(async ([a, i, x]) => { await fetch(`${a}/entities/${i}/delete`, { method: "POST", headers: { "Content-Type": "application/x-www-form-urlencoded" }, body: new URLSearchParams({ _csrf: x }) }); }, [admin, id, t]);
}
const insert = (p, parent, index, kind) => p.evaluate(([a, i, k]) => window.__hmBuilder.insertAt({ parent: a, index: i, }, k), [parent, index, kind]);
async function nav(p) {
  if ((await p.locator('[data-hm-bs-ltab="nav"]').getAttribute("aria-selected")) !== "true") await p.locator('[data-hm-bs-ltab="nav"]').click();
}
async function pickRow(p, kind, n = 0) {
  await nav(p);
  const id = (await nodes(p)).filter((x) => x.t === kind)[n].id;
  await p.locator(`${TREE}[data-id="${id}"]`).click();
  await wait(p, 500);
  return id;
}
async function frame(p) {
  for (const f of p.frames()) if (f !== p.mainFrame() && f.url() !== "about:blank" && (await (await f.frameElement()).isVisible())) return f;
  throw new Error("no canvas frame");
}
async function canvasPoint(p, id) {
  const f = await frame(p);
  const box = await (await f.frameElement()).boundingBox();
  const r = await f.evaluate((i) => { const e = document.querySelector(`[data-hm-node="${i}"] [data-hm-prop], [data-hm-node="${i}"][data-hm-prop], [data-hm-node="${i}"]`); e.scrollIntoView({ block: "center" }); const g = e.getBoundingClientRect(); return { x: g.x + Math.min(g.width / 2, 40), y: g.y + g.height / 2, w: innerWidth }; }, id);
  const k = box.width / r.w;
  return { x: box.x + r.x * k, y: box.y + r.y * k };
}
// Scroll the inspector (its aside) to the top, or to `sel` when given. A focused control scrolls itself into view a moment after
// a commit, so this waits first; Element.scrollIntoView is not used: it would also scroll the page, whose body is overflow-hidden.
const scrollInspector = async (p, sel = null) => {
  await wait(p, 800);
  await p.evaluate((q) => {
    const aside = document.querySelector("aside.hm-bs__inspector");
    const el = q && document.querySelector(q);
    aside.scrollTop = el ? Math.max(0, el.getBoundingClientRect().top - aside.getBoundingClientRect().top + aside.scrollTop - 260) : 0;
  }, sel);
  await wait(p, 200);
};
const style = (p) => p.getByRole("tab", { name: "Style" }).click();
const content = (p) => p.getByRole("tab", { name: "Content" }).click();
const inspector = (p) => p.locator("[data-hm-bs-panel]");

const p = await newSession(b, { theme: "light", width: 1440 });
p.on("dialog", (d) => d.accept().catch(() => {}));

// ---- the page: a hero heading, a 2-column section (heading, text, button | image), Tabs ----
const id = await draft(p);
await open(p, id);
await insert(p, "", 0, "heading");
await insert(p, "", 1, "columns");
await wait(p, 600);
const cols = (await nodes(p)).find((n) => n.t === "columns");
await insert(p, cols.c[0].id, 0, "heading");
await insert(p, cols.c[0].id, 1, "text");
await insert(p, cols.c[0].id, 2, "button");
await insert(p, cols.c[1].id, 0, "image");
await insert(p, "", 2, "tabs");
await wait(p, 900);
await saved(p);
await open(p, id);
await nav(p);

// toolbar and breadcrumbs: select the heading in the first column by one click on the canvas
const inner = (await nodes(p)).find((n) => n.t === "columns").c[0].c[0];
const pt = await canvasPoint(p, inner.id);
await p.mouse.click(pt.x, pt.y);
await wait(p, 700);
await shot(p, "01-toolbar-and-breadcrumbs");

// "+" handles: the pointer over the section shows them between the elements; then the layout picker from one
const sectionPt = await canvasPoint(p, cols.id);
await p.mouse.move(sectionPt.x + 200, sectionPt.y + 20);
await wait(p, 500);
await shot(p, "02-plus-handles");
await p.locator("[data-hm-pe-plus][data-show]").last().click({ force: true });
await p.getByRole("dialog", { name: "Add a section" }).waitFor();
await wait(p, 400);
await shot(p, "03-layout-picker");
await p.keyboard.press("Escape");

// zoom control
await p.locator('[data-hm-pe-strip] [data-zoom="75"]').click();
await wait(p, 500);
await shot(p, "04-zoom-75");
await p.locator('[data-hm-pe-strip] [data-zoom="fit"]').click();
await wait(p, 300);

// device band: Tablet and Phone
await p.locator('[data-hm-bs-device="md"]').click();
await wait(p, 700);
await shot(p, "05-device-band-tablet");
await p.locator('[data-hm-bs-device="sm"]').click();
await wait(p, 700);
await shot(p, "06-device-band-phone");
await p.locator('[data-hm-bs-device="base"]').click();
await wait(p, 300);

// inspector: header, Class row, Style tab with State, groups, Show on and source chips (a padding set on Desktop reads
// "Inherited from Desktop" on Tablet)
await pickRow(p, "heading", 1);
await style(p);
// a heading starts with its groups closed except those that hold a value: open them all for the shots
for (const name of ["Layout", "Spacing", "Fill", "Typography"]) {
  const head = inspector(p).locator("button.hm-bs-grp__t", { hasText: new RegExp(`^${name}`) }).first();
  if ((await head.getAttribute("aria-expanded")) !== "true") await head.click();
}
await inspector(p).getByLabel("Sz font size", { exact: true }).first().fill("36");
await inspector(p).locator('.hm-bs-bm__p input[data-side="top"]').first().fill("24");
await p.keyboard.press("Tab");
await inspector(p).getByLabel("Alignment", { exact: true }).first().selectOption({ label: "Center" }).catch(() => {});
await wait(p, 400);
await p.locator('[data-hm-bs-device="md"]').click();
await wait(p, 600);
await scrollInspector(p);
await shot(p, "07-inspector-style-tablet");
await scrollInspector(p, "[data-hm-bs-panel] .hm-bs-showon");
await shot(p, "07b-show-on-and-padding-tablet");
await p.locator('[data-hm-bs-panel] select[aria-label="State"]').selectOption("hover");
await wait(p, 500);
await scrollInspector(p);
await shot(p, "08-inspector-style-hover");
await p.locator('[data-hm-bs-panel] select[aria-label="State"]').selectOption("");
await p.locator('[data-hm-bs-device="base"]').click();
await wait(p, 400);
await scrollInspector(p, '[data-hm-bs-panel] .hm-bs-grp:has(button:has-text("Effects"))');
await shot(p, "09-style-groups");
await scrollInspector(p, "[data-hm-bs-panel] .hm-bs-bm");
await shot(p, "10-box-model", { clip: await p.locator("[data-hm-bs-panel] .hm-bs-bm").first().boundingBox().then((r) => ({ x: Math.max(0, r.x - 16), y: Math.max(0, r.y - 40), width: Math.min(r.width + 32, 1440 - r.x + 16), height: Math.min(r.height + 60, 900 - r.y + 40) })) });
const sw = p.locator("[data-hm-bs-panel] .hm-bs-swatch").first();
await scrollInspector(p, "[data-hm-bs-panel] .hm-bs-swatch");
await sw.click();
await wait(p, 500);
await shot(p, "11-colour-control");
await p.keyboard.press("Escape");

// Content tab: Button, Image, Columns, Items (Tabs)
for (const [kind, name, n] of [["button", "12-content-button", 0], ["image", "13-content-image", 0], ["columns", "14-content-columns", 0], ["tabs", "15-content-items-tabs", 0]]) {
  await pickRow(p, kind, n);
  await content(p);
  await wait(p, 500);
  await shot(p, name);
}

// link panel on a heading: double-click a word, Ctrl+K
await pickRow(p, "heading", 0);
const h0 = (await nodes(p)).find((n) => n.t === "heading");
for (let i = 0; i < 4; i++) {
  const q = await canvasPoint(p, h0.id);
  await p.mouse.dblclick(q.x, q.y);
  await wait(p, 500);
  if (await (await frame(p)).evaluate(() => document.querySelector("[contenteditable]") && getSelection().toString().length > 0).catch(() => false)) break;
}
await p.keyboard.press("Control+k");
await p.locator("#hm-fmt-href").waitFor();
await p.keyboard.type("/ab");
await wait(p, 400);
await shot(p, "16-link-panel-heading");
await p.keyboard.press("Escape");
await p.keyboard.press("Escape");
await saved(p).catch(() => {});

// restore dialog: two saved versions of the heading, then Restore from an earlier version
await p.fill("[data-hm-bs-title]", "Build pages you can edit " + id.slice(0, 6));
await p.locator('[data-hm-bs-save="publish"]:visible').click();
await p.waitForFunction(() => document.querySelector('[data-hm-bs-save="save"]')?.textContent.trim() === "Update", null, { timeout: 20000 });
for (const text of ["Build pages you can edit", "Build pages fast"]) {
  await open(p, id);
  await pickRow(p, "heading", 0);
  const f = inspector(p).getByLabel("Text", { exact: true }).first();
  await f.click();
  await p.keyboard.press("Control+a");
  await p.keyboard.type(text);
  await wait(p, 700);
  await p.locator('[data-hm-bs-save="save"]:visible').click();
  await wait(p, 600);
  await saved(p);
}
await open(p, id);
await pickRow(p, "heading", 0);
await p.getByRole("button", { name: "Element options" }).click();
await p.getByRole("menuitem", { name: /Restore from an earlier version/ }).click();
await p.locator("dialog.hm-dialog[open] label.hm-radiocard").first().waitFor();
await wait(p, 600);
await shot(p, "17-restore-dialog");
await p.keyboard.press("Escape");

// phone: the Inspector sheet over a selected heading
await p.setViewportSize({ width: 390, height: 844 });
await wait(p, 600);
await p.locator('[data-hm-bs-dock="nav"]').click();
await pickRow(p, "heading", 1);
await wait(p, 600);
await shot(p, "19-phone-inspector");
await style(p);
await wait(p, 400);
await shot(p, "20-phone-style");
await p.setViewportSize({ width: 1440, height: 900 });
await drop(p, id);
made.length = 0;

// dark mode: the Style tab of a heading
const d = await newSession(b, { theme: "dark", width: 1440 });
const did = await draft(d);
await open(d, did);
await insert(d, "", 0, "heading");
await insert(d, "", 1, "text");
await wait(d, 800);
await pickRow(d, "heading", 0);
await d.locator('[data-hm-bs-device="md"]').click();
await style(d);
await d.locator('[data-hm-bs-panel] select[aria-label="State"]').selectOption("hover");
await wait(d, 600);
await shot(d, "18-dark-style-tab");
await saved(d);
await drop(d, did);
await b.close();
console.log("done", OUT);
