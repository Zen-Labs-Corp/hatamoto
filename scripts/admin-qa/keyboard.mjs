// Keyboard-only checks with real key presses (ported from the mockup spike).
//  1. Tab through every screen: each stop is visible, has a focus indicator, is
//     not inside a hidden panel and is not covered by sticky chrome; the skip
//     link comes first.
//  2. Widget scripts on the real admin: skip link, theme toggle, row menu
//     popover, confirm dialog, tabs, and on the editors: slash menu, bubble
//     toolbar, Post | SEO tabs, section menu, add-section dialog; on the builder
//     (QA_PAGE_NAME = a page with a page_doc): navigator tree, palette, inspector tabs,
//     shortcut sheet.
// node scripts/admin-qa/keyboard.mjs [chromium|firefox|webkit] [screen ...]
// Exit 1 on any problem.
import { launch, newSession, visit, resolvePath, writeResult, base, role } from "./lib.mjs";
import { inventory } from "./urls.mjs";

const engine = process.argv[2] && ["chromium", "firefox", "webkit"].includes(process.argv[2]) ? process.argv[2] : "chromium";
const only = process.argv.slice(2).filter((a) => !["chromium", "firefox", "webkit"].includes(a));
const b = await launch(engine);
const res = {};
let bad = 0;

async function tabWalk(p, max = 400) {
  const stops = [];
  for (let i = 0; i < max; i++) {
    await p.keyboard.press("Tab");
    const s = await p.evaluate(async () => {
      await new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r)));
      const el = document.activeElement;
      if (!el || el === document.body) return null;
      const r = el.getBoundingClientRect();
      const cs = getComputedStyle(el);
      const ring = cs.outlineStyle !== "none" && parseFloat(cs.outlineWidth) >= 2;
      const shadow = cs.boxShadow !== "none";
      const wrap = el.closest(".hm-field, .hm-switch, .hm-check, .hm-radio, .hm-tile, .hm-pe__canvas, label");
      const wrapRing = wrap && (getComputedStyle(wrap).outlineStyle !== "none" || getComputedStyle(wrap).boxShadow !== "none");
      // not covered by sticky chrome (WCAG 2.4.11)
      const cx = r.left + Math.min(r.width / 2, 8), cy = r.top + r.height / 2;
      const top = cy >= 0 && cy <= innerHeight ? document.elementFromPoint(cx, cy) : el;
      const obscured = !!top && !el.contains(top) && !top.contains(el) && !!top.closest(".hm-savebar, .hm-sidebar, .hm-toasts, [data-sticky]");
      // A focused rich-text writing area (contenteditable) shows a caret, which is its focus indicator:
      // a ring around the page-like writing view is deliberately absent.
      const caret = el.isContentEditable && !!el.closest(".ProseMirror");
      const label = (el.getAttribute("aria-label") || el.textContent || el.id || el.tagName).trim().replace(/\s+/g, " ").slice(0, 40);
      return { tag: el.tagName, label, obscured, visible: r.width > 0 && r.height > 0, indicator: caret || ring || shadow || !!wrapRing || el.classList.contains("hm-skip"), hiddenPanel: !!el.closest("[hidden]"), key: el.tagName + "|" + label + "|" + Math.round(r.x) + "," + Math.round(r.y) };
    });
    if (!s) break;
    if (stops.some((x) => x.key === s.key)) break; // wrapped around
    stops.push(s);
  }
  return stops;
}

for (const [width, theme] of [[1440, "light"], [1440, "dark"], [390, "light"], [390, "dark"]]) {
  const p = await newSession(b, { theme, width });
  for (const s of inventory({ only })) {
    const v = await visit(p, s);
    const id = `${s.name}/${width}/${theme}`;
    if (v.skipped) { res[id] = { skipped: true }; continue; }
    if (!v.ok) { res[id] = { problems: [`status ${v.status}`] }; bad++; continue; }
    await p.waitForTimeout(100);
    const stops = await tabWalk(p);
    const problems = stops.filter((x) => !x.visible || !x.indicator || x.hiddenPanel || x.obscured);
    const skipFirst = stops[0]?.label === "Skip to content";
    res[id] = { stops: stops.length, first: stops[0]?.label, problems: problems.map((x) => `${x.tag} "${x.label}" visible=${x.visible} indicator=${x.indicator} hiddenPanel=${x.hiddenPanel} obscured=${x.obscured}`) };
    if (!skipFirst) res[id].problems.push(`first stop is "${stops[0]?.label}", not the skip link`);
    bad += res[id].problems.length;
    console.log(`${id.padEnd(48)} ${stops.length} stops, ${res[id].problems.length} problems`);
  }
  await p.context().close();
}

// ---- widget scripts ----
const checks = {};
async function check(name, fn) {
  if (process.env.QA_ONLY && !new RegExp(process.env.QA_ONLY, "i").test(name)) return;
  try { checks[name] = { ok: true, detail: await fn() }; }
  catch (e) { checks[name] = { ok: false, detail: String(e.message || e).split("\n")[0] }; bad++; }
  console.log(`${checks[name].ok ? "ok  " : "FAIL"} ${name}: ${checks[name].detail}`);
}
const p = await newSession(b, { theme: "light", width: 1440 });
const active = () => p.evaluate(() => (document.activeElement.getAttribute("aria-label") || document.activeElement.textContent).trim().replace(/\s+/g, " ").slice(0, 60));
const entities = `${base}/admin/entities`;

