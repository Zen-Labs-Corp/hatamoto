// The menu editor, by click paths (A1 to A7) with JavaScript on, and A6 once more
// with JavaScript off. It seeds a Footer tree (4 group headings written the old way,
// as the address "#", with 21 links under them and one loose item), drives the editor
// with a real pointer and keyboard, saves, reloads and compares the tree with what the
// editor showed, and reads the public footer. The Footer menu is put back at the end.
// node scripts/admin-qa/menu-editor.mjs   Exit 1 on a failed check.
import { launch, newSession, base } from "./lib.mjs";

const b = await launch();
let bad = 0;
const ok = (c, m) => { console.log(`${c ? "ok  " : "FAIL"} ${m}`); if (!c) bad++; };
const same = (a, c) => JSON.stringify(a) === JSON.stringify(c);

const GROUPS = {
  Product: ["AI crawler", "MCP server", "Docs", "Changelog", "Roadmap"],
  Compare: ["Versus Alpha", "Versus Beta", "Versus Gamma", "Versus Delta"],
  Resources: ["Blog", "Guides", "Templates", "Status", "Support", "Community"],
  Trust: ["Security", "Privacy", "Terms", "Cookies", "Accessibility", "Contact us"],
};
const LOOSE = "Press";

// seedRows is the seeded tree as [depth, label] pairs, in order.
const seedRows = [];
for (const [head, kids] of Object.entries(GROUPS)) {
  seedRows.push([0, head]);
  kids.forEach((k) => seedRows.push([1, k]));
}
seedRows.push([0, LOOSE]);

const page = await newSession(b, { theme: "light", width: 1440 });
const dialogs = [];
page.on("dialog", (d) => { dialogs.push(d.type()); d.accept().catch(() => {}); });
const errors = [];
page.on("pageerror", (e) => errors.push(e.message));
page.on("console", (m) => { if (m.type() === "error") errors.push(m.text()); });

// -- helpers -----------------------------------------------------------------

const rowsOf = (p) => p.$$eval("[data-hm-item]", (ls) => ls.map((l) => [Number(l.dataset.depth), l.querySelector('[name$=".label"]').value]));
const row = (p, label) => p.locator("[data-hm-item]").filter({ has: p.locator(`[name$=".label"][value="${label}"]`) });
// byLabel finds a row by the label as it is now (the value attribute is the saved one).
const byLabel = async (p, label) => {
  const i = (await rowsOf(p)).findIndex((r) => r[1] === label);
  return i < 0 ? null : p.locator("[data-hm-item]").nth(i);
};
const clickRow = async (p, label) => {
  const li = await byLabel(p, label);
  await li.scrollIntoViewIfNeeded();
  await li.click({ position: { x: 240, y: 12 } });
  return li;
};
const state = (p) => p.locator(".hm-savestate").first().getAttribute("data-state");

async function postSave(p, fields) {
  const token = await p.$eval('meta[name="csrf-token"]', (m) => m.content);
  return p.evaluate(async ({ fields, token }) => {
    const body = new URLSearchParams([["_csrf", token], ...fields]);
    const res = await fetch(location.pathname, { method: "POST", body, credentials: "same-origin", headers: { "X-CSRF-Token": token }, redirect: "follow" });
    return res.status;
  }, { fields, token });
}

async function save(p) {
  await Promise.all([p.waitForNavigation(), p.locator('.hm-savebar button[name="_op"][value="save"]').click()]);
}

async function publicFooter() {
  const res = await page.request.get(`${base}/`);
  const html = await res.text();
  return html.slice(html.indexOf('<footer class="hm-footer'), html.indexOf("</footer>") + 9);
}

// -- seed --------------------------------------------------------------------

await page.goto(`${base}/admin/theme/menus`);
const footerHref = await page.$$eval('a[href*="/menus/"]', (as) => as.filter((a) => a.textContent.trim() === "Footer").map((a) => a.getAttribute("href"))[0]);
if (!footerHref) {
  ok(false, "the seeded instance has a Footer menu");
  await b.close();
  process.exit(1);
}
await page.goto(base + footerHref);
const original = await page.$eval("form[data-hm-editor]", (f) => [...new FormData(f).entries()].filter(([k]) => k === "name" || k === "location" || k.startsWith("i.")));
const seed = [["name", "Footer"], ["location", "footer"], ["_op", "save"]];
seedRows.forEach(([depth, label], i) => {
  seed.push([`i.${i}.label`, label], [`i.${i}.depth`, String(depth)]);
  if (depth === 0 && label !== LOOSE) seed.push([`i.${i}.type`, "custom"], [`i.${i}.url`, "#"]);
  else seed.push([`i.${i}.type`, "custom"], [`i.${i}.url`, `/${label.toLowerCase().replace(/[^a-z0-9]+/g, "-")}`]);
});

