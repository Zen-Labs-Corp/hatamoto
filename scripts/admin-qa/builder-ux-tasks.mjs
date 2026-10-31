// Builder UX shell tasks (UX-1): T1..T10 from ref/tasks.md by mouse, by keyboard and at 390px, the first-use path
// (visible buttons only, no shortcut) for T1, T2, T5 and T8, the shell checks (status chip, device announce, More menu,
// Add tab, Ctrl+K, conflict and recovery banners, coach marks, phone dock, Site styles, dark mode) and axe on seven surfaces.
//
// Counting. Every action goes through the actor `act(page, {mode, firstUse})`, which counts and refuses what the mode forbids:
//   mouse     1440px, no keyboard except Tab/Enter/Escape and typing or select-all in a field (shortcuts are refused)
//   keyboard  1440px, no mouse at all
//   phone     390px, taps and typing, no shortcuts
//   firstUse  mouse + no shortcut anywhere, for T1, T2, T5, T8 (what a person who has not read the shortcut sheet can do)
// clicks = mouse clicks (a double-click is 2). keys = typed characters and shortcut presses. nav = Tab/Enter/Escape/arrow
// presses, reported but not part of keys (ref/tasks.md counted "key presses and typed characters", not Tab).
// The baselines are the UX-0 scripted expert paths in ref/tasks.md, which mix mouse and keyboard. A task therefore passes
// when the mouse run or the keyboard run (and, for T1, T2, T5, T8, the first-use run on its own) is <= the baseline in every
// column the baseline has, and the median wall time of 3 runs is <= 1.5 x the baseline (machine noise; the raw numbers stay in
// the result file). Every run must complete. Phone runs must complete; their counts are the first phone measurements
// (ref/tasks.md: "Not run: keyboard-only and phone-width variants"). T4 (hover shadow as a class on two cards), T5 (Show on),
// T9 (undo three steps, then restore one element from an earlier version) and T10 (deepest click, padding in the box model)
// run the UX-2 way. A task may also have a "plus" run (mouse, no shortcut): T1 and T2 by the layout picker and "+" with no
// palette search and no key, which must complete and stay within the same baseline. T9 counts only the undo part against
// the baseline (ref/tasks.md: restoring an element was impossible, so it has none): the restore part is reported next to it.
//
// Run: scripts/admin-qa/run-local.sh builder-ux-tasks.mjs      Exit 1 on any problem. Result: results/builder-ux-tasks.json
// Env: QA_RUNS (timing runs per mode, default 3), QA_ONLY (regex of task ids or check names), QA_MODE (regex of modes), QA_VERBOSE=1.
import fs from "node:fs";
import { execSync } from "node:child_process";
import { launch, newSession, base, admin, writeResult, median, password } from "./lib.mjs";

const axeSrc = fs.readFileSync(new URL("./vendor/axe.min.js", import.meta.url), "utf8");
const tags = ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"];
const RUNS = Number(process.env.QA_RUNS || 3);
const ONLY = process.env.QA_ONLY ? new RegExp(process.env.QA_ONLY, "i") : null;
const VERBOSE = !!process.env.QA_VERBOSE;
const MODE = process.env.QA_MODE ? new RegExp(process.env.QA_MODE, "i") : null; // mouse|keyboard|firstUse (phone always runs)

// ref/tasks.md summary table (UX-0, Playwright, 1440x900). sec is the wall of the expert path (T1 with text edits: 8.8).
const BASELINE = {
  T1: { clicks: 7, keys: 47, sec: 8.8 },
  T2: { clicks: 3, keys: 32, sec: 4.8 },
  T3: { clicks: 9, sec: 8.2 },
  T4: { clicks: 5, keys: 4, sec: 6 },
  T5: { clicks: 4, keys: 6, sec: 3.0 },
  T6: { clicks: 1, keys: 2, sec: 11.4 },
  T7: { clicks: 4, keys: 5, sec: 6.6, partial: "a per-instance headline change is not built (E12): the instance offers Edit block and Detach" },
  T8: { clicks: 7, keys: 23, sec: 3.7 },
  T9: { clicks: 2, keys: 4, sec: 2.5 },
  T10: { clicks: 2, keys: 2, sec: 3.0 },
};

const browser = await launch("chromium");
let bad = 0;
const checks = [];
const results = {};
const axeRes = {};
const errors = [];

const watch = (p) => {
  p.on("pageerror", (e) => { errors.push(e.message); if (VERBOSE) console.log("  pageerror", e.message); });
  p.on("dialog", (d) => d.accept().catch(() => {}));
  if (VERBOSE) p.on("console", (m) => m.type() === "error" && console.log("  console", m.text().slice(0, 160)));
};
const main = await newSession(browser, { theme: "light", width: 1440 });
await main.context().grantPermissions(["clipboard-read", "clipboard-write"], { origin: base });
watch(main);

const TREE = '[data-hm-bs-tree] [role="treeitem"]';
const MENU = '.hm-menu[aria-label="Element actions"]';
const settle = (p, ms = 150) => p.waitForTimeout(ms);
const flat = (ns) => ns.flatMap((n) => [n, ...flat(n.c ?? [])]);
const doc = (p) => p.evaluate(() => window.__hmBuilder.doc());
const nodes = async (p) => flat((await doc(p)).nodes);
const row = (p, id) => p.locator(`${TREE}[data-id="${id}"]`);
const rowOfKind = async (p, t, n = 0) => { const f = (await nodes(p)).filter((x) => x.t === t); if (!f[n]) throw new Error(`no ${t} in the document`); return row(p, f[n].id); };
const phoneNow = (p) => p.viewportSize().width < 600;
const sheet = (p) => p.evaluate(() => document.querySelector("[data-hm-builder]")?.dataset.hmBsTab);
const need = (cond, msg) => { if (!cond) throw new Error(msg); };

async function frame(p) {
  for (const f of p.frames()) {
    if (f === p.mainFrame() || f.url() === "about:blank") continue;
    if (await (await f.frameElement()).isVisible()) return f;
  }
  throw new Error("no visible canvas frame");
}
// The page point of the text of a canvas element (the frame is scaled to fit its pane, which Playwright's frame clicks do
// not follow). `word` aims at that word of the text.
async function canvasPoint(p, selector, word) {
  const f = await frame(p);
  const box = await (await f.frameElement()).boundingBox();
  const r = await f.evaluate(([s, w]) => {
    const e = document.querySelector(s);
    if (!e) return null;
    e.scrollIntoView({ block: "center", behavior: "instant" });
    const g = document.createRange();
    g.selectNodeContents(e);
    if (w) {
      const t = document.createTreeWalker(e, 4);
      for (let n = t.nextNode(); n; n = t.nextNode()) {
        const i = n.nodeValue.indexOf(w);
        if (i >= 0) { g.setStart(n, i); g.setEnd(n, i + w.length); break; }
      }
    }
    const b = g.getBoundingClientRect();
    return { x: b.x + (w ? b.width / 2 : Math.min(b.width / 2, 30)), y: b.y + b.height / 2, w: innerWidth };
  }, [selector, word]);
  if (!r) return null;
  const k = box.width / r.w;
  return { x: box.x + r.x * k, y: box.y + r.y * k };
}
const textSel = (id) => `[data-hm-node="${id}"] [data-hm-prop], [data-hm-node="${id}"][data-hm-prop]`;

// ---- the counting actor ----
const NAV = new Set(["Tab", "Shift+Tab", "Enter", "Escape", "ArrowUp", "ArrowDown", "ArrowLeft", "ArrowRight", "Home", "End"]);
const fieldIn = () => { const a = document.activeElement; return !!a && (/^(INPUT|TEXTAREA|SELECT)$/.test(a.tagName) || a.isContentEditable); };
// A text field has focus: in the page, or in the canvas frame when the page's focus is the frame element itself.
async function inField(p) {
  if (await p.evaluate(fieldIn)) return true;
  if (!(await p.evaluate(() => document.activeElement?.tagName === "IFRAME"))) return false;
  for (const f of p.frames()) if (f !== p.mainFrame() && (await f.evaluate(fieldIn).catch(() => false))) return true;
  return false;
}
function act(page, { mode, firstUse = false }) {
  const c = { clicks: 0, keys: 0, nav: 0 };
  const refuse = (what) => { throw new Error(`${what} is not allowed in ${firstUse ? "first-use " : ""}${mode} mode`); };
  const a = {
    c,
    page,
    async click(loc, o = {}) {
      if (mode === "keyboard") refuse("a click");
      c.clicks += o.clickCount === 2 ? 2 : 1;
      await loc.click({ timeout: 8000, ...o });
    },
    async rightClick(loc) { await a.click(loc, { button: "right" }); },
    /** A choice in a <select> by the mouse: one click. By keyboard the arrow keys do it (a.press). */
    async select(loc, value) {
      if (mode === "keyboard") refuse("a click");
      c.clicks++;
      await loc.selectOption(value, { timeout: 8000 });
    },
    /** Remember the counts so far: the part of a task that has a baseline. */
    split() { a.part = { ...c, at: Date.now() }; },
    async clickAt(pt, o = {}) {
      if (mode === "keyboard") refuse("a click");
      c.clicks += o.clickCount === 2 ? 2 : 1;
      await page.mouse.click(pt.x, pt.y, o);
    },
    async press(key) {
      if (NAV.has(key)) {
        if (mode === "phone" && key !== "Enter" && key !== "Escape") refuse(`the ${key} key`);
        if (mode === "mouse" && key !== "Tab" && key !== "Shift+Tab" && key !== "Enter" && key !== "Escape" && !(await inField(page))) refuse(`the ${key} key`);
        if (firstUse && key !== "Enter" && key !== "Escape" && key !== "Tab" && !(await inField(page))) refuse(`the ${key} key`);
        c.nav++;
      } else {
        const typing = await inField(page);
        const allowed = typing && key === "Control+a"; // select-all inside a field is a text edit, not a shortcut
        if (!allowed && mode !== "keyboard") refuse(`the shortcut ${key}`);
        if (!allowed && firstUse) refuse(`the shortcut ${key}`);
        c.keys++;
      }
      await page.keyboard.press(key);
    },
    async type(text) {
      if (!(await inField(page))) refuse("typing outside a text field");
      c.keys += text.length;
      await page.keyboard.type(text);
    },
    /** Tab until the focused element passes `test` (runs in the page); every press is counted as nav. */
    async tabTo(test, arg = null, max = 400, key = "Tab") {
      for (let i = 0; i < max; i++) {
        if (await page.evaluate(test, arg)) return;
        await a.press(key);
      }
      throw new Error("Tab never reached the target: " + test + " (focus is " + (await page.evaluate(() => document.activeElement?.outerHTML.slice(0, 120))) + ")");
    },
  };
  return a;
}

// Ctrl+K, then wait until the search has focus (typing before that would go to the page).
async function openPalette(a) {
  await a.press("Control+k");
  await a.page.waitForFunction(() => document.activeElement?.id === "hm-bs-ck-q", null, { timeout: 5000 });
}

// Keyboard: Tab into the Navigator, then Home/Down until the row of `id` has focus and is selected.
async function treeTo(a, id) {
  await a.tabTo(() => document.activeElement?.getAttribute("role") === "treeitem");
  for (let i = 0; i < 40; i++) {
    if (await a.page.evaluate((x) => document.activeElement?.dataset?.id === x && window.__hmBuilder.selection().includes(x), id)) return;
    await a.press(i === 0 ? "Home" : "ArrowDown");
    await a.page.waitForTimeout(60); // the tree repaints on the next frame; keep up with it
  }
  throw new Error("the arrow keys never reached the row, focus is " + (await a.page.evaluate(() => document.activeElement?.outerHTML.slice(0, 100))));
}