await check("skip link: Enter moves focus to main, next Tab lands inside it", async () => {
  await p.goto(entities);
  await p.keyboard.press("Tab"); await p.keyboard.press("Enter"); await p.keyboard.press("Tab");
  if (!(await p.evaluate(() => !!document.activeElement.closest("main")))) throw new Error("next Tab not in main");
  return "ok";
});
await check("Theme sub-navigation: a section link takes focus, Enter opens it and it becomes current", async () => {
  await p.goto(`${base}/admin/theme/menus`);
  const link = p.locator('.hm-subnav a[href="/admin/theme/templates"]');
  if (!(await link.count())) throw new Error("no Templates link in the Theme sidebar");
  await link.focus();
  await Promise.all([p.waitForURL("**/admin/theme/templates"), p.keyboard.press("Enter")]);
  const cur = await p.evaluate(() => [location.pathname, document.querySelector('.hm-subnav [aria-current="page"]')?.textContent.trim()]);
  if (cur[0] !== "/admin/theme/templates" || cur[1] !== "Templates") throw new Error(JSON.stringify(cur));
  return "Menus -> Templates by keyboard, current marked";
});
await check("theme toggle: Enter flips light/dark and the choice persists", async () => {
  await p.goto(entities);
  await p.focus("[data-hm-theme-toggle]");
  await Promise.all([p.waitForLoadState("load"), p.keyboard.press("Enter")]);
  await p.waitForFunction(() => document.documentElement.dataset.adminTheme === "dark");
  await p.reload();
  const t = await p.evaluate(() => document.documentElement.dataset.adminTheme);
  await p.focus("[data-hm-theme-toggle]");
  await Promise.all([p.waitForLoadState("load"), p.keyboard.press("Enter")]); // back to light
  await p.waitForFunction(() => document.documentElement.dataset.adminTheme === "light");
  if (t !== "dark") throw new Error("theme after reload: " + t);
  return "light -> dark, persisted across reload";
});
await check("row menu: Enter opens, focus enters the menu, Esc closes back to the button", async () => {
  await p.goto(entities);
  const btn = p.locator('[popovertarget^="menu-"]').first();
  if (!(await btn.count())) throw new Error("no row menu button on the entities list");
  await btn.focus(); await p.keyboard.press("Enter");
  await p.waitForFunction(() => document.activeElement.closest("[popover]"), null, { timeout: 2000 });
  const first = await active();
  await p.keyboard.press("ArrowDown"); const second = await active();
  await p.keyboard.press("Escape");
  const back = await p.evaluate(() => document.activeElement.getAttribute("popovertarget"));
  if (!back?.startsWith("menu-")) throw new Error(JSON.stringify({ first, second, back }));
  return `${first} -> ${second}; Esc back to the opener`;
});
await check("confirm dialog: opens from a menu Delete, Tab trapped, Esc returns focus", async () => {
  await p.goto(entities);
  const btn = p.locator('[popovertarget^="menu-"]').first();
  await btn.focus(); await p.keyboard.press("Enter");
  await p.waitForFunction(() => document.activeElement.closest("[popover]"), null, { timeout: 2000 });
  await p.keyboard.press("End"); await p.keyboard.press("Enter");
  await p.waitForSelector("dialog[open]", { timeout: 2000 });
  const inside = [];
  for (let i = 0; i < 5; i++) { await p.keyboard.press("Tab"); inside.push(await p.evaluate(() => document.activeElement === document.body || !!document.activeElement.closest("dialog"))); }
  if (inside.includes(false)) throw new Error("focus escaped the dialog");
  await p.keyboard.press("Escape");
  await p.waitForSelector("dialog[open]", { state: "detached", timeout: 2000 }).catch(() => {});
  const closed = await p.evaluate(() => !document.querySelector("dialog[open]"));
  if (!closed) throw new Error("Esc did not close the dialog");
  return "trapped, closed with Esc";
});
// The analytics views are link rows (no role=tab since v1-24); the blog editor has the real tablist.
await check("tabs: arrow keys move between tabs (first role=tablist on the blog editor)", async () => {
  const path = await resolvePath(p, "/admin/entities/{post}/edit");
  if (!path) throw new Error("no post seeded");
  await p.goto(base + path, { waitUntil: "load" });
  const tabs = p.locator('[role="tab"]');
  if ((await tabs.count()) < 2) {
    if (process.env.QA_STRICT === "1") throw new Error("no tablist found");
    return "n/a: no role=tab on this screen (QA_STRICT=1 makes this a failure)";
  }
  await tabs.first().focus();
  const a = await p.evaluate(() => document.activeElement.id || document.activeElement.textContent.trim());
  await p.keyboard.press("ArrowRight");
  const c = await p.evaluate(() => document.activeElement.id || document.activeElement.textContent.trim());
  if (a === c) throw new Error("ArrowRight did not move focus");
  return `${a} -> ${c}`;
});

// ---- editor widgets (v1-23f) ----
const openEditor = async (token) => {
  const path = await resolvePath(p, `/admin/entities/{${token}}/edit`);
  if (!path) throw new Error(`no ${token} seeded`);
  await p.goto(base + path, { waitUntil: "load" });
};
const pmText = () => p.evaluate(() => document.querySelector(".ProseMirror").textContent);
const gone = (sel) => p.waitForSelector(sel, { state: "hidden", timeout: 2000 }).catch(() => {});
const optionId = () => p.evaluate(() => document.querySelector('.hm-slash [role="option"][aria-selected="true"]')?.textContent.trim().replace(/\s+/g, " ").slice(0, 30) || null);

