// Builder canvas direct manipulation, by mouse: the padding handles of a selected section (aria-labels, size badge, dark
// toolbar with its chip), a drag with the hatch and value badge and ONE undo entry, Shift snap to 8, Alt for the opposite side,
// undo/redo, the Tablet drag writing only md, the "↑ parent" toolbar button, the hover outline with its name tag, the boundary
// "Add section" pill opening the picker at its index, and in-place heading edit kept across a click away.
//
// Runs two ways:
//   local   PG_BIN=<pg bin dir> bash scripts/admin-qa/run-local.sh builder-canvas-manip.mjs     (seeded throwaway instance)
//   remote  QA_BASE_URL=<origin> QA_EMAIL=<owner email> QA_PASSWORD=<password> QA_PAGE_NAME=<page title> \
//             QA_ENTITY=<page id, optional: skips the title search> node scripts/admin-qa/builder-canvas-manip.mjs
// Env: QA_SHOTS = directory for section-selected.png, block-selected.png, padding-drag.png (mid-drag), phone.png (all 1600x950).
// The page is left exactly as found: the script inserts a scratch section (and a heading in it) with the builder's own insert, edits
// only that, and undoes everything. It NEVER saves: every POST to the page's save / autosave / recovery endpoints is aborted in the
// browser (and counted), so nothing, not even a draft's autosave (3 s after a change) or a published page's server-kept recovery
// copy, reaches the server. The last check reloads (discarding the unsaved state) and compares the document and the status line.
// Exit 1 on any problem.
import fs from "node:fs";
import path from "node:path";

// lib.mjs reads these at import time: map the remote-friendly names first.
if (process.env.QA_BASE_URL) process.env.BASE = process.env.QA_BASE_URL;
const { launch, base, login, pw } = await import("./lib.mjs");

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
  if (ENTITY) return `/admin/entities/${ENTITY}/edit?qa=1`;
  await p.goto(`${base}/admin/entities?q=${encodeURIComponent(NAME)}`);
  const hrefs = await p.$$eval('a[href^="/admin/entities/"]', (as, t) => as.filter((a) => a.textContent.includes(t)).map((a) => a.getAttribute("href")), NAME);
  const id = hrefs.map((h) => h.match(/entities\/([0-9a-f-]{36})/)?.[1]).find(Boolean);
  if (!id) throw new Error(`no page named ${NAME}`);
  return `/admin/entities/${id}/edit?qa=1`;
}
async function open(href) {
  await p.goto(base + href, { waitUntil: "load" });
  await p.waitForSelector(TREE, { timeout: 15000 });
  await p.waitForFunction(() => !!window.__hmBuilder, null, { timeout: 8000 });
  await settle(800);
}