async function reopen() {
  await page.goto(base + footerHref);
  await page.waitForSelector("[data-hm-item]");
}

try {
  await postSave(page, seed);
  await reopen();
  ok(same(await rowsOf(page), seedRows), `seeded the Footer tree (${seedRows.length} items)`);
  await page.setViewportSize({ width: 1440, height: 5200 }); // the whole tree on screen, so a drag needs no scrolling

  // ---- the editor with JavaScript on: items 1 to 3 --------------------------
  ok((await state(page)) === "saved", "item 1: the save bar is an hm/savestate and reads saved on load");
  ok(await page.locator("#menu-inspector.hm-card").count() === 1 && (await page.locator("#menu-preview.hm-card").count()) === 1, "item 2: the inspector and the preview are cards");
  const undoClass = await page.locator("[data-hm-undo-all]").getAttribute("class");
  const redoClass = await page.locator("[data-hm-redo]").getAttribute("class");
  const discardClass = await page.locator("[data-hm-discard]").getAttribute("class");
  ok(![undoClass, redoClass, discardClass].some((c) => c.includes("ghost")), "item 3: Undo, Redo and Discard changes are not the ghost variant");
  ok((await page.locator("[data-hm-handle]:visible").count()) === seedRows.length, "drag grips show with JavaScript on");

  // ---- A1: No link saves, the public footer writes a span -------------------
  await clickRow(page, LOOSE);
  ok(await page.locator("[data-hm-ins-single]").isVisible(), "A1: selecting an item opens the inspector");
  await page.locator('[data-hm-ins-type="none"]').click();
  ok((await state(page)) === "unsaved", "A1: changing the link type marks the menu unsaved");
  await save(page);
  const foot = await publicFooter();
  ok(/<span[^>]*>Press<\/span>/.test(foot) && !/<a [^>]*href="#"[^>]*>Press/.test(foot), "A1: the public footer shows Press as a span with no link");
  await reopen();
  ok((await (await byLabel(page, LOOSE)).getAttribute("data-type")) === "none", "A1: the type No link is kept after the save");

  // ---- A2: a Page item with no target warns, the count cycles ----------------
  await page.locator('#add-type').selectOption("entity");
  await Promise.all([page.waitForNavigation(), page.locator('button[name="_op"][value="add"]').click()]);
  await page.waitForSelector("[data-hm-item]");
  const rowsAfterAdd = await rowsOf(page);
  const newIndex = rowsAfterAdd.length - 1;
  const fresh = page.locator("[data-hm-item]").nth(newIndex);
  await fresh.click({ position: { x: 240, y: 12 } });
  await page.locator("#ins-label").fill("Brand new");
  const chip = page.locator('[data-hm-issue="warn"]');
  await chip.waitFor();
  ok((await chip.textContent()).replace(/\s+/g, " ").trim() === "1 without a link", "A2: the save bar says 1 without a link");
  ok(await chip.locator(".hm-badge--warning").count() === 1, "A2: the count is an amber hm/badge");
  await chip.click();
  ok(await page.evaluate(() => document.activeElement?.closest("[data-hm-item]")?.querySelector('[name$=".label"]').value === "Brand new"), "A2: clicking the count moves to the item without a link");
  await save(page);
  ok(await page.locator("[data-hm-item]").count() === seedRows.length + 1, "A2: an item without a link saves (a warning does not block)");
  await reopen();
  ok(await page.locator('[data-hm-issue="warn"]').count() === 1, "A2: the warning is still counted after the save");

  // ---- A3: class, style and colour reach the public link --------------------
  await clickRow(page, "Docs");
  const styles = await page.locator("#ins-style option").allTextContents();
  const chips = await page.locator("[data-hm-ins-class]").allTextContents();
  ok(["Default", "Button", "Link", "Muted"].every((l) => styles.includes(l)) && chips.includes("footer-cta"), "item 10: the theme's styles and class suggestions fill the inspector");
  await page.locator("#ins-style").selectOption({ label: "Button" });
  await page.locator("#ins-color").selectOption({ label: "Accent" });
  await page.locator('[data-hm-ins-class="footer-cta"]').click();
  ok((await page.locator("[data-hm-ins-output]").textContent()).includes("footer-cta hm-nav__item--button hm-nav-color--accent"), "A3: the Output line shows the classes the link will get");
  await save(page);
  const foot3 = await publicFooter();
  const docsLink = /<a [^>]*>Docs<\/a>/.exec(foot3)?.[0] ?? "";
  ok(["hm-nav__item--button", "hm-nav-color--accent", "footer-cta"].every((c) => docsLink.includes(c)), `A3: the public link has the class, style and colour (${docsLink})`);
  await reopen();

  // ---- A4: pointer drag into a group, Move to, Alt+Left ---------------------
  const before4 = await rowsOf(page);
  const handle = (await byLabel(page, LOOSE)).locator("[data-hm-handle]");
  const lastKid = await byLabel(page, "Roadmap");
  const hb = await handle.boundingBox();
  const kb = await lastKid.boundingBox();
  const indentPx = await page.evaluate(() => document.querySelector('[data-hm-item][data-depth="1"]').getBoundingClientRect().left - document.querySelector('[data-hm-item][data-depth="0"]').getBoundingClientRect().left);
  await page.mouse.move(hb.x + hb.width / 2, hb.y + hb.height / 2);
  await page.mouse.down();
  await page.mouse.move(hb.x + hb.width / 2 + indentPx, kb.y + kb.height + 4, { steps: 12 });
  const label = await page.locator("[data-hm-droplabel]").first().textContent({ timeout: 3000 }).catch(() => "none");
  ok(label === "inside Product", `A4: the drop line says "${label}" while the pointer is over the end of Product`);
  await page.mouse.up();
  const dragged = await rowsOf(page);
  const expect4 = before4.filter((r) => r[1] !== LOOSE);
  expect4.splice(before4.findIndex((r) => r[1] === "Roadmap") + 1, 0, [1, LOOSE]);
  ok(same(dragged.filter((r) => r[1] !== "Brand new"), expect4.filter((r) => r[1] !== "Brand new")), "A4: the dragged item sits at depth 1 at the end of Product, as the line showed");
  ok(await page.locator(".hm-menutree__dropline").count() === 0, "A4: the drop line is gone after the drop");
  await save(page);
  await reopen();
  ok(same(await rowsOf(page), dragged), "A4: the tree after a save and reload is the tree the drag made");
  // a touch drag takes the same path (pointer events with pointerType touch)
  const cdp = await page.context().newCDPSession(page);
  const th = (await byLabel(page, LOOSE)).locator("[data-hm-handle]");
  const tb = await th.boundingBox();
  const tx = tb.x + tb.width / 2;
  const ty = tb.y + tb.height / 2;
  const target = await (await byLabel(page, "Community")).boundingBox();
  await cdp.send("Input.dispatchTouchEvent", { type: "touchStart", touchPoints: [{ x: tx, y: ty }] });
  for (let s = 1; s <= 8; s++) await cdp.send("Input.dispatchTouchEvent", { type: "touchMove", touchPoints: [{ x: tx, y: ty + ((target.y + target.height + 4 - ty) * s) / 8 }] });
  const touchLabel = await page.locator("[data-hm-droplabel]").first().textContent({ timeout: 3000 }).catch(() => "none");
  await cdp.send("Input.dispatchTouchEvent", { type: "touchEnd", touchPoints: [] });
  const touched = await rowsOf(page);
  const ci = touched.findIndex((r) => r[1] === "Community");
  ok(touchLabel === "inside Resources" && same(touched[ci + 1], [1, LOOSE]), `A4: a touch drag moves the item too (line "${touchLabel}")`);
  await reopen(); // drop the touch edit

  // Move to: the destination first, then Move to in the item's actions menu
  await page.locator("#moveto-target").selectOption({ label: "Under Compare" });
  const mover = await byLabel(page, "Blog");
  await mover.locator('[popovertarget]').click();
  await mover.locator('[name="_op"][value^="moveto."]').click();
  const moved = await rowsOf(page);
  const cmp = moved.findIndex((r) => r[1] === "Compare");
  ok(moved[cmp + 5]?.[1] === "Blog" && moved[cmp + 5][0] === 1 && moved[cmp + 6]?.[1] === "Resources", "A4: Move to puts Blog last under Compare");
  const afterMove = moved;
  await save(page);
  await reopen();
  ok(same(await rowsOf(page), afterMove), "A4: Move to gives the same tree after a save as in the editor");

  // Outdent with Alt+Left: after the parent's whole group, later siblings stay
  const li = await byLabel(page, "MCP server");
  await li.focus();
  await page.keyboard.press("Alt+ArrowLeft");
  const out = (await rowsOf(page)).filter((r) => !["Brand new", LOOSE].includes(r[1]));
  const pi = out.findIndex((r) => r[1] === "Product");
  ok(same(out.slice(pi, pi + 6), [[0, "Product"], [1, "AI crawler"], [1, "Docs"], [1, "Changelog"], [1, "Roadmap"], [0, "MCP server"]]), "A4: Alt+Left puts MCP server after Product's group and the siblings stay under Product");
  const afterOut = await rowsOf(page);
  ok((await state(page)) === "unsaved", "A4: the keyboard edit marks the menu unsaved");
  await save(page);
  await reopen();
  ok(same(await rowsOf(page), afterOut), "A4: the outdent is the same after a save (the server applies no other rule)");

  // ---- item 6: the accessible names follow the tree -----------------------------
  const names = () => page.$$eval("[data-hm-item]", (ls) => ls.map((l, i) => {
    const next = ls[i + 1];
    const parent = Boolean(next) && Number(next.dataset.depth) > Number(l.dataset.depth);
    const rm = l.querySelector('[name="_op"][value^="remove."] .hm-visually-hidden').textContent;
    const dup = l.querySelector('[name="_op"][value^="dup."] .hm-visually-hidden').textContent;
    return rm.includes("and its sub-items") === parent && dup.includes("and its sub-items") === parent;
  }).every(Boolean));
  ok(await names(), "item 6: Remove and Duplicate name the sub-items exactly when there are some");
  await (await byLabel(page, "MCP server")).focus();
  await page.keyboard.press("Alt+ArrowRight"); // back under Product: Product gains a child again
  await (await byLabel(page, "Versus Beta")).focus();
  await page.keyboard.press("Alt+ArrowUp");
  ok(await names(), "item 6: the names are right after an indent and a reorder");
  await reopen();

  // ---- A7: Remove takes the group, Undo gives it back ----------------------------
  const full = await rowsOf(page);
  const group = await byLabel(page, "Trust");
  await group.locator("[popovertarget]").click();
  await group.locator('[name="_op"][value^="remove."]').click();
  const gone = await rowsOf(page);
  ok(gone.length === full.length - 7 && !gone.some((r) => r[1] === "Trust" || r[1] === "Privacy"), "A7: Remove deletes the group with its 6 items");
  ok(await page.locator("[data-hm-undo-all]").isEnabled(), "A7: the save bar Undo is ready");
  await page.locator("[data-hm-undo-all]").click();
  ok(same(await rowsOf(page), full), "A7: Undo restores the same order and depths");
  ok((await state(page)) === "saved", "A7: after Undo the menu reads saved again");
  await (await byLabel(page, "Trust")).focus();
  await page.keyboard.press("Delete");
  ok((await rowsOf(page)).length === full.length - 7, "A7: Delete on a focused item removes it with its sub-items");
  await page.keyboard.press("Control+z");
  ok(same(await rowsOf(page), full), "A7: Ctrl+Z undoes the removal");
  await page.keyboard.press("Control+Shift+z");
  ok((await rowsOf(page)).length === full.length - 7, "A7: Ctrl+Shift+Z redoes it");
  await page.keyboard.press("Control+z");

  // ---- the preview shows the unsaved tree, with a none item as a span ----------
  await clickRow(page, LOOSE);
  await page.locator("#ins-label").fill("Press room");
  await page.waitForFunction(() => {
    const f = document.querySelector("iframe[name=menu-preview]");
    return f?.contentDocument?.body?.textContent.includes("Press room");
  });
  const frame = page.frame({ name: "menu-preview" });
  ok(await frame.evaluate(() => { const s = [...document.querySelectorAll("footer span")].find((e) => e.textContent === "Press room"); return Boolean(s) && !s.closest("a"); }), "preview: the unsaved label shows in the theme's footer, the none item as a span");
  await page.locator("[data-hm-preview-scheme=dark]").click();
  ok((await frame.evaluate(() => document.documentElement.dataset.theme)) === "dark", "preview: Dark sets the scheme of the previewed page");
  await page.locator("[data-hm-preview-scheme=light]").click();
  await page.locator("[data-hm-preview-width=mobile]").click();
  ok((await page.locator("iframe[name=menu-preview]").boundingBox()).width < 500, "preview: Mobile narrows the frame");
  await page.locator("[data-hm-preview-width=desktop]").click();
  await frame.locator("footer a", { hasText: "Security" }).click();
  ok((await page.locator("[data-hm-item][data-hm-selected] [name$='.label']").inputValue()) === "Security", "preview: a click on a link selects its item and does not leave the editor");

  // ---- the save bar and the leave warning ----------------------------------------
  const issues = page.locator("[data-hm-issues] .hm-badge").first();
  ok(await issues.count() === 1, "item 1: the issue count is an hm/badge");
  dialogs.length = 0;
  await page.goto(`${base}/admin`);
  ok(dialogs.includes("beforeunload"), "leave warning: unsaved edits ask before the page is left");
  ok(errors.length === 0, `no script errors on the editor (${errors.slice(0, 2).join(" / ")})`);

  // ---- A5: the header menu renders its nested item ------------------------------
  const html = await (await page.request.get(`${base}/`)).text();
  ok(/hm-nav__sub/.test(html) && /Team/.test(html), "A5: the public header menu still shows its nested item");

  // ---- A6: with JavaScript off ----------------------------------------------------
  const plain = await newSession(b, { theme: "light", width: 1440, javaScriptEnabled: false });
  await plain.goto(base + footerHref);
  ok(!(await plain.locator("#menu-inspector").isVisible()) && !(await plain.locator("[data-hm-handle]").first().isVisible()) && !(await plain.locator("[data-hm-undo-all]").isVisible()), "A6: without JavaScript there is no inspector, no grip and no Undo");
  const at = async (label) => (await rowsOf(plain)).findIndex((r) => r[1] === label);
  const item = (i) => plain.locator("[data-hm-item]").nth(i);
  const log = await at("Changelog");
  await item(log).locator(".hm-menutree__edit > summary").click();
  await plain.fill(`[name="i.${log}.label"]`, "Release notes");
  await item(log).locator("details.hm-menutree__more", { hasText: "Appearance" }).locator("summary").click();
  await plain.fill(`[name="i.${log}.class"]`, "footer-cta");
  const st = await at("Status");
  await item(st).locator(".hm-menutree__edit > summary").click();
  await item(st).locator("label.hm-segmented__opt", { hasText: "No link" }).click();
  await Promise.all([plain.waitForNavigation(), plain.locator('.hm-savebar button[name="_op"][value="save"]').click()]);
  await plain.goto(base + footerHref);
  const row6 = await plain.$$eval("[data-hm-item]", (ls) => ls.map((l) => [l.querySelector('[name$=".label"]').value, l.dataset.type, l.querySelector('[name$=".class"]').value]));
  ok(same(row6.find((r) => r[0] === "Release notes"), ["Release notes", "custom", "footer-cta"]) && row6.find((r) => r[0] === "Status")?.[1] === "none", "A6: without JavaScript a rename, a class and No link all save");
  const foot6 = await publicFooter();
  ok(/<a [^>]*footer-cta[^>]*>Release notes<\/a>/.test(foot6) && /<span[^>]*>Status<\/span>/.test(foot6), "A6: the public footer shows the renamed link with its class and Status as a span");
} finally {
  // put the Footer menu back as it was
  try {
    await page.goto(base + footerHref);
    await postSave(page, [...original.map(([k, v]) => [k, v]), ["_op", "save"]]);
  } catch (e) {
    console.log(`WARN could not restore the Footer menu: ${e.message}`);
  }
}
await b.close();
console.log(bad ? `${bad} failed` : "all passed");
process.exit(bad ? 1 : 0);