await check("slash menu: / opens, arrows move, Enter inserts, Esc closes", async () => {
  await openEditor("post");
  await p.click(".ProseMirror");
  await p.keyboard.press("Control+End");
  await p.keyboard.press("Enter");
  await p.keyboard.type("/");
  await p.waitForSelector(".hm-slash", { state: "visible", timeout: 2000 });
  const a = await optionId();
  await p.keyboard.press("ArrowDown");
  const c = await optionId();
  if (!a || a === c) throw new Error(`arrow did not move the active option: ${a} -> ${c}`);
  const before = await p.evaluate(() => document.querySelector(".ProseMirror").innerHTML.length);
  await p.keyboard.press("Enter");
  await gone(".hm-slash");
  const after = await p.evaluate(() => document.querySelector(".ProseMirror").innerHTML.length);
  if (await p.locator(".hm-slash:visible").count()) throw new Error("menu stayed open after Enter");
  if (after === before) throw new Error("Enter inserted nothing");
  await p.keyboard.press("Enter");
  await p.keyboard.type("/");
  await p.waitForSelector(".hm-slash", { state: "visible", timeout: 2000 });
  await p.keyboard.press("Escape");
  await gone(".hm-slash");
  if (await p.locator(".hm-slash:visible").count()) throw new Error("Esc did not close the menu");
  if (!(await p.evaluate(() => !!document.activeElement.closest(".ProseMirror")))) throw new Error("focus left the editor on Esc");
  return `${a} -> ${c}; Enter inserted; Esc closed, focus stayed in the editor`;
});
await check("bubble toolbar: selecting text shows it, Tab reaches buttons, arrows move, Esc returns", async () => {
  await openEditor("post");
  await p.click(".ProseMirror p");
  await p.keyboard.press("Control+a");
  await p.waitForSelector(".hm-bubble", { state: "visible", timeout: 2000 });
  await p.keyboard.press("Tab");
  const a = await active();
  await p.keyboard.press("ArrowRight");
  const c = await active();
  if (a !== "Bold" || c !== "Italic") throw new Error(`Tab then ArrowRight gave ${a} / ${c}`);
  await p.keyboard.press("End");
  const d = await active();
  if (d !== "Link") throw new Error(`End gave ${d}`);
  await p.keyboard.press("Escape");
  await p.waitForFunction(() => !!document.activeElement.closest(".ProseMirror"), null, { timeout: 2000 }).catch(() => { throw new Error("Esc did not return to the editor"); });
  return `Tab -> ${a}, ArrowRight -> ${c}, End -> ${d}; Esc back in the text`;
});
await check("blog tabs: arrows move Post <-> SEO and AEO, the panel follows", async () => {
  await openEditor("post");
  await p.focus("#be-tab-post");
  await p.keyboard.press("ArrowRight");
  const sel = await p.evaluate(() => ({ id: document.activeElement.id, on: document.activeElement.getAttribute("aria-selected"), seoHidden: document.getElementById("be-tabpanel-seo").hidden, postHidden: document.getElementById("be-tabpanel-post").hidden }));
  if (sel.id !== "be-tab-seo" || sel.on !== "true" || sel.seoHidden || !sel.postHidden) throw new Error(JSON.stringify(sel));
  await p.keyboard.press("ArrowLeft");
  const back = await p.evaluate(() => document.activeElement.id);
  if (back !== "be-tab-post") throw new Error("ArrowLeft gave " + back);
  return "Post -> SEO and AEO -> Post, panels swap";
});
await check("blog editor ⋯ menu: arrows reach every visible item, none is a hidden one", async () => {
  await openEditor("post");
  await p.setViewportSize({ width: 1440, height: 900 });
  await p.focus('[popovertarget="be-more"]');
  await p.keyboard.press("Enter");
  await p.waitForFunction(() => document.activeElement.closest("#be-more"), null, { timeout: 2000 });
  const seen = [];
  for (let i = 0; i < 12; i++) {
    const it = await p.evaluate(() => {
      const el = document.activeElement;
      return { label: el.textContent.trim(), shown: el.getClientRects().length > 0 };
    });
    if (!it.shown) throw new Error(`focus sits on a hidden item: ${it.label}`);
    if (seen.includes(it.label)) break;
    seen.push(it.label);
    await p.keyboard.press("ArrowDown");
  }
  await p.keyboard.press("Escape");
  if (!seen.includes("Open preview in a new tab") || !seen.includes("Delete")) throw new Error("ArrowDown skipped items: " + seen.join(" | "));
  return seen.join(" > ");
});
// The classic page editor checks were removed with the classic editor (B10): pages
// always open in the builder, whose keyboard scripts follow.

// ---- builder widgets (B3) ----
// Every script that edits undoes its edit, and a "leave page" prompt from a still-dirty
// builder is accepted so the next script can navigate.
p.on("dialog", (d) => d.accept().catch(() => {}));
const TREE = '[data-hm-bs-tree] [role="treeitem"]';
const openBuilder = async () => {
  await openEditor("page");
  await p.waitForSelector(TREE, { timeout: 10000 });
  await p.waitForTimeout(500);
};
const rows = () => p.evaluate((sel) => [...document.querySelectorAll(sel)].map((r) => ({ id: r.dataset.id, sel: r.getAttribute("aria-selected") === "true", level: +r.getAttribute("aria-level"), exp: r.getAttribute("aria-expanded"), pos: +r.getAttribute("aria-posinset"), tab: r.tabIndex })), TREE);
const sig = (rs) => rs.map((r) => `${r.id}:${r.level}:${r.pos}`).join(",");
const selectedId = async () => (await rows()).find((r) => r.sel)?.id ?? null;
const focusedRow = () => p.evaluate(() => document.activeElement?.closest?.('[role="treeitem"]')?.dataset.id ?? null);
const settle = (ms = 150) => p.waitForTimeout(ms);
const press = async (key, n = 1) => { for (let i = 0; i < n; i++) { await p.keyboard.press(key); await settle(60); } };
const pick = async (id) => { await p.locator(`${TREE}[data-id="${id}"]`).click(); await settle(); };

