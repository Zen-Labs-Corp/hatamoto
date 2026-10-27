// Builder states (E2): keyboard-only State switch, hover-lift card, forced hover on the canvas
// at phone width, reduced motion, axe on the Style tab. Needs QA_PAGE_NAME = a page with a page_doc.
// node scripts/admin-qa/builder-states.mjs [chromium|firefox|webkit]   Exit 1 on any problem.
import fs from "node:fs";
import { launch, newSession, resolvePath, base } from "./lib.mjs";

const engine = ["chromium", "firefox", "webkit"].includes(process.argv[2]) ? process.argv[2] : "chromium";
const axe = fs.readFileSync(new URL("./vendor/axe.min.js", import.meta.url), "utf8");
const tags = ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"];
const b = await launch(engine);
let bad = 0;
let rows0 = 0; // tree rows before the check inserted its widget
async function check(name, fn) {
  try { console.log(`ok   ${name}: ${await fn()}`); }
  catch (e) { bad++; console.log(`FAIL ${name}: ${String(e.message || e).split("\n")[0]}`); }
  // Undo the inserted widget (and any edits after it) so the page is left as it was.
  await p.evaluate(() => document.activeElement?.blur?.());
  for (let i = 0; rows0 && i < 8 && (await p.locator(TREE).count()) > rows0; i++) { await p.keyboard.press("Control+z"); await settle(); }
}

const TREE = '[data-hm-bs-tree] [role="treeitem"]';
const STATE = '[role="radiogroup"][aria-label="State"]';
const p = await newSession(b, { theme: "light", width: 1440 });
p.on("dialog", (d) => d.accept().catch(() => {}));
const settle = (ms = 150) => p.waitForTimeout(ms);
const press = async (key, n = 1) => { for (let i = 0; i < n; i++) { await p.keyboard.press(key); await settle(60); } };
// The visible canvas frame (the builder keeps a second, hidden one for the next render, and which is which changes after a render).
const canvas = async () => { for (const f of p.frames()) if (f !== p.mainFrame() && f.url() !== "about:blank" && (await (await f.frameElement()).isVisible())) return f; return null; };
const forced = async () => (await canvas())?.evaluate(() => document.querySelector("[data-hm-force]")?.getAttribute("data-hm-force") ?? null);
const transformOf = async () => (await canvas()).evaluate(() => { const e = document.querySelector("[data-hm-force]"); return e ? getComputedStyle(e).transform : null; });

async function open() {
  const path = await resolvePath(p, "/admin/entities/{page}/edit");
  if (!path) throw new Error("no page seeded");
  await p.goto(base + path, { waitUntil: "load" });
  await p.waitForSelector(TREE, { timeout: 10000 });
  await settle(500);
}
// Style groups are disclosure buttons; Transform and Transition start closed.
async function openGroup(name) {
  const more = p.locator("details.hm-bs-more-opts").first(); // every group sits under "More style options"
  if (!(await more.evaluate((d) => d.open))) { await more.locator("summary").click(); await settle(100); }
  const head = p.locator("button.hm-bs-group__t", { hasText: new RegExp(`^${name}`) }).first();
  if ((await head.getAttribute("aria-expanded")) !== "true") { await head.click(); await settle(100); }
}
async function selectCardAndStyle() {
  await open();
  // The seeded About page is a v1 conversion: its tree rows are not canvas nodes. A widget from
  // the palette is, and it lands selected.
  rows0 = await p.locator(TREE).count();
  await p.locator(TREE).first().click(); await settle();
  await p.keyboard.press("/");
  await p.keyboard.type("divider"); await settle();
  await press("ArrowDown"); await press("Enter"); await settle(500);
  await p.locator('[data-hm-bs-ltab="nav"]').click(); // the Add tab hides the tree: back to the Navigator for the row clicks
  await p.locator('[data-hm-bs-panel] [role="tab"]', { hasText: "Style" }).click(); await settle();
}