// ---- pages: drafts are made through the real form route and removed after the run ----
async function csrf(p) {
  if (!p.url().endsWith(`${admin}/entities`)) await p.goto(`${base}${admin}/entities`);
  return p.evaluate(() => document.querySelector("#create-menu input[name=_csrf]").value);
}
const made = new Set();
async function newDraft(p) {
  const token = await csrf(p);
  const url = await p.evaluate(async ([a, t]) => (await fetch(a + "/entities/drafts", { method: "POST", headers: { "Content-Type": "application/x-www-form-urlencoded" }, body: new URLSearchParams({ _csrf: t, type: "page" }) })).url, [admin, token]);
  const id = url.match(/entities\/([0-9a-f-]{36})/)?.[1];
  need(id, "the draft was not created: " + url);
  made.add(id);
  return id;
}
async function drop(p, id) {
  if (!id) return;
  await p.goto(`${base}${admin}/entities`);
  const token = await csrf(p);
  await p.evaluate(async ([a, i, t]) => { await fetch(`${a}/entities/${i}/delete`, { method: "POST", headers: { "Content-Type": "application/x-www-form-urlencoded" }, body: new URLSearchParams({ _csrf: t }) }); }, [admin, id, token]);
  made.delete(id);
}
const idOf = (p) => p.url().match(/entities\/([0-9a-f-]{36})/)?.[1];
async function ready(p, { qa = true } = {}) {
  await p.waitForFunction((q) => (!q || !!window.__hmBuilder) && document.querySelector("[data-hm-bs-tree]"), qa, { timeout: 15000 });
  await p.waitForSelector("[data-hm-bs-state] .hm-savestate", { timeout: 10000 });
}
async function openBuilder(p, id, ms = 400) {
  await p.goto(`${base}${admin}/entities/${id}/edit?qa=1`, { waitUntil: "load" });
  await ready(p);
  await settle(p, ms);
}
const savedNow = (p, ms = 20000) => p.waitForFunction(() => /Saved/.test(document.querySelector("[data-hm-bs-state]")?.textContent ?? ""), null, { timeout: ms });
const tileFor = (p, key) => p.locator(`[data-hm-bs-lpanel="add"] [data-pal="${key}"], [data-hm-bs-palette] [data-pal="${key}"]`).first();
const device = (p, name) => p.locator(`[data-hm-bs-device="${name}"]`);

// Uncounted setup helpers (plain page actions).
async function showAdd(p) {
  if (phoneNow(p)) { if ((await sheet(p)) !== "add") await p.locator('[data-hm-bs-dock="add"]').click(); }
  else if ((await p.locator('[data-hm-bs-ltab="add"]').getAttribute("aria-selected")) !== "true") await p.locator('[data-hm-bs-ltab="add"]').click();
}
async function showNav(p) {
  if (phoneNow(p)) { if ((await sheet(p)) !== "nav") await p.locator('[data-hm-bs-dock="nav"]').click(); }
  else if ((await p.locator('[data-hm-bs-ltab="nav"]').getAttribute("aria-selected")) !== "true") await p.locator('[data-hm-bs-ltab="nav"]').click();
}
async function seed(p, keys) {
  for (const k of keys) {
    await showAdd(p);
    const before = JSON.stringify(await doc(p)).length;
    await tileFor(p, k).click();
    await p.waitForFunction((n) => JSON.stringify(window.__hmBuilder.doc()).length > n, before, { timeout: 5000 });
    await settle(p, 300);
  }
}
// Reload so nothing is selected and the history is empty, with the Navigator tab showing.
async function fresh(p, id) {
  await savedNow(p);
  await openBuilder(p, id);
  await showNav(p);
}

// The Add tab (and the pane it sits in) is a tab list on the desktop and a dock sheet on the phone.
async function toNavigator(a) {
  const p = a.page;
  if (phoneNow(p)) { if ((await sheet(p)) !== "nav") await a.click(p.locator('[data-hm-bs-dock="nav"]')); }
  else if ((await p.locator('[data-hm-bs-ltab="nav"]').getAttribute("aria-selected")) !== "true") await a.click(p.locator('[data-hm-bs-ltab="nav"]'));
}
async function toAdd(a) {
  const p = a.page;
  if (phoneNow(p)) { if ((await sheet(p)) !== "add") await a.click(p.locator('[data-hm-bs-dock="add"]')); }
  else if ((await p.locator('[data-hm-bs-ltab="add"]').getAttribute("aria-selected")) !== "true") await a.click(p.locator('[data-hm-bs-ltab="add"]'));
}
async function toInspector(a) {
  const p = a.page;
  if (phoneNow(p) && (await sheet(p)) !== "insp") await a.click(p.locator('[data-hm-bs-dock="insp"]'));
}
// A labelled field of the inspector: clicked (mouse, phone) or reached with Tab (keyboard), then typed into.
async function fillField(a, label, text, { replace = false, mode = "mouse" } = {}) {
  const p = a.page;
  await toInspector(a);
  if (mode === "keyboard") await a.tabTo((l) => { const e = document.activeElement; return !!e?.closest?.("[data-hm-bs-panel]") && (e.getAttribute("aria-label") === l || e.labels?.[0]?.textContent.trim() === l); }, label);
  else await a.click(p.locator("[data-hm-bs-panel]").getByLabel(label, { exact: true }).first());
  if (replace) await a.press("Control+a");
  await a.type(text);
}
async function menuPick(a, re) {
  const p = a.page;
  await p.locator(MENU).waitFor({ timeout: 3000 });
  await a.click(p.locator(`${MENU} [role="menuitem"]`, { hasText: re }).first());
  await p.waitForSelector(MENU, { state: "detached", timeout: 3000 });
}
const hiddenOn = (n, bp) => JSON.stringify(n).includes(`"hide"`) && JSON.stringify(n.s ?? n).includes(bp);

const seeded = async (p, kinds = ["heading", "text"]) => { const id = await newDraft(p); await openBuilder(p, id); await seed(p, kinds); await savedNow(p); return id; };
// A published page (a draft keeps its autosave; only a published page keeps a recovery copy on the server): titled, then published.
const publishedPage = async (p, kinds = ["heading"]) => {
  const id = await seeded(p, kinds);
  await p.fill("[data-hm-bs-title]", "QA recovery " + id.slice(0, 6));
  await p.locator('[data-hm-bs-save="publish"]:visible').click();
  await p.waitForFunction(() => document.querySelector('[data-hm-bs-save="save"]')?.textContent.trim() === "Update", null, { timeout: 20000 });
  await openBuilder(p, id);
  return id;
};
// Edit a published page and wait until the server holds the recovery copy of it, then open the page again.
const leaveEdited = async (p, id) => {
  await showAdd(p);
  const kept = p.waitForResponse((r) => r.url().includes("/builder/recovery") && !r.url().endsWith("/discard") && r.request().method() === "POST", { timeout: 15000 });
  await tileFor(p, "text").click();
  await kept;
  await p.goto(`${base}${admin}/entities/${id}/edit?qa=1`);
  await ready(p);
};

// ---- the tasks ----
// start(p) -> env (uncounted; makes the page); mouse/keyboard(a, env) (counted; phone runs mouse with taps);
// verify(p, env) -> throws when the work is wrong; cleanup(p, env).
const T = {};

// T1: Create > Page, the Hero pattern, a headline (the expert baseline built heading, text, 2 buttons and an image).
T.T1 = {
  title: "empty page to a hero with a headline",
  firstUse: true,
  start: async (p) => { await p.goto(`${base}${admin}/entities`); return {}; },
  async mouse(a) {
    const p = a.page;
    await a.click(p.getByRole("button", { name: "Create" }));
    await a.click(p.getByRole("menuitem", { name: "Page", exact: true }));
    await ready(p, { qa: false });
    await toAdd(a);
    await a.click(p.locator("#hm-bs-pal-q"));
    await a.type("hero");
    await a.click(tileFor(p, "component:hero"));
    await fillField(a, "Headline", "Welcome to the site");
  },
  // The layout picker, no search and no key: Add a section > Insert a ready-made section... > the Hero tile.
  async plus(a) {
    const p = a.page;
    await a.click(p.getByRole("button", { name: "Create" }));
    await a.click(p.getByRole("menuitem", { name: "Page", exact: true }));
    await ready(p, { qa: false });
    await toNavigator(a);
    await a.click(p.getByRole("button", { name: "Add a section" }));
    await a.click(p.getByRole("dialog", { name: "Add a section" }).getByRole("button", { name: /^Insert a ready-made section/ }));
    await a.click(tileFor(p, "component:hero"));
    await fillField(a, "Headline", "Welcome to the site");
  },
  async keyboard(a) {
    const p = a.page;
    await a.tabTo(() => document.activeElement?.textContent?.trim() === "Create");
    await a.press("Enter");
    await settle(p, 250); // the menu opens and moves focus to its first item; Tab only if it has not
    await a.tabTo(() => document.activeElement?.closest("#create-menu") && document.activeElement.textContent.trim() === "Page", null, 6);
    await a.press("Enter");
    await ready(p, { qa: false });
    await openPalette(a);
    await a.type("hero");
    await a.press("Enter");
    await fillField(a, "Headline", "Welcome to the site", { mode: "keyboard" });
  },
  verify: async (p) => {
    await savedNow(p);
    await openBuilder(p, idOf(p));
    const hero = (await nodes(p)).find((n) => n.t === "component" && n.p?.type === "hero");
    need(hero, "no hero on the page");
    need(JSON.stringify(hero).includes("Welcome to the site"), "the headline is not in the document: " + JSON.stringify(hero).slice(0, 200));
  },
  cleanup: async (p) => drop(p, idOf(p)),
};

// T2: a three-column feature grid that stacks on a phone: the Features pattern, then the Phone view.
T.T2 = {
  title: "three-column feature grid, looked at on the phone view",
  firstUse: true,
  // An empty page opens on the Add tab; "Add a section" is the Navigator's empty-state button, so the "+" run starts with the
  // Navigator showing (the tab last used; opening it is one more click, counted nowhere here: it would make 4 against a baseline of 3).
  start: async (p, variant) => { const id = await newDraft(p); await openBuilder(p, id); if (variant === "plus") await showNav(p); return { id }; },
  async mouse(a) {
    const p = a.page;
    await toAdd(a);
    await a.click(tileFor(p, "component:features"));
    await p.waitForFunction(() => JSON.stringify(window.__hmBuilder.doc()).includes("features"), null, { timeout: 5000 });
    if (!phoneNow(p)) { await a.click(device(p, "sm")); await settle(p, 300); }
  },
  // "Add a section" and the 3 columns layout, then the Phone view: no palette, no key.
  async plus(a) {
    const p = a.page;
    await toNavigator(a);
    await a.click(p.getByRole("button", { name: "Add a section" }));
    await a.click(p.getByRole("dialog", { name: "Add a section" }).getByRole("button", { name: "3 columns", exact: true }));
    await p.waitForFunction(() => window.__hmBuilder.doc().nodes.some((n) => n.t === "columns"), null, { timeout: 5000 });
    await a.click(device(p, "sm"));
    await settle(p, 300);
  },
  async keyboard(a) {
    await openPalette(a);
    await a.type("features");
    await a.press("Enter");
    await a.page.waitForFunction(() => JSON.stringify(window.__hmBuilder.doc()).includes("features"), null, { timeout: 5000 });
    await a.tabTo(() => document.activeElement?.dataset?.hmBsDevice === "sm");
    await a.press("Enter");
    await settle(a.page, 300);
  },
  verify: async (p, env, mode, variant) => {
    if (variant === "plus") { const c = (await nodes(p)).find((n) => n.t === "columns"); need(c && (c.c ?? []).length === 3, "no 3 columns on the page"); }
    else need((await nodes(p)).some((n) => n.p?.type === "features"), "no features section on the page");
    if (!phoneNow(p)) need((await (await frame(p)).evaluate(() => innerWidth)) < 700, "the Phone view is not narrow");
  },
  cleanup: async (p, env) => drop(p, env.id),
};