// The seeded page is one theme section, a single Navigator row without blocks: add n heading widgets from the palette (each lands selected, after the last) and undo them with undoWidgets.
const addHeadings = async (n) => {
  const start = (await rows()).length;
  await pick((await rows())[0].id);
  for (let i = 0; i < n; i++) { await press("/"); await p.keyboard.type("heading"); await settle(); await press("ArrowDown"); await press("Enter"); await settle(300); }
  return start;
};
const undoWidgets = async (start) => {
  await p.evaluate(() => document.activeElement?.blur?.());
  await p.locator(TREE).first().focus();
  for (let i = 0; i < 12 && (await rows()).length > start; i++) await press("Control+z");
};
// A parent row with children: indent a new heading into the seeded section (Alt+ArrowRight), which then holds two children and shows as a section row with block rows below it. Undo with undoWidgets.
const nestHeading = async () => {
  const start = await addHeadings(1);
  await press("Alt+ArrowRight");
  return start;
};

await check("builder tree: roving tab stop, arrows move the selection, Left/Right fold, Home/End", async () => {
  await openBuilder();
  const start0 = await nestHeading();
  try {
    const rs = await rows();
    if (!rs.length) throw new Error("no treeitems");
    if (rs.filter((r) => r.tab === 0).length !== 1) throw new Error("not exactly one tab stop: " + rs.filter((r) => r.tab === 0).length);
    const role = await p.evaluate(() => document.querySelector('[data-hm-bs-tree] [role="tree"]')?.getAttribute("aria-label"));
    if (!role) throw new Error("no role=tree with a label");
    await pick(rs[0].id); // the new heading is selected: walk down from the first row
    await p.locator(TREE).first().focus();
    await press("ArrowDown");
    const a = await selectedId();
    await press("ArrowDown");
    const c = await selectedId();
    if (!a || a === c || (await focusedRow()) !== c) throw new Error(`ArrowDown: ${a} -> ${c}, focus on ${await focusedRow()}`);
    const parent = (await rows()).find((r) => r.exp === "true");
    await pick(parent.id);
    await press("ArrowLeft");
    if ((await rows()).find((r) => r.id === parent.id).exp !== "false") throw new Error("ArrowLeft did not collapse " + parent.id);
    await press("ArrowRight");
    if ((await rows()).find((r) => r.id === parent.id).exp !== "true") throw new Error("ArrowRight did not expand");
    await press("ArrowRight");
    const kid = await selectedId();
    if (kid === parent.id) throw new Error("ArrowRight on an open node did not go to its first child");
    await press("ArrowLeft");
    if ((await selectedId()) !== parent.id) throw new Error("ArrowLeft on a child did not return to its parent");
    await press("End"); const last = await selectedId();
    await press("Home"); const first = await selectedId();
    const all = await rows();
    if (last !== all[all.length - 1].id || first !== all[0].id) throw new Error(`End/Home gave ${last} / ${first}`);
    return `${all.length} rows; Down, Left/Right, Home/End ok`;
  } finally { await undoWidgets(start0); }
});
await check("builder tree: Alt+arrows move, indent and outdent, Delete removes, Ctrl+Z restores", async () => {
  await openBuilder();
  const start = sig(await rows());
  const second = async () => (await rows()).find((r) => r.level >= 2 && r.pos === 2);
  let target = await second();
  // The seed has no section with two children: nest a heading into the seeded section (undone with the rest below).
  if (!target) {
    await addHeadings(1);
    await press("Alt+ArrowRight");
    target = await second();
  }
  if (!target) throw new Error("no second child at level >= 2 to move, even after adding widgets: " + sig(await rows()));
  const { id, level } = target;
  await pick(id);
  await press("Alt+ArrowUp");
  let r = (await rows()).find((x) => x.id === id);
  if (r.pos !== 1) throw new Error("Alt+ArrowUp left position " + r.pos);
  await press("Alt+ArrowDown");
  r = (await rows()).find((x) => x.id === id);
  if (r.pos !== 2) throw new Error("Alt+ArrowDown left position " + r.pos);
  await press("Alt+ArrowLeft");
  r = (await rows()).find((x) => x.id === id);
  if (r.level !== level - 1) throw new Error("Alt+ArrowLeft left level " + r.level);
  if ((await focusedRow()) !== id) throw new Error("focus left the moved row");
  await press("Alt+ArrowRight");
  r = (await rows()).find((x) => x.id === id);
  if (r.level !== level) throw new Error("Alt+ArrowRight left level " + r.level);
  const n = (await rows()).length;
  await press("Delete");
  if ((await rows()).length >= n) throw new Error("Delete removed nothing");
  for (let i = 0; i < 12 && sig(await rows()) !== start; i++) await press("Control+z");
  const end = sig(await rows());
  if (end !== start) throw new Error("undo did not restore the structure");
  return "moves keep focus on the row; undo restores the tree";
});
await check("builder palette: / opens search, filter narrows, ArrowDown enters the list, Enter inserts and focus returns to the tree", async () => {
  await openBuilder();
  const start = (await rows()).length;
  await pick((await rows())[0].id);
  await press("/");
  if (!(await p.evaluate(() => document.activeElement?.id === "hm-bs-pal-q"))) throw new Error("/ did not focus the palette search");
  await p.keyboard.type("button"); await settle();
  const shown = await p.locator("[data-pal]").count();
  if (shown < 1 || shown > 3) throw new Error(`filter "button" shows ${shown} items`);
  await press("ArrowDown");
  if (!(await p.evaluate(() => document.activeElement?.hasAttribute("data-pal")))) throw new Error("ArrowDown did not reach the list");
  await press("ArrowUp");
  if (!(await p.evaluate(() => document.activeElement?.id === "hm-bs-pal-q"))) throw new Error("ArrowUp did not return to the search");
  await press("Enter");
  if ((await rows()).length !== start + 1) throw new Error("Enter inserted nothing");
  if (!(await focusedRow())) throw new Error("focus did not return to the tree after the insert");
  await press("a");
  await p.keyboard.type("heading"); await settle();
  await press("ArrowDown"); await press("Enter");
  if ((await rows()).length !== start + 2) throw new Error("Enter on a list item inserted nothing");
  await press("Control+z", 2);
  if ((await rows()).length !== start) throw new Error("undo did not remove the inserts");
  return "search -> list -> Enter inserts twice, both undone";
});
await check("builder inspector tabs: Enter reaches the inspector, arrows/Home/End move between Content, Style, Advanced", async () => {
  await openBuilder();
  await pick((await rows())[0].id);
  await press("Enter");
  if (!(await p.evaluate(() => !!document.activeElement.closest("[data-hm-bs-panel]")))) throw new Error("Enter did not move focus into the inspector");
  const tab = () => p.evaluate(() => { const t = document.activeElement; return { name: t.textContent, sel: t.getAttribute("aria-selected"), panel: document.getElementById(t.getAttribute("aria-controls"))?.getAttribute("aria-labelledby") === t.id, stops: [...document.querySelectorAll('[data-hm-bs-panel] [role="tab"]')].filter((x) => x.tabIndex === 0).length }; });
  await p.locator('[data-hm-bs-panel] [role="tab"][aria-selected="true"]').focus();
  const seen = [];
  for (const key of ["ArrowRight", "ArrowRight", "ArrowRight", "ArrowLeft", "End", "Home"]) {
    await press(key);
    const t = await tab();
    if (t.sel !== "true" || !t.panel || t.stops !== 1) throw new Error(`${key}: ${JSON.stringify(t)}`);
    seen.push(t.name);
  }
  if (seen.join(">") !== "Style>Advanced>Content>Advanced>Advanced>Content") throw new Error("order " + seen.join(">"));
  return seen.join(" > ");
});
await check("builder shortcut sheet: ? opens the dialog, Tab trapped, Esc closes and returns focus", async () => {
  await openBuilder();
  await p.locator(TREE).first().focus();
  await press("?");
  await p.waitForSelector("dialog#hm-bs-sheet[open]", { timeout: 2000 });
  const label = await p.evaluate(() => document.querySelector("#hm-bs-sheet").getAttribute("aria-labelledby"));
  if (!label) throw new Error("dialog has no accessible name");
  const inside = [];
  for (let i = 0; i < 6; i++) { await p.keyboard.press("Tab"); inside.push(await p.evaluate(() => document.activeElement === document.body || !!document.activeElement.closest("dialog#hm-bs-sheet"))); }
  if (inside.includes(false)) throw new Error("focus escaped the dialog");
  await p.keyboard.press("Escape");
  await p.waitForSelector("dialog#hm-bs-sheet[open]", { state: "detached", timeout: 2000 }).catch(() => {});
  if (await p.evaluate(() => !!document.querySelector("dialog#hm-bs-sheet[open]"))) throw new Error("Esc did not close the sheet");
  const back = await p.evaluate(() => document.activeElement?.closest?.('[data-hm-bs-tree]') ? "tree" : document.activeElement?.tagName);
  if (back !== "tree") throw new Error("focus after Esc: " + back);
  await p.locator("[data-hm-bs-keys]").focus(); await p.keyboard.press("Enter");
  await p.waitForSelector("dialog#hm-bs-sheet[open]", { timeout: 2000 });
  await p.keyboard.press("Escape");
  if (!(await p.evaluate(() => document.activeElement?.hasAttribute("data-hm-bs-keys")))) throw new Error("focus not back on the Shortcuts button");
  return "trapped over 6 Tabs; Esc returns focus to the tree row or the button that opened it";
});