await check("State switch by keyboard: Tab reaches one radio stop, ArrowRight selects Hover and the canvas forces it", async () => {
  await selectCardAndStyle();
  const stops = await p.locator(`${STATE} [role="radio"]`).evaluateAll((r) => r.filter((x) => x.tabIndex === 0).length);
  if (stops !== 1) throw new Error(`${stops} tab stops in the State group`);
  await p.locator('[data-hm-bs-panel] [role="tab"][aria-selected="true"]').focus();
  let at = "";
  for (let i = 0; i < 12 && !at; i++) {
    await press("Tab");
    at = await p.evaluate((g) => { const a = document.activeElement; return a?.closest(g) ? a.textContent + ":" + a.getAttribute("aria-checked") : ""; }, STATE);
  }
  if (at !== "Normal:true") throw new Error("Tab from the tab strip did not land on the checked radio: " + (at || "never reached"));
  await press("ArrowRight");
  const sel = await p.evaluate(() => document.activeElement.textContent + ":" + document.activeElement.getAttribute("aria-checked"));
  if (sel !== "Hover:true") throw new Error("after ArrowRight: " + sel);
  await settle(300);
  if ((await forced()) !== "hover") throw new Error("canvas has no data-hm-force=hover");
  return "Tab -> Normal, ArrowRight -> Hover, canvas forced";
});

await check("State switch: End and Home select the last and first radio and move the force", async () => {
  await selectCardAndStyle();
  const names = await p.locator(`${STATE} [role="radio"]`).allTextContents();
  const clipped = await p.locator(`${STATE} [role="radio"]`).evaluateAll((r) => r.filter((x) => x.scrollWidth > x.clientWidth).map((x) => x.textContent));
  if (clipped.length) throw new Error("State options are clipped: " + clipped.join(", "));
  await p.locator(`${STATE} [aria-checked="true"]`).focus();
  await press("End");
  const last = (await p.locator(`${STATE} [aria-checked="true"]`).textContent()).trim();
  if (last !== names[names.length - 1].trim()) throw new Error(`End selected ${last}, wanted ${names[names.length - 1]}`);
  await settle(300);
  if ((await forced()) === null) throw new Error("End did not force the last state");
  await press("Home");
  const first = (await p.locator(`${STATE} [aria-checked="true"]`).textContent()).trim();
  await settle(300);
  if (first !== "Normal" || (await forced()) !== null) throw new Error(`Home selected ${first}, force ${await forced()}`);
  return `End -> ${last}, Home -> Normal`;
});

await check("hover-lift card: ty -4px on Hover moves the forced element, Normal restores it", async () => {
  await selectCardAndStyle();
  await p.locator(`${STATE} [aria-checked="true"]`).focus();
  await press("ArrowRight");
  await settle(300);
  // The forced element comes from the canvas render that follows the key; a frame swap in between has none yet, so wait for it.
  let el = null;
  for (let i = 0; i < 20 && !el; i++) {
    el = await (await canvas())?.evaluate(() => [...(document.querySelector("[data-hm-force]")?.classList ?? [])].find((c) => c.startsWith("hm-n-")) ?? null).catch(() => null);
    if (!el) await settle(150);
  }
  if (!el) throw new Error("no forced element with an hm-n-* class");
  const transform = async () => (await canvas()).evaluate((c) => getComputedStyle(document.querySelector("." + c)).transform, el);
  const before = await transform();
  await openGroup("Effects");
  // "Move Y" is the number field alone (the unit select and slider carry their own names); the unit defaults to px.
  const ty = p.getByLabel("Move Y", { exact: true });
  if ((await ty.count()) !== 1) throw new Error(`"Move Y" matches ${await ty.count()} elements, wanted the number field alone`);
  await ty.fill("-4"); await ty.press("Enter"); await settle(400);
  const on = await transform();
  if (on === before || on === "none") throw new Error(`forced hover transform is ${on} (was ${before})`);
  await p.locator(`${STATE} [role="radio"]`, { hasText: "Normal" }).click(); await settle(300);
  if ((await forced()) !== null) throw new Error("force not cleared on Normal");
  const off = await transform();
  if (off !== before) throw new Error(`Normal transform ${off}, wanted ${before}`);
  await p.keyboard.press("Control+z"); await settle();
  return `hover transform ${on}; Normal ${off}`;
});

await check("forced hover is visible at phone width", async () => {
  await selectCardAndStyle();
  await p.locator(`${STATE} [aria-checked="true"]`).focus();
  await press("ArrowRight");
  await p.getByRole("button", { name: /phone/i }).first().click(); await settle(400);
  if ((await forced()) !== "hover") throw new Error("force lost on device switch");
  return "data-hm-force=hover kept on Phone";
});