// T3: brand colour and the Medium corners for the whole site, from the builder (More > Site styles).
const NEW_COLOUR = "#7a1fa2";
const NEW_RADIUS = "12px";
const themeValues = async (p) => {
  await p.goto(`${base}${admin}/theme/colours`);
  return p.evaluate(() => Object.fromEntries([...document.querySelectorAll("form[action$='/theme/colours'] input[name]")].filter((i) => i.type !== "hidden" || i.name === "_csrf").map((i) => [i.name, i.value])));
};
T.T3 = {
  title: "brand colour and corner radius for the whole site",
  owner: true,
  start: async (p) => { const id = await newDraft(p); const was = await themeValues(p); await openBuilder(p, id); return { id, was }; },
  async mouse(a) {
    const p = a.page;
    await a.click(p.getByRole("button", { name: "More actions" }));
    await a.click(p.getByRole("menuitem", { name: "Site styles" }));
    const dlg = p.locator("dialog[open].hm-dialog");
    await dlg.waitFor({ timeout: 5000 });
    await a.press("Control+a");
    await a.type(NEW_COLOUR);
    await a.click(p.locator("#hm-bs-ss-radius_md"));
    await a.press("Control+a");
    await a.type(NEW_RADIUS);
    await a.click(dlg.getByRole("button", { name: "Save site styles" }));
    await dlg.waitFor({ state: "detached", timeout: 8000 });
  },
  async keyboard(a) {
    const p = a.page;
    await openPalette(a);
    await a.type("site styles");
    await a.press("Enter");
    const dlg = p.locator("dialog[open].hm-dialog");
    await dlg.waitFor({ timeout: 5000 });
    await a.press("Control+a");
    await a.type(NEW_COLOUR);
    await a.tabTo(() => document.activeElement?.id === "hm-bs-ss-radius_md");
    await a.press("Control+a");
    await a.type(NEW_RADIUS);
    await a.press("Enter");
    await dlg.waitFor({ state: "detached", timeout: 8000 });
  },
  verify: async (p, env) => {
    const now = await themeValues(p);
    need(now.primary?.toLowerCase() === NEW_COLOUR && now.radius_md === NEW_RADIUS, `the theme holds ${now.primary} / ${now.radius_md}`);
    need(Object.keys(env.was).every((k) => k === "primary" || k === "radius_md" || k === "_csrf" || env.was[k] === now[k]), "another theme value changed");
  },
  cleanup: async (p, env) => {
    await p.goto(`${base}${admin}/theme/colours`);
    await p.evaluate(async (was) => {
      const t = document.querySelector("form[action$='/theme/colours'] input[name=_csrf]").value;
      await fetch(document.querySelector("form[action$='/theme/colours']").getAttribute("action"), { method: "POST", headers: { "Content-Type": "application/x-www-form-urlencoded", "X-Hm-Fetch": "1" }, body: new URLSearchParams({ ...was, _csrf: t }) });
    }, env.was);
    await drop(p, env.id);
  },
};

// T4: a hover shadow on all cards: State Hover, Shadow in Effects, Save as class..., then the other card takes the class.
// The class name is four random letters: the keyboard baseline has four keys, and the name is the typing.
const className = () => Array.from({ length: 4 }, () => "abcdefghijklmnopqrstuvwxyz"[Math.floor(Math.random() * 26)]).join("");
const classOf = (n) => n.p?.preset;
T.T4 = {
  title: "hover shadow on all cards (a class applied to a second card)",
  start: async (p) => {
    const id = await newDraft(p);
    await openBuilder(p, id);
    for (let i = 0; i < 2; i++) await p.evaluate((i) => window.__hmBuilder.insertAt({ parent: "", index: i }, "container"), i);
    await p.waitForFunction(() => window.__hmBuilder.doc().nodes.length === 2, null, { timeout: 5000 });
    await fresh(p, id);
    return { id, name: className() };
  },
  async mouse(a, env) {
    const p = a.page;
    const [first, second] = (await nodes(p)).filter((n) => n.t === "container");
    await toNavigator(a);
    await a.click(row(p, first.id));
    await toInspector(a);
    await a.click(p.getByRole("tab", { name: "Style" }));
    await p.locator('[data-hm-bs-panel] select[aria-label="State"]').selectOption("hover");
    await openMore(p);
    const effects = p.locator("[data-hm-bs-panel] .hm-bs-group__t", { hasText: "Effects" });
    if ((await effects.getAttribute("aria-expanded")) === "false") await a.click(effects); // open already in some states
    await a.select(p.locator("[data-hm-bs-panel]").getByLabel("Shadow", { exact: true }), { label: "Large" });
    await a.click(p.getByRole("button", { name: "Save as class…" }));
    await a.type(env.name);
    await a.click(p.getByRole("button", { name: "Save class", exact: true }));
    await p.waitForFunction((x) => window.__hmBuilder.doc().nodes.some((n) => n.p?.preset === x), env.name, { timeout: 8000 });
    await toNavigator(a);
    await a.click(row(p, second.id));
    await toInspector(a);
    await a.select(p.locator("[data-hm-bs-panel]").getByLabel("Class", { exact: true }), env.name);
    await settle(p, 250);
  },
  async keyboard(a, env) {
    const p = a.page;
    const [first, second] = (await nodes(p)).filter((n) => n.t === "container");
    await treeTo(a, first.id);
    await a.press("Enter");
    await a.tabTo(() => document.activeElement?.getAttribute("role") === "tab", null, 20, "Shift+Tab");
    await a.press("ArrowRight"); // Style
    await a.tabTo(() => document.activeElement?.dataset?.state === "");
    await a.press("ArrowRight"); // Hover
    await openMore(p);
    await a.tabTo(() => document.activeElement?.classList.contains("hm-bs-group__t") && /Effects/.test(document.activeElement.textContent));
    if ((await p.evaluate(() => document.activeElement.getAttribute("aria-expanded"))) === "false") await a.press("Enter");
    await a.tabTo(selectNamed, "Shadow");
    await a.press("ArrowDown"); // Large
    await a.tabTo(() => /Save as class/.test(document.activeElement?.textContent ?? ""), null, 400, "Shift+Tab");
    await a.press("Enter");
    await a.type(env.name);
    await a.tabTo(() => document.activeElement?.textContent === "Save class");
    await a.press("Enter");
    await p.waitForFunction((x) => window.__hmBuilder.doc().nodes.some((n) => n.p?.preset === x), env.name, { timeout: 8000 });
    await treeTo(a, second.id);
    await a.press("Enter");
    await a.tabTo(selectNamed, "Class");
    await a.press("ArrowDown");
    await settle(p, 250);
  },
  verify: async (p, env) => {
    await savedNow(p);
    await openBuilder(p, env.id);
    const cards = (await nodes(p)).filter((n) => n.t === "container");
    need(cards.length === 2 && cards.every((n) => classOf(n) === env.name), "the cards hold " + cards.map((n) => classOf(n)).join(", ") + ", wanted " + env.name);
    const css = await (await frame(p)).evaluate((n) => [...document.querySelectorAll("style")].map((e) => e.textContent).filter((t) => t.includes(`.hm-p-${n}:hover`) && /box-shadow/.test(t)).length, env.name);
    need(css > 0, "the rendered page holds no hover shadow for the class " + env.name);
  },
  cleanup: async (p, env) => { await dropClass(p, env.name); await drop(p, env.id); },
};
// The focused element is a <select> whose label (for= or aria-labelledby) reads `name`.
const selectNamed = (name) => {
  const e = document.activeElement;
  return e?.tagName === "SELECT" && (e.labels?.[0]?.textContent ?? document.getElementById(e.getAttribute("aria-labelledby") ?? "")?.textContent ?? "").trim() === name;
};
// More style options opens by itself in a state other than Normal; open it when it is not.
async function openMore(p) {
  const d = p.locator("[data-hm-bs-panel] details.hm-bs-more-opts");
  if (!(await d.evaluate((e) => e.open))) await d.locator("summary").click();
}
async function dropClass(p, name) {
  await p.goto(`${base}${admin}/theme/presets`);
  await p.evaluate(async ([a, n]) => {
    const t = document.querySelector("input[name=_csrf]")?.value;
    await fetch(`${a}/theme/presets/${n}/delete`, { method: "POST", headers: { "Content-Type": "application/x-www-form-urlencoded" }, body: new URLSearchParams({ _csrf: t ?? "" }) });
  }, [admin, name]);
}

// T5: hide an element on the phone view: the Show on switches of the Style tab (the keyboard run keeps the H key in the Navigator).
T.T5 = {
  title: "hide an element on the phone view (Show on)",
  firstUse: true,
  start: async (p) => { const id = await newDraft(p); await openBuilder(p, id); await seed(p, ["heading", "text"]); await fresh(p, id); return { id }; },
  async mouse(a) {
    const p = a.page;
    await toNavigator(a);
    await a.click(await rowOfKind(p, "heading"));
    await toInspector(a);
    await a.click(p.getByRole("tab", { name: "Style" }));
    await a.click(p.locator("[data-hm-bs-panel] .hm-bs-showon label", { hasText: "Phone" }));
    await settle(p, 250);
  },
  async keyboard(a) {
    const p = a.page;
    await a.tabTo(() => document.activeElement?.dataset?.hmBsDevice === "sm");
    await a.press("Enter");
    await a.tabTo(() => document.activeElement?.getAttribute("role") === "treeitem");
    await a.press("h");
    await settle(p, 250);
  },
  verify: async (p, env, mode) => {
    const h = (await nodes(p)).find((n) => n.t === "heading");
    need(JSON.stringify(h).includes("hide"), "the heading is not hidden anywhere: " + JSON.stringify(h).slice(0, 200));
    if (mode === "mouse" || mode === "phone") need(await p.locator('[data-hm-bs-panel] .hm-bs-showon input[type="checkbox"]').evaluateAll((l) => l.map((x) => x.checked).join()) === "true,true,false", "Show on is not Desktop and Tablet only");
    if (!phoneNow(p) && mode !== "keyboard") { await device(p, "sm").click(); await settle(p, 200); } // the row says Hidden on the width being edited
    if (!phoneNow(p)) await p.waitForFunction(() => /hidden/i.test(document.querySelector('[data-hm-bs-tree] [role="treeitem"]')?.innerText ?? ""), null, { timeout: 3000 }).catch(() => { throw new Error("the Navigator row does not say it is hidden"); });
  },
  cleanup: async (p, env) => drop(p, env.id),
};

// T6: copy a section from one page to another through the clipboard.
T.T6 = {
  title: "copy a section from page A to page B",
  start: async (p) => {
    const a = await newDraft(p);
    await openBuilder(p, a);
    await seed(p, ["container"]);
    await savedNow(p);
    const b = await newDraft(p);
    await openBuilder(p, b);
    await seed(p, ["heading"]);
    await savedNow(p);
    await openBuilder(p, a);
    await showNav(p);
    return { a, b };
  },
  async mouse(a, env) {
    const p = a.page;
    await toNavigator(a);
    await a.rightClick(await rowOfKind(p, "container"));
    await menuPick(a, /^Copy(?! style)/);
    await p.goto(`${base}${admin}/entities/${env.b}/edit?qa=1`);
    await ready(p);
    await toNavigator(a);
    await a.rightClick(await rowOfKind(p, "heading"));
    await menuPick(a, /^Paste after/);
    await p.waitForFunction(() => JSON.stringify(window.__hmBuilder.doc()).includes('"container"'), null, { timeout: 8000 });
  },
  async keyboard(a, env) {
    const p = a.page;
    await treeTo(a, (await nodes(p)).find((n) => n.t === "container").id);
    await a.press("Control+c");
    await p.goto(`${base}${admin}/entities/${env.b}/edit?qa=1`);
    await ready(p);
    await treeTo(a, (await nodes(p)).find((n) => n.t === "heading").id);
    await a.press("Control+v");
    await p.waitForFunction(() => JSON.stringify(window.__hmBuilder.doc()).includes('"container"'), null, { timeout: 8000 });
  },
  verify: async (p) => { need((await nodes(p)).some((n) => n.t === "container"), "the section did not arrive on page B"); },
  cleanup: async (p, env) => { await drop(p, env.a); await drop(p, env.b); },
};

