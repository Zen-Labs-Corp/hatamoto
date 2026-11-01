// A5: does an in-canvas double-click text edit revert when the host takes focus (a click on the host page)?
// An earlier investigation saw it under CDP (Input.insertText): the text went back ~4 ms after a blur and no undo entry was made.
// This drives the edit three ways (page.keyboard.type = real key events, a CDP Input.dispatchKeyEvent text key,
// and CDP Input.insertText), on a heading in a container section and one in a plain div, clicks the host page
// (not the frame) and asserts: text kept, status not "Saved", exactly one undo entry (undo restores, redo re-applies).
// RESULT (local admin-qa, Chromium): the in-place revert on blur does NOT reproduce. A heading in a <section> container and in a
// <div> container keeps its text after a host click with all three input methods (page.keyboard.type: 3 `text` posts for 3 keys;
// dispatchKeyEvent: 3; Input.insertText: 1 post), the status says "Unsaved changes", exactly one undo entry exists, undo restores
// and redo re-applies. What was seen there is a THEME section (a component): its headline is a v1 data-hm-field that is never edited in
// place; the first keystroke puts the canvas back to the stored text (the "revert", no undo entry, no dirty state) and moves the
// focus to the inspector field. Real bug found there and fixed in screen.js toField: it dispatched "inspect", which focused the
// inspector's first editor (the Body), so the keys typed before focusField reached the headline were written into the Body.
// node scripts/admin-qa/builder-inline-blur.mjs   (needs QA_PAGE_NAME, default "About"; leaves the page as found)
// It SAVES: two QA sections are pasted into the page and Update publishes them (a published page goes live until the teardown).
// So it refuses any BASE that is not loopback unless QA_ALLOW_REMOTE_SAVE=1.
import { launch, newSession, base } from "./lib.mjs";

if (!["127.0.0.1", "localhost", "[::1]"].includes(new URL(base).hostname) && process.env.QA_ALLOW_REMOTE_SAVE !== "1") {
  console.error(`refusing to run against ${base}: this script saves and publishes. Use a local install, or set QA_ALLOW_REMOTE_SAVE=1 to accept that.`);
  process.exit(2);
}

const NAME = process.env.QA_PAGE_NAME || "About";
const b = await launch("chromium");
let bad = 0;
const p = await newSession(b, { theme: "light", width: 1440, bypassCSP: true });
await p.context().grantPermissions(["clipboard-read", "clipboard-write"], { origin: base });
const errors = [];
p.on("pageerror", (e) => errors.push(e.message));
p.on("dialog", (d) => d.accept().catch(() => {}));
// Count frame->host `text` messages (the canvas talks over a private MessagePort).
await p.addInitScript(() => {
  if (window !== window.top) return;
  const Native = window.MessageChannel;
  window.__qaPort = { text: 0 };
  window.MessageChannel = function () {
    const ch = new Native();
    ch.port1.addEventListener("message", (e) => { if (e.data && e.data.hm === 2 && e.data.type === "text") window.__qaPort.text++; });
    return ch;
  };
  window.MessageChannel.prototype = Native.prototype;
});
const TREE = '[data-hm-bs-tree] [role="treeitem"]';
const MENU = '.hm-menu[aria-label="Element actions"]';
const BAR = ".hm-pe__fmt";
const settle = (ms = 150) => p.waitForTimeout(ms);
async function frame() {
  for (const f of p.frames()) {
    if (f === p.mainFrame() || f.url() === "about:blank") continue;
    if (await (await f.frameElement()).isVisible()) return f;
  }
  throw new Error("no visible canvas frame");
}
const doc = () => p.evaluate(() => window.__hmBuilder.doc());
const flat = (ns) => ns.flatMap((n) => [n, ...flat(n.c ?? [])]);
const nodes = async () => flat((await doc()).nodes);
const byId = async (id) => (await nodes()).find((n) => n.id === id);
const byText = async (text) => (await nodes()).find((n) => n.p?.text === text);
const sig = async () => JSON.stringify((await doc()).nodes);
// The page is published, so edits stay in the browser until Update; this is the explicit save.
async function update() {
  await p.evaluate(() => document.activeElement?.blur?.());
  const resp = p.waitForResponse((r) => /\/builder\/(save|autosave)$/.test(new URL(r.url()).pathname) && r.request().method() === "POST", { timeout: 15000 });
  await p.locator('[data-hm-bs-save="save"]').first().click();
  const r = await resp;
  if (r.status() !== 200) throw new Error("save answered " + r.status());
  await settle(600);
}
const saved = () => p.waitForFunction(() => /\bSaved\b|\bDraft saved\b/.test(document.querySelector("[data-hm-bs-state]")?.textContent ?? ""), null, { timeout: 15000 }).catch(() => {});
async function check(name, fn) {
  if (process.env.QA_ONLY && !/^setup/.test(name) && !new RegExp(process.env.QA_ONLY, "i").test(name)) return;
  try { console.log(`ok   ${name}: ${await fn()}`); }
  catch (e) {
    bad++; console.log(`FAIL ${name}: ${String(e.message || e).split("\n").slice(0, process.env.QA_VERBOSE ? 30 : 1).join(" | ")}`);
    for (let i = 0; i < 2; i++) { await p.keyboard.press("Escape").catch(() => {}); await settle(300); } // leave no edit open for the next check
  }
}
const noErrors = (from) => { if (errors.length > from) throw new Error("page error: " + errors[from]); };

