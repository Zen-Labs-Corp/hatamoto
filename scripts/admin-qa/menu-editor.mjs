// The menu editor, by click paths (A1 to A7) with JavaScript on, and A6 once more
// with JavaScript off. It seeds a Footer tree (4 group headings written the old way,
// as the address "#", with 21 links under them and one loose item), drives the editor
// with a real pointer and keyboard, saves, reloads and compares the tree with what the
// editor showed, and reads the public footer. The Footer menu is put back at the end.
// node scripts/admin-qa/menu-editor.mjs   Exit 1 on a failed check.
import fs from "node:fs";
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

// treeSemantics reports what assistive technology reads from the tree: the roles,
// each row's level, its selection and, on a parent, whether it is expanded.
const treeSemantics = (p) => p.$eval("[data-hm-menutree]", (t) => {
  const lis = [...t.querySelectorAll(":scope > [data-hm-item]")];
  return t.getAttribute("role") === "tree" && lis.every((l, i) => {
    const d = Number(l.dataset.depth);
    const parent = i + 1 < lis.length && Number(lis[i + 1].dataset.depth) > d;
    return l.getAttribute("role") === "treeitem"
      && l.getAttribute("aria-level") === String(d + 1)
      && l.getAttribute("aria-selected") === String(l.hasAttribute("data-hm-selected"))
      && (parent ? ["true", "false"].includes(l.getAttribute("aria-expanded")) : !l.hasAttribute("aria-expanded"));
  });
});

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
  dialogs.length = 0;
  await save(page);
  ok(!dialogs.includes("beforeunload"), "leave warning: Save does not ask before it leaves the page");
  await page.goto(`${base}/admin`);
  ok(!dialogs.includes("beforeunload"), "leave warning: no warning after a Save, leaving the page is silent");
  await page.goto(base + footerHref);
  const foot = await publicFooter();
  ok(/<span[^>]*>Press<\/span>/.test(foot) && !/<a [^>]*href="#"[^>]*>Press/.test(foot), "A1: the public footer shows Press as a span with no link");
  await reopen();
  ok((await (await byLabel(page, LOOSE)).getAttribute("data-type")) === "none", "A1: the type No link is kept after the save");

  // ---- A2: a Page item with no target warns, the count cycles ----------------
  // The server's own Add item (hidden when scripts run) still works: press it the way a no-script form does.
  await Promise.all([page.waitForNavigation(), page.locator('button[name="_op"][value="add"]').evaluate((b) => b.click())]);
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
  ok(await page.evaluate(() => document.querySelector("[data-hm-item][data-hm-selected] [name$='.label']").value === "Brand new" && document.activeElement?.closest("[data-hm-inspector]") !== null), "A2: clicking the count selects the item without a link and moves to its fields in the inspector");
  await save(page);
  ok(await page.locator("[data-hm-item]").count() === seedRows.length + 1, "A2: an item without a link saves (a warning does not block)");
  await reopen();
  ok(await page.locator('[data-hm-issue="warn"]').count() === 1, "A2: the warning is still counted after the save");

  // ---- A3: class, style and colour reach the public link --------------------
  await clickRow(page, "Docs");
  const styles = await page.locator('[data-hm-ins-group="style"] .hm-segmented__btn').allTextContents();
  const chips = await page.locator("[data-hm-ins-class]").allTextContents();
  ok(["Default", "Button", "Link", "Muted"].every((l) => styles.includes(l)) && chips.includes("footer-cta"), "item 10: the theme's styles and class suggestions fill the inspector");
  await page.locator('[data-hm-ins-group="style"] label', { hasText: "Button" }).click();
  await page.locator('[data-hm-ins-group="color"] [role="radio"]', { hasText: "Accent" }).click();
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
  const indentPx = await page.evaluate(() => document.querySelector('[data-hm-item][data-depth="1"] [data-hm-handle]').getBoundingClientRect().left - document.querySelector('[data-hm-item][data-depth="0"] [data-hm-handle]').getBoundingClientRect().left);
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
  ok(await treeSemantics(page), "tree: roles and levels follow an indent and a reorder");
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
  ok(await treeSemantics(page), "tree: roles, levels, selection and expanded state are right after an undo");
  ok((await state(page)) === "saved", "A7: after Undo the menu reads saved again");
  const onRow = () => page.evaluate(() => Boolean(document.activeElement?.closest("[data-hm-item]")));
  ok(await onRow(), "focus: the save bar Undo that empties its stack leaves focus on a row");
  const rm = (await rowsOf(page)).findIndex((r) => r[1] === "Roadmap");
  await page.locator("[data-hm-item]").nth(rm).focus();
  await page.keyboard.press("Control+d");
  await page.locator("[data-hm-item]").nth(rm + 1).focus(); // the copy
  await page.keyboard.press("Control+z");
  ok(await onRow(), "focus: Ctrl+Z that removes the copy leaves focus on a row");
  await (await byLabel(page, "Trust")).focus();
  await page.keyboard.press("Delete");
  ok((await rowsOf(page)).length === full.length - 7, "A7: Delete on a focused item removes it with its sub-items");
  ok(await treeSemantics(page), "tree: roles, levels, selection and expanded state are right after a removal");
  await page.keyboard.press("Control+z");
  ok(same(await rowsOf(page), full), "A7: Ctrl+Z undoes the removal");
  await page.keyboard.press("Control+Shift+z");
  ok((await rowsOf(page)).length === full.length - 7, "A7: Ctrl+Shift+Z redoes it");
  await page.keyboard.press("Control+z");

  // ---- L: layout, compact rows, the Add popover, pickers and the save bar ---------
  await reopen();
  await page.setViewportSize({ width: 1440, height: 900 });
  const box = async (sel) => (await page.locator(sel).first().boundingBox());
  const visibleRows = () => page.locator("[data-hm-item]:not([hidden])");
  const guidesOk = () => page.$$eval("[data-hm-item]", (ls) => {
    const level = (() => { const d1 = ls.find((l) => l.dataset.depth === "1"); return d1 ? parseFloat(getComputedStyle(d1).backgroundSize) : 0; })();
    return ls.every((l) => {
      const d = Number(l.dataset.depth);
      const w = parseFloat(getComputedStyle(l).backgroundSize);
      return d === 0 ? w === 0 : level > 0 && Math.abs(w - d * level) < 1.5;
    });
  });

  // ISC-27 to 30: the grid at 1440 px, the sticky inspector, one column at 900 px
  const tree = await box("#menu-items");
  const ins = await box("#menu-inspector");
  const prev = await box("#menu-preview");
  ok(ins.x >= tree.x + tree.width - 1 && Math.abs(ins.y - tree.y) <= 8, `ISC-27: the inspector is right of the tree with the tops within 8 px (tree ${Math.round(tree.x)},${Math.round(tree.y)} ${Math.round(tree.width)}w; inspector ${Math.round(ins.x)},${Math.round(ins.y)})`);
  ok(prev.y >= Math.max(tree.y + tree.height, ins.y + ins.height) - 1 && Math.abs(prev.x - tree.x) <= 4 && Math.abs(prev.x + prev.width - (ins.x + ins.width)) <= 4, "ISC-28: the preview is below both and spans from the tree's left edge to the inspector's right edge");
  await page.mouse.move(700, 500);
  await page.mouse.wheel(0, 600);
  await page.waitForTimeout(250);
  const insScrolled = await box("#menu-inspector");
  ok(insScrolled.y >= 0 && insScrolled.y < 450, `ISC-29: the inspector stays in view after scrolling 600 px (top ${Math.round(insScrolled.y)})`);
  await clickRow(page, "Docs"); // a selected row makes the inspector tall
  await page.locator("#menu-preview").scrollIntoViewIfNeeded();
  await page.waitForTimeout(250);
  const [insAtPreview, prevAtPreview] = [await box("#menu-inspector"), await box("#menu-preview")];
  ok(insAtPreview.y + insAtPreview.height <= prevAtPreview.y + 1, `ISC-29: scrolled to the preview, the inspector ends above it (inspector bottom ${Math.round(insAtPreview.y + insAtPreview.height)}, preview top ${Math.round(prevAtPreview.y)})`);
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.setViewportSize({ width: 900, height: 900 });
  const [t9, i9, p9] = [await box("#menu-items"), await box("#menu-inspector"), await box("#menu-preview")];
  ok(i9.y >= t9.y + t9.height - 1 && p9.y >= i9.y + i9.height - 1, "ISC-30: at 900 px the inspector is under the tree and the preview under both");
  await page.setViewportSize({ width: 1440, height: 900 });

  // ISC-31 (owner decision): Name and Location share one compact row directly under the title, above the columns
  const title = await box("main h1");
  const nameBox = await box("#name");
  const locBox = await box("#location-trigger");
  const itemsTop = (await box("#menu-items")).y;
  ok(Math.abs(nameBox.y - locBox.y) <= 4 && nameBox.y >= title.y + title.height - 1 && nameBox.y - (title.y + title.height) <= 120 && nameBox.y + nameBox.height <= itemsTop, `ISC-31: Name and Location are on one line directly under the title and above the columns (name ${Math.round(nameBox.y)}, location ${Math.round(locBox.y)}, title bottom ${Math.round(title.y + title.height)})`);
  ok(!(await page.locator("#menu-details").innerText()).includes("Details") && !(await page.locator("#menu-details .hm-card__head").isVisible()), "ISC-31: the Details card heading is hidden");

  // ISC-32 to 34, 36, 37: compact rows, chips, the old add control gone, guides, the count
  const heights = await visibleRows().evaluateAll((ls) => ls.map((l) => l.getBoundingClientRect().height));
  const treeH = (await box("#menu-tree")).height;
  ok(heights.length > 20 && Math.max(...heights) <= 44 && treeH <= 1300, `ISC-32: every row is at most 44 px tall (max ${Math.max(...heights)}) and the tree at most 1300 px (${Math.round(treeH)})`);
  const chipsOf = async (label) => (await (await byLabel(page, label)).locator("[data-hm-chips] .hm-badge").allTextContents()).map((t) => t.trim());
  const press = await chipsOf(LOOSE);
  ok(press.includes("No link"), `ISC-33: a No link item shows the chip No link (${press.join(", ")})`);
  const headingIcon = await (await byLabel(page, LOOSE)).locator("[data-hm-kind-slot] svg").evaluate((svg) => svg.outerHTML);
  const headingRef = await page.evaluate(() => document.querySelector('template[data-hm-tpl="kinds"]').content.querySelector('[data-hm-kind="none"] svg').outerHTML);
  ok(headingIcon === headingRef && !headingIcon.includes('d="M5 12h14"'), "ISC-33: a No link row uses the heading icon, not the dash");
  await page.evaluate(() => document.activeElement?.blur());
  await page.keyboard.press("?");
  await page.locator("#menu-shortcuts[open]").waitFor();
  const subItemsLines = await page.evaluate(() => {
    const out = [];
    for (const dd of document.querySelectorAll("#menu-shortcuts dd")) {
      if (!dd.textContent.includes("sub-items")) continue;
      const walker = document.createTreeWalker(dd, NodeFilter.SHOW_TEXT);
      let node = walker.nextNode();
      while (node && !node.textContent.includes("sub-items")) node = walker.nextNode();
      if (!node) { out.push(0); continue; } // the word is split over several nodes: not whole
      const at = node.textContent.indexOf("sub-items");
      const range = document.createRange();
      range.setStart(node, at);
      range.setEnd(node, at + "sub-items".length);
      out.push(new Set([...range.getClientRects()].map((r) => Math.round(r.top))).size);
    }
    return out;
  });
  ok(subItemsLines.length > 0 && subItemsLines.every((n) => n === 1), `ISC-33: the shortcuts dialog never breaks "sub-items" across lines (${subItemsLines.join(",")})`);
  await page.keyboard.press("Escape");
  const productRow = await byLabel(page, LOOSE);
  const noLinkTwice = await productRow.evaluate((li) => [...li.querySelectorAll(".hm-menutree__head *")].filter((e) => e.children.length === 0 && e.getClientRects().length > 0 && /^No link$/i.test(e.textContent.trim())).length);
  ok(noLinkTwice === 1, `ISC-33: a heading row says No link once, in its chip (${noLinkTwice} visible)`);
  const nopage = await chipsOf("Brand new");
  ok(nopage.includes("No page"), `ISC-33: a Page item with no page shows No page (${nopage.join(", ")})`);
  const docs = await chipsOf("Docs");
  ok(["Button", "Accent", "footer-cta"].every((c) => docs.includes(c)), `ISC-33: a styled item shows its style, colour and classes (${docs.join(", ")})`);
  const docsRow = await byLabel(page, "Docs");
  ok((await docsRow.locator("[data-hm-chips]").isVisible()) && (await docsRow.locator("[data-hm-kind-slot] svg").count()) === 1 && (await docsRow.locator("[data-hm-handle]").isVisible()) && (await (await byLabel(page, "Product")).locator("[data-hm-collapse]").isVisible()), "ISC-33: a row shows grip, caret (on a parent), kind icon, title and chips");
  await clickRow(page, "Security");
  await page.locator('[data-hm-ins-type="custom"]').click();
  await page.locator("#ins-url").fill("ftp://x");
  ok((await chipsOf("Security")).includes("Bad address"), "ISC-33: a malformed custom address shows Bad address");
  await page.locator("#ins-url").fill("/security");
  const badges = await page.locator("#ins-badge option").allTextContents();
  ok(badges.some((t) => /new/i.test(t)), `ISC-33: the seeded theme declares a New badge (${badges.join(", ")})`);
  await page.locator("#ins-badge").selectOption({ label: badges.find((t) => /new/i.test(t)) });
  ok((await chipsOf("Security")).some((t) => /new/i.test(t)), "ISC-33: a new-badge item shows the chip New");
  await page.locator("#ins-badge").selectOption({ index: 0 });
  ok(!(await page.locator(".hm-menutree__edit").first().isVisible()) && !(await page.locator("#add-type").isVisible()) && !(await page.locator('button[name="_op"][value="add"]').isVisible()), "ISC-34 and 37: with JavaScript on the per-row Edit and the old add select and button are not visible");
  ok(await guidesOk(), "ISC-36: the depth guides follow data-depth on load");
  const n0 = (await rowsOf(page)).length;
  const countText = () => page.locator("[data-hm-count]").innerText().then((t) => t.trim());
  ok((await countText()) === `${n0} items`, `ISC-37: the header says ${n0} items (${await countText()})`);
  const header = page.locator(".hm-menutree__tools");
  ok((await header.locator("[data-hm-filter]").isVisible()) && (await header.locator("[data-hm-collapse-all]").isVisible()) && (await header.locator("[data-hm-expand-all]").isVisible()) && (await header.locator("[data-hm-add-open]").isVisible()), "ISC-37: the header shows the filter, collapse all, expand all and Add");
  const indentAt = (await rowsOf(page)).findIndex((r) => r[1] === LOOSE);
  const depth0 = Number(await page.locator("[data-hm-item]").nth(indentAt).getAttribute("data-depth"));
  await page.locator("[data-hm-item]").nth(indentAt).focus();
  await page.keyboard.press("Alt+ArrowRight");
  ok((await page.locator("[data-hm-item]").nth(indentAt).getAttribute("data-depth")) === String(depth0 + 1) && await guidesOk(), "ISC-36: the guides follow an indent");
  await page.keyboard.press("Control+z");
  ok((await page.locator("[data-hm-item]").nth(indentAt).getAttribute("data-depth")) === String(depth0) && await guidesOk(), "ISC-36: the guides follow an undo");
  await (await byLabel(page, LOOSE)).focus();
  await page.keyboard.press("Delete");
  ok((await countText()) === `${n0 - 1} items`, "ISC-37: the count follows a removal");
  await page.keyboard.press("Control+z");
  ok((await countText()) === `${n0} items`, "ISC-37: the count follows an undo");

  // ISC-35: the row actions show on hover and on focus, and the overflow menu has what the plan lists
  const probe = await byLabel(page, "Blog");
  await probe.scrollIntoViewIfNeeded();
  await page.mouse.move(5, 5);
  const opacity = (li) => li.locator('.hm-menutree__ops > [popovertarget]').evaluate((o) => getComputedStyle(o).opacity);
  const idle = await opacity(await byLabel(page, "Guides"));
  const moveVisible = await (await byLabel(page, "Guides")).locator('[data-hm-op="up"], [data-hm-op="down"]').evaluateAll((bs) => bs.length === 2 && bs.every((b) => getComputedStyle(b).display !== "none" && getComputedStyle(b).opacity === "1" && b.getBoundingClientRect().width > 0));
  ok(moveVisible, "ISC-11: Move up and Move down are visible on an idle row in JS mode");
  await probe.hover();
  ok(idle === "0" && (await opacity(probe)) === "1", `ISC-35: the row actions appear on hover (idle ${idle})`);
  await page.mouse.move(5, 5);
  await (await byLabel(page, "Templates")).locator("[data-hm-add-after]").focus();
  ok((await opacity(await byLabel(page, "Templates"))) === "1" && await page.evaluate(() => document.activeElement?.hasAttribute("data-hm-add-after")), "ISC-35: the plus action is reachable by keyboard and shows on focus");
  await (await byLabel(page, "Templates")).locator('[popovertarget][aria-haspopup="menu"]').click();
  const menuItems = (await (await byLabel(page, "Templates")).locator('[role="menuitem"]').allTextContents()).map((t) => t.replace(/\s+/g, " ").trim());
  ok(["Move to", "Duplicate", "Remove", "Indent", "Outdent"].every((w) => menuItems.some((t) => t.startsWith(w))), `ISC-35: the overflow menu has Move to, Duplicate, Remove, Indent and Outdent (${menuItems.length} items)`);
  await page.keyboard.press("Escape");

  // ISC-38: the Add popover
  const posts = [];
  page.on("request", (r) => { if (r.method() === "POST" && !r.url().endsWith("/menus/preview")) posts.push(r.url()); });
  const undoOnce = () => page.locator("[data-hm-undo-all]").click();
  const nBefore = (await rowsOf(page)).length;
  await page.locator("[data-hm-add-open]").click();
  await page.locator("#menu-add:popover-open").waitFor();
  ok(await page.waitForFunction(() => document.activeElement?.id === "add-search", null, { timeout: 2000 }).then(() => true, () => false), "ISC-38: Add opens the popover with the cursor in the page search");
  const tabNames = await page.locator("#menu-add [role=tab]").allTextContents();
  ok(same(tabNames, ["Pages", "Address", "Heading", "Archive"]), `ISC-38: the popover offers Pages, Address, Heading and Archive (${tabNames.join(", ")})`);
  // the panel hangs off its button and the confirm button is in view, at 1440 and at 900 px wide, 900 px high
  const hangs = async (sel, tag) => {
    await page.waitForFunction(() => document.activeElement?.id === "add-search", null, { timeout: 2000 }); // the panel is placed when focus arrives
    await page.locator("#add-pages-list [role=option]").first().waitFor();
    await page.locator('[data-hm-add-confirm="pages"]').waitFor();
    const m = await page.evaluate((sel) => {
      const r = (s) => document.querySelector(s).getBoundingClientRect();
      return { btn: r(sel), pop: r("#menu-add"), conf: r('[data-hm-add-confirm="pages"]'), vw: document.documentElement.clientWidth, vh: innerHeight };
    }, sel);
    const inView = m.conf.top >= 0 && m.conf.bottom <= m.vh && m.pop.left >= 0 && m.pop.right <= m.vw && m.pop.top >= 0 && m.pop.bottom <= m.vh;
    const attached = m.pop.top >= m.btn.bottom - 1 || m.pop.bottom <= m.btn.top + 1;
    const aligned = Math.abs(m.pop.right - m.btn.right) <= 2 || Math.abs(m.pop.left - m.btn.left) <= 2 || m.pop.left === 8 || Math.abs(m.pop.right - (m.vw - 8)) <= 2;
    ok(inView && attached && aligned, `ISC-38: ${tag}: the panel hangs off its button and Add selected pages is in view (button ${Math.round(m.btn.left)},${Math.round(m.btn.top)}-${Math.round(m.btn.bottom)}; panel ${Math.round(m.pop.left)}-${Math.round(m.pop.right)} x ${Math.round(m.pop.top)}-${Math.round(m.pop.bottom)}; confirm bottom ${Math.round(m.conf.bottom)} of ${m.vh})`);
  };
  await hangs("[data-hm-add-open]", "1440x900 header Add");
  await page.keyboard.press("Escape");
  await page.setViewportSize({ width: 900, height: 900 });
  await page.locator("[data-hm-add-open]").click();
  await page.locator("#menu-add:popover-open").waitFor();
  await hangs("[data-hm-add-open]", "900x900 header Add");
  await page.keyboard.press("Escape");
  await page.setViewportSize({ width: 1440, height: 520 });
  await page.locator("[data-hm-add-open]").click();
  await page.locator("#menu-add:popover-open").waitFor();
  await hangs("[data-hm-add-open]", "1440x520 header Add");
  await page.keyboard.press("Escape");
  await page.setViewportSize({ width: 1440, height: 900 });
  const plusRow = await byLabel(page, "Templates");
  await plusRow.scrollIntoViewIfNeeded();
  await plusRow.hover();
  await plusRow.locator("[data-hm-add-after]").click();
  await page.locator("#menu-add:popover-open").waitFor();
  await page.evaluate(() => document.querySelector("[data-hm-item]:hover [data-hm-add-after]")?.setAttribute("data-qa-plus", ""));
  await hangs("[data-qa-plus]", "row plus");
  await page.evaluate(() => document.querySelector("[data-qa-plus]")?.removeAttribute("data-qa-plus"));
  await page.keyboard.press("Escape");
  await page.locator("[data-hm-add-open]").click();
  await page.locator("#menu-add:popover-open").waitFor();
  await page.locator("#add-pages-list [role=option]").first().waitFor();
  const pageOpts = page.locator("#add-pages-list [role=option]");
  const picks = [(await pageOpts.nth(0).getAttribute("data-label")), (await pageOpts.nth(1).getAttribute("data-label"))];
  await pageOpts.nth(0).click();
  await pageOpts.nth(1).click();
  ok((await pageOpts.nth(0).getAttribute("aria-selected")) === "true" && (await page.locator("[data-hm-add-status]").innerText()).includes("2 pages"), "ISC-38: two pages can be selected in the list");
  await page.locator('[data-hm-add-confirm="pages"]').click();
  const added = (await rowsOf(page)).slice(nBefore);
  ok(added.length === 2 && same(added.map((r) => r[1]), picks), `ISC-38: two page rows with the page titles are added (${added.map((r) => r[1]).join(", ")})`);
  ok(await page.evaluate(() => !document.querySelector("#menu-add:popover-open") && Boolean(document.activeElement?.closest("[data-hm-item]"))), "ISC-38: the popover closes and the first new row has focus");
  ok((await page.locator("[data-hm-item]").evaluateAll((ls, n) => ls.slice(n).map((l) => l.dataset.type), nBefore)).join() === "entity,entity", "ISC-38: the new rows are of type Page");
  await undoOnce();
  ok((await rowsOf(page)).length === nBefore, "ISC-38: one undo removes both pages");
  const adds = [
    ["address", async () => { await page.locator('[data-hm-add-label="address"]').fill("Addr"); await page.locator("#add-address-url").fill("/addr"); }, "custom", "Addr"],
    ["heading", async () => { await page.locator('[data-hm-add-label="heading"]').fill("Head"); }, "none", "Head"],
    ["archive", async () => { await page.locator("#add-archive-term").fill("news"); await page.waitForTimeout(700); await page.keyboard.press("Tab"); await page.waitForTimeout(300); }, "archive", "news"],
  ];
  for (const [tab, fill, type, label] of adds) {
    await page.locator("[data-hm-add-open]").click();
    await page.locator(`#add-tab-${tab}`).click();
    await fill();
    await page.locator(`[data-hm-add-confirm="${tab}"]`).click();
    const last = (await rowsOf(page)).at(-1);
    const lastType = await page.locator("[data-hm-item]").last().getAttribute("data-type");
    ok((await rowsOf(page)).length === nBefore + 1 && last[1] === label && lastType === type, `ISC-38: ${tab} adds one ${type} row`);
    await undoOnce();
  }
  // a page whose title is over 100 characters gets a label the server accepts
  const longTitle = "L".repeat(150);
  await page.route("**/pick/entities*", (r) => r.fulfill({ contentType: "application/json", body: JSON.stringify([{ value: "00000000-0000-0000-0000-000000000001", label: longTitle, hint: "" }]) }));
  await page.locator("[data-hm-add-open]").click();
  await page.locator("#add-search").fill("long");
  const longOpt = page.locator(`#add-pages-list [role=option][data-label="${longTitle}"]`);
  await longOpt.waitFor();
  await longOpt.click();
  await page.locator('[data-hm-add-confirm="pages"]').click();
  const longRow = page.locator("[data-hm-item]").last();
  ok((await longRow.locator('[name$=".label"]').evaluate((f) => [...f.value].length)) === 100 && (await longRow.locator('[name$=".entity_q"]').inputValue()) === longTitle, "ISC-38: a page title over 100 characters is cut to 100 in the label and kept whole in the search text");
  await undoOnce();
  await page.unroute("**/pick/entities*");
  // a reopened panel runs its search again, and a failed search says so
  await page.locator("[data-hm-add-open]").click();
  ok(await page.waitForFunction((t) => { const o = document.querySelector("#add-pages-list [role=option]"); return o && o.dataset.label !== t; }, longTitle, { timeout: 3000 }).then(() => true, () => false), "ISC-38: a reopened panel lists the pages again, not the last search");
  await page.keyboard.press("Escape");
  await page.route("**/pick/entities*", (r) => r.fulfill({ status: 500, contentType: "text/plain", body: "boom" }));
  const seenFail = errors.length;
  await page.locator("[data-hm-add-open]").click();
  ok(await page.waitForFunction(() => document.querySelector("[data-hm-add-status]")?.textContent === "Could not load pages", null, { timeout: 3000 }).then(() => true, () => false), "ISC-38: a failed page search says Could not load pages");
  await page.keyboard.press("Escape");
  await page.unroute("**/pick/entities*");
  errors.splice(seenFail); // the browser logs the 500 it was given
  const afterRow = (await rowsOf(page)).findIndex((r) => r[1] === "Docs");
  await (await byLabel(page, "Docs")).locator("[data-hm-add-after]").click();
  await page.locator("#menu-add:popover-open").waitFor();
  await page.locator("#add-tab-heading").click();
  await page.locator('[data-hm-add-label="heading"]').fill("After Docs");
  await page.locator('[data-hm-add-confirm="heading"]').click();
  const withAfter = await rowsOf(page);
  ok(withAfter[afterRow + 1]?.[1] === "After Docs" && withAfter[afterRow + 1][0] === withAfter[afterRow][0], "ISC-35 and 38: the plus action of a row adds the new item right after it, at its level");
  await undoOnce();
  // a row's plus places the panel at itself, hands focus back to itself and survives a second press while open
  const plus = (await byLabel(page, "Templates")).locator("[data-hm-add-after]");
  await plus.scrollIntoViewIfNeeded();
  await plus.click();
  await page.locator("#menu-add:popover-open").waitFor();
  await page.waitForTimeout(150); // the panel is placed when its toggle event fires
  const gap = await page.evaluate(() => document.querySelector("#menu-add").getBoundingClientRect().top);
  const plusBox = await plus.boundingBox();
  ok(Math.abs(gap - (plusBox.y + plusBox.height + 4)) <= 4, `ISC-38: the panel opened from a row's plus sits under that plus (panel ${Math.round(gap)}, plus bottom ${Math.round(plusBox.y + plusBox.height)})`);
  ok((await page.locator('[data-hm-add-open][popovertarget="menu-add"]').getAttribute("aria-expanded")) !== "true", "ISC-38: the header Add is not marked expanded by a row's plus");
  const seenTwice = errors.length;
  await plus.evaluate((e) => e.click());
  ok(errors.length === seenTwice, "ISC-38: pressing a plus while the panel is open raises no error");
  await page.keyboard.press("Escape");
  ok(await plus.evaluate((e) => e === document.activeElement), "ISC-38: Escape returns focus to the row's plus");
  await page.locator("[data-hm-add-open]").click();
  await page.locator("#menu-add:popover-open").waitFor();
  await page.keyboard.press("Escape");
  ok(await page.evaluate(() => !document.querySelector("#menu-add:popover-open") && document.activeElement?.hasAttribute("data-hm-add-open")), "ISC-38: Escape closes the popover and returns focus to Add");
  ok(posts.length === 0, `ISC-38: adding rows does not post to the server (${posts.join(", ")})`);
  // ISC-39: tiles and swatches are radio groups
  await clickRow(page, "Blog");
  const swatch = page.locator('[data-hm-ins-group="color"] [role="radio"]');
  const colorField = (await byLabel(page, "Blog")).locator('[name$=".color"]');
  ok((await page.locator('[data-hm-ins-group="color"] [role="radiogroup"]').count()) === 1 && (await page.locator('[data-hm-ins-group="style"]').getAttribute("role")) === "radiogroup", "ISC-39: the style tiles and colour swatches are radio groups");
  const pickerRows = await page.evaluate(() => {
    const tops = (sel) => [...new Set([...document.querySelectorAll(sel)].map((e) => Math.round(e.getBoundingClientRect().top)))];
    const names = [...document.querySelectorAll('[data-hm-ins-group="style"] .hm-segmented__btn')].map((e) => e.textContent.trim());
    return { styleTops: tops('[data-hm-ins-group="style"] .hm-segmented__opt'), colourTops: tops('[data-hm-ins-group="color"] [role="radio"]'), names, nTiles: names.length, nSwatches: document.querySelectorAll('[data-hm-ins-group="color"] [role="radio"]').length };
  });
  ok(new Set(pickerRows.names).size === pickerRows.names.length, `ISC-39: no style is listed twice (${pickerRows.names.join(", ")})`);
  ok(pickerRows.styleTops.length === 1 && pickerRows.colourTops.length === 1 && pickerRows.nTiles === 4 && pickerRows.nSwatches === 5, `ISC-39: at 1440 the ${pickerRows.nTiles} style tiles and ${pickerRows.nSwatches} colour swatches each sit on one line (tops ${pickerRows.styleTops}, ${pickerRows.colourTops})`);
  ok(await page.locator('[data-hm-ins-group="style"] .hm-segmented__opt').first().locator("input").isChecked(), "ISC-39: an item with no style shows the Default tile as chosen");
  ok((await swatch.evaluateAll((l) => l.every((e) => e.getAttribute("title") && e.textContent.trim()))), "ISC-39: a dots-only swatch keeps its name as tooltip and accessible name");
  ok((await swatch.evaluateAll((l) => l.filter((e) => e.getAttribute("aria-checked") === "true").length)) === 1, "ISC-39: exactly one colour is aria-checked");
  await swatch.first().focus();
  await page.keyboard.press("ArrowRight");
  const second = await swatch.nth(1).getAttribute("data-value");
  ok((await colorField.inputValue()) === second && (await swatch.nth(1).getAttribute("aria-checked")) === "true", "ISC-39: ArrowRight on a swatch selects the next colour and writes i.N.color");
  await page.keyboard.press("Space");
  await page.keyboard.press("Home");
  ok((await colorField.inputValue()) === "" && (await swatch.first().getAttribute("aria-checked")) === "true", "ISC-39: Home goes back to the first colour (Default writes nothing)");
  const tiles = page.locator('[data-hm-ins-group="style"] input');
  await tiles.nth(0).focus();
  await page.keyboard.press("ArrowRight");
  ok((await (await byLabel(page, "Blog")).locator('[name$=".style"]').inputValue()) === (await tiles.nth(1).getAttribute("value")) && await tiles.nth(1).isChecked(), "ISC-39: arrow keys move the style tile and write i.N.style");
  await page.keyboard.press("Home");
  ok(await swatch.first().evaluate((e) => e.style.getPropertyValue("--swatch") === "") && (await swatch.nth(1).evaluate((e) => e.style.getPropertyValue("--swatch"))) !== "", "ISC-39: each swatch gets its colour through the CSSOM");
  await page.reload();
  await page.waitForSelector("[data-hm-item]");

  // ISC-40: the save bar is one line with both issue chips
  await clickRow(page, "Blog");
  await page.locator("#ins-label").fill("");
  await page.locator('[data-hm-issue="err"]').waitFor();
  await clickRow(page, "Brand new");
  await page.locator('[data-hm-issue="warn"]').waitFor();
  for (const width of [1280, 1440]) {
    await page.setViewportSize({ width, height: 900 });
    await page.waitForTimeout(150);
    const tops = await page.$$eval(".hm-savebar button:not([hidden]), .hm-savebar a, .hm-savebar [data-hm-issue]", (els) => els.filter((e) => e.getBoundingClientRect().width > 0).map((e) => { const r = e.getBoundingClientRect(); return [e.textContent.trim().replace(/\s+/g, " ").slice(0, 16), r.top + r.height / 2]; }));
    const saveTop = tops.find((t) => t[0] === "Save")[1];
    const off = tops.filter((t) => Math.abs(t[1] - saveTop) > 4);
    ok(tops.length >= 8 && off.length === 0 && tops.some((t) => t[0].endsWith("to fix")) && tops.some((t) => t[0].includes("without")), `ISC-40: at ${width} px the save bar is one line with both issue chips, centre lines within 4 px (${tops.map((t) => t.join("@")).join(", ")})`);
    const ring = await page.$eval("[data-hm-state] .hm-savestate__dot", (d) => { const c = getComputedStyle(d); return [c.backgroundColor, c.borderTopWidth]; });
    ok((await state(page)) === "unsaved" && ring[0] === "rgba(0, 0, 0, 0)" && ring[1] !== "0px", `ISC-40: at ${width} px unsaved is a hollow ring, which differs from the filled saved dot by shape (${ring.join(" ")})`);
  }
  await page.setViewportSize({ width: 1440, height: 5200 });
  await reopen();

  // ---- a save the server refuses: its summary, count and inspector work with JavaScript on ----
  const longLabel = "x".repeat(101);
  const guides = await byLabel(page, "Guides");
  const guidesAt = (await rowsOf(page)).findIndex((r) => r[1] === "Guides");
  await guides.locator('[name$=".label"]').evaluate((f, v) => { f.value = v; }, longLabel); // a script skips maxlength
  const seenErrors = errors.length;
  await save(page);
  errors.splice(seenErrors); // the browser logs the 422 it was given
  ok((await page.locator(".hm-formerrors a").count()) === 1, "server error: the failed save lists one problem");
  await page.locator('[data-hm-issue="err"]').waitFor();
  ok((await page.locator('[data-hm-issue="err"]').innerText()).trim().startsWith("1 "), "server error: the save bar counts the item to fix, though the label is not empty");
  ok(await page.waitForFunction(() => document.querySelector("[data-hm-selected]") && !document.querySelector("[data-hm-ins-server]").hidden, null, { timeout: 2000 }).then(() => true, () => false), "server error: the item with the message is selected and the inspector shows the message");
  await page.locator("#ins-label").evaluate((e) => e.blur());
  await page.locator(".hm-formerrors a").click();
  ok(await page.evaluate(() => document.activeElement?.id === "ins-label"), "server error: the summary link focuses the field in the inspector");
  ok((await page.locator("[data-hm-item][data-hm-selected] [name$='.label']").evaluate((f) => f.value.length)) === 101 && (await page.locator("[data-hm-item]").nth(guidesAt).getAttribute("data-hm-selected")) !== null, "server error: the link selected the item it names");
  await page.locator("#ins-label").fill("Guides");
  ok((await page.locator('[data-hm-issue="err"]').count()) === 0 && (await page.locator("[data-hm-ins-server]").isHidden()), "server error: editing the field clears the count and the stale message");
  await reopen();

  // ---- rows added through the popover save with their page and term, and reach the public footer ----
  const before = (await rowsOf(page)).length;
  // the seeded instance has no category, so the run makes one and removes it again
  const post = (path, fields) => page.evaluate(async ({ path, fields }) => {
    const token = document.querySelector('meta[name="csrf-token"]').content;
    const res = await fetch(path, { method: "POST", body: new URLSearchParams([["_csrf", token], ...fields]), credentials: "same-origin", headers: { "X-CSRF-Token": token }, redirect: "manual" });
    return res.status;
  }, { path, fields });
  const TERM = "QA menu term";
  let termID = "";
  try {
    await post("/admin/settings/taxonomies/terms", [["kind", "category"], ["name", TERM]]);
    termID = await page.evaluate(async (name) => {
      const doc = new DOMParser().parseFromString(await (await fetch("/admin/settings/taxonomies?kind=category")).text(), "text/html");
      return [...doc.querySelectorAll('a[href*="/taxonomies/terms/"]')].find((a) => a.textContent.trim() === name)?.getAttribute("href").split("/").pop() ?? "";
    }, TERM);
    await page.locator("[data-hm-add-open]").click();
    await page.locator("#add-pages-list [role=option]").nth(1).waitFor();
    const opts = page.locator("#add-pages-list [role=option]");
    const wanted = [[await opts.nth(0).getAttribute("data-value"), await opts.nth(0).getAttribute("data-label")], [await opts.nth(1).getAttribute("data-value"), await opts.nth(1).getAttribute("data-label")]];
    await opts.nth(0).click();
    await opts.nth(1).click();
    await page.locator('[data-hm-add-confirm="pages"]').click();
    await page.locator("[data-hm-add-open]").click();
    await page.locator("#add-tab-archive").click();
    await page.locator("#add-archive-term").fill("QA menu");
    await page.locator("#add-panel-archive [role=option]").first().waitFor();
    await page.locator("#add-panel-archive [role=option]").first().click();
    const term = await page.locator("#add-panel-archive [data-hm-ss-value]").inputValue();
    ok(term !== "" && termID !== "", "popover save: the archive pick has a term");
    await page.locator('[data-hm-add-confirm="archive"]').click();
    await save(page);
    await reopen();
    const saved = await page.locator("[data-hm-item]").evaluateAll((ls, n) => ls.slice(n).map((l) => [l.dataset.type, l.querySelector('[name$=".label"]').value, l.querySelector('[name$=".entity"]')?.value ?? "", l.querySelector('[name$=".term"]')?.value ?? ""]), before);
    ok(saved.length === 3 && same(saved.slice(0, 2).map((r) => r.slice(0, 3)), wanted.map(([v, t]) => ["entity", t, v])), `popover save: two pages are saved as Page items with their page (${saved.map((r) => r.join("/")).join(", ")})`);
    ok(saved[2]?.[0] === "archive" && saved[2][3] === term, "popover save: the archive is saved as an Archive item with its term");
    const links = await page.evaluate((html) => [...new DOMParser().parseFromString(html, "text/html").querySelectorAll("footer a")].map((a) => a.textContent.trim()), await publicFooter());
    ok(wanted.every(([, t]) => links.includes(t)) && links.includes(TERM), `popover save: the public footer links the new items (${links.length} links)`);
  } finally {
    // take the three rows out again, so the rest of the run sees the seeded tree
    await reopen();
    for (let i = (await rowsOf(page)).length; i > before; i--) {
      await page.locator("[data-hm-item]").nth(i - 1).focus();
      await page.keyboard.press("Delete");
    }
    await save(page);
    await reopen();
    ok((await rowsOf(page)).length === before, "popover save: the added rows are removed again");
    if (termID) await post(`/admin/settings/taxonomies/terms/${termID}/delete`, []);
  }

  // ---- the preview shows the unsaved tree, with a none item as a span ----------
  await clickRow(page, LOOSE);
  await page.locator("#ins-label").fill("Press room");
  await page.waitForFunction(() => {
    const f = document.querySelector("iframe[data-hm-preview-frame]:not([hidden])");
    return f?.contentDocument?.body?.textContent.includes("Press room");
  });
  const shown = () => page.locator("iframe[data-hm-preview-frame]:not([hidden])");
  const frame = await (await shown().elementHandle()).contentFrame();
  ok(await frame.evaluate(() => { const s = [...document.querySelectorAll("footer span")].find((e) => e.textContent === "Press room"); return Boolean(s) && !s.closest("a"); }), "preview: the unsaved label shows in the theme's footer, the none item as a span");
  await page.locator("[data-hm-preview-scheme=dark]").click();
  ok((await frame.evaluate(() => document.documentElement.dataset.theme)) === "dark", "preview: Dark sets the scheme of the previewed page");
  await page.locator("[data-hm-preview-scheme=light]").click();
  await page.locator("[data-hm-preview-width=mobile]").click();
  ok((await shown().boundingBox()).width < 500, "preview: Mobile narrows the frame");
  await page.locator("[data-hm-preview-width=desktop]").click();
  // Preview refreshes replace the frame's history entry: Back leaves the page, and a bad render is recovered from.
  const settle = (text) => page.waitForFunction((t) => document.querySelector("iframe[data-hm-preview-frame]:not([hidden])")?.contentDocument?.body?.textContent.includes(t), text);
  const entries = await page.evaluate(() => history.length);
  for (const text of ["Press one", "Press two", "Press three"]) {
    await page.locator("#ins-label").fill(text);
    await settle(text);
  }
  ok((await page.evaluate(() => history.length)) === entries, "preview: refreshing adds no history entry, so Back leaves the page");
  const seen = errors.length;
  await page.route("**/theme/menus/preview", (r) => r.fulfill({ status: 500, contentType: "text/plain", body: "boom" }));
  await page.locator("#ins-label").fill("Press bad");
  await page.locator("#menu-preview").waitFor({ state: "hidden" });
  await page.unroute("**/theme/menus/preview");
  errors.splice(seen); // the browser logs the 500 it was given
  await page.locator("#ins-label").fill("Press room");
  await page.locator("#menu-preview").waitFor({ state: "visible" });
  await settle("Press room");
  ok(true, "preview: the card comes back after a bad render");
  const again = await (await shown().elementHandle()).contentFrame();
  await again.locator("footer a", { hasText: "Security" }).click();
  ok((await page.locator("[data-hm-item][data-hm-selected] [name$='.label']").inputValue()) === "Security", "preview: a click on a link selects its item and does not leave the editor");

  // axe accepts the tree pattern on the editor's rows.
  const scan = await newSession(b, { theme: "light", width: 1440, bypassCSP: true });
  await scan.setViewportSize({ width: 1440, height: 2400 }); // tall enough that the sticky save bar never covers a row
  await scan.goto(base + footerHref);
  await scan.waitForSelector("[data-hm-item][role=treeitem]");
  const rowsEnd = await scan.evaluate(() => ({ tree: document.querySelector("#menu-tree").getBoundingClientRect().bottom, bar: document.querySelector(".hm-savebar").getBoundingClientRect().top }));
  ok(rowsEnd.tree <= rowsEnd.bar, `tree: the last row ends above the sticky save bar at 2400px (tree ${Math.round(rowsEnd.tree)}, bar ${Math.round(rowsEnd.bar)}); raise the viewport height if the fixture grew`);
  await scan.addScriptTag({ content: fs.readFileSync(new URL("./vendor/axe.min.js", import.meta.url), "utf8") });
  const axeBad = await scan.evaluate(async () => (await axe.run("#menu-tree", { runOnly: { type: "tag", values: ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"] }, rules: { "color-contrast": { enabled: false } } })).violations.map((v) => `${v.id} x${v.nodes.length} ${v.nodes[0].target.join(" ")} ${v.nodes[0].html.slice(0, 80)}`));
  ok(axeBad.length === 0, `tree: axe finds no violation in the tree (${axeBad.join(", ")})`);

  // ---- the item limit: Add (button and keyboard) and Duplicate refuse in the server's words ----
  const count = (await rowsOf(page)).length;
  await page.evaluate((n) => { window.__stay = 1; document.querySelector("form[data-hm-editor]").dataset.maxItems = String(n); }, count);
  const refusal = `A menu holds at most ${count} items.`;
  const notice = () => page.locator("[data-hm-notice]").innerText().then((t) => t.replace(/\s+/g, " ").trim());
  await page.locator('button[name="_op"][value="add"]').evaluate((b) => b.click());
  ok((await notice()).includes(refusal) && (await rowsOf(page)).length === count && (await page.evaluate(() => window.__stay)) === 1, "limit: the Add button refuses at the limit, in the server's words");
  await page.locator("[data-hm-notice]").evaluate((n) => n.replaceChildren());
  await (await byLabel(page, "Docs")).focus();
  await page.keyboard.press("Alt+n");
  await page.locator("#menu-add:popover-open").waitFor();
  await page.locator("#add-tab-heading").click();
  await page.locator('[data-hm-add-label="heading"]').fill("Over the limit");
  await page.locator('[data-hm-add-confirm="heading"]').click();
  ok((await notice()).includes(refusal) && (await rowsOf(page)).length === count && (await page.evaluate(() => window.__stay)) === 1, "limit: Alt+N opens Add, which refuses at the limit");
  await page.keyboard.press("Escape");
  await page.keyboard.press("Control+d");
  ok((await notice()).includes(refusal) && (await rowsOf(page)).length === count, "limit: Duplicate refuses at the limit, in the server's words");
  await page.evaluate(() => { document.querySelector("form[data-hm-editor]").dataset.maxItems = "200"; });

  // ---- Enter in a field confirms it and does not save the menu ---------------------
  await page.evaluate(() => { window.__stay = 1; });
  await page.locator("[data-hm-filter]").fill("Sec");
  await page.keyboard.press("Enter");
  await clickRow(page, "Security");
  await page.locator("#ins-label").fill("Security notes");
  await page.keyboard.press("Enter");
  await page.waitForTimeout(400);
  ok((await page.evaluate(() => window.__stay)) === 1 && (await state(page)) === "unsaved" && (await rowsOf(page)).some((r) => r[1] === "Security notes"), "Enter in the filter or in a rename field does not save the menu");
  ok(await page.evaluate(() => document.activeElement?.matches("[data-hm-item]")), "Enter in the inspector label returns focus to the row");
  await page.locator("[data-hm-filter]").fill("");
  await page.locator("#ins-label").fill("Security");

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
  await plain.goto(base + footerHref);
  const seenJs = (sel) => plain.locator(sel).first().isVisible();
  ok((await seenJs(".hm-menutree__edit")) && (await seenJs("#add-type")) && (await seenJs('button[name="_op"][value="add"]')) && (await seenJs("#moveto-target")), "ISC-34: without JavaScript Edit, the Add item controls and Move to are visible");
  const jsOnly = await plain.$$eval("[data-hm-editor-only], [data-hm-chips], [data-hm-kind-slot], [data-hm-add-after], [data-hm-add-open], #menu-add", (els) => els.filter((e) => e.getBoundingClientRect().width > 0 && getComputedStyle(e).visibility !== "hidden").length);
  ok(jsOnly === 0, `ISC-34: without JavaScript none of the editor-only markup shows (${jsOnly} visible)`);
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