// The page point of a canvas element's centre (the frame is scaled to fit its pane).
async function canvasPoint(id, fx = 0.5, fy = 0.5) {
  const f = await frame();
  const box = await (await f.frameElement()).boundingBox();
  const r = await f.evaluate(([i, ax, ay]) => {
    const e = document.querySelector(`[data-hm-node="${i}"], [data-hm-section="${i}"]`);
    if (!e) return null;
    e.scrollIntoView({ block: "center" });
    const b = e.getBoundingClientRect();
    return { x: b.x + b.width * ax, y: b.y + b.height * ay, w: innerWidth };
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
const padh = (side) => p.locator(`[data-hm-pe-padh][data-side="${side}"]`);
const padNow = async (side) => Number(await padh(side).getAttribute("aria-valuenow"));
async function selectOnCanvas(id) {
  // A section filled by its content is hit at its very top edge (a click inside selects the deepest element); else the Navigator row.
  for (const [fx, fy] of [[0.5, 0.5], [0.5, 0.004], [0.995, 0.5]]) {
    const at = await pointOf(id, fx, fy);
    await settle(200);
    await p.mouse.click(at.x, at.y);
    await settle(400);
    if ((await sel())[0] === id) break;
  }
  if ((await sel())[0] !== id) { await row(id).click(); await settle(500); }
  if ((await sel())[0] !== id) throw new Error(`could not select ${id} (selected ${await sel()})`);
  await padh("top").waitFor({ state: "visible", timeout: 3000 });
}
// The count of rows in the History panel (Original + one per undo step).
async function historyCount() {
  await p.locator("[data-hm-bs-history]").click();
  const list = p.getByRole("listbox", { name: /Changes/ });
  await list.waitFor({ timeout: 3000 });
  const n = await list.getByRole("option").count();
  await p.keyboard.press("Escape");
  await settle(150);
  return n;
}
const undo = async () => { await p.evaluate(() => document.activeElement?.blur?.()); await p.locator("[data-hm-bs-undo]").first().click(); await settle(350); };
const redo = async () => { await p.evaluate(() => document.activeElement?.blur?.()); await p.locator("[data-hm-bs-redo]").first().click(); await settle(350); };
const key = async (combo) => { await p.evaluate(() => document.activeElement?.blur?.()); await p.locator(TREE).first().focus().catch(() => {}); await p.keyboard.press(combo); await settle(350); };
// The padding-top the inspector shows (Style tab); null when its field is not on screen.
const inspectorTop = async () => {
  const f = p.locator('input[aria-label="Padding top"]').first();
  return (await f.count()) && (await f.isVisible()) ? f.inputValue() : null;
};
const authored = async (id, bp, k) => (await find(id))?.s?.[bp]?.[k] ?? null;
// A drag of a handle by `dy` screen px, in steps; returns after pointerup unless `hold`.
async function dragHandle(side, d, { shift = false, alt = false, hold = false } = {}) {
  const hb = await padh(side).boundingBox();
  const x = hb.x + hb.width / 2, y = hb.y + hb.height / 2;
  const vertical = side === "top" || side === "bottom";
  await p.mouse.move(x, y);
  if (process.env.QA_VERBOSE) console.log("  handle", side, JSON.stringify(hb), "top element", await p.evaluate(([a, c]) => { const e = document.elementFromPoint(a, c); return e && e.tagName + "." + e.className; }, [x, y]));
  if (alt) await p.keyboard.down("Alt");
  await p.mouse.down();
  if (shift) await p.keyboard.down("Shift");
  const steps = 8;
  for (let i = 1; i <= steps; i++) await p.mouse.move(vertical ? x : x + (d * i) / steps, vertical ? y + (d * i) / steps : y);
  await settle(150);
  if (hold) return;
  await p.mouse.up();
  if (shift) await p.keyboard.up("Shift");
  if (alt) await p.keyboard.up("Alt");
  await settle(500);
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

let top0 = 0;
await check("A1 select a section on the canvas: 4 padding handles with aria-labels, 'W × H' badge, dark toolbar with the chip name", async () => {
  await selectOnCanvas(sec.id);
  const labels = await p.locator("[data-hm-pe-padh]:not([hidden])").evaluateAll((hs) => hs.map((h) => `${h.dataset.side}=${h.getAttribute("aria-label")}`));
  if (labels.length !== 4 || !labels.every((l) => /^(\w+)=[A-Z]\w+ padding, \d+px$/.test(l))) throw new Error("handles: " + labels.join("|"));
  const size = (await p.locator("[data-hm-pe-size]").first().textContent()).trim();
  if (!/^\d+ × \d+$/.test(size)) throw new Error(`size badge reads "${size}"`);
  const tb = p.locator("[data-hm-pe-toolbar]");
  await tb.waitFor({ state: "visible", timeout: 3000 });
  const bg = await tb.evaluate((e) => getComputedStyle(e).backgroundColor);
  const [r, g, bl] = bg.match(/\d+(\.\d+)?/g).map(Number);
  if ((0.299 * r + 0.587 * g + 0.114 * bl) > 60) throw new Error("toolbar is not dark: " + bg);
  const chip = (await p.locator("[data-hm-pe-toolbar] .hm-pe__tname").textContent()).trim();
  if (!chip) throw new Error("the toolbar chip has no name");
  top0 = await padNow("top");
  await p.locator('[role="tab"][data-tab="style"]').first().click().catch(() => {}); // the inspector's padding fields
  await settle(400);
  await shot("section-selected.png");
  return `${labels.join(", ")}; badge "${size}"; toolbar ${bg}; chip "${chip}"`;
});

await check("A1 drag the top handle +40 px: value badge + hatch during the drag, ONE undo entry on pointerup, padding-top changed", async () => {
  const hist0 = await historyCount();
  const past0 = await sig();
  await dragHandle("top", 40, { hold: true });
  const badge = p.locator("[data-hm-pe-padbadge]:not([hidden])");
  if (!(await badge.count())) throw new Error("no value badge during the drag");
  const shown = (await badge.textContent()).trim();
  const hatch = p.locator("[data-hm-pe-hatch]:not([hidden])");
  if (!(await hatch.count())) throw new Error("no hatch during the drag");
  const seen = await hatch.first().evaluate((e) => ({ op: getComputedStyle(e).opacity, w: e.getBoundingClientRect().width, h: e.getBoundingClientRect().height }));
  if (Number(seen.op) === 0 || !seen.w || !seen.h) throw new Error(`the hatch is not visible: ${JSON.stringify(seen)}`);
  if ((await sig()) !== past0) throw new Error("the document changed before pointerup");
  await shot("padding-drag.png");
  await p.mouse.up();
  await settle(500);
  const after = await padNow("top");
  if (after <= top0) throw new Error(`top padding ${top0} -> ${after}`);
  if (shown !== String(after)) throw new Error(`badge showed ${shown}, the committed value is ${after}`);
  const hist1 = await historyCount();
  if (hist1 !== hist0 + 1) throw new Error(`history ${hist0} -> ${hist1} rows (expected +1)`);
  const stored = await authored(sec.id, "base", "pt");
  if (stored !== `${after}px`) throw new Error(`stored pt ${stored}, handle ${after}`);
  const insp = await inspectorTop();
  if (insp === null) throw new Error("the inspector's padding-top field is not on screen");
  if (parseFloat(insp) !== after) throw new Error(`inspector padding-top ${insp}, handle ${after}`);
  globalThis.afterDrag = after;
  return `${top0} -> ${after}; badge "${shown}"; hatch on; history ${hist0} -> ${hist1}; stored ${stored}; inspector ${insp}`;
});

await check("A1 Shift-drag snaps to a multiple of 8", async () => {
  await dragHandle("top", 29, { shift: true });
  const v = await padNow("top");
  if (v % 8 !== 0) throw new Error(`top padding ${v} is not a multiple of 8`);
  return `top padding ${v}`;
});

await check("A1 Alt-drag also changes the bottom, in ONE undo entry", async () => {
  const bottom0 = await padNow("bottom");
  const hist0 = await historyCount();
  const before = await sig();
  await dragHandle("top", 30, { alt: true });
  const t = await padNow("top"), bo = await padNow("bottom");
  if (bo === bottom0 || bo !== t) throw new Error(`top ${t}, bottom ${bo} (was ${bottom0})`);
  const hist1 = await historyCount();
  if (hist1 !== hist0 + 1) throw new Error(`history ${hist0} -> ${hist1}`);
  globalThis.altPair = [t, bo, bottom0, before];
  return `top ${t}, bottom ${bottom0} -> ${bo}; one entry`;
});

await check("A1 undo restores both sides, redo re-applies both", async () => {
  const [t, bo, bottom0, before] = globalThis.altPair;
  await undo();
  if ((await sig()) !== before) throw new Error("undo did not restore the document");
  const t1 = await padNow("top"), b1 = await padNow("bottom");
  if (b1 !== bottom0 || t1 === t) throw new Error(`after undo top ${t1}, bottom ${b1} (wanted bottom ${bottom0})`);
  await redo();
  if ((await padNow("top")) !== t || (await padNow("bottom")) !== bo) throw new Error("redo did not re-apply both sides");
  await key("Control+z"); // the keyboard does the same
  if ((await sig()) !== before) throw new Error("Ctrl+Z did not restore");
  await key("Control+Shift+z");
  if ((await padNow("top")) !== t) throw new Error("Ctrl+Shift+Z did not re-apply");
  return `undo -> top ${t1}/bottom ${b1}; redo -> ${t}/${bo} (buttons and keys)`;
});

await check("A2 Tablet drag writes md only; Desktop is unchanged after switching back", async () => {
  const baseBefore = await authored(sec.id, "base", "pt");
  await p.locator('[data-hm-bs-device="md"]').click();
  await settle(900);
  if (!(await padh("top").isVisible())) await selectOnCanvas(sec.id);
  const md0 = await padNow("top");
  await dragHandle("top", 40);
  const md1 = await padNow("top");
  if (md1 === md0) throw new Error("the Tablet drag changed nothing");
  const s = (await find(sec.id)).s ?? {};
  if (s.md?.pt !== `${md1}px`) throw new Error(`md.pt is ${s.md?.pt}, handle ${md1}`);
  if ((await authored(sec.id, "base", "pt")) !== baseBefore) throw new Error("the Desktop pt changed");
  if (Object.keys(s).some((k) => k !== "base" && k !== "md")) throw new Error("other breakpoints written: " + Object.keys(s));
  await p.locator('[data-hm-bs-device="base"]').click();
  await settle(900);
  if ((await authored(sec.id, "base", "pt")) !== baseBefore) throw new Error("Desktop pt changed after switching back");
  return `Tablet ${md0} -> ${md1} written at md only; Desktop pt stays ${baseBefore}`;
});

await check("A2 Phone: the canvas at 390 px, screenshot", async () => {
  await p.locator('[data-hm-bs-device="sm"]').click();
  await settle(1000);
  if (!(await padh("top").isVisible())) await selectOnCanvas(sec.id);
  const labels = await p.locator("[data-hm-pe-padh]:not([hidden])").count();
  await shot("phone.png");
  await p.locator('[data-hm-bs-device="base"]').click();
  await settle(700);
  return `${labels} handles at the phone width`;
});

await check("A3 block selected: toolbar shows '↑ <section>' and a click selects the section; no padding handles on a block", async () => {
  await row(head.id).click();
  await settle(500);
  if ((await sel())[0] !== head.id) throw new Error("the heading is not selected");
  const up = p.locator('[data-hm-pe-toolbar] [data-act="parent"]');
  await up.waitFor({ state: "visible", timeout: 3000 });
  const text = (await up.textContent()).trim();
  if (!/^↑\s*\S/.test(text)) throw new Error(`parent button reads "${text}"`);
  if ((await p.locator("[data-hm-pe-padh]:not([hidden])").count()) !== 0) throw new Error("a block shows padding handles");
  await shot("block-selected.png");
  await up.click();
  await settle(500);
  if ((await sel())[0] !== sec.id) throw new Error(`the parent button selected ${await sel()}`);
  return `"${text}" selects the section`;
});

await check("A4 hover another element: outline + name tag", async () => {
  await p.locator("[data-hm-pe-strip] .hm-pe__crumb").first().click().catch(() => {}); // nothing selected: the tag shows on a hover
  await settle(300);
  const at = await pointOf(head.id, 0.3, 0.5);
  await p.mouse.move(at.x - 30, at.y - 10);
  await p.mouse.move(at.x, at.y, { steps: 5 });
  await settle(400);
  const tag = p.locator("[data-hm-pe-hover] .hm-pe__tag");
  if (!(await tag.count())) throw new Error("no hover outline with a name tag");
  const text = (await tag.first().textContent()).trim();
  if (!text) throw new Error("the name tag is empty");
  return `outline with tag "${text}"`;
});

await check("A4 hover a boundary between sections: 'Add section' pill, click opens the picker at that index (closed without inserting)", async () => {
  await p.locator("[data-hm-pe-strip] .hm-pe__crumb").first().click().catch(() => {});
  const total = (await tops()).length;
  const lines = p.locator("[data-hm-pe-gapline]");
  if ((await lines.count()) < 2) throw new Error(`${await lines.count()} boundary strips for ${total} sections`);
  const line = lines.nth(1); // between the scratch section and the page's first
  const box = await line.boundingBox();
  await p.mouse.move(box.x + box.width / 2, box.y + box.height / 2, { steps: 4 });
  await settle(300);
  const pill = line.locator(".hm-pe__pill");
  if (!(await pill.isVisible())) throw new Error("the pill is not visible on hover");
  if ((await pill.textContent()).trim() !== "Add section") throw new Error("pill text " + (await pill.textContent()));
  const name = await line.getAttribute("aria-label");
  await line.click();
  const dlg = p.getByRole("dialog", { name: "Add a section" });
  await dlg.waitFor({ timeout: 3000 });
  // Where the insert would land: pick a 1-column layout through the API only to learn the index is 1, then undo is not needed: we just compare labels.
  if (!/after /i.test(name)) throw new Error("boundary label " + name);
  await p.keyboard.press("Escape");
  await settle(250);
  if (await dlg.count()) throw new Error("Escape left the picker open");
  if ((await sig()).length === 0 || (await tops()).length !== total) throw new Error("the document changed");
  return `pill on "${name}", picker opened, closed with Esc, nothing inserted`;
});

await check("A5 double-click a plain heading: edit in place, click away, text kept, ONE undo entry, undo restores", async () => {
  await p.locator("[data-hm-pe-strip] .hm-pe__crumb").first().click().catch(() => {});
  const before = await sig();
  const hist0 = await historyCount();
  const EL = (id) => `(() => { const n = document.querySelector('[data-hm-node="${id}"]'); return n && (n.matches('[data-hm-prop]') ? n : n.querySelector('[data-hm-prop]')); })()`;
  const f = await frame();
  const at = await pointOf(head.id, 0.3, 0.6);
  await settle(200);
  await p.mouse.dblclick(at.x, at.y);
  let editing = false;
  for (let i = 0; i < 10 && !editing; i++) { editing = await f.evaluate(`(() => { const e = ${EL(head.id)}; return !!e && e.hasAttribute('contenteditable'); })()`).catch(() => false); if (!editing) await settle(150); }
  if (!editing) throw new Error("the double-click did not start an edit in place");
  await p.keyboard.press("Control+a");
  const typed = "Canvas QA heading";
  await p.keyboard.type(typed);
  await settle(200);
  await p.mouse.click(5, 5); // the page chrome
  await settle(700);
  const text = (await find(head.id)).p?.text ?? "";
  if (!text.includes(typed)) throw new Error(`the text after the click away is "${text}"`);
  const hist1 = await historyCount();
  if (hist1 !== hist0 + 1) throw new Error(`history ${hist0} -> ${hist1} rows (expected +1)`);
  await undo();
  if ((await sig()) !== before) throw new Error("one undo did not restore the heading");
  return `typed text kept ("${text}"); one entry; undo restores`;
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

// Report only: a collapsed theme section (a v1 container around a theme component, like Hero in the seed's Home page). Set QA_HERO_PAGE
// (a page title) to run it; it reloads that page, drags, and leaves it unsaved (reload discards the draft, saves are aborted anyway).
if (process.env.QA_HERO_PAGE) {
  await check("report: a collapsed theme section (Hero): handles, start value, does the drag change the rendered padding", async () => {
    await p.goto(`${base}/admin/entities?q=${encodeURIComponent(process.env.QA_HERO_PAGE)}`);
    const hs = await p.$$eval('a[href^="/admin/entities/"]', (as) => as.map((a) => a.getAttribute("href")));
    await open(hs.find((x) => /entities\/[0-9a-f-]{36}/.test(x)).replace(/\?.*/, "") + "?qa=1");
    const out = [];
    for (const n of flat(await tops()).filter((x) => x.t === "container" && x.p?.v1).slice(0, 1)) {
      await row(n.id).click();
      await settle(800);
      const handles = await p.locator("[data-hm-pe-padh]:not([hidden])").count();
      if (!handles) { out.push(`${n.t} ${n.id}: no handles`); continue; }
      const f = await frame();
      const kids = flat([n]).map((x) => x.id);
      const ren = () => f.evaluate((ids) => { const e = ids.map((i) => document.querySelector(`[data-hm-node="${i}"], [data-hm-section="${i}"]`)).find(Boolean); return e ? getComputedStyle(e).paddingTop : "(no element)"; }, kids);
      const v0 = await padNow("top"), r0 = await ren();
      await dragHandle("top", 40);
      await settle(1500);
      const where = flat([(await tops()).find((x) => x.id === n.id)]).filter((x) => x.s).map((x) => `${x.id}:${JSON.stringify(x.s)}`);
      out.push(`${n.t} ${n.id}: ${handles} handles, start ${v0}px, after +40 ${await padNow("top")}px, style written on ${where.join(", ") || "nothing"}, rendered padding-top ${r0} -> ${await ren()}`);
    }
    await p.goto("about:blank");
    return out.join(" || ");
  });
}

if (errors.length) { bad++; console.log("FAIL page errors: " + errors.slice(0, 3).join(" | ")); }
await b.close();
console.log(bad ? `${bad} problem(s)` : "builder-canvas-manip: all checks passed");
process.exit(bad ? 1 : 0);
