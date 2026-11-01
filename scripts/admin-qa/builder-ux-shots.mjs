// Screenshots of the builder shell (UX-1): desktop with a selection and Ctrl+K open, the Insert tab, the start panel on an
// empty page, the Site styles drawer, the phone dock with the Navigator sheet and the Inspector sheet, dark mode, the
// conflict banner, the More menu and the shortcut sheet. Scratch pages are made through the Create menu route and deleted.
// Run: scripts/admin-qa/run-local.sh builder-ux-shots.mjs   Output: SHOTS_OUT (default results/shots)
import fs from "node:fs";
import path from "node:path";
import { launch, newSession, base, here } from "./lib.mjs";

const OUT = process.env.SHOTS_OUT || path.join(here, "results", "shots");
fs.mkdirSync(OUT, { recursive: true });
const b = await launch("chromium");
const TREE = '[data-hm-bs-tree] [role="treeitem"]';
const made = [];
const wait = (p, ms = 400) => p.waitForTimeout(ms);
const shot = async (p, name) => { await p.screenshot({ path: path.join(OUT, name + ".png") }); console.log("shot", name); };

async function csrf(p) {
  if (!/\/admin\/entities$/.test(p.url())) await p.goto(`${base}/admin/entities`);
  return p.evaluate(() => document.querySelector("#create-menu input[name=_csrf]").value);
}
async function draft(p) {
  const t = await csrf(p);
  const url = await p.evaluate(async (x) => (await fetch("/admin/entities/drafts", { method: "POST", headers: { "Content-Type": "application/x-www-form-urlencoded" }, body: new URLSearchParams({ _csrf: x, type: "page" }) })).url, t);
  const id = url.match(/entities\/([0-9a-f-]{36})/)[1];
  made.push(id);
  return id;
}
async function open(p, id) {
  await p.goto(`${base}/admin/entities/${id}/edit?qa=1`);
  await p.waitForFunction(() => !!window.__hmBuilder && document.querySelector("[data-hm-bs-tree]"));
  await wait(p, 700);
}
const saved = (p) => p.waitForFunction(() => /Saved/.test(document.querySelector("[data-hm-bs-state]")?.textContent ?? ""), null, { timeout: 20000 });
async function add(p, key) {
  if ((await p.locator('[data-hm-bs-ltab="add"]').getAttribute("aria-selected")) !== "true") await p.locator('[data-hm-bs-ltab="add"]').click();
  await p.locator(`[data-pal="${key}"]`).first().click();
  await wait(p, 900);
}
async function drop(p, id) {
  await p.goto(`${base}/admin/entities`);
  const t = await csrf(p);
  await p.evaluate(async ([i, x]) => { await fetch(`/admin/entities/${i}/delete`, { method: "POST", headers: { "Content-Type": "application/x-www-form-urlencoded" }, body: new URLSearchParams({ _csrf: x }) }); }, [id, t]);
}

const p = await newSession(b, { theme: "light", width: 1440 });
p.on("dialog", (d) => d.accept().catch(() => {}));

// the start panel on an empty page
const empty = await draft(p);
await open(p, empty);
await shot(p, "03-start-panel");

// a page with a hero, features and some text: selection, Ctrl+K, the Insert tab, More, shortcuts, Site styles, conflict
await add(p, "component:hero");
await add(p, "component:features");
await add(p, "heading");
await saved(p);
await open(p, empty);
await p.locator('[data-hm-bs-ltab="nav"]').click();
await p.locator(TREE).first().click();
await wait(p, 600);
await p.keyboard.press("Control+k");
await p.locator("#hm-bs-ck-q").fill("he");
await wait(p, 500);
await shot(p, "01-desktop-selection-ctrl-k");
await p.keyboard.press("Escape");

await p.locator('[data-hm-bs-ltab="add"]').click();
await wait(p, 500);
await shot(p, "02-add-tab");

await p.getByRole("button", { name: "More actions" }).click();
await wait(p, 300);
await shot(p, "08-more-menu");
await p.keyboard.press("Escape");

await p.getByRole("button", { name: "More actions" }).click();
await p.getByRole("menuitem", { name: "Site styles" }).click();
await p.locator("dialog[open].hm-dialog").waitFor();
await wait(p, 500);
await shot(p, "04-site-styles-drawer");
await p.keyboard.press("Escape");

await p.locator("[data-hm-bs-keys]").click();
await p.locator("#hm-bs-sheet[open]").waitFor();
await wait(p, 400);
await shot(p, "09-shortcut-sheet");
await p.keyboard.press("Escape");

// the conflict banner: another tab saves first
const other = await p.context().newPage();
await open(other, empty);
await add(other, "text");
await saved(other);
await other.close();
await p.locator('[data-hm-bs-ltab="add"]').click();
await p.locator('[data-pal="spacer"]').first().click();
await p.locator('[data-banner="conflict"]').waitFor({ timeout: 20000 });
await wait(p, 400);
await shot(p, "07-conflict-banner");
await p.locator('[data-banner="conflict"]').getByRole("button", { name: "Keep my version" }).click();
await saved(p);

// phone: the dock with the Navigator sheet, then the Inspector sheet after a tap on a row
await p.setViewportSize({ width: 390, height: 844 });
await wait(p, 500);
await shot(p, "05a-phone-canvas");
await p.locator('[data-hm-bs-dock="nav"]').click();
await wait(p, 400);
await shot(p, "05b-phone-navigator-sheet");
await p.locator(TREE).first().click();
await wait(p, 700);
await shot(p, "05c-phone-inspector-sheet");
await p.setViewportSize({ width: 1440, height: 900 });

// dark mode
const d = await newSession(b, { theme: "dark", width: 1440 });
await open(d, empty);
await d.locator('[data-hm-bs-ltab="nav"]').click();
await d.locator(TREE).first().click();
await d.locator('[data-hm-bs-device="md"]').click();
await wait(d, 800);
await shot(d, "06-dark-mode");
await d.context().close();

for (const id of made) await drop(p, id);
await b.close();