let pageHref = "";
async function open() {
  if (!pageHref) {
    await p.goto(`${base}/admin/entities?q=${encodeURIComponent(NAME)}`);
    const hrefs = await p.$$eval('a[href^="/admin/entities/"]', (as, t) => as.filter((a) => a.textContent.includes(t)).map((a) => a.getAttribute("href")), NAME);
    const h = hrefs.find((x) => /entities\/[0-9a-f-]{36}/.test(x));
    if (!h) throw new Error(`no page named ${NAME}`);
    pageHref = h + (h.includes("?") ? "&" : "?") + "qa=1";
  }
  await p.goto(base + pageHref, { waitUntil: "load" });
  await p.waitForSelector(TREE, { timeout: 10000 });
  await p.waitForFunction(() => !!window.__hmBuilder, null, { timeout: 5000 });
  await settle(600);
}
const boxOf = (sel) => p.evaluate((q) => [...document.querySelectorAll(q)].map((e) => { const r = e.getBoundingClientRect(); return [r.x, r.y, r.right, r.bottom].map(Math.round); }), sel);
async function elBox(id) {
  const f = await frame();
  const r = await f.evaluate(`(() => { const r = ${EL(id)}.getBoundingClientRect(); return { x: r.x, y: r.y, r: r.right, b: r.bottom }; })()`);
  const a = await toPage(f, r.x, r.y), z = await toPage(f, r.r, r.b);
  return [a.x, a.y, z.x, z.y].map(Math.round);
}
const row = (id) => p.locator(`${TREE}[data-id="${id}"]`);