await check("selection change and tab change clear the State and the force", async () => {
  await selectCardAndStyle();
  await p.locator(`${STATE} [aria-checked="true"]`).focus();
  await press("ArrowRight");
  await p.locator(TREE).nth(1).click(); await settle(300);
  let checked = await p.locator(`${STATE} [aria-checked="true"]`).textContent();
  if (checked.trim() !== "Normal" || (await forced()) !== null) throw new Error(`after selection: state ${checked}, force ${await forced()}`);
  await p.locator(`${STATE} [aria-checked="true"]`).focus();
  await press("ArrowRight");
  if ((await forced()) === null) throw new Error("Hover did not force the second element");
  await p.locator('[data-hm-bs-panel] [role="tab"]', { hasText: "Content" }).click(); await settle(300);
  if ((await forced()) !== null) throw new Error("force kept on the Content tab");
  await p.locator('[data-hm-bs-panel] [role="tab"]', { hasText: "Style" }).click(); await settle(300);
  checked = await p.locator(`${STATE} [aria-checked="true"]`).textContent();
  if (checked.trim() !== "Normal") throw new Error("State is " + checked + " after the tab round trip");
  return "Normal, no force";
});

await check("source chips: Set here, then Inherited from Desktop on Tablet; Reset returns to it", async () => {
  await selectCardAndStyle();
  const row = () => p.locator(".hm-bs-essentials .hm-bs-field").filter({ has: p.getByLabel("Corners", { exact: true }) });
  const chip = async () => ((await row().locator(".hm-bs-src").textContent()) ?? "").trim();
  const corners = p.getByLabel("Corners", { exact: true });
  await corners.fill("8"); await corners.press("Enter"); await settle(300);
  if ((await chip()) !== "Set here") throw new Error(`on Desktop the chip says "${await chip()}"`);
  await p.getByRole("button", { name: /tablet/i }).first().click(); await settle(400);
  if ((await chip()) !== "Inherited from Desktop") throw new Error(`on Tablet the chip says "${await chip()}"`);
  await corners.fill("12"); await corners.press("Enter"); await settle(300);
  if ((await chip()) !== "Set here") throw new Error(`after a Tablet value the chip says "${await chip()}"`);
  await row().getByRole("button", { name: "Reset Corners" }).click(); await settle(300);
  const back = await chip();
  if (back !== "Inherited from Desktop") throw new Error(`after Reset the chip says "${back}"`);
  await p.getByRole("button", { name: /^desktop/i }).first().click(); await settle(300);
  if ((await chip()) !== "Set here") throw new Error("Reset on Tablet cleared the Desktop value");
  return "Set here -> Inherited from Desktop -> Set here -> Reset -> Inherited from Desktop";
});

await check("Show on: a phone-only element takes two switches and one switch never moves another", async () => {
  await selectCardAndStyle();
  const sw = (name) => p.getByRole("switch", { name, exact: true });
  const state = async () => (await Promise.all(["Desktop", "Tablet", "Phone"].map(async (n) => ((await sw(n).isChecked()) ? "on" : "off")))).join(" ");
  if ((await state()) !== "on on on") throw new Error(`a new element shows on ${await state()}`);
  await sw("Desktop").click(); await settle(300);
  if ((await state()) !== "off on on") throw new Error(`Desktop off gave ${await state()}`);
  await sw("Tablet").click(); await settle(300);
  if ((await state()) !== "off off on") throw new Error(`Tablet off gave ${await state()}`);
  await sw("Desktop").click(); await settle(300);
  if ((await state()) !== "on off on") throw new Error(`Desktop on again gave ${await state()} (Tablet must stay off)`);
  const ringed = await p.locator('[role="switch"][aria-current="true"]').count();
  if (ringed !== 1) throw new Error(`${ringed} switches carry aria-current`);
  return "Desktop off, Tablet off -> Phone only; Desktop on keeps Tablet off";
});

await check("reduced motion: the transition runs without it and is dropped with it", async () => {
  await selectCardAndStyle();
  await openGroup("Transition");
  await p.getByLabel("Transition", { exact: true }).first().selectOption({ label: "Transform and shadow" }); await settle(500);
  const duration = async () => (await canvas()).evaluate(() => {
    return [...document.querySelectorAll("[data-hm-node]")].filter((x) => [...x.classList].some((c) => c.startsWith("hm-n-"))).map((x) => getComputedStyle(x).transitionDuration).find((d) => d !== "0s") ?? "0s";
  });
  try {
    await p.emulateMedia({ reducedMotion: "no-preference" }); await settle(200);
    const on = await duration();
    await p.emulateMedia({ reducedMotion: "reduce" }); await settle(200);
    const off = await duration();
    if (on !== "0.2s") throw new Error("without reduced motion the duration is " + on + ", wanted 0.2s");
    if (off !== "0s") throw new Error("with reduced motion the duration is " + off + ", wanted 0s");
    return `no-preference ${on}, reduce ${off}`;
  } finally {
    await p.emulateMedia({ reducedMotion: "no-preference" });
    await p.keyboard.press("Control+z"); await settle();
  }
});