// ---- builder direct manipulation (E4): menu, rename, text-control isolation, multi-select, history ----
const MENU = '.hm-menu[aria-label="Element actions"]';
const inMenu = () => p.waitForFunction(() => document.activeElement?.closest(".hm-menu"), null, { timeout: 2000 });
await check("builder element menu: Shift+F10 opens it with focus inside, arrows move, Esc closes it and focus returns to the row", async () => {
  await openBuilder();
  const id = (await rows())[0].id;
  await pick(id);
  await p.locator(`${TREE}[data-id="${id}"]`).focus();
  await press("Shift+F10");
  await p.waitForSelector(MENU, { timeout: 3000 });
  await inMenu();
  if ((await p.locator(MENU).getAttribute("role")) !== "menu") throw new Error("the popover is not role=menu");
  const items = await p.locator(`${MENU} [role="menuitem"]`).count();
  if (items < 8) throw new Error(`${items} menu items`);
  const first = await active();
  await press("ArrowDown");
  const second = await active();
  if (first === second) throw new Error("ArrowDown did not move");
  await press("End");
  const last = await active();
  await press("Home");
  if ((await active()) !== first || last === first) throw new Error("Home/End did not wrap the ends");
  await press("Escape");
  await p.waitForSelector(MENU, { state: "detached", timeout: 2000 });
  if ((await focusedRow()) !== id) throw new Error("focus after Esc is on " + (await focusedRow()));
  await press("ContextMenu");
  await p.waitForSelector(MENU, { timeout: 3000 });
  await press("Escape");
  return `${items} items; first ${first.slice(0, 10)}; Esc and the menu key both return to the row`;
});
await check("builder rename: F2 opens an input on the row, Esc cancels and returns focus, Enter commits and undo reverts it", async () => {
  await openBuilder();
  const start = sig(await rows());
  const id = (await rows())[0].id;
  await pick(id);
  await p.locator(`${TREE}[data-id="${id}"]`).focus();
  await press("F2");
  const input = p.locator(`${TREE} input[aria-label="Name"]`);
  if ((await input.count()) !== 1) throw new Error("F2 opened no rename input");
  if (!(await p.evaluate(() => document.activeElement?.matches('input[aria-label="Name"]')))) throw new Error("the input is not focused");
  await p.keyboard.type("Zed"); await press("Escape");
  if ((await p.locator(`${TREE}[data-id="${id}"]`).textContent()).includes("Zed")) throw new Error("Esc kept the name");
  if ((await focusedRow()) !== id) throw new Error("focus after Esc is on " + (await focusedRow()));
  await press("F2"); await p.keyboard.type("Zed"); await press("Enter");
  if (!(await p.locator(`${TREE}[data-id="${id}"]`).textContent()).includes("Zed")) throw new Error("Enter did not name the row");
  if ((await focusedRow()) !== id) throw new Error("focus after Enter is on " + (await focusedRow()));
  await press("Control+z");
  if ((await p.locator(`${TREE}[data-id="${id}"]`).textContent()).includes("Zed")) throw new Error("undo kept the name");
  if (sig(await rows()) !== start) throw new Error("the structure changed");
  return "F2 -> input; Esc cancels; Enter names; one undo reverts";
});
await check("builder text controls keep their keys: Ctrl+C/X/V/G/Z/D/A in the inspector field and the rename input leave the page untouched", async () => {
  await openBuilder();
  const start0 = await addHeadings(1);
  try {
    const start = sig(await rows());
    const id = await selectedId();
    await p.locator(`${TREE}[data-id="${id}"]`).focus();
    await press("Enter");
    const field = p.locator('[data-hm-bs-panel] input[type="text"]:not([readonly]), [data-hm-bs-panel] textarea').first();
    if (!(await field.count())) throw new Error("the inspector has no text field for the selected row");
    await field.focus();
    await p.keyboard.type("abc");
    for (const k of ["Control+c", "Control+x", "Control+v", "Control+g", "Control+Shift+g", "Control+d", "Control+a"]) await p.keyboard.press(k);
    await settle(300);
    if (sig(await rows()) !== start) throw new Error("a shortcut typed in the inspector field changed the tree");
    await p.evaluate(() => document.activeElement?.blur?.());
    await pick(id);
    await p.locator(`${TREE}[data-id="${id}"]`).focus();
    await press("F2");
    await p.keyboard.type("q");
    for (const k of ["Control+c", "Control+x", "Control+v", "Control+g", "Control+Shift+g", "Control+d", "Control+a"]) await p.keyboard.press(k);
    await settle(300);
    if (sig(await rows()) !== start) throw new Error("a shortcut typed in the rename input changed the tree");
    if ((await p.locator(`${TREE} input[aria-label="Name"]`).count()) !== 1) throw new Error("the rename input closed");
    await press("Escape");
    // The same key on the row does act, so the check above can fail: Ctrl+G wraps.
    await p.locator(`${TREE}[data-id="${id}"]`).focus();
    await p.keyboard.press("Control+g"); await settle(300);
    const acted = sig(await rows()) !== start;
    if (!acted) throw new Error("Ctrl+G on the row did nothing: the isolation check proves nothing");
    return "keys inert in both inputs; the same Ctrl+G wraps from the row";
  } finally { await undoWidgets(start0); }
});
await check("builder tree is aria-multiselectable and Ctrl+click adds a row to the selection", async () => {
  await openBuilder();
  const start0 = await addHeadings(2);
  try {
    const tree = p.locator('[data-hm-bs-tree] [role="tree"]');
    if ((await tree.getAttribute("aria-multiselectable")) !== "true") throw new Error("aria-multiselectable is " + (await tree.getAttribute("aria-multiselectable")));
    const rs = await rows();
    const a = rs[rs.length - 1].id, c = rs[rs.length - 2].id;
    await pick(a);
    await p.locator(`${TREE}[data-id="${c}"]`).click({ modifiers: ["Control"] }); await settle();
    const sel = await p.locator(`${TREE}[aria-selected="true"]`).evaluateAll((x) => x.map((r) => r.dataset.id));
    if (sel.length !== 2 || !sel.includes(a) || !sel.includes(c)) throw new Error("selected rows: " + sel.join());
    return "2 rows aria-selected after Ctrl+click";
  } finally { await undoWidgets(start0); }
});
await check("builder history: Ctrl+H opens the panel with the current step focused, arrows move, Esc returns focus", async () => {
  await openBuilder();
  const id = (await rows())[0].id;
  await pick(id);
  await p.locator(`${TREE}[data-id="${id}"]`).focus();
  await press("Control+h");
  await p.waitForSelector('[role="listbox"][aria-label^="Changes"]', { timeout: 3000 });
  const cur = await p.evaluate(() => document.activeElement?.getAttribute("aria-selected") + ":" + document.activeElement?.getAttribute("role"));
  if (cur !== "true:option") throw new Error("focus is on " + cur);
  const n = await p.locator('[role="listbox"][aria-label^="Changes"] [role="option"]').count();
  await press("Home");
  await press("Escape");
  await p.waitForSelector('[role="listbox"][aria-label^="Changes"]', { state: "detached", timeout: 2000 });
  if (!(await p.evaluate(() => document.activeElement?.hasAttribute("data-hm-bs-history")))) throw new Error("focus is not on the History button");
  return `${n} row(s); current row focused; Esc -> History button`;
});