// The element of a node that holds its text (the node itself for heading and text).
const EL = (id) => `(() => { const n = document.querySelector('[data-hm-node="${id}"]'); return n && (n.matches('[data-hm-prop]') ? n : n.querySelector('[data-hm-prop]')); })()`;
const elState = async (id) => (await frame()).evaluate(`(() => { const e = ${EL(id)}; return e && { html: e.innerHTML, editing: e.hasAttribute('contenteditable'), sel: String(getSelection()), focused: document.activeElement === e }; })()`);
// Page coordinates of the frame point (fx, fy), and of a word of a node's text: the frame is scaled to fit its pane.
async function toPage(f, x, y) {
  const box = await (await f.frameElement()).boundingBox();
  const w = await f.evaluate(() => innerWidth);
  const k = box.width / w;
  return { x: box.x + x * k, y: box.y + y * k };
}
async function nodePoint(id) {
  const f = await frame();
  const r = await f.evaluate(`(() => { const e = ${EL(id)}; if (!e) return null; e.scrollIntoView({ block: "center", behavior: "instant" }); const g = document.createRange(); g.selectNodeContents(e); const r = g.getBoundingClientRect(); return { x: r.x + r.width / 2, y: r.y + r.height / 2 }; })()`);
  return r && toPage(f, r.x, r.y);
}
async function wordSpan(id, word) {
  const f = await frame();
  const r = await f.evaluate(`((word) => { const e = ${EL(id)}; const w = document.createTreeWalker(e, 4); let n; while ((n = w.nextNode())) { const i = n.nodeValue.indexOf(word); if (i >= 0) { const a = document.createRange(); a.setStart(n, i); a.setEnd(n, i + word.length); const b = a.getBoundingClientRect(); return { l: b.left + 1, r: b.right - 1, y: b.top + b.height / 2 }; } } return null; })(${JSON.stringify(word)})`);
  if (!r) throw new Error(`no word ${word} in ${id}`);
  return { a: await toPage(f, r.l, r.y), z: await toPage(f, r.r, r.y) };
}
// Double-click the node (the edit starts), then wait for the frame to say so.
async function startEdit(id) {
  // The toolbar of the element selected before floats over its upper neighbour: the Page crumb clears the selection first.
  if (await p.locator("[data-hm-pe-toolbar]:not([hidden])").count()) await p.locator("[data-hm-pe-strip] .hm-pe__crumb").first().click().catch(() => {});
  for (let i = 0; i < 6; i++) {
    const at = await nodePoint(id);
    if (at) {
      await settle(200);
      await p.mouse.dblclick(at.x + 20, (await elBox(id))[1] + 18); // well inside the text: the boundary strip between sections covers the top edge
      for (let j = 0; j < 10; j++) { if ((await elState(id))?.editing) return; await settle(100); }
    }
    await settle(300);
  }
  throw new Error(`double-click never started the edit of ${id}: ${JSON.stringify(await nodePoint(id))} ${JSON.stringify(await elState(id))} sel ${await p.evaluate(() => window.__hmBuilder.selected())} gaps ${JSON.stringify(await boxOf('.hm-pe__gapline'))} box ${JSON.stringify(await elBox(id))} top ${await (async () => { const a = await nodePoint(id); return p.evaluate(([x, y]) => { const e = document.elementFromPoint(x, y); return e && e.tagName + '.' + e.className; }, [a.x, a.y]); })()}`);

}

const HEAD_A = "Blur probe in a section", HEAD_B = "Blur probe in a div";
let start = "";
const ids = {};
const hostClick = () => p.mouse.click(5, 5); // the page chrome (top-left of the host), never the frame
const status = () => p.locator("[data-hm-bs-state]").first().innerText().then((t) => t.replace(/\s+/g, " "));
const undoOn = () => p.locator("[data-hm-bs-undo]").first().isEnabled();
const redoOn = () => p.locator("[data-hm-bs-redo]").first().isEnabled();
const texts = () => p.evaluate(() => window.__qaPort.text);
const cdp = await p.context().newCDPSession(p);

await check("setup: paste a heading in a <section> container and one in a <div> container; save; reload (empty undo history)", async () => {
  await open();
  start = await sig();
  const env = { hm: "nodes", v: 2, nodes: [
    { id: "qa-sa", t: "container", p: { tag: "section" }, c: [{ id: "qa-ha", t: "heading", p: { text: HEAD_A, level: 2 } }, { id: "qa-xa", t: "text", p: { text: "Spacer" } }] },
    { id: "qa-db", t: "container", p: { tag: "div" }, c: [{ id: "qa-hb", t: "heading", p: { text: HEAD_B, level: 2 } }, { id: "qa-xb", t: "text", p: { text: "Spacer" } }] },
  ], site: base };
  await p.evaluate((t) => navigator.clipboard.writeText(t), JSON.stringify(env));
  await row((await doc()).nodes[0].id).click({ button: "right" });
  await p.waitForSelector(MENU, { timeout: 3000 });
  const resp = p.waitForResponse((r) => r.url().endsWith("/builder/paste") && r.request().method() === "POST", { timeout: 5000 });
  await p.locator(`${MENU} [role="menuitem"]`, { hasText: /^Paste after/ }).first().click();
  if ((await resp).status() !== 200) throw new Error("paste answered " + (await resp).status());
  await settle(1200);
  await update();
  await open();
  ids.a = (await byText(HEAD_A))?.id; ids.b = (await byText(HEAD_B))?.id;
  if (!ids.a || !ids.b) throw new Error("pasted headings missing after reload");
  const seen = new Set([ids.a, ids.b]);
  ids.c = (await nodes()).find((n) => n.t === "heading" && !seen.has(n.id) && !String(n.id).startsWith("qa-") && n.p?.text)?.id;
  if (await undoOn()) throw new Error("undo is enabled after a reload: the one-entry count would be wrong");
  return `section heading ${ids.a}, div heading ${ids.b}, existing heading ${ids.c}`;
});