await check("preset applied with the keyboard: focus stays on the preset select, not <body>", async () => {
  await selectCardAndStyle();
  const select = p.getByLabel("Class", { exact: true });
  // The seed has no preset: give the node a style and save it as one (it is applied), then change it with the keys alone.
  await openGroup("Effects");
  const ty = p.getByLabel("Move Y", { exact: true });
  await ty.fill("4"); await ty.press("Enter"); await settle(400);
  const name = `qa-kb-${Date.now().toString(36)}`;
  await p.getByRole("button", { name: "Save as class…", exact: true }).click();
  await p.getByLabel("Name for the new class", { exact: true }).fill(name);
  await p.getByRole("button", { name: "Save class", exact: true }).click();
  await p.waitForFunction((n) => document.querySelector('select[data-focus="select"]')?.value === n, name, { timeout: 5000 }).catch(async () => { throw new Error("saving the preset did not apply it: " + (await p.locator(".hm-bs-err[role=status]").allTextContents()).join("|")); });
  const focused = () => p.evaluate(() => ({ tag: document.activeElement.tagName, label: document.activeElement.labels?.[0]?.textContent ?? "" }));
  const steps = [];
  for (const [key, want] of [["ArrowUp", ""], ["ArrowDown", name]]) {
    await select.focus();
    await press(key);
    await settle(600); // the redrawn panel fills its Preset section once the lazy chunks are in
    if ((await select.inputValue()) !== want) throw new Error(`${key} left the preset "${await select.inputValue()}", wanted "${want}"`);
    const at = await focused();
    if (at.tag === "BODY" || at.label !== "Class") throw new Error(`after ${key}: focus is on ${at.tag} "${at.label}", wanted the Class select`);
    steps.push(key);
  }
  return `focus on the Class select after ${steps.join(" and ")}`;
});

await check("axe: Style tab with the State switch has no violations", async () => {
  await selectCardAndStyle();
  for (const g of ["Size", "Position", "Background", "Border", "Effects", "Transform", "Filter", "Transition"]) await openGroup(g); // every control kind on screen
  await p.evaluate(axe); // evaluate is not subject to the page CSP, addScriptTag is
  const v = await p.evaluate((tags) => axe.run(document.querySelector("[data-hm-bs-panel]") ?? document, { runOnly: { type: "tag", values: tags } }).then((r) => r.violations.map((x) => x.id + "×" + x.nodes.length)), tags);
  if (v.length) throw new Error(v.join(", "));
  return "0 violations";
});

// Light and dark (the hm_theme cookie the server reads; selectCardAndStyle reloads the page, so it takes effect).
for (const theme of ["light", "dark"]) {
  await check(`axe: the open colour popover and the box model have no violations (${theme})`, async () => {
    await p.context().addCookies([{ name: "hm_theme", value: theme, url: base }]);
    try {
      await selectCardAndStyle();
      if ((await p.locator("html").getAttribute("data-admin-theme")) !== theme) throw new Error(`the page is not in the ${theme} theme`);
      await p.locator('button.hm-bs-swatch[aria-label^="Text colour"]').click(); await settle(300);
      if (!(await p.locator(".hm-popover-panel:popover-open").count())) throw new Error("the swatch did not open its popover");
      const run = () => p.evaluate((tags) => axe.run(document.querySelector(".hm-popover-panel:popover-open") ?? document, { runOnly: { type: "tag", values: tags } }).then((r) => r.violations.map((x) => x.id + "×" + x.nodes.length)), tags);
      await p.evaluate(axe);
      const popover = await run();
      await p.keyboard.press("Escape"); await settle(200);
      const box = await p.evaluate((tags) => axe.run(document.querySelector(".hm-bs-box"), { runOnly: { type: "tag", values: tags } }).then((r) => r.violations.map((x) => x.id + "×" + x.nodes.length)), tags);
      if (popover.length || box.length) throw new Error(`popover: ${popover.join(", ") || "none"}; box model: ${box.join(", ") || "none"}`);
      return "popover 0, box model 0";
    } finally {
      await p.context().addCookies([{ name: "hm_theme", value: "light", url: base }]);
    }
  });
}

await b.close();
process.exit(bad ? 1 : 0);