// T7: turn a section into a reusable block; on another page the instance has no per-instance headline (E12, documented).
T.T7 = {
  title: "section into a reusable block",
  start: async (p) => { const id = await newDraft(p); await openBuilder(p, id); await seed(p, ["container"]); await fresh(p, id); return { id }; },
  async mouse(a) {
    const p = a.page;
    await toNavigator(a);
    await a.rightClick(await rowOfKind(p, "container"));
    await menuPick(a, /^Save as block/);
    const dlg = p.getByRole("dialog");
    await dlg.waitFor({ timeout: 4000 });
    await a.type("Promo");
    await a.click(dlg.getByRole("button", { name: /Save block/ }));
  },
  async keyboard(a) {
    const p = a.page;
    await treeTo(a, (await nodes(p)).find((n) => n.t === "container").id);
    await openPalette(a);
    await a.type("save as block");
    await a.press("Enter");
    const dlg = p.getByRole("dialog");
    await dlg.waitFor({ timeout: 4000 });
    await a.type("Promo");
    await a.press("Enter");
  },
  verify: async (p, env) => {
    await p.waitForFunction(() => window.__hmBuilder.doc().nodes.some((n) => n.t === "ref" && n.p?.block), null, { timeout: 8000 }).catch(() => { throw new Error("the section did not become a block reference"); });
    env.block = (await nodes(p)).find((n) => n.t === "ref").p.block;
    await p.locator('[data-hm-bs-ltab="nav"]').click().catch(() => {});
    await p.getByRole("button", { name: "More actions" }).click();
    need(await p.getByRole("menuitem", { name: "Versions" }).isVisible(), "no Versions entry in More");
    await p.keyboard.press("Escape");
    // The partial part: on another page the block is an instance with no per-instance headline (E12, not built). The Content
    // tab offers Edit block (every page) and Detach, nothing that edits the headline of this instance only.
    await savedNow(p);
    env.other = await newDraft(p);
    await openBuilder(p, env.other);
    await showAdd(p);
    await tileFor(p, "blk:" + env.block).click();
    await p.waitForFunction(() => window.__hmBuilder.doc().nodes.some((n) => n.t === "ref"), null, { timeout: 5000 });
    await settle(p, 400);
    const panel = await p.locator("[data-hm-bs-panel]").innerText();
    need(/Edit block/.test(panel) && /Detach/.test(panel), "the instance panel offers no Edit block / Detach: " + panel.slice(0, 160));
    need((await p.locator('[data-hm-bs-panel] input[aria-label="Text"], [data-hm-bs-panel] [contenteditable="true"]').count()) === 0, "the instance panel edits text of the block");
  },
  cleanup: async (p, env) => { await drop(p, env.block); await drop(p, env.id); await drop(p, env.other); },
};

// T8: fix a typo on the canvas; bold one word with a link. First-use: double-click a word, then the format bar buttons.
const TYPO = "Welcome to our new sitee today";
T.T8 = {
  title: "fix a typo on the canvas, bold one word with a link",
  firstUse: true,
  start: async (p) => {
    const id = await newDraft(p);
    await openBuilder(p, id);
    await seed(p, ["text"]);
    const ed = p.locator('[data-hm-bs-panel] [contenteditable="true"][aria-label="Text"]').first();
    await ed.waitFor({ timeout: 5000 });
    await ed.click();
    await p.keyboard.press("Control+a");
    await p.keyboard.type(TYPO);
    await settle(p, 900);
    await fresh(p, id);
    await p.locator(`${TREE}`).first().click();
    await settle(p, 600);
    return { id };
  },
  async mouse(a) {
    const p = a.page;
    const t = (await nodes(p)).find((n) => n.t === "text");
    const sel = textSel(t.id);
    const dbl = async (word) => {
      for (let i = 0; i < 6; i++) {
        const pt = await canvasPoint(p, sel, word);
        if (pt) {
          await a.clickAt(pt, { clickCount: 2 });
          await (await frame(p)).waitForFunction(() => document.querySelector("[contenteditable]") && getSelection().toString().length > 0, null, { timeout: 3000 }).catch(() => {});
          return;
        }
        await settle(p, 300);
      }
      throw new Error("no canvas point for " + word);
    };
    if (phoneNow(p) && (await sheet(p)) !== "canvas") await a.click(p.locator(`[data-hm-bs-dock="${await sheet(p)}"]`));
    await dbl("sitee");
    await a.type("site");
    await dbl("new");
    await a.click(p.locator('.hm-pe__fmt button[data-cmd="bold"]'));
    await a.click(p.locator('.hm-pe__fmt button[data-cmd="link"]'));
    await p.locator("#hm-fmt-href").waitFor({ state: "visible", timeout: 3000 });
    await a.type("/about");
    await a.press("Enter");
    await settle(p, 500);
  },
  // The selected text is edited on the canvas by Enter, then bold and a link from the keyboard (the inspector editor has no link).
  async keyboard(a) {
    const p = a.page;
    // The frame is not in the Tab order (tabindex -1, aria-hidden): Enter with the page focused (nothing in a control) edits the
    // selected text and moves the focus into the frame.
    await p.evaluate(() => document.activeElement?.blur?.());
    await a.press("Enter");
    await (await frame(p)).waitForFunction(() => document.querySelector("[contenteditable]"), null, { timeout: 4000 });
    await a.press("Control+End");
    await a.press("Control+ArrowLeft"); // before "today"
    await a.press("ArrowLeft"); // after "sitee"
    await a.press("Backspace"); // the typo
    await a.press("Control+ArrowLeft");
    await a.press("Control+ArrowLeft"); // before "new"
    await a.press("Shift+Control+ArrowRight");
    await settle(p, 150); // the editor reads a selection change on the next event; no person presses Ctrl+B in the same millisecond
    await a.press("Control+b");
    await a.press("Control+k"); // in a text edit Ctrl+K is the link, not the palette
    await p.locator("#hm-fmt-href").waitFor({ state: "visible", timeout: 3000 });
    // The panel lists the site's pages (a datalist); the browser's own suggestion popup does not take synthetic keys, so the
    // address is typed in full once the list is seen to offer About.
    need(await p.locator('#hm-fmt-pages option[value="/about"]').count(), "the link panel's page list has no About");
    await a.type("/about");
    await a.press("Enter"); // apply the link
    await settle(p, 1000); // the editor commits on a short delay
  },
  verify: async (p, env, mode) => {
    await p.waitForFunction(() => /<strong>/.test(JSON.stringify(window.__hmBuilder.doc())), null, { timeout: 4000 }).catch(() => {});
    const t = (await nodes(p)).find((n) => n.t === "text");
    const html = String(t.p.html ?? t.p.text);
    need(!/sitee/.test(html), "the typo is still there: " + html);
    need(/<strong>/.test(html), "no bold in: " + html);
    need(/<a [^>]*href="\/about\/?"/.test(html), "no link to the About page in: " + html);
  },
  cleanup: async (p, env) => drop(p, env.id),
};

// T9: undo three steps (History > Original by mouse, Ctrl+Z three times by keyboard), then bring one element back from an
// earlier saved version. Only the undo is held to the baseline (a.split()); the restore had none, it was impossible.
const FIRST_TITLE = "First title";
async function titledVersions(p) {
  // A published page keeps one version per save: the heading says First title, then Second title; the page is reopened after each.
  const id = await publishedPage(p, ["heading"]);
  const a = act(p, { mode: "mouse" });
  for (const text of [FIRST_TITLE, "Second title"]) {
    await showNav(p);
    await (await rowOfKind(p, "heading")).click();
    await fillField(a, "Text", text, { replace: true });
    await p.locator('[data-hm-bs-save="save"]:visible').click();
    await settle(p, 400);
    await savedNow(p);
    await openBuilder(p, id);
  }
  return id;
}
async function restoreDialogFirst(a) {
  const p = a.page;
  const dlg = p.locator("dialog.hm-dialog[open]");
  await dlg.waitFor({ timeout: 5000 });
  const pick = dlg.locator("label.hm-radiocard", { hasText: FIRST_TITLE }).first();
  await pick.waitFor({ timeout: 5000 });
  return { dlg, pick };
}
T.T9 = {
  title: "undo three steps, then restore one element from an earlier version",
  start: async (p) => {
    const id = await titledVersions(p);
    await seed(p, ["text", "button", "spacer"]); // three steps after the reload
    await showNav(p);
    return { id };
  },
  async mouse(a) {
    const p = a.page;
    if (phoneNow(p)) { await a.click(p.getByRole("button", { name: "More actions" })); await a.click(p.getByRole("menuitem", { name: "History" })); }
    else await a.click(p.locator("[data-hm-bs-history]"));
    const list = p.getByRole("listbox", { name: /Changes/ });
    await list.waitFor({ timeout: 3000 });
    await a.click(list.getByRole("option").first());
    await settle(p, 250);
    a.split();
    await toNavigator(a);
    await a.click(await rowOfKind(p, "heading"));
    await toInspector(a);
    await a.click(p.getByRole("button", { name: "Element options" }));
    await a.click(p.getByRole("menuitem", { name: /Restore from an earlier version/ }));
    const { dlg, pick } = await restoreDialogFirst(a);
    if (!(await pick.locator("input").isChecked())) await a.click(pick);
    await a.click(dlg.getByRole("button", { name: "Restore this element" }));
    await dlg.waitFor({ state: "detached", timeout: 3000 });
  },
  async keyboard(a) {
    const p = a.page;
    await p.evaluate(() => document.activeElement?.blur?.());
    for (let i = 0; i < 3; i++) await a.press("Control+z");
    await settle(p, 250);
    a.split();
    await treeTo(a, (await nodes(p)).find((n) => n.t === "heading").id);
    await a.press("Shift+F10");
    await p.locator(MENU).waitFor({ timeout: 3000 });
    await a.tabTo(() => /Restore from an earlier version/.test(document.activeElement?.textContent ?? ""), null, 40, "ArrowDown");
    await a.press("Enter");
    const { dlg, pick } = await restoreDialogFirst(a);
    if (!(await pick.locator("input").isChecked())) { await a.tabTo(() => document.activeElement?.type === "radio"); await a.tabTo(() => document.activeElement?.closest("label")?.textContent.includes("First title"), null, 6, "ArrowDown"); }
    await a.tabTo(() => /Restore this element/.test(document.activeElement?.textContent ?? ""));
    await a.press("Enter");
    await dlg.waitFor({ state: "detached", timeout: 3000 });
  },
  verify: async (p) => {
    const ns = await nodes(p);
    need(ns.length === 1 && ns[0].t === "heading", `${ns.length} elements are left after three undos: ${ns.map((n) => n.t).join()}`);
    need(ns[0].p.text === FIRST_TITLE, "the heading text is " + ns[0].p.text + " after the restore");
  },
  cleanup: async (p, env) => drop(p, env.id),
};