// How the text is typed.
const METHODS = {
  "page.keyboard.type (real key events)": (s) => p.keyboard.type(s, { delay: 20 }),
  "CDP Input.dispatchKeyEvent (keyDown with text)": async (s) => {
    for (const ch of s) {
      await cdp.send("Input.dispatchKeyEvent", { type: "keyDown", text: ch, key: ch, unmodifiedText: ch });
      await cdp.send("Input.dispatchKeyEvent", { type: "keyUp", key: ch });
    }
  },
  "CDP Input.insertText": (s) => cdp.send("Input.insertText", { text: s }),
};
const TYPED = " QA";

for (const [which, hid] of [["heading in a <section> container", "a"], ["heading in a <div> container", "b"], ...(ids.c ? [["an existing page heading (theme section)", "c"]] : [])]) {
  for (const [method, type] of Object.entries(METHODS)) {
    await check(`${which}, ${method}: dblclick, type, click the host page: text kept, dirty, one undo entry, undo restores, redo re-applies`, async () => {
      const id = ids[hid];
      const from = errors.length;
      const original = (await elState(id) ?? (await startEditThenState(id))).html;
      const stored = (await byId(id)).p.text;
      await startEdit(id);
      await p.keyboard.press("End");
      const t0 = await texts();
      await type(TYPED);
      await settle(200);
      const typed = (await elState(id)).html;
      if (!typed.includes(TYPED.trim())) throw new Error("the typing did not reach the element: " + typed);
      const posts = (await texts()) - t0;
      const docBefore = (await byId(id)).p.text;
      await hostClick();
      await settle(1500); // the host re-renders the frame some 100 ms later; give a revert every chance
      const after = (await elState(id))?.html;
      const docAfter = (await byId(id)).p.text;
      const info = `text posts ${posts}; doc before blur "${docBefore}", after "${docAfter}"; canvas typed "${typed}", after "${after}"; status "${await status()}"`;
      if (!after?.includes(TYPED.trim())) throw new Error("REVERTED on the canvas: " + info);
      if (docAfter !== stored + TYPED) throw new Error("doc text not kept: " + info);
      if (/\bSaved\b|\bDraft saved\b/.test(await status()) && !/Not saved|Unsaved|Saving|Update/.test(await status())) throw new Error("status is not dirty: " + info);
      if (!(await undoOn())) throw new Error("no undo entry: " + info);
      await p.locator("[data-hm-bs-undo]").first().click();
      await settle(900);
      if ((await byId(id)).p.text !== stored) throw new Error("undo did not restore: " + (await byId(id)).p.text);
      if (await undoOn()) throw new Error("more than one undo entry (undo is still enabled after one undo)");
      if (!(await redoOn())) throw new Error("redo is not enabled");
      await p.locator("[data-hm-bs-redo]").first().click();
      await settle(900);
      if ((await byId(id)).p.text !== stored + TYPED) throw new Error("redo did not re-apply: " + (await byId(id)).p.text);
      if ((await elState(id))?.html !== original + "" && !(await elState(id)).html.includes(TYPED.trim())) throw new Error("canvas lost the text after redo");
      // put it back for the next run: undo, and the history is empty again
      await p.locator("[data-hm-bs-undo]").first().click();
      await settle(900);
      if ((await byId(id)).p.text !== stored) throw new Error("could not restore");
      if (errors.length > from) throw new Error("page error: " + errors[from]);
      return info;
    });
  }
}