// ---- builder Style tab controls (E3): colour popover, number + unit + slider, box model ----
// Each check adds a heading widget, edits it and undoes everything, so the page is left as it was.
const styleOn = async () => {
  await openBuilder();
  const start = (await rows()).length;
  await pick((await rows())[0].id);
  await press("/");
  await p.keyboard.type("heading"); await settle();
  await press("ArrowDown"); await press("Enter"); await settle(500);
  await p.locator('[data-hm-bs-panel] [role="tab"]', { hasText: "Style" }).click(); await settle();
  return start;
};
const styleOff = async (start) => {
  await p.evaluate(() => document.activeElement?.blur?.());
  for (let i = 0; i < 40 && (await rows()).length > start; i++) await press("Control+z");
};
const openStyleGroup = async (name) => {
  const head = p.locator("button.hm-bs-grp__t", { hasText: new RegExp(`^${name}`) }).first();
  if ((await head.getAttribute("aria-expanded")) !== "true") { await head.click(); await settle(100); }
};
const inPanel = () => p.evaluate(() => !!document.activeElement?.closest(".hm-popover-panel"));
await check("builder colour popover: opens from the swatch, Tab and Shift+Tab wrap inside it, Esc returns focus to the swatch", async () => {
  const start = await styleOn();
  try {
    await openStyleGroup("Typography"); // Text colour is in Typography
    const swatch = p.locator('button.hm-bs-swatch[aria-label^="Text colour"]');
    const name = await swatch.getAttribute("aria-label");
    if (!/^Text colour, /.test(name)) throw new Error("swatch name " + name);
    await swatch.focus(); await press("Enter"); await settle(200);
    if (!(await p.locator(".hm-popover-panel:popover-open").count())) throw new Error("Enter on the swatch did not open the popover");
    if ((await swatch.getAttribute("aria-expanded")) !== "true") throw new Error("aria-expanded not true while open");
    if (!(await inPanel())) throw new Error("focus did not move into the popover");
    const stops = await p.locator(".hm-popover-panel:popover-open").evaluate((m) => m.querySelectorAll("button:not([disabled]), input:not([disabled])").length);
    for (let i = 0; i < stops * 2 + 3; i++) { await p.keyboard.press("Tab"); if (!(await inPanel())) throw new Error(`Tab ${i + 1} left the popover`); }
    for (let i = 0; i < stops + 2; i++) { await p.keyboard.press("Shift+Tab"); if (!(await inPanel())) throw new Error(`Shift+Tab ${i + 1} left the popover`); }
    await press("Escape"); await settle(200);
    if (await p.locator(".hm-popover-panel:popover-open").count()) throw new Error("Esc did not close the popover");
    if (!(await p.evaluate(() => document.activeElement?.classList.contains("hm-bs-swatch")))) throw new Error("focus after Esc is not the swatch");
    return `${stops} stops; Tab wraps in both directions; Esc -> swatch`;
  } finally { await styleOff(start); }
});
await check("builder colour popover: choosing a site colour commits its token, Clear unsets it, one undo each", async () => {
  const start = await styleOn();
  try {
    await openStyleGroup("Typography"); // Text colour is in Typography
    const swatch = p.locator('button.hm-bs-swatch[aria-label^="Text colour"]');
    await swatch.click(); await settle(200);
    await p.locator(".hm-popover-panel:popover-open .hm-bs-copt", { hasText: "accent" }).click(); await settle(300);
    const set = await swatch.getAttribute("aria-label");
    if (!/^Text colour, Accent, /.test(set)) throw new Error("after choosing accent the swatch is " + set);
    await swatch.click(); await settle(200);
    const hex = p.locator(".hm-popover-panel:popover-open input[aria-label='Hex colour']");
    await hex.fill("#12"); await hex.press("Enter"); await settle(200);
    if ((await hex.getAttribute("aria-invalid")) !== "true") throw new Error("an invalid hex was not flagged");
    await p.locator(".hm-popover-panel:popover-open button", { hasText: "Clear" }).click(); await settle(300);
    if (!/not set|inherited/.test(await swatch.getAttribute("aria-label"))) throw new Error("Clear left " + await swatch.getAttribute("aria-label"));
    await p.evaluate(() => document.activeElement?.blur?.());
    await press("Control+z"); await settle(300);
    if (!/^Text colour, Accent, /.test(await p.locator('button.hm-bs-swatch[aria-label^="Text colour"]').getAttribute("aria-label"))) throw new Error("one undo did not restore accent");
    return "token committed; invalid hex refused; Clear and its undo are one step";
  } finally { await styleOff(start); }
});
await check("builder number + unit + slider: Move Y is the number field alone, arrows step 1 / Shift 10 with an undo step each", async () => {
  const start = await styleOn();
  try {
    await openStyleGroup("Effects");
    const n = await p.getByLabel("Move Y", { exact: true }).count();
    if (n !== 1) throw new Error(`"Move Y" matches ${n}`);
    for (const extra of ["Move Y unit", "Move Y slider"]) if ((await p.getByLabel(extra, { exact: true }).count()) !== 1) throw new Error(`no single "${extra}"`);
    const num = p.getByLabel("Move Y", { exact: true });
    await num.focus();
    await press("ArrowUp"); await press("ArrowUp"); await press("ArrowUp", 1);
    await p.keyboard.press("Shift+ArrowUp"); await settle(200);
    if ((await num.inputValue()) !== "13") throw new Error("3 x Up + Shift+Up gave " + await num.inputValue());
    await press("ArrowDown");
    if ((await num.inputValue()) !== "12") throw new Error("Down gave " + await num.inputValue());
    await p.evaluate(() => document.activeElement?.blur?.());
    await press("Control+z"); await settle(300);
    if ((await p.getByLabel("Move Y", { exact: true }).inputValue()) !== "13") throw new Error("one undo did not undo one arrow press (value " + await p.getByLabel("Move Y", { exact: true }).inputValue() + ")");
    return "13 after 3 Up + Shift+Up; Down 12; one undo -> 13";
  } finally { await styleOff(start); }
});
await check("builder slider: one drag is one undo step, even with a pause in the middle", async () => {
  const start = await styleOn();
  try {
    await openStyleGroup("Layout");
    const slider = p.getByLabel("Width slider", { exact: true });
    await slider.scrollIntoViewIfNeeded(); // opening the groups can leave the Layout group scrolled under the top bar
    const box = await slider.boundingBox();
    if (!box) throw new Error("no Width slider");
    const y = box.y + box.height / 2;
    await p.mouse.move(box.x + 6, y); await p.mouse.down();
    for (let i = 1; i <= 10; i++) await p.mouse.move(box.x + 6 + ((box.width - 12) * i) / 20, y);
    await p.waitForTimeout(1500); // longer than the old 1 s merge window
    for (let i = 11; i <= 20; i++) await p.mouse.move(box.x + 6 + ((box.width - 12) * i) / 20, y);
    await p.mouse.up(); await settle(300);
    const set = await p.getByLabel("Width", { exact: true }).inputValue();
    if (!set || set === "0") throw new Error("the drag set nothing: " + set);
    await p.evaluate(() => document.activeElement?.blur?.());
    await press("Control+z"); await settle(300);
    const after = await p.getByLabel("Width", { exact: true }).inputValue();
    if (after !== "") throw new Error(`one undo left Width at ${after} (drag set ${set})`);
    return `drag set ${set}; one undo cleared it`;
  } finally { await styleOff(start); }
});
await check("builder box model: tab order is margin then padding (one nested box model), top right bottom left; Link writes all four sides in one undo step", async () => {
  const start = await styleOn();
  try {
    const want = ["Margin top", "Margin right", "Margin bottom", "Margin left", "Padding top", "Padding right", "Padding bottom", "Padding left"]; // the Spacing box model: padding sits inside margin, and the margin sides come first in the DOM
    await p.locator(".hm-bs-bm__m > .hm-bs-bm__n--top").focus();
    const seen = ["Margin top"];
    if ((await p.locator(".hm-bs-bm--nested").count()) !== 1) throw new Error("the Style tab does not show exactly one nested box model");
    for (let i = 0; i < 120 && seen.length < want.length; i++) {
      await p.keyboard.press("Tab");
      const l = await p.evaluate(() => (document.activeElement?.tagName === "INPUT" ? document.activeElement.getAttribute("aria-label")?.split(",")[0] : null)); // "Margin top, 0px, not set" -> "Margin top"
      if (l && l !== seen.at(-1) && want.includes(l)) seen.push(l);
    }
    if (seen.join(">") !== want.join(">")) throw new Error("tab order " + seen.join(">"));
    const link = p.getByRole("button", { name: "Link all sides", exact: true });
    if ((await link.getAttribute("aria-pressed")) !== "false") throw new Error("Link starts pressed");
    await link.focus(); await press("Space");
    if ((await link.getAttribute("aria-pressed")) !== "true") throw new Error("Space did not press Link");
    const side = (k) => p.locator(`.hm-bs-bm__p > .hm-bs-bm__n--${k}`); // the aria-label also carries the value, so the sides are found by class
    const top = side("top");
    await top.fill("8"); await top.press("Enter"); await settle(300);
    for (const k of ["right", "bottom", "left"]) if ((await side(k).inputValue()) !== "8") throw new Error(`${k} is ${await side(k).inputValue()} when linked`);
    await p.evaluate(() => document.activeElement?.blur?.());
    await press("Control+z"); await settle(300);
    for (const k of ["top", "right", "bottom", "left"]) if ((await side(k).inputValue()) !== "0") throw new Error(`one undo left ${k} at ${await side(k).inputValue()}`);
    return "8 inputs in T,R,B,L order; Link writes four sides; one undo clears them";
  } finally { await styleOff(start); }
});
await b.close();

writeResult(`keyboard-${engine}.json`, { engine, role, pages: res, widgets: checks, problems: bad });
console.log(`\n${engine} role=${role}: ${bad} problems`);
process.exit(bad ? 1 : 0);