// T10: padding of a deeply nested element (columns > container > container > heading): one canvas click on the deepest
// element, the breadcrumbs name its path, the padding is typed in the box model.
T.T10 = {
  title: "padding of a deeply nested element",
  start: async (p) => {
    const id = await newDraft(p);
    await openBuilder(p, id);
    await seed(p, ["columns"]);
    await showNav(p);
    await (await rowOfKind(p, "container")).click();
    await seed(p, ["container"]);
    await seed(p, ["heading"]);
    await fresh(p, id);
    return { id };
  },
  async mouse(a) {
    const p = a.page;
    const heading = (await nodes(p)).find((n) => n.t === "heading");
    if (phoneNow(p)) {
      await toNavigator(a);
      await a.click(row(p, heading.id));
    } else {
      // One click on the canvas lands on the deepest element (columns > container > container > heading), and the strip names it.
      let pt = null;
      for (let i = 0; i < 20 && !pt; i++) pt = (await canvasPoint(p, textSel(heading.id))) ?? (await settle(p, 300), null);
      need(pt, "no canvas point for the heading");
      await a.clickAt(pt);
      await p.waitForFunction((id) => window.__hmBuilder.selection()[0] === id, heading.id, { timeout: 3000 }).catch(() => { throw new Error("the canvas click did not select the deepest element"); });
      const crumbs = await p.locator("[data-hm-pe-strip] .hm-pe__crumb").allTextContents();
      need(crumbs.length >= 5 && crumbs[0] === "Page" && crumbs.at(-1) === "Heading", "breadcrumbs: " + crumbs.join(" > "));
    }
    await toInspector(a);
    await a.click(p.getByRole("tab", { name: "Style" }));
    // Padding is an Essential for every kind (the box model's padding ring): no disclosure to open.
    await a.click(p.locator('[data-hm-bs-panel] [aria-label="Padding top"]'));
    await a.type("20");
    await a.press(phoneNow(p) ? "Enter" : "Tab");
    await settle(p, 250);
  },
  async keyboard(a) {
    const p = a.page;
    await treeTo(a, (await nodes(p)).find((n) => n.t === "heading").id);
    await a.press("Enter");
    await a.tabTo(() => document.activeElement?.getAttribute("role") === "tab", null, 20, "Shift+Tab"); // back to the tab list
    await a.press("ArrowRight"); // Style
    await a.tabTo(() => document.activeElement?.getAttribute("aria-label") === "Padding top");
    await a.type("20");
    await a.press("Tab");
    await settle(p, 250);
  },
  verify: async (p) => {
    const h = (await nodes(p)).find((n) => n.t === "heading");
    need(/"padding[^"]*top[^"]*":"?20|pt"?:"?20|20px/i.test(JSON.stringify(h)), "no padding of 20 on the heading: " + JSON.stringify(h).slice(0, 220));
  },
  cleanup: async (p, env) => drop(p, env.id),
};

// ---- run machinery ----
async function runOnce(id, mode, firstUse, variant = mode) {
  const task = T[id];
  const p = main;
  const run = variant === "keyboard" ? task.keyboard : variant === "plus" ? task.plus : task.mouse;
  await p.setViewportSize({ width: 1440, height: 900 });
  const env = await task.start(p, variant);
  if (mode === "phone") { await p.setViewportSize({ width: 390, height: 844 }); await settle(p, 500); }
  const a = act(p, { mode, firstUse });
  const t0 = Date.now();
  let error = null;
  try { await run(a, env); await task.verify(p, env, mode, variant); } catch (e) { error = String(e.message || e).split("\n").slice(0, VERBOSE ? 12 : 1).join(" | "); }
  const sec = (Date.now() - t0) / 1000;
  await p.setViewportSize({ width: 1440, height: 900 });
  await task.cleanup(p, env).catch((e) => console.log("  cleanup:", String(e.message).split("\n")[0]));
  const out = { ...a.c, sec: Math.round(sec * 10) / 10, ok: !error, ...(error ? { error } : {}) };
  if (a.part) out.part = { clicks: a.part.clicks, keys: a.part.keys, nav: a.part.nav, sec: Math.round(((a.part.at - t0) / 1000) * 10) / 10 };
  return out;
}
const within = (r, b) => (b.clicks === undefined || r.clicks <= b.clicks) && (b.keys === undefined || r.keys <= b.keys);
async function runTask(id) {
  const task = T[id];
  const b = BASELINE[id];
  const out = (results[id] = { title: task.title, baseline: b });
  if (task.owner && !isOwner) { out.skipped = "owner only"; return; }
  const modes = [["mouse", false, "mouse"], ...(task.keyboard ? [["keyboard", false, "keyboard"]] : []), ...(task.firstUse ? [["mouse", true, "firstUse"]] : []), ...(task.plus ? [["mouse", true, "plus"]] : [])].filter(([, , key]) => !MODE || MODE.test(key));
  for (const [mode, first, key] of modes) {
    const runs = [];
    for (let i = 0; i < RUNS; i++) runs.push(await runOnce(id, mode, first, key === "firstUse" ? "mouse" : key));
    const okAll = runs.every((r) => r.ok);
    // The counted part (T9: the undo) when a run marks one, else the whole run.
    const mine = runs.map((r) => r.part ?? r);
    const worst = { clicks: Math.max(...mine.map((r) => r.clicks)), keys: Math.max(...mine.map((r) => r.keys)), nav: Math.max(...mine.map((r) => r.nav)) };
    const sec = median(mine.map((r) => r.sec));
    const countsOk = within(worst, b);
    const wallOk = b.sec === undefined || sec <= b.sec * 1.5;
    const whole = runs[0].part ? { whole: { clicks: Math.max(...runs.map((r) => r.clicks)), keys: Math.max(...runs.map((r) => r.keys)), nav: Math.max(...runs.map((r) => r.nav)), sec: median(runs.map((r) => r.sec)) } } : {};
    out[key] = { ...worst, sec, secs: runs.map((r) => r.sec), ...whole, completed: okAll, countsOk, wallOk, pass: okAll && countsOk && wallOk, ...(okAll ? {} : { error: runs.find((r) => !r.ok).error }) };
    console.log(`${okAll ? "ok  " : "FAIL"} ${id} ${key.padEnd(8)} clicks ${worst.clicks} keys ${worst.keys} nav ${worst.nav} ${sec}s (baseline ${b.clicks ?? "-"}/${b.keys ?? "-"}/${b.sec}s x1.5) ${okAll ? (countsOk && wallOk ? "within" : `OVER${countsOk ? "" : " counts"}${wallOk ? "" : " wall"}`) : out[key].error}${whole.whole ? ` [whole run ${whole.whole.clicks}c/${whole.whole.keys}k/${whole.whole.nav}n ${whole.whole.sec}s]` : ""}`);
  }
  // phone: complete once, record
  const r = await runOnce(id, "phone", false);
  out.phone = { ...r, completed: r.ok, pass: r.ok };
  console.log(`${r.ok ? "ok  " : "FAIL"} ${id} phone    clicks ${r.clicks} keys ${r.keys} nav ${r.nav} ${r.sec}s ${r.ok ? "completed" : r.error}`);
  if (b.partial) out.partial = b.partial;
  const fu = [out.firstUse, out.plus].filter(Boolean);
  const best = ["mouse", "keyboard"].map((m) => out[m]).filter(Boolean);
  const pass = best.every((x) => x.completed) && best.some((x) => x.pass) && fu.every((x) => x.pass) && out.phone.completed;
  out.pass = pass;
  if (!pass) bad++;
}

// ---- checks ----
async function check(name, fn) {
  if (ONLY && !ONLY.test(name)) return;
  const e0 = errors.length;
  try {
    const detail = await fn();
    if (errors.length > e0) throw new Error("page error: " + errors[e0]);
    checks.push({ name, ok: true, detail });
    console.log(`ok   ${name}: ${detail}`);
  } catch (e) {
    bad++;
    const msg = String(e.message || e).split("\n").slice(0, VERBOSE ? 20 : 1).join(" | ");
    checks.push({ name, ok: false, detail: msg });
    console.log(`FAIL ${name}: ${msg}`);
  }
}
async function axeOn(p, what) {
  await settle(p, 500); // dialogs fade in; contrast read mid-fade is not the colour the person sees
  await p.evaluate(axeSrc);
  const r = await p.evaluate((t) => window.axe.run(document, { runOnly: { type: "tag", values: t } }), tags);
  axeRes[what] = { violations: r.violations.map((v) => `${v.id} (${v.nodes[0].target.join(" ")})${v.id === "target-size" ? " " + v.nodes[0].any.map((a) => a.message).join(" ") : ""}`), passes: r.passes.length };
  if (r.violations.length) throw new Error(`axe on ${what}: ` + axeRes[what].violations.join("; "));
  return `axe clean on ${what} (${r.passes.length} rules passed)`;
}

const isOwner = (process.env.QA_ROLE || "owner") === "owner";
const want = (id) => !ONLY || ONLY.test(id);

// ---------------- run ----------------
console.log(`builder-ux-tasks: ${RUNS} timing runs per mode, base ${base}`);
const ids = Object.keys(T).filter(want);
for (const id of ids) await runTask(id);

// ---- shell checks ----
await check("topbar-chip: one status chip, Draft and Saved, then Not saved while an edit is pending", async () => {
  const id = await newDraft(main);
  await openBuilder(main, id);
  const chip = main.locator("[data-hm-bs-state]");
  const first = (await chip.innerText()).replace(/\s+/g, " ");
  need(/Draft/.test(first) && /Saved/.test(first), "chip reads: " + first);
  need((await main.locator("[data-hm-bs-status]").count()) === 0 || !(await main.locator("[data-hm-bs-status]").isVisible()), "the server badge shows next to the chip");
  await tileFor(main, "heading").click();
  await main.waitForFunction(() => !/Saved/.test(document.querySelector("[data-hm-bs-state]")?.textContent ?? ""), null, { timeout: 3000 });
  await savedNow(main);
  await drop(main, id);
  return `"${first}" -> unsaved -> saved`;
});

await check("device-announce: switching the width says so in the live region and shows a labelled chip", async () => {
  const id = await seeded(main);
  await device(main, "md").click();
  await settle(main, 400);
  const chip = main.locator("[data-hm-bs-device-chip]");
  need(await chip.isVisible(), "no device chip on Tablet");
  const chipText = await chip.innerText();
  need(/Tablet/.test(chipText), "chip: " + chipText);
  const live = await main.locator('[role="status"][aria-live="polite"].hm-visually-hidden').allInnerTexts();
  need(live.some((t) => /Tablet|tablet/.test(t)), "live region: " + live.join("|"));
  const labels = await main.locator(".hm-bs__devices [data-hm-bs-device]").allInnerTexts();
  need(labels.every((l) => /\d{3,4}/.test(l.replace(/\s+/g, " ")) || /Desktop/.test(l)), "device labels lack their widths: " + labels.join("|"));
  await device(main, "base").click();
  need(await chip.isHidden(), "the chip stays on Desktop");
  await drop(main, id);
  return `${chipText}; labels ${labels.join(" / ").replace(/\s+/g, " ")}`;
});

await check("more-menu-order: Versions, Page settings, Site styles (owner), Keyboard shortcuts, Delete page last", async () => {
  const id = await seeded(main, ["heading"]);
  await main.getByRole("button", { name: "More actions" }).click();
  const items = (await main.locator("#bs-more [role=menuitem]:visible").allInnerTexts()).map((t) => t.trim());
  const at = (t) => items.indexOf(t);
  need(at("Versions") === 0 && at("Page settings") === 1, "start of the menu: " + items.join("|"));
  need(items.at(-1) === "Delete page", "Delete page is not last: " + items.join("|"));
  need(at("Keyboard shortcuts") > 1 && at("Keyboard shortcuts") < items.length - 1, "shortcuts entry misplaced: " + items.join("|"));
  if (isOwner) need(items.includes("Site styles"), "owner has no Site styles: " + items.join("|"));
  else need(!items.includes("Site styles"), "a non-owner sees Site styles");
  const danger = await main.locator('#bs-more [role=menuitem][data-tone="danger"]').count();
  need(danger === 1, "Delete page is not marked as danger");
  await main.keyboard.press("Escape");
  await drop(main, id);
  return items.join(" | ");
});

await check("add-tab: Navigator | Add is a tab list with roving focus; the Add tab lists Patterns and searches", async () => {
  const id = await seeded(main, ["heading"]);
  const tabs = main.locator("[data-hm-bs-ltabs] [role=tab]");
  need((await tabs.count()) === 2, "two tabs expected");
  await main.locator('[data-hm-bs-ltab="nav"]').click();
  need((await main.locator('[data-hm-bs-ltab="add"]').getAttribute("tabindex")) === "-1", "the inactive tab is in the tab order");
  await main.locator('[data-hm-bs-ltab="nav"]').focus();
  await main.keyboard.press("ArrowRight");
  need((await main.locator('[data-hm-bs-ltab="add"]').getAttribute("aria-selected")) === "true", "ArrowRight did not select Add");
  need(await main.locator("#hm-bs-lp-add").isVisible(), "the Add panel is hidden");
  const groups = await main.locator("[data-hm-bs-lpanel=add] h3").allInnerTexts();
  need(groups.includes("Patterns") && groups.includes("Recent") === (groups[0] === "Recent"), "groups: " + groups.join("|"));
  await main.locator("#hm-bs-pal-q").fill("zzzz");
  need(/Nothing matches/.test(await main.locator("[data-hm-bs-lpanel=add]").innerText()), "no empty message");
  await main.locator("#hm-bs-pal-q").fill("");
  await drop(main, id);
  return `groups ${groups.join(", ")}`;
});

await check("commandk: opens from the tree, from a text field and with the pane hidden; not during a canvas text edit", async () => {
  const id = await seeded(main, ["heading", "text"]);
  await showNav(main);
  const dlg = main.locator("#hm-bs-cmdk");
  await (await rowOfKind(main, "heading")).click();
  await main.keyboard.press("Control+k");
  await dlg.waitFor({ state: "visible", timeout: 3000 });
  need(await main.locator("#hm-bs-ck-q").evaluate((e) => document.activeElement === e), "focus is not in the search");
  await main.keyboard.press("Escape");
  need(!(await dlg.isVisible()), "Esc left it open");
  await main.locator("[data-hm-bs-title]").focus();
  await main.keyboard.press("Control+k");
  await dlg.waitFor({ state: "visible", timeout: 3000 });
  await main.keyboard.press("Escape");
  await main.keyboard.press("Control+Alt+[");
  await main.keyboard.press("Control+k");
  await dlg.waitFor({ state: "visible", timeout: 3000 });
  await main.keyboard.press("Escape");
  await main.keyboard.press("Control+Alt+[");
  const t = (await nodes(main)).find((n) => n.t === "text");
  let editing = false;
  for (let i = 0; i < 5 && !editing; i++) {
    const pt = await canvasPoint(main, textSel(t.id));
    if (pt) await main.mouse.dblclick(pt.x, pt.y);
    editing = await (await frame(main)).waitForFunction(() => document.querySelector("[contenteditable]"), null, { timeout: 1500 }).then(() => true, () => false);
  }
  need(editing, "the double-click never started a canvas text edit");
  await main.keyboard.press("Control+k");
  await settle(main, 300);
  need(!(await dlg.isVisible()), "Ctrl+K opened the palette during a text edit (it is the link shortcut there)");
  await main.keyboard.press("Escape");
  await drop(main, id);
  return "tree, field, hidden pane open it; a text edit keeps Ctrl+K for links";
});

await check("conflict-banner: a save from another tab shows the banner over the canvas, the layout does not move", async () => {
  const id = await seeded(main, ["heading"]);
  const other = await main.context().newPage();
  watch(other);
  await openBuilder(other, id);
  await seed(other, ["text"]);
  await savedNow(other);
  await other.close();
  const box = () => main.locator("[data-hm-builder]").evaluate((e) => { const r = e.getBoundingClientRect(); const c = document.querySelector("[data-hm-bs-canvas]").getBoundingClientRect(); return [r.x, r.y, r.width, r.height, c.y, c.height].map(Math.round).join(","); });
  const before = await box();
  await showAdd(main);
  await tileFor(main, "spacer").click();
  const banner = main.locator('[data-banner="conflict"]');
  await banner.waitFor({ state: "visible", timeout: 20000 });
  const tBanner = Date.now();
  const chipErr = await main.locator("[data-hm-bs-state] .hm-savestate").getAttribute("data-state");
  const after = await box();
  need(before === after, `the layout moved: ${before} -> ${after}`);
  const text = await banner.innerText();
  need(/Keep my version/.test(text) && /Load their version/.test(text), "banner choices: " + text);
  need(!/overwrite|reload theirs/i.test(text), "old wording: " + text);
  const bb = await banner.boundingBox();
  const cv = await main.locator("[data-hm-bs-canvas]").boundingBox();
  need(bb.y >= cv.y - 1 && bb.y + bb.height <= cv.y + cv.height + 1, "the banner is not inside the canvas area");
  need(chipErr === "error" || chipErr === "conflict", "the chip says " + chipErr);
  await banner.getByRole("button", { name: "Keep my version" }).click();
  await banner.waitFor({ state: "detached", timeout: 15000 });
  await savedNow(main);
  await drop(main, id);
  return `banner over the canvas in ${Date.now() - tBanner}ms of the chip, layout ${after}, cleared by Keep my version`;
});

await check("recovery-banner-noshift: an unsaved copy kept on the server is offered without moving the canvas", async () => {
  const id = await publishedPage(main);
  const cv0 = await main.locator("[data-hm-bs-canvas]").boundingBox();
  await leaveEdited(main, id);
  const banner = main.locator('[data-banner="recovery"]');
  await banner.waitFor({ state: "visible", timeout: 8000 });
  const cv1 = await main.locator("[data-hm-bs-canvas]").boundingBox();
  need(Math.abs(cv0.y - cv1.y) < 1 && Math.abs(cv0.height - cv1.height) < 1, `the canvas moved: ${JSON.stringify(cv0)} -> ${JSON.stringify(cv1)}`);
  const text = await banner.innerText();
  need(/Restore my unsaved changes/.test(text) && /Discard them/.test(text), "banner: " + text);
  await banner.getByRole("button", { name: "Discard them" }).click();
  await drop(main, id);
  return "banner floats; canvas box unchanged";
});

await check("coachmarks-once: a hint shows on first use, dismisses for good and does not return after a reload", async () => {
  const s = await newSession(browser, { theme: "light", width: 1440 });
  watch(s);
  const id = await seeded(main, ["heading"]); // made in another browser profile: this one has not inserted anything
  await openBuilder(s, id);
  const hints = s.locator(".hm-bs-hint");
  need((await hints.count()) >= 1, "no hint on a first visit");
  const n = await hints.count();
  // Undoing a delete re-inserts the element, but it is not a first insert: the hints stay.
  await (await rowOfKind(s, "heading")).click();
  await s.keyboard.press("Delete");
  await s.keyboard.press("Control+z");
  await settle(s, 300);
  need((await nodes(s)).some((x) => x.t === "heading"), "undo did not bring the heading back");
  need((await hints.count()) === n, "an undo counted as a first insert and dismissed the hints");
  for (let i = 0; i < n; i++) await s.locator(".hm-bs-hint").first().getByRole("button", { name: "Got it" }).click();
  await openBuilder(s, id);
  need((await s.locator(".hm-bs-hint").count()) === 0, "a hint came back after Got it");
  await drop(main, id);
  await s.context().close();
  return `${n} hint(s) shown once`;
});

await check("phone-dock: Navigator, Add and Inspector sit in a dock; picking an element opens the Inspector sheet", async () => {
  const id = await seeded(main, ["heading", "text"]);
  await main.setViewportSize({ width: 390, height: 844 });
  await settle(main, 400);
  try {
    const dock = main.locator("[data-hm-bs-dock]");
    need(await dock.first().isVisible(), "no dock");
    const names = (await main.locator("[data-hm-bs-dock] button").allInnerTexts()).map((t) => t.trim());
    need(names.join() === "Navigator,Add,Inspector", "dock: " + names.join());
    await main.locator('[data-hm-bs-dock="nav"]').click();
    await (await rowOfKind(main, "heading")).click();
    await settle(main, 400);
    need((await sheet(main)) === "insp", "the Inspector sheet did not open, sheet is " + (await sheet(main)));
    need(await main.locator("[data-hm-bs-device]").first().isVisible(), "the device switch is not visible on the phone");
    await main.getByRole("button", { name: "More actions" }).click();
    need(await main.locator('#bs-more [role=menuitem]:visible', { hasText: /^Undo$/ }).count(), "Undo is not reachable from More on the phone");
    await main.keyboard.press("Escape");
    need((await main.evaluate(() => document.documentElement.scrollWidth)) <= 390, "the page scrolls sideways");
  } finally { await main.setViewportSize({ width: 1440, height: 900 }); }
  await drop(main, id);
  return "dock, auto-open sheet, device switch visible, Undo in More, no sideways scroll";
});

await check("sitestyles-owner-only: an owner opens the drawer from More; an editor never sees the entry", async () => {
  const id = await seeded(main, ["heading"]);
  await main.getByRole("button", { name: "More actions" }).click();
  const owner = await main.locator("#bs-more [role=menuitem]", { hasText: "Site styles" }).isVisible();
  await main.keyboard.press("Escape");
  let editorSees = null;
  const ed = await browser.newContext({ viewport: { width: 1440, height: 900 } });
  const ep = await ed.newPage();
  await ep.goto(`${base}${admin}/login`);
  await ep.fill("#email", process.env.QA_EDITOR_EMAIL || "editor@qa.test");
  await ep.fill("#password", password);
  await Promise.all([ep.waitForURL((u) => !u.pathname.endsWith(`${admin}/login`)), ep.click(`form[action="${admin}/login"] button[type=submit]`)]);
  await ep.goto(`${base}${admin}/entities/${id}/edit?qa=1`);
  await ready(ep);
  await ep.getByRole("button", { name: "More actions" }).click();
  editorSees = await ep.locator("#bs-more [role=menuitem]:visible", { hasText: "Site styles" }).count();
  const cmd = await ep.evaluate(() => JSON.stringify(window.__hmBuilder.doc()).length >= 0);
  await ep.keyboard.press("Control+k");
  await ep.locator("#hm-bs-cmdk").waitFor({ state: "visible", timeout: 3000 });
  await ep.locator("#hm-bs-ck-q").fill("site styles");
  const inPalette = await ep.locator("#hm-bs-cmdk [role=option]", { hasText: "Site styles" }).count();
  await ed.close();
  need(cmd, "editor session failed");
  need(owner === isOwner || !isOwner, "the owner does not see Site styles");
  need(editorSees === 0 && inPalette === 0, `the editor sees Site styles (menu ${editorSees}, palette ${inPalette})`);
  await drop(main, id);
  return `owner ${owner ? "sees" : "does not see"} it; the editor sees it in neither the menu nor Ctrl+K`;
});

await check("start-panel-restore: restoring the kept copy on an empty page does not leave the start panel over it", async () => {
  const id = await publishedPage(main, []);
  await leaveEdited(main, id);
  const banner = main.locator('[data-banner="recovery"]');
  await banner.waitFor({ state: "visible", timeout: 8000 });
  await main.locator(".hm-bs__start").waitFor({ timeout: 5000 });
  await banner.getByRole("button", { name: "Restore my unsaved changes" }).click();
  await settle(main, 300);
  need((await nodes(main)).length > 0, "nothing was restored");
  need((await main.locator(".hm-bs__start").count()) === 0, "the start panel still covers the restored page");
  await drop(main, id);
  return "start panel gone after Restore";
});

await check("recovery-gone-after-save: a recovery offer that was ignored goes with the first save", async () => {
  const id = await publishedPage(main);
  await leaveEdited(main, id);
  await main.locator('[data-banner="recovery"]').waitFor({ state: "visible", timeout: 8000 });
  await showAdd(main);
  await tileFor(main, "spacer").click();
  await main.locator('[data-hm-bs-save="save"]:visible').click();
  await main.waitForFunction(() => !document.querySelector('[data-banner="recovery"]'), null, { timeout: 20000 });
  await drop(main, id);
  return "offer cleared by the first save";
});

await check("note-banner: an info note can be dismissed and goes with the next save", async () => {
  const id = await seeded(main, ["heading"]);
  const key = `hm-builder-note:${id}`;
  for (const how of ["dismiss", "save"]) {
    await main.evaluate(([k]) => sessionStorage.setItem(k, "The slug is taken, so this page was saved as a draft."), [key]);
    await openBuilder(main, id);
    const note = main.locator('[data-banner="note"]');
    await note.waitFor({ state: "visible", timeout: 5000 });
    if (how === "dismiss") await note.getByRole("button", { name: "Dismiss" }).click();
    else { await showAdd(main); await tileFor(main, "spacer").click(); }
    await main.waitForFunction(() => !document.querySelector('[data-banner="note"]'), null, { timeout: 20000 });
    await savedNow(main);
  }
  await drop(main, id);
  return "note dismissed by its button and cleared by the next save";
});

await check("insertAt-resolves-after-insert: the UX-2 contract resolves true only once the element is in the page", async () => {
  const id = await newDraft(main);
  await openBuilder(main, id);
  const r = await main.evaluate(async () => {
    const was = window.__hmBuilder.doc().nodes.length;
    const ok = await window.__hmBuilder.insertAt({ parent: "", index: 0 }, "heading");
    const now = window.__hmBuilder.doc().nodes.length;
    const none = await window.__hmBuilder.insertAt({ parent: "", index: 0 }, "no-such-kind");
    return { was, ok, now, none };
  });
  need(r.ok === true && r.now === r.was + 1, "resolved " + JSON.stringify(r));
  need(r.none === false, "an unknown item resolved " + r.none);
  await drop(main, id);
  return JSON.stringify(r);
});

await check("add-tab-compact: one short Patterns row with a toggle, elements in view, and he + Enter inserts Heading", async () => {
  const id = await newDraft(main);
  await openBuilder(main, id);
  await showAdd(main);
  const pats = main.locator('[data-hm-bs-palette] section[aria-label="Patterns"]');
  const n = await pats.locator("[data-pal]").count();
  need(n >= 1 && n <= 4, `${n} pattern tiles show before Show all`);
  const toggle = pats.getByRole("button", { name: /^Show all patterns \(\d+\)$/ });
  if (await toggle.count()) {
    await toggle.click();
    need((await pats.locator("[data-pal]").count()) > n, "Show all patterns did not add tiles");
    const kept = pats.getByRole("button", { name: "Show fewer patterns", expanded: true });
    need(await kept.evaluate((b) => b === document.activeElement), "focus left the Show all patterns toggle");
    await kept.click();
    need(await pats.getByRole("button", { name: /^Show all patterns/, expanded: false }).evaluate((b) => b === document.activeElement), "focus left the toggle on Show fewer");
    await main.evaluate(() => document.activeElement.blur()); // the "/" key is for focus outside controls
  }
  const pane = await main.locator('[data-hm-bs-lpanel="add"]').boundingBox();
  const head = await main.locator('[data-hm-bs-palette] section[aria-label="Elements"] [data-pal="heading"]').boundingBox();
  need(head && head.y + head.height <= pane.y + pane.height, `Heading is below the fold: ${head?.y} vs pane bottom ${pane.y + pane.height}`);
  await main.keyboard.press("/");
  await main.keyboard.type("he");
  await main.keyboard.press("Enter");
  await main.waitForFunction(() => window.__hmBuilder.doc().nodes.length > 0, null, { timeout: 5000 });
  const t = (await doc(main)).nodes[0].t;
  need(t === "heading", "he + Enter inserted " + t);
  await drop(main, id);
  return `${n} pattern tiles, Heading in view, he + Enter inserted ${t}`;
});

await check("footer-hints: the / key sits inside Add element, and the palette button is named and shows its key", async () => {
  const id = await newDraft(main);
  await openBuilder(main, id);
  const add = main.locator("[data-hm-bs-addbtn]");
  const pal = main.getByRole("button", { name: /open command palette$/ });
  need(await pal.isVisible(), "no button whose name ends in open command palette (the visible Ctrl K leads the name: WCAG 2.5.3)");
  const hint = (await pal.innerText()).trim();
  need(/^(Ctrl K|⌘K)$/.test(hint), "the key hint reads " + hint);
  const after = await add.evaluate((b) => getComputedStyle(b, "::after").content);
  need(/^"\/"/.test(after), "the / keycap is not drawn in the button: " + after);
  const kbd = await main.locator(".hm-bs__navfoot kbd").count();
  need(kbd === 0, "a loose <kbd> is left outside the button");
  need((await add.getAttribute("aria-keyshortcuts")) === "/", "no aria-keyshortcuts on Add element");
  await drop(main, id);
  return `Add element draws ${after}; palette button reads ${hint}`;
});

await check("start-blank: Start blank removes the panel and puts the focus in the Add search", async () => {
  const id = await newDraft(main);
  await openBuilder(main, id, 800);
  const start = main.locator(".hm-bs__start");
  await start.waitFor({ timeout: 5000 });
  const first = (await start.locator(".hm-bs-start__n").first().innerText()).trim();
  await start.getByRole("button", { name: "Start blank" }).click();
  await main.waitForFunction(() => document.activeElement?.id === "hm-bs-pal-q", null, { timeout: 5000 });
  need((await main.locator(".hm-bs__start").count()) === 0, "the start panel is still there");
  await drop(main, id);
  return `first start tile: ${first}`;
});

await check("phone-chrome: one pane chooser (the dock) and the status chip says Draft · Saved, not a bare dot", async () => {
  const id = await seeded(main, ["heading"]);
  await main.setViewportSize({ width: 390, height: 844 });
  await settle(main, 400);
  try {
    await main.locator('[data-hm-bs-dock="add"]').click();
    need(!(await main.locator(".hm-bs__ltabs").isVisible()), "the inner Navigator | Add tab list shows under the dock");
    const chip = await main.locator("[data-hm-bs-state] .hm-savestate").evaluate((e) => ({ after: getComputedStyle(e, "::after").content, w: parseFloat(getComputedStyle(e, "::after").width) }));
    need(/Saved/.test(chip.after) && chip.w > 20, "the chip shows " + JSON.stringify(chip));
    return "dock only, chip " + chip.after;
  } finally { await main.setViewportSize({ width: 1440, height: 900 }); await drop(main, id); }
});

await check("small-fixes: the shortcut sheet search keeps its height, and the Add search placeholder fits", async () => {
  const id = await newDraft(main);
  await openBuilder(main, id);
  await main.locator("body").click({ position: { x: 5, y: 5 } });
  await main.keyboard.press("?");
  const q = main.locator("#hm-bs-sheet-q");
  await q.waitFor({ state: "visible", timeout: 5000 });
  const box = await q.boundingBox();
  need(box.height >= 30, "the shortcut search is squashed: " + box.height + "px");
  await main.keyboard.press("Escape");
  await showAdd(main);
  const fits = await main.locator("#hm-bs-pal-q").evaluate((e) => { const c = document.createElement("canvas").getContext("2d"); c.font = getComputedStyle(e).font; return c.measureText(e.placeholder).width <= e.clientWidth - 16; });
  need(fits, "the Add search placeholder is clipped");
  await drop(main, id);
  return `sheet search ${Math.round(box.height)}px tall`;
});

await check("restore-dialog: one element goes back to an earlier saved version, the rest of the page stays, one undo undoes it", async () => {
  const id = await publishedPage(main, ["heading", "text"]); // a draft's autosaves join one version; Update keeps one per save
  const a = act(main, { mode: "mouse" });
  const save = async () => {
    await main.locator('[data-hm-bs-save="save"]:visible').click();
    await settle(main, 400);
    await savedNow(main);
  };
  const edit = async (text) => {
    await showNav(main);
    await (await rowOfKind(main, "heading")).click();
    await fillField(a, "Text", text, { replace: true });
    await save();
    await openBuilder(main, id);
  };
  await edit("First title");
  await showNav(main);
  await (await rowOfKind(main, "text")).click();
  await fillField(a, "Text", "Other words stay", { replace: true });
  await save();
  await openBuilder(main, id);
  await edit("Second title");
  await showNav(main);
  await (await rowOfKind(main, "heading")).click();
  await main.getByRole("button", { name: "Element options" }).click();
  await main.getByRole("menuitem", { name: /Restore from an earlier version/ }).click();
  const dlg = main.locator("dialog.hm-dialog[open]");
  await dlg.waitFor({ timeout: 5000 });
  const label = await dlg.locator("h2").textContent();
  need(/^Restore “Heading · /.test(label), "the dialog title is " + label);
  const buttons = await dlg.locator(".hm-dialog__foot button").allTextContents();
  need(/Cancel/.test(buttons[0]) && /Restore this element/.test(buttons[1]), "buttons " + buttons.join("|"));
  const pick = dlg.locator("label.hm-radiocard", { hasText: "First title" }).first();
  await pick.waitFor({ timeout: 5000 }).catch(async () => { throw new Error("no First title in the list: " + (await dlg.innerText()).replace(/\s+/g, " ")); });
  await pick.click();
  await dlg.getByRole("button", { name: "Restore this element" }).click();
  await dlg.waitFor({ state: "detached", timeout: 3000 });
  let ns = await nodes(main);
  need(ns.find((n) => n.t === "heading").p.text === "First title", "the heading text is " + ns.find((n) => n.t === "heading").p.text);
  need(ns.find((n) => n.t === "text").p.text === "Other words stay", "the other element changed");
  await main.evaluate(() => document.activeElement?.blur?.());
  await main.keyboard.press("Control+z");
  await settle(main, 300);
  ns = await nodes(main);
  need(ns.find((n) => n.t === "heading").p.text === "Second title", "one undo did not bring the heading back to Second title");
  await drop(main, id);
  return "heading back to First title, text untouched, one undo";
});

// ---- canvas checks: a 2-column section with a heading in its first column ----
async function columnsWithHeading(p) {
  const id = await newDraft(p);
  await openBuilder(p, id);
  const ok = await p.evaluate(async () => {
    if (!(await window.__hmBuilder.insertAt({ parent: "", index: 0 }, "columns"))) return "columns";
    const cols = window.__hmBuilder.doc().nodes[0];
    return (await window.__hmBuilder.insertAt({ parent: cols.c[0].id, index: 0 }, "heading")) ? "" : "heading";
  });
  need(!ok, "the " + ok + " was not inserted");
  await settle(p, 700);
  const cols = (await doc(p)).nodes[0];
  return { id, cols, head: cols.c[0].c[0] };
}
// A canvas element's box in page pixels (the frame is scaled to its pane).
async function elementBox(p, id) {
  const f = await frame(p);
  const fb = await (await f.frameElement()).boundingBox();
  const r = await f.evaluate((i) => { const e = document.querySelector(`[data-hm-node="${i}"]`).getBoundingClientRect(); return { x: e.x, y: e.y, w: e.width, h: e.height, iw: innerWidth }; }, id);
  const k = fb.width / r.iw;
  return { x: fb.x + r.x * k, y: fb.y + r.y * k, w: r.w * k, h: r.h * k, k };
}
async function clickCanvas(p, id) {
  let pt = null;
  for (let i = 0; i < 20 && !pt; i++) pt = (await canvasPoint(p, textSel(id))) ?? (await settle(p, 300), null);
  need(pt, "no canvas point for " + id);
  await p.mouse.click(pt.x, pt.y);
  await settle(p, 500);
  return pt;
}

await check("hit-test-deepest: a click at the centre of a heading in a 2-column section selects the heading, not its column or section", async () => {
  const { id, cols, head } = await columnsWithHeading(main);
  await clickCanvas(main, head.id);
  const sel = await main.evaluate(() => window.__hmBuilder.selection());
  need(sel[0] === head.id, `selected ${sel[0]}, wanted the heading ${head.id} (column ${cols.c[0].id}, section ${cols.id})`);
  const crumbs = await main.locator("[data-hm-pe-strip] .hm-pe__crumb").allTextContents();
  need(crumbs[0] === "Page" && crumbs.at(-1) === "Heading" && crumbs.length >= 4, "breadcrumbs: " + crumbs.join(" > "));
  await main.locator("[data-hm-pe-strip] .hm-pe__crumb").nth(crumbs.length - 2).click();
  await settle(main, 300);
  need((await main.evaluate(() => window.__hmBuilder.selection()))[0] === cols.c[0].id, "a crumb did not select its ancestor");
  await drop(main, id);
  return `heading selected; ${crumbs.join(" > ")}; a crumb selects the column`;
});

await check("toolbar-outside: the element toolbar floats outside the selected element, is built once, and works by keyboard", async () => {
  const { id, head } = await columnsWithHeading(main);
  const pt = await clickCanvas(main, head.id);
  const tb = main.locator("[data-hm-pe-toolbar]");
  await tb.waitFor({ state: "visible", timeout: 3000 });
  const t = await tb.boundingBox();
  const el = await elementBox(main, head.id);
  need(!(t.x < el.x + el.w && t.x + t.width > el.x && t.y < el.y + el.h && t.y + t.height > el.y), `the toolbar covers the element: ${JSON.stringify(t)} vs ${JSON.stringify(el)}`);
  await main.evaluate(() => { window.__tb = document.querySelector("[data-hm-pe-toolbar]"); });
  await main.mouse.move(pt.x + 5, pt.y + 5);
  await main.mouse.move(pt.x + 40, pt.y + 20);
  await settle(main, 300);
  need(await main.evaluate(() => window.__tb === document.querySelector("[data-hm-pe-toolbar]") && window.__tb.isConnected), "a hover redraw rebuilt the toolbar");
  const acts = await main.locator("[data-hm-pe-toolbar] [data-act]").evaluateAll((bs) => bs.map((b) => b.getAttribute("aria-label").replace(/^(Select parent),.*/, "$1")));
  need(acts.join("|") === "Select parent|Move up|Move down|Duplicate|Hide on this device|Delete|More element actions", "toolbar buttons: " + acts.join("|"));
  // the keyboard path to the picker: More, then Add after
  await main.locator('[data-hm-pe-toolbar] [data-act="more"]').focus();
  await main.keyboard.press("Enter");
  await main.getByRole("menuitem", { name: /^Add after/ }).click();
  await main.getByRole("dialog", { name: "Add a section" }).waitFor({ state: "visible", timeout: 3000 }).catch(() => need(false, "More > Add after did not open the layout picker"));
  await main.keyboard.press("Escape");
  await settle(main, 200);
  await main.locator('[data-hm-pe-toolbar] [data-act="parent"]').focus();
  await main.keyboard.press("ArrowRight");
  need((await main.evaluate(() => document.activeElement?.dataset.act)) === "duplicate", "ArrowRight did not move to Duplicate");
  const n = (await nodes(main)).length;
  await main.keyboard.press("Enter");
  await settle(main, 500);
  need((await nodes(main)).length > n, "Enter on Duplicate did not duplicate");
  await drop(main, id);
  return `toolbar ${Math.round(t.y + t.height)}px vs element top ${Math.round(el.y)}px, kept across a hover, buttons by arrow keys, More > Add after opens the picker`;
});

await check("zoom-floor: a fixed zoom is exact, survives a resize, and Fit never goes under half size", async () => {
  const { id } = await columnsWithHeading(main);
  const scale = async () => { const f = await frame(main); const b = await (await f.frameElement()).boundingBox(); return b.width / (await f.evaluate(() => innerWidth)); };
  const z = (m) => main.locator(`[data-hm-pe-strip] [data-zoom="${m}"]`).click();
  const seen = {};
  for (const m of ["100", "75", "50"]) { await z(m); await settle(main, 300); seen[m] = await scale(); need(Math.abs(seen[m] - Number(m) / 100) < 0.02, `zoom ${m}% gave scale ${seen[m]}`); }
  await z("75");
  await main.setViewportSize({ width: 1250, height: 900 });
  await settle(main, 500);
  need(Math.abs((await scale()) - 0.75) < 0.02, "a resize changed the 75% zoom to " + (await scale()));
  await z("fit");
  await main.setViewportSize({ width: 960, height: 900 });
  await settle(main, 600);
  need((await scale()) >= 0.49, "Fit went below half size: " + (await scale()));
  await main.setViewportSize({ width: 1440, height: 900 });
  await drop(main, id);
  return `100% ${seen["100"].toFixed(2)}, 75% ${seen["75"].toFixed(2)}, 50% ${seen["50"].toFixed(2)}; 75% kept across a resize; Fit floor 0.5`;
});

await check("device-band: Tablet and Phone each show a coloured band and a label with their width; Desktop shows none", async () => {
  const { id } = await columnsWithHeading(main);
  const label = main.locator(".hm-pe__bandlabel");
  const colour = () => main.locator(".hm-pe__stage").evaluate((e) => getComputedStyle(e, "::before").backgroundColor);
  need(await label.isHidden(), "a band label shows on Desktop");
  const got = {};
  for (const [d, re] of [["md", /^Tablet · editing styles up to 1024 px · Desktop is not changed$/], ["sm", /^Phone · editing styles up to 640 px · Desktop is not changed$/]]) {
    await device(main, d).click();
    await settle(main, 300);
    const t = await label.textContent();
    need(re.test(t), `the ${d} label reads "${t}"`);
    got[d] = await colour();
    need(got[d] !== "rgba(0, 0, 0, 0)", "no band at " + d);
  }
  await device(main, "base").click();
  await settle(main, 200);
  need(await label.isHidden(), "the band label stays on Desktop");
  await drop(main, id);
  return `Tablet ${got.md}, Phone ${got.sm}`;
});

await check("ctrlk-from-frame: Ctrl+K pressed while the canvas has the focus opens the command palette (a text edit keeps it for links)", async () => {
  const { id, head } = await columnsWithHeading(main);
  await clickCanvas(main, head.id);
  await (await (await frame(main)).frameElement()).focus(); // the click selected; the keys go to whatever has the focus, here the frame
  need(await main.evaluate(() => document.activeElement?.tagName === "IFRAME"), "the canvas frame cannot take the focus");
  await main.keyboard.press("Control+k");
  await main.locator("#hm-bs-cmdk").waitFor({ state: "visible", timeout: 3000 });
  need(await main.locator("#hm-bs-ck-q").evaluate((e) => document.activeElement === e), "the palette search has no focus");
  await main.keyboard.press("Escape");
  await drop(main, id);
  return "palette open from the frame, search focused";
});

// ---- axe on the surfaces ----
const ax = await newSession(browser, { theme: "light", width: 1440, bypassCSP: true });
watch(ax);
let axId = null;
await check("axe: start panel on an empty page", async () => {
  axId = await newDraft(ax);
  await openBuilder(ax, axId, 800);
  await ax.locator(".hm-bs__start").waitFor({ timeout: 5000 });
  return axeOn(ax, "start panel");
});
await check("axe: Add tab (patterns and tiles)", async () => {
  await showAdd(ax);
  return axeOn(ax, "Add tab");
});
await check("axe: builder with a selection", async () => {
  await seed(ax, ["heading", "text"]);
  await showNav(ax);
  await (await rowOfKind(ax, "heading")).click();
  await settle(ax, 500);
  return axeOn(ax, "builder with a selection");
});
await check("axe: Style tab (State, Essentials, source chips, Show on, box model)", async () => {
  await ax.getByRole("tab", { name: "Style" }).click();
  await settle(ax, 300);
  try { return await axeOn(ax, "Style tab"); } finally { await ax.getByRole("tab", { name: "Content" }).click(); }
});
await check("axe: layout picker open", async () => {
  await ax.locator(`[data-hm-pe-gapline][data-show="sel"]`).first().click(); // the pill after the selected section
  await ax.getByRole("dialog", { name: "Add a section" }).waitFor({ timeout: 3000 });
  try { return await axeOn(ax, "layout picker"); } finally { await ax.keyboard.press("Escape"); await settle(ax, 200); }
});
await check("axe: link panel open on the canvas", async () => {
  const t = (await nodes(ax)).find((n) => n.t === "text");
  let editing = false;
  for (let i = 0; i < 5 && !editing; i++) {
    const pt = await canvasPoint(ax, textSel(t.id));
    if (pt) await ax.mouse.dblclick(pt.x, pt.y);
    editing = await (await frame(ax)).waitForFunction(() => document.querySelector("[contenteditable]") && getSelection().toString().length > 0, null, { timeout: 1500 }).then(() => true, () => false);
  }
  need(editing, "the double-click never selected a word in a canvas text edit");
  await ax.keyboard.press("Control+k");
  await ax.locator("#hm-fmt-href").waitFor({ state: "visible", timeout: 3000 });
  await settle(ax, 400);
  const tb = await ax.locator("[data-hm-pe-toolbar]").boundingBox();
  const lk = await ax.locator("#hm-fmt-link").boundingBox();
  need(!tb || !(tb.x < lk.x + lk.width && tb.x + tb.width > lk.x && tb.y < lk.y + lk.height && tb.y + tb.height > lk.y), `the editing hint covers the link row: ${JSON.stringify(tb)} over ${JSON.stringify(lk)}`);
  try { return await axeOn(ax, "link panel"); } finally { await ax.keyboard.press("Escape"); await ax.keyboard.press("Escape"); await settle(ax, 300); }
});
await check("axe: command palette open", async () => {
  await ax.keyboard.press("Control+k");
  await ax.locator("#hm-bs-cmdk").waitFor({ state: "visible", timeout: 3000 });
  try { return await axeOn(ax, "command palette"); } finally { await ax.keyboard.press("Escape"); await settle(ax, 200); }
});
if (isOwner) {
  await check("axe: Site styles drawer open", async () => {
    await ax.getByRole("button", { name: "More actions" }).click();
    await ax.getByRole("menuitem", { name: "Site styles" }).click();
    await ax.locator("dialog[open].hm-dialog").waitFor({ timeout: 5000 });
    try { return await axeOn(ax, "Site styles drawer"); } finally { await ax.keyboard.press("Escape"); await settle(ax, 200); }
  });
}
await check("axe: phone dock with the Inspector sheet", async () => {
  await ax.setViewportSize({ width: 390, height: 844 });
  await settle(ax, 400);
  try {
    await ax.locator('[data-hm-bs-dock="nav"]').click();
    await (await rowOfKind(ax, "heading")).click();
    await settle(ax, 500);
    return await axeOn(ax, "phone dock");
  } finally { await ax.setViewportSize({ width: 1440, height: 900 }); }
});
await check("axe: element restore dialog", async () => {
  const id = await titledVersions(ax);
  try {
    await showNav(ax);
    await (await rowOfKind(ax, "heading")).click();
    await ax.getByRole("button", { name: "Element options" }).click();
    await ax.getByRole("menuitem", { name: /Restore from an earlier version/ }).click();
    await ax.locator("dialog.hm-dialog[open] label.hm-radiocard").first().waitFor({ timeout: 5000 });
    try { return await axeOn(ax, "restore dialog"); } finally { await ax.keyboard.press("Escape"); await settle(ax, 200); }
  } finally { await drop(ax, id); await openBuilder(ax, axId); }
});
await check("shell-dark: dark theme reads (axe colour contrast on the shell, the chip and the palette)", async () => {
  await drop(ax, axId);
  await ax.context().close();
  const d = await newSession(browser, { theme: "dark", width: 1440, bypassCSP: true });
  watch(d);
  const id = await seeded(d, ["heading", "text"]);
  need((await d.evaluate(() => document.documentElement.dataset.adminTheme)) === "dark", "not dark");
  await showNav(d);
  await (await rowOfKind(d, "heading")).click();
  await device(d, "md").click();
  // the Style tab in dark: State switch (pressed option), source chips, Show on switches, box-model link toggles
  await d.getByRole("tab", { name: "Style" }).click();
  await d.locator("details.hm-bs-more-opts > summary").click();
  await settle(d, 300);
  const rs = await axeOn(d, "dark mode style tab");
  await d.keyboard.press("Control+k");
  await d.locator("#hm-bs-cmdk").waitFor({ state: "visible", timeout: 3000 });
  const r = rs + "; " + (await axeOn(d, "dark mode"));
  await d.keyboard.press("Escape");
  await drop(d, id);
  await d.context().close();
  return r;
});

// ---- clean up and report ----
for (const id of [...made]) await drop(main, id).catch(() => {});
let commit = "";
try { commit = execSync("git rev-parse --short HEAD", { cwd: new URL(".", import.meta.url).pathname }).toString().trim(); } catch {}
const summary = Object.fromEntries(Object.entries(results).map(([k, r]) => [k, r.skipped ? { skipped: r.skipped } : { pass: r.pass, ...Object.fromEntries(["mouse", "keyboard", "firstUse", "plus", "phone"].filter((m) => r[m]).map((m) => [m, `${r[m].clicks}c/${r[m].keys}k/${r[m].sec}s${r[m].pass ? "" : " !"}`])) }]));
const file = writeResult("builder-ux-tasks.json", { when: new Date().toISOString(), commit, role: isOwner ? "owner" : "editor", runs: RUNS, results, summary, checks, axe: axeRes, errors });
await browser.close();
console.log(JSON.stringify(summary, null, 1));
console.log(`wrote ${file}`);
console.log(bad ? `${bad} problem(s)` : "builder-ux-tasks: all tasks and checks passed");
process.exit(bad ? 1 : 0);