// A theme section (a v1 section drawn as a component): its headline is a data-hm-field, not a v2 prop, and is not edited in
// place (fieldjump.js, Q7). The first keystroke puts the canvas back to the stored text and moves the focus to the inspector's
// headline field. Asserted: the focus ends on that field, and what was typed in the meantime never lands in ANOTHER field
// (the inspector's first editor, the Body, used to take the characters typed before the handoff) nor stays on the canvas.
for (const [method, type] of Object.entries(METHODS)) {
  await check(`theme section headline (component field), ${method}: nothing typed lands in another field; the focus ends on the headline field; canvas and doc agree`, async () => {
    const comp = (await nodes()).find((n) => n.t === "component" && typeof n.p?.content?.headline === "string" && n.p.content.headline);
    if (!comp) throw new Error("no component with a headline on the page");
    const before = JSON.stringify(comp.p.content);
    const f = await frame();
    const field = () => f.evaluate((id) => { const e = (document.querySelector(`[data-hm-node="${id}"]`) || document).querySelector('[data-hm-field="headline"]'); return e && e.textContent; }, comp.id);
    await p.locator(TREE).first().click();
    await settle(900);
    const at = await f.evaluate((id) => { const e = (document.querySelector(`[data-hm-node="${id}"]`) || document).querySelector('[data-hm-field="headline"]'); if (!e) return null; e.scrollIntoView({ block: "center", behavior: "instant" }); const r = e.getBoundingClientRect(); return { x: r.x + 20, y: r.y + r.height / 2 }; }, comp.id);
    if (!at) throw new Error("no headline field on the canvas for " + comp.id);
    const pt = await toPage(f, at.x, at.y);
    await p.mouse.dblclick(pt.x, pt.y);
    await settle(800);
    await type(TYPED);
    await settle(2500); // the inspector form is fetched after the panel draws
    const focus = await p.evaluate(() => document.activeElement?.getAttribute("name"));
    const now = (await byId(comp.id)).p.content;
    const info = `focus "${focus}"; canvas "${await field()}"; content now ${JSON.stringify(Object.fromEntries(Object.entries(now).map(([k, v]) => [k, String(v).slice(0, 24)])))}`;
    if (JSON.stringify(now) !== before) {
      await p.locator("[data-hm-bs-undo]").first().click().catch(() => {});
      throw new Error("typing on a theme headline changed the doc (another field?): " + info);
    }
    if (focus !== `n.${comp.id}.c.headline`) throw new Error("the focus did not end on the headline field: " + info);
    if ((await field()) !== comp.p.content.headline) throw new Error("the canvas shows text the doc does not hold: " + info);
    return info;
  });
}

async function startEditThenState(id) { await startEdit(id); await p.keyboard.press("Escape"); await settle(600); return elState(id); }

await check("the page is left as found", async () => {
  if ((await sig()) !== start) {
    const first = (await doc()).nodes.find((n) => n.id !== undefined && (n.c ?? []).some((c) => c.id === ids.a));
    await p.evaluate(() => document.activeElement?.blur?.());
    for (const id of [ids.a, ids.b]) {
      const parent = (await nodes()).find((n) => (n.c ?? []).some((c) => c.id === id));
      await row(parent.id).click({ button: "right" });
      await p.waitForSelector(MENU, { timeout: 3000 });
      await p.locator(`${MENU} [role="menuitem"]`, { hasText: /^Delete/ }).first().click();
      await settle(600);
    }
    if ((await sig()) !== start) { const a = JSON.parse(start), c = JSON.parse(await sig()); const at = JSON.stringify(a).split("").findIndex((ch, i) => ch !== JSON.stringify(c)[i]); throw new Error("deleting the pasted sections did not give back the page found: " + JSON.stringify(a).slice(Math.max(0, at - 60), at + 80) + " vs " + JSON.stringify(c).slice(Math.max(0, at - 60), at + 80)); }
    void first;
  }
  await update();
  await open();
  if ((await sig()) !== start) throw new Error("the reloaded page differs from the one found");
  return "restored, saved and reloaded";
});

await b.close();
if (bad) { console.log(`${bad} check(s) failed`); process.exit(1); }
console.log("builder-inline-blur: all checks passed");
