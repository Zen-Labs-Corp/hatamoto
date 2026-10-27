// Inline rich text on the canvas (E5): bold and link on a heading and a text node by mouse and by keyboard, the
// saved and rendered markup (only allowed tags, rel on target=_blank, javascript: dropped), rich paste, Esc,
// Shift+Enter, a component field click (Q7), the keystroke cost (Chromium) against a bare contenteditable, no
// patch message while typing, the inspector editor by keyboard, and axe with the bar and link dialog open.
// Needs QA_PAGE_NAME = a page with a page_doc (default "About"). The page is left as found: the last check undoes the edits.
// node scripts/admin-qa/builder-inline.mjs [chromium]   Exit 1 on any problem. Latency is Chromium only.
import fs from "node:fs";
import { launch, newSession, base } from "./lib.mjs";

const axe = fs.readFileSync(new URL("./vendor/axe.min.js", import.meta.url), "utf8");
const tags = ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"];
const NAME = process.env.QA_PAGE_NAME || "About";
const ALLOWED = new Set(["strong", "em", "b", "i", "u", "s", "code", "br", "a"]);
const P95_MS = 2;
const b = await launch("chromium");
let bad = 0;
const p = await newSession(b, { theme: "light", width: 1440, bypassCSP: true });
await p.context().grantPermissions(["clipboard-read", "clipboard-write"], { origin: base });
const errors = [];
p.on("pageerror", (e) => { errors.push(e.message); if (process.env.QA_VERBOSE) console.log("  pageerror", e.message); });
p.on("dialog", (d) => d.accept().catch(() => {}));
// The canvas talks to the host over a private MessagePort, so window "message" events see nothing. Wrap
// MessageChannel before host.js runs: each port1 gets a listener for frame->host messages and a postMessage
// wrapper for host->frame ones. Counts land in window.__qaPort; QA_VERBOSE also logs them.
await p.addInitScript((verbose) => {
  if (window !== window.top) return;
  const Native = window.MessageChannel;
  window.__qaPort = { text: 0, patch: 0 };
  window.MessageChannel = function () {
    const ch = new Native(), p1 = ch.port1, send = p1.postMessage;
    p1.addEventListener("message", (e) => {
      const d = e.data;
      if (d && d.hm === 2 && d.type === "text") window.__qaPort.text++;
      if (verbose && d && typeof d === "object" && d.type !== "layout" && d.type !== "hover") console.log("MSG", JSON.stringify(d).slice(0, 160));
    });
    p1.postMessage = function (d, ...r) {
      if (d && d.hm === 2 && d.type === "patch") window.__qaPort.patch++;
      if (verbose) console.log("GOT", JSON.stringify(d).slice(0, 160));
      return send.call(this, d, ...r);
    };
    return ch;
  };
  window.MessageChannel.prototype = Native.prototype;
}, !!process.env.QA_VERBOSE);

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
const saved = () => p.waitForFunction(() => /Saved/.test(document.querySelector("[data-hm-bs-state]")?.textContent ?? ""), null, { timeout: 15000 }).catch(() => {});
async function check(name, fn) {
  if (process.env.QA_ONLY && !/^setup/.test(name) && !new RegExp(process.env.QA_ONLY, "i").test(name)) return;
  try { console.log(`ok   ${name}: ${await fn()}`); }
  catch (e) {
    bad++; console.log(`FAIL ${name}: ${String(e.message || e).split("\n").slice(0, process.env.QA_VERBOSE ? 30 : 1).join(" | ")}`);
    for (let i = 0; i < 2; i++) { await p.keyboard.press("Escape").catch(() => {}); await settle(300); } // leave no edit open for the next check
  }
}
const noErrors = (from) => { if (errors.length > from) throw new Error("page error: " + errors[from]); };
const tagsIn = (html) => [...html.matchAll(/<\/?([a-z][a-z0-9]*)/gi)].map((m) => m[1].toLowerCase());
const onlyAllowed = (html, what) => {
  const bad = tagsIn(html).filter((t) => !ALLOWED.has(t));
  if (bad.length) throw new Error(`${what} has tags ${[...new Set(bad)].join(",")}: ${html.slice(0, 120)}`);
  if (/ on[a-z]+=|javascript:|style=|<script/i.test(html)) throw new Error(`${what} has unsafe markup: ${html.slice(0, 120)}`);
};

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
      await p.mouse.dblclick(at.x, at.y);
      for (let j = 0; j < 10; j++) { if ((await elState(id))?.editing) return; await settle(100); }
    }
    await settle(300);
  }
  throw new Error(`double-click never started the edit of ${id}`);
}
async function dragSelect(id, word) {
  await p.keyboard.press("Home"); // the double-click selected a word: pressing inside a selection starts a text drag, not a new selection
  await settle(100);
  const { a, z } = await wordSpan(id, word);
  await p.mouse.move(a.x, a.y); await p.mouse.down(); await p.mouse.move(z.x, z.y, { steps: 6 }); await p.mouse.up();
  await settle(150);
  if ((await elState(id)).sel !== word) throw new Error(`drag selected "${(await elState(id)).sel}", wanted "${word}"`);
}
const barButton = (cmd) => p.locator(`${BAR} button[data-cmd="${cmd}"]`);
const barVisible = () => p.locator(BAR).first().isVisible().catch(() => false);
async function waitBar() {
  await p.locator(BAR).first().waitFor({ state: "visible", timeout: 4000 }).catch(() => { throw new Error("the formatting bar never appeared"); });
}
async function endEdit(id) {
  await p.keyboard.press("Enter"); // Enter ends the edit of a heading and a text
  for (let j = 0; j < 20; j++) { if (!(await elState(id))?.editing) break; await settle(100); }
  await settle(500); // the canvas re-renders the node from the server
}
async function applyLink(href, newTab) {
  await barButton("link").click();
  const input = p.locator("#hm-fmt-href");
  await input.waitFor({ state: "visible", timeout: 2000 });
  await input.fill(href);
  if (newTab) await p.locator("#hm-fmt-tab").check();
  await p.keyboard.press("Enter");
}
const linkOf = (html) => html.match(/<a\b[^>]*>/i)?.[0] ?? "";

if (process.env.QA_VERBOSE) p.on("console", (m) => /^(MSG|GOT)/.test(m.text()) && console.log("  ", m.text()));
const spy = async () => {
  await p.evaluate(() => { for (const t of ["pointerdown", "mousedown", "focusin", "click"]) document.addEventListener(t, (e) => console.log("MSG par", t, e.target.nodeName, e.target.className, e.defaultPrevented, document.activeElement.nodeName), false); });
  await (await frame()).evaluate(() => { for (const t of ["focusout", "input", "beforeinput", "blur", "focus"]) window.addEventListener(t, (e) => console.log("GOT ev", t, e.target.nodeName, document.hasFocus(), String(getSelection()), document.activeElement.nodeName), true); });
};
const HEAD = "Rich heading words", TEXT = "Plain paragraph words here";
let start = "", headId = "", textId = "";

await check("setup: paste a 60-node section (a heading, a text and 58 more text nodes) onto the page", async () => {
  await open();
  start = await sig();
  const kids = [{ id: "qa-h", t: "heading", p: { text: HEAD, level: 2 } }, { id: "qa-t", t: "text", p: { text: TEXT } }, { id: "qa-b", t: "button", p: { text: "Press here", href: "/x" } }];
  for (let i = 0; i < 58; i++) kids.push({ id: `qa-n${i}`, t: "text", p: { text: `Filler paragraph number ${i} with a few words in it` } });
  const env = { hm: "nodes", v: 2, nodes: [{ id: "qa-c", t: "container", p: { tag: "section" }, c: kids }], site: base };
  await p.evaluate((t) => navigator.clipboard.writeText(t), JSON.stringify(env));
  const first = (await doc()).nodes[0];
  await row(first.id).click({ button: "right" });
  await p.waitForSelector(MENU, { timeout: 3000 });
  const resp = p.waitForResponse((r) => r.url().endsWith("/builder/paste") && r.request().method() === "POST", { timeout: 5000 });
  await p.locator(`${MENU} [role="menuitem"]`, { hasText: /^Paste after/ }).first().click();
  if ((await resp).status() !== 200) throw new Error("paste endpoint answered " + (await resp).status());
  await settle(1200);
  headId = (await byText(HEAD))?.id; textId = (await byText(TEXT))?.id;
  if (!headId || !textId) throw new Error("the pasted heading and text are not in the doc");
  const n = (await nodes()).filter((x) => x.t === "heading" || x.t === "text").length;
  if (n < 60) throw new Error(`only ${n} text nodes on the page`);
  return `${n} heading/text nodes; heading ${headId}, text ${textId}`;
});

await check("Q7: a click on a component's body selects it and focuses its inspector editor; typing in its headline puts the stored text back and focuses the field; no page error", async () => {
  const from = errors.length;
  const comp = (await nodes()).find((n) => n.t === "component");
  if (!comp) throw new Error("no component on the page");
  const click = async (field) => {
    await p.locator(TREE).first().click(); // the selection moves away first
    await settle(900); // the frame scrolls to it: measure only when it has stopped
    const f = await frame();
    const at = await f.evaluate((fld) => { const e = document.querySelector(`[data-hm-field="${fld}"]`); if (!e) return null; e.scrollIntoView({ block: "center", behavior: "instant" }); const r = e.getBoundingClientRect(); return { x: r.x + 20, y: r.y + r.height / 2 }; }, field);
    if (!at) throw new Error(`no data-hm-field=${field} on the canvas`);
    const pt = await toPage(f, at.x, at.y);
    await p.mouse.click(pt.x, pt.y);
    return f;
  };
  const inInspector = () => p.evaluate(() => { const a = document.activeElement; return !!a?.closest(".hm-bs-panel") && (a.getAttribute("name") || a.className); });
  await click("body");
  await settle(1500);
  if ((await p.evaluate(() => window.__hmBuilder.selected())) !== comp.id) throw new Error("clicking the body selected " + (await p.evaluate(() => window.__hmBuilder.selected())) + ", wanted " + comp.id);
  const rich = await inInspector();
  if (!rich) throw new Error("after clicking the body the focus is not in the inspector");
  const f = await click("headline");
  await settle(600);
  if ((await p.evaluate(() => window.__hmBuilder.selected())) !== comp.id) throw new Error("clicking the headline did not select the component");
  await p.keyboard.type("Q");
  await settle(1800);
  const plain = await inInspector();
  if (!plain) throw new Error("after typing in the headline the focus is not in the inspector");
  const shown = await (await frame()).evaluate(() => document.querySelector('[data-hm-field="headline"]')?.textContent);
  if (shown !== comp.p.content.headline) throw new Error(`the canvas shows "${shown}", the stored headline is "${comp.p.content.headline}"`);
  if (/fell behind/.test(await p.evaluate(() => document.body.innerText))) throw new Error("the preview reported it fell behind (the render of the stored text failed)");
  noErrors(from);
  return `body -> ${rich}; headline -> ${plain}, canvas back to the stored text`;
});

async function boldAndLink(id, word, how) {
  const from = errors.length;
  if (process.env.QA_VERBOSE) await spy();
  await startEdit(id);
  await waitBar();
  if (how === "mouse") {
    await dragSelect(id, word);
    await settle(250);
    await barButton("bold").click();
  } else {
    await p.keyboard.press("End");
    await p.keyboard.press("Control+Shift+ArrowLeft");
    await settle(100);
    if ((await elState(id)).sel.trim() !== word) throw new Error(`keyboard selected "${(await elState(id)).sel}", wanted "${word}"`);
    await p.keyboard.press("Control+b");
  }
  await settle(200);
  let s = await elState(id);
  if (!new RegExp(`<strong>\\s*${word}\\s*</strong>`).test(s.html)) throw new Error(`bold did not wrap ${word}: ${s.html}`);
  if (!s.editing || !s.focused) throw new Error("bold ended the edit or took focus from the frame");
  if ((await barButton("bold").getAttribute("aria-pressed")) !== "true") throw new Error("bar does not show bold as pressed");
  if (how === "mouse") await applyLink("https://example.com/a?x=1&y=2", true);
  else { await p.keyboard.press("Control+k"); await p.locator("#hm-fmt-href").waitFor({ state: "visible", timeout: 2000 }); await p.locator("#hm-fmt-href").fill("/docs/page"); await p.keyboard.press("Enter"); }
  await settle(250);
  s = await elState(id);
  const a = linkOf(s.html);
  if (!/href="/.test(a)) throw new Error("no link in " + s.html);
  if (how === "mouse" && !(/target="_blank"/.test(a) && /rel="noopener noreferrer"/.test(a))) throw new Error("new-tab link has no rel=noopener noreferrer: " + a);
  if (!s.editing || !s.focused) throw new Error("the link dialog left the edit or the frame focus");
  await endEdit(id);
  noErrors(from);
  return (await elState(id)).html;
}

await check("heading, by mouse: select a word, bar B, Link dialog with a new tab; Enter ends; canvas shows the server output", async () => {
  const html = await boldAndLink(headId, "heading", "mouse");
  const a = linkOf(html);
  if (!/target="_blank"/.test(a) || !/rel="[^"]*noopener[^"]*"/.test(a)) throw new Error("server render lost target/rel: " + html);
  onlyAllowed(html, "the heading on the canvas");
  return html;
});

await check("javascript: in the link dialog is refused with a message and nothing is applied", async () => {
  await startEdit(headId);
  await waitBar();
  await dragSelect(headId, "Rich");
  await barButton("link").click();
  await p.locator("#hm-fmt-href").fill("javascript:alert(1)");
  await p.keyboard.press("Enter");
  await settle(200);
  const msg = await p.locator(`${BAR} .hm-field__error`).textContent();
  if (!msg?.trim()) throw new Error("no error shown for javascript:");
  const html = (await elState(headId)).html;
  if (/javascript:/i.test(html) || /<a [^>]*>Rich/.test(html)) throw new Error("a link was applied: " + html);
  await p.keyboard.press("Escape"); // closes the dialog, back to the frame
  await settle(200);
  await p.keyboard.press("Escape"); // Esc in the frame restores and ends
  await settle(600);
  return `refused: ${msg.trim()}`;
});

await check("text node, by mouse: bold and a plain link", async () => {
  const html = await boldAndLink(textId, "words", "mouse"); // not the first word: the move handle sits over the node's left edge
  onlyAllowed(html, "the text on the canvas");
  return html;
});

await check("heading, by keyboard: Ctrl+Shift+Left selects the last word, Ctrl+B, Ctrl+K opens the dialog, Enter applies", async () => {
  const from = errors.length;
  await startEdit(headId);
  await waitBar();
  await p.keyboard.press("End");
  await p.keyboard.press("Control+Shift+ArrowLeft");
  const sel = (await elState(headId)).sel.trim();
  await p.keyboard.press("Control+b");
  await settle(200);
  let s = await elState(headId);
  if (!new RegExp(`<strong>\\s*${sel}\\s*</strong>`).test(s.html)) throw new Error(`Ctrl+B did not wrap "${sel}": ${s.html}`);
  await p.keyboard.press("Control+k");
  await p.locator("#hm-fmt-href").waitFor({ state: "visible", timeout: 2000 });
  if (!(await p.evaluate(() => document.activeElement?.id === "hm-fmt-href"))) throw new Error("the link field has no focus after Ctrl+K");
  await p.keyboard.type("https://example.org/k");
  await p.keyboard.press("Enter");
  await settle(250);
  s = await elState(headId);
  if (!/href="https:\/\/example.org\/k"/.test(s.html)) throw new Error("no link after Enter: " + s.html);
  if (!s.editing) throw new Error("applying the link ended the edit");
  await endEdit(headId);
  noErrors(from);
  return (await elState(headId)).html;
});

await check("text node, by keyboard: Ctrl+B and Ctrl+K on the last word", async () => {
  const from = errors.length;
  await startEdit(textId);
  await waitBar();
  await p.keyboard.press("End");
  await p.keyboard.press("Control+Shift+ArrowLeft");
  const sel = (await elState(textId)).sel.trim();
  await p.keyboard.press("Control+b");
  await p.keyboard.press("Control+k");
  await p.locator("#hm-fmt-href").waitFor({ state: "visible", timeout: 2000 });
  await p.keyboard.type("mailto:qa@example.com");
  await p.keyboard.press("Enter");
  await settle(250);
  const html = (await elState(textId)).html;
  if (!new RegExp(`<strong>\\s*${sel}\\s*</strong>`).test(html) || !/href="mailto:qa@example.com"/.test(html)) throw new Error("bold or link missing: " + html);
  await endEdit(textId);
  noErrors(from);
  return html;
});

// Page-space box of a node's text element, and of a page element.
async function elBox(id) {
  const f = await frame();
  const r = await f.evaluate(`(() => { const r = ${EL(id)}.getBoundingClientRect(); return { x: r.x, y: r.y, r: r.right, b: r.bottom }; })()`);
  const a = await toPage(f, r.x, r.y), z = await toPage(f, r.r, r.b);
  return { x: a.x, y: a.y, r: z.x, b: z.y };
}
const boxOf = (sel) => p.evaluate((q) => [...document.querySelectorAll(q)].filter((e) => e.getClientRects().length).map((e) => { const r = e.getBoundingClientRect(); return { x: r.x, y: r.y, r: r.right, b: r.bottom }; }), sel);
const overlap = (a, c) => a.x < c.r - 1 && c.x < a.r - 1 && a.y < c.b - 1 && c.y < a.b - 1;

await check("the bar is ready before any edit and shows with the first selection (double-click, Shift+arrows), no formatting key needed", async () => {
  const fresh = await p.context().newPage(); // a page that has not edited anything yet (the pasted section lives only in the first one)
  try {
    await fresh.goto(base + pageHref, { waitUntil: "load" });
    await fresh.waitForSelector(TREE, { timeout: 10000 });
    await fresh.waitForTimeout(1500);
    if ((await fresh.locator(BAR).count()) !== 1) throw new Error("the formatting bar is not mounted ahead of the first edit (it would wait for its chunk)");
  } finally { await fresh.close(); }
  await startEdit(headId);
  await p.waitForSelector(`${BAR}:not([hidden])`, { timeout: 700 }).catch(() => { throw new Error("no bar within 700ms of the double-click"); });
  await p.keyboard.press("Escape"); await settle(500);
  await startEdit(headId);
  await p.keyboard.press("Home");
  await p.keyboard.press("Shift+ArrowRight");
  await p.waitForSelector(`${BAR}:not([hidden])`, { timeout: 700 }).catch(() => { throw new Error("no bar after Shift+ArrowRight"); });
  await p.keyboard.press("Escape"); await settle(500);
  return "bar visible after the double-click and after Shift+ArrowRight";
});

await check("while editing, the element toolbar is off the node's text and the bar and the link dialog stay clear of the element and inside the canvas pane", async () => {
  await startEdit(headId);
  await waitBar();
  await settle(300);
  const el = await elBox(headId);
  const check1 = async (what) => {
    for (const t of await boxOf('[data-hm-pe-toolbar]:not([hidden])')) if (overlap(t, el)) throw new Error(`${what}: the element toolbar covers the text`);
    const bars = await boxOf(BAR);
    if (bars.length !== 1) throw new Error(`${what}: ${bars.length} bars`);
    if (overlap(bars[0], el)) throw new Error(`${what}: the bar covers the edited element (${JSON.stringify(bars[0])} vs ${JSON.stringify(el)})`);
    const sc = await p.evaluate(() => { const r = document.querySelector(".hm-pe__stage").parentElement.getBoundingClientRect(); return { x: r.x, y: r.y, r: r.right, b: r.bottom }; });
    if (bars[0].x < sc.x - 1 || bars[0].r > sc.r + 1 || bars[0].y < sc.y - 1 || bars[0].b > sc.b + 1) throw new Error(`${what}: the bar leaves the canvas pane (${JSON.stringify(bars[0])} in ${JSON.stringify(sc)})`);
  };
  await check1("editing");
  await barButton("link").click();
  await p.locator("#hm-fmt-href").waitFor({ state: "visible", timeout: 2000 });
  await settle(200);
  await check1("link dialog open");
  await p.keyboard.press("Escape"); await settle(150);
  await p.keyboard.press("Escape"); await settle(500);
  return "toolbar clear of the text; bar and dialog above the element inside the pane";
});

await check("link dialog: a mouse click on the 'Open in new tab' label and on the checkbox keeps the dialog open", async () => {
  await startEdit(headId);
  await waitBar();
  await barButton("link").click();
  await p.locator("#hm-fmt-href").waitFor({ state: "visible", timeout: 2000 });
  await p.locator("#hm-fmt-href").fill("https://example.com/x");
  await p.locator(".hm-pe__fmt-tab").click();
  await settle(300);
  if (!(await p.locator("#hm-fmt-link").isVisible())) throw new Error("clicking the label closed the link dialog");
  if (!(await p.locator("#hm-fmt-tab").isChecked())) throw new Error("the label click did not tick the box");
  await p.locator("#hm-fmt-tab").click();
  await settle(300);
  if (!(await p.locator("#hm-fmt-link").isVisible())) throw new Error("clicking the checkbox closed the link dialog");
  if ((await p.locator("#hm-fmt-href").inputValue()) !== "https://example.com/x") throw new Error("the address was lost");
  await p.keyboard.press("Escape"); await settle(150);
  await p.keyboard.press("Escape"); await settle(500);
  return "dialog and address survive mouse clicks on the label and the checkbox";
});

await check("link dialog: opens on the current link with its address and new-tab state, empty outside a link; the bar sits by the element, not on the caret line", async () => {
  const from = errors.length;
  await startEdit(textId);
  await waitBar();
  const dialog = async (word, href, blank) => {
    if (word === "Plain") {
      await p.keyboard.press("Home");
      for (let i = 0; i < 5; i++) await p.keyboard.press("Shift+ArrowRight");
    } else await dragSelect(textId, word);
    await settle(200);
    await p.keyboard.press("Control+k");
    await p.locator("#hm-fmt-href").waitFor({ state: "visible", timeout: 2000 });
    const got = { href: await p.locator("#hm-fmt-href").inputValue(), blank: await p.locator("#hm-fmt-tab").isChecked() };
    const expanded = await barButton("link").getAttribute("aria-expanded");
    await p.keyboard.press("Escape"); // back to the text, the edit stays open
    await settle(200);
    if (got.href !== href || got.blank !== blank) throw new Error(`${word}: dialog shows ${JSON.stringify(got)}, wanted ${JSON.stringify({ href, blank })}`);
    if (expanded !== "true") throw new Error("the Link button does not say it opened the dialog: " + expanded);
  };
  await dialog("words", "https://example.com/a?x=1&y=2", true);
  await dialog("here", "mailto:qa@example.com", false);
  await dialog("Plain", "", false);
  // the bar is above or below the whole element, never over the line being typed
  const el = await (await frame()).evaluate(`(() => { const r = ${EL(textId)}.getBoundingClientRect(); return { top: r.top, bottom: r.bottom }; })()`);
  const top = await toPage(await frame(), 0, el.top), bottom = await toPage(await frame(), 0, el.bottom);
  const box = await p.locator(BAR).first().boundingBox();
  if (box.y + box.height > top.y + 2 && box.y < bottom.y - 2) throw new Error(`the bar (${box.y}-${box.y + box.height}) covers the element (${top.y}-${bottom.y})`);
  await endEdit(textId);
  noErrors(from);
  return "words, here, Plain";
});

await check("saved doc and the render of it: html holds only allowed tags, a new-tab link has rel noopener noreferrer, hostile html is cleaned", async () => {
  await update();
  await open(); // from the database
  const h = await byId(headId), t = await byId(textId);
  if (!h?.p.html || !t?.p.html) throw new Error("the reload lost the html props: " + JSON.stringify([h?.p, t?.p]));
  for (const [n, what] of [[h, "heading html"], [t, "text html"]]) {
    if (!/<strong>/.test(n.p.html) || !/<a [^>]*href=/.test(n.p.html)) throw new Error(`${what} lacks bold or link: ${n.p.html}`);
    onlyAllowed(n.p.html, what);
  }
  if (!/<a [^>]*target="_blank"[^>]*>/.test(h.p.html) || !/rel="noopener noreferrer"/.test(h.p.html)) throw new Error("stored new-tab link has no rel: " + h.p.html);
  // The render of hostile stored html: stage it as a draft and read the canvas page the server builds from it.
  const hostile = '<strong onclick="x()">ok</strong><script>alert(1)</script><img src=x onerror=alert(1)><a href="javascript:alert(1)">bad</a> <a href="//evil.example/" target="_blank">proto</a> <a href="/rel" target="_blank" rel="opener">rel</a> <font color=red style="x:y">f</font>';
  const d = await doc();
  const copy = JSON.parse(JSON.stringify(d));
  flat(copy.nodes).find((n) => n.id === h.id).p.html = hostile;
  const out = await p.evaluate(async ({ href, doc: dd }) => {
    const id = location.pathname.match(/entities\/([0-9a-f-]{36})/)[1];
    const tok = document.querySelector('meta[name="csrf-token"]').content;
    const r = await fetch(`/admin/entities/${id}/builder/stage`, { method: "POST", credentials: "same-origin", headers: { "Content-Type": "application/json", "X-CSRF-Token": tok }, body: JSON.stringify({ doc: dd }) });
    const { url } = await r.json();
    // The preview URL carries a one-time frame ticket the admin guard accepts only for an iframe navigation (a fetch gets the 404 page).
    const f = document.createElement("iframe");
    f.style.display = "none";
    const loaded = new Promise((res) => f.addEventListener("load", res, { once: true }));
    f.src = url;
    document.body.append(f);
    await loaded;
    const html = f.contentDocument.documentElement.outerHTML;
    f.remove();
    return html;
  }, { href: pageHref, doc: copy });
  const dom = new DOMParser0(out);
  const el = dom(h.id);
  if (!el) throw new Error("the staged render has no heading element");
  onlyAllowed(el, "the rendered hostile heading");
  if (!/<strong>ok<\/strong>/.test(el)) throw new Error("the allowed mark was lost: " + el);
  if (/href="\/\/evil/.test(el) || /javascript:/i.test(el)) throw new Error("unsafe href survived: " + el);
  for (const a of el.match(/<a\b[^>]*>/g) ?? []) if (/target="_blank"/.test(a) && !/rel="noopener noreferrer"/.test(a)) throw new Error("target=_blank without rel noopener noreferrer: " + a);
  if (/rel="opener"/.test(el)) throw new Error("an input rel survived: " + el);
  return `stored ${h.p.html.length}+${t.p.html.length} chars clean; hostile -> ${el.slice(0, 140)}`;
});
// The innerHTML of the node's [data-hm-prop] element in a canvas page text, found by the node id.
function DOMParser0(page) {
  return (id) => {
    const at = page.indexOf(`data-hm-node="${id}"`);
    if (at < 0) return null;
    const open = page.lastIndexOf("<", at);
    const tag = page.slice(open + 1).match(/^[a-z0-9]+/i)[0];
    const start = page.indexOf(">", at) + 1;
    const end = page.indexOf(`</${tag}>`, start);
    return page.slice(start, end);
  };
}

await check("rich paste keeps only allowed marks (script, img, font, styles, javascript: links gone); plain text paste still works", async () => {
  const from = errors.length;
  await startEdit(textId);
  await p.keyboard.press("Control+a");
  await p.keyboard.press("Delete");
  const f = await frame();
  const rich = '<meta charset="utf-8"><p class="MsoNormal" style="mso-margin:0"><span style="font-weight:700;color:red">Word</span> <b>bold</b> <i>it</i> <u>un</u> <script>alert(1)</script><img src=x onerror=alert(1)> <a href="javascript:alert(1)">bad</a> <a href="https://ok.example/">ok</a> <font color=red face=Arial>f</font><o:p></o:p></p>';
  const paste = (html, plain) => f.evaluate(`((html, plain) => { const dt = new DataTransfer(); if (html) dt.setData('text/html', html); dt.setData('text/plain', plain); const e = ${EL(textId)}; e.dispatchEvent(new ClipboardEvent('paste', { clipboardData: dt, bubbles: true, cancelable: true })); })(${JSON.stringify(html)}, ${JSON.stringify(plain)})`);
  await paste(rich, "Word bold it un bad ok f");
  await settle(300);
  const html = (await elState(textId)).html;
  onlyAllowed(html, "the pasted text");
  for (const want of ["<strong>bold</strong>", "<em>it</em>", "href=\"https://ok.example/\""]) if (!html.includes(want) && !(want.startsWith("<strong") && /<b>bold<\/b>/.test(html))) throw new Error(`pasted html lacks ${want}: ${html}`);
  if (/Word/.test(html) === false) throw new Error("the text of a span was lost: " + html);
  if (/<(img|script|font|span|p)\b/i.test(html)) throw new Error("foreign element kept: " + html);
  await p.keyboard.press("Control+a");
  await p.keyboard.press("Delete");
  await paste("", "line one\nline two");
  await settle(200);
  const plain = (await elState(textId)).html;
  if (tagsIn(plain).length || !/line one\s+line two/.test(plain)) throw new Error("plain paste is " + JSON.stringify(plain));
  await p.keyboard.press("Escape");
  await settle(600);
  noErrors(from);
  return `rich -> ${html.slice(0, 110)}; plain -> ${plain}`;
});

await check("a real paste (clipboard, Ctrl+V) with the caret inside a bold word goes through the cleaner: no span, img, p or style reaches the canvas", async () => {
  const from = errors.length;
  await startEdit(textId);
  await p.keyboard.press("Control+a");
  await p.keyboard.press("Delete");
  await p.keyboard.type("Grow faster");
  await p.keyboard.press("Shift+ArrowLeft", { delay: 0 });
  for (let i = 0; i < 5; i++) await p.keyboard.press("Shift+ArrowLeft");
  await p.keyboard.press("Control+b");
  await settle(200);
  await p.keyboard.press("ArrowRight"); // collapse to the end of the bold word, then step back inside it
  for (let i = 0; i < 3; i++) await p.keyboard.press("ArrowLeft");
  const before = (await elState(textId)).html;
  if (!/<strong>faster<\/strong>/.test(before)) throw new Error("the word is not bold: " + before);
  await p.evaluate(async () => {
    const h = '<meta charset="utf-8"><span style="color:red">X</span><img src="https://probe.test/x" style="color:red"><p>Y</p>';
    await navigator.clipboard.write([new ClipboardItem({ "text/html": new Blob([h], { type: "text/html" }), "text/plain": new Blob(["XY"], { type: "text/plain" }) })]);
  });
  await (await frame()).evaluate(() => window.focus());
  await p.keyboard.press("Control+v");
  await settle(300);
  const html = (await elState(textId)).html;
  onlyAllowed(html, "the pasted text");
  if (/<(img|span|p|div)\b|style=/i.test(html)) throw new Error("foreign markup reached the canvas: " + html);
  if (!/X/.test(html) || !/Y/.test(html)) throw new Error("the pasted text is missing: " + html);
  await p.keyboard.press("Escape");
  await settle(600);
  noErrors(from);
  return html;
});

await check("typing past 20,000 characters is refused with a visible message (status line), not dropped silently", async () => {
  const from = errors.length;
  await startEdit(textId);
  await p.keyboard.press("Control+a");
  await p.keyboard.press("Delete");
  const f = await frame();
  const paste = (plain) => f.evaluate(`((plain) => { const dt = new DataTransfer(); dt.setData('text/plain', plain); const e = ${EL(textId)}; e.dispatchEvent(new ClipboardEvent('paste', { clipboardData: dt, bubbles: true, cancelable: true })); })(${JSON.stringify(plain)})`);
  await paste("x".repeat(19990));
  await paste("y".repeat(50));
  await settle(300);
  const len = (await elState(textId)).html.length;
  const msg = await p.evaluate(() => document.querySelector(".hm-savestate__text")?.textContent ?? "");
  if (len > 20000) throw new Error("the canvas holds " + len + " characters");
  if (!/20,000/.test(msg)) throw new Error("no limit message, the status line says: " + msg);
  await p.keyboard.press("Control+a");
  await p.keyboard.press("Delete");
  await p.keyboard.press("Escape");
  await settle(600);
  noErrors(from);
  return `${len} characters kept; "${msg}"`;
});

await check("Esc restores the original markup and the doc is unchanged; typing before it really changed the canvas", async () => {
  const before = await sig();
  const original = (await elState(textId)).html;
  await startEdit(textId);
  await p.keyboard.press("End");
  await p.keyboard.type(" ZZZ", { delay: 10 });
  if (!(await elState(textId)).html.includes("ZZZ")) throw new Error("typing changed nothing, the check cannot tell");
  await settle(200);
  await p.keyboard.press("Escape");
  await settle(800);
  const s = await elState(textId);
  if (s.html !== original) throw new Error(`Esc left ${s.html}, wanted ${original}`);
  if (s.editing) throw new Error("Esc left the element editable");
  if ((await sig()).includes("ZZZ") || (await sig()) !== before) throw new Error("the doc kept the escaped typing");
  return "restored";
});

await check("Shift+Enter inserts a <br> in a text and does nothing in a heading", async () => {
  await startEdit(textId);
  await p.keyboard.press("End");
  await p.keyboard.press("Shift+Enter");
  await p.keyboard.type("second", { delay: 10 });
  const t = await elState(textId);
  if (!/<br>/.test(t.html) || !t.editing) throw new Error("no <br> in the text: " + t.html);
  await p.keyboard.press("Escape"); await settle(700);
  await startEdit(headId);
  const before = (await elState(headId)).html;
  await p.keyboard.press("End");
  await p.keyboard.press("Shift+Enter");
  await settle(150);
  const h = await elState(headId);
  if (/<br>/.test(h.html) || h.html !== before) throw new Error("the heading changed on Shift+Enter: " + h.html);
  if (!h.editing) throw new Error("Shift+Enter ended the heading edit");
  await p.keyboard.press("Escape"); await settle(700);
  return "br in text, none in heading";
});

await check("a plain text node: type, Enter; the canvas keeps the typed text after its re-render (nothing is blanked, no 'fell behind')", async () => {
  const from = errors.length;
  const plain = (await nodes()).find((n) => n.t === "text" && n.id !== textId && !n.p.html && n.p.text);
  await startEdit(plain.id);
  await p.keyboard.press("End");
  await p.keyboard.type("!?");
  await endEdit(plain.id);
  const shown = (await elState(plain.id)).html;
  const stored = (await byId(plain.id)).p.text;
  if (stored !== plain.p.text + "!?" || !shown.includes("!?") || shown.length < 5) throw new Error(`stored "${stored}", canvas "${shown}"`);
  const msg = await p.evaluate(() => document.body.innerText);
  if (/fell behind/.test(msg)) throw new Error("the preview reported it fell behind");
  await startEdit(plain.id);
  await p.keyboard.press("End");
  await p.keyboard.press("Backspace");
  await p.keyboard.press("Backspace");
  await endEdit(plain.id);
  noErrors(from);
  return shown.slice(-30);
});

await check("a button label: type, Enter ends the edit and it stays ended (Enter does not start it again)", async () => {
  const from = errors.length;
  const btn = (await nodes()).find((n) => n.t === "button" && n.p.text === "Press here");
  if (!btn) throw new Error("no pasted button");
  await startEdit(btn.id);
  await p.keyboard.press("End");
  await p.keyboard.type("!");
  await settle(300);
  await p.keyboard.press("Enter");
  // at once, not after the re-render: the host swaps the frame some 100 ms later, which hides an edit that started again
  const s = await elState(btn.id);
  if (s.editing) throw new Error("the label is editable again right after Enter");
  await settle(800);
  if ((await byId(btn.id)).p.text !== "Press here!") throw new Error("stored " + (await byId(btn.id)).p.text);
  noErrors(from);
  return "ended after Enter";
});

await check("apostrophes, quotes and a line break with a bold word: the save adopts nothing (no frame reload, history kept) and the stored html is what the canvas sent", async () => {
  const from = errors.length;
  await startEdit(textId);
  await p.keyboard.press("Control+a");
  await p.keyboard.press("Delete");
  await p.keyboard.type("Don't say \"hi\" & <go>");
  await p.keyboard.press("Shift+Enter");
  await p.keyboard.type("it's second");
  await p.keyboard.press("Control+Shift+ArrowLeft");
  await p.keyboard.press("Control+b");
  await settle(200);
  await endEdit(textId);
  const sent = (await byId(textId)).p.html;
  if (!sent || !sent.includes("Don't say \"hi\" &amp; &lt;go&gt;<br>") || !/<strong>second<\/strong>/.test(sent)) throw new Error("canvas html: " + sent);
  const f = await frame();
  await f.evaluate(() => { window.__qaFrame = 1; });
  const undoBefore = await p.locator("[data-hm-bs-undo]").first().isEnabled();
  await p.evaluate(() => document.activeElement?.blur?.());
  const resp = p.waitForResponse((r) => /\/builder\/(save|autosave)$/.test(new URL(r.url()).pathname) && r.request().method() === "POST", { timeout: 15000 });
  await p.locator('[data-hm-bs-save="save"]').first().click();
  const out = await (await resp).json();
  await settle(1000);
  if (!out.ok) throw new Error("save: " + JSON.stringify(out).slice(0, 200));
  if (out.doc !== undefined) throw new Error("the save reply carries a doc, so the client adopts and reloads the frame");
  if (!(await (await frame()).evaluate(() => window.__qaFrame === 1))) throw new Error("the frame was reloaded after the save");
  if (!undoBefore || !(await p.locator("[data-hm-bs-undo]").first().isEnabled())) throw new Error("undo history was lost");
  if ((await byId(textId)).p.html !== sent) throw new Error("the doc html changed after the save: " + (await byId(textId)).p.html);
  await open();
  if ((await byId(textId)).p.html !== sent) throw new Error("the stored html differs from what the canvas sent: " + (await byId(textId)).p.html);
  noErrors(from);
  return sent;
});

await check("typing 200 chars: no patch reaches the frame, text posts reach the host, the saved doc keeps them", async () => {
  const f = await frame();
  await p.evaluate(() => { window.__qaPort.text = 0; window.__qaPort.patch = 0; });
  await startEdit(textId);
  await p.keyboard.press("End");
  await p.keyboard.type("x".repeat(200), { delay: 5 });
  await settle(400);
  const { patch: patches, text: texts } = await p.evaluate(() => window.__qaPort);
  if (!(await elState(textId)).html.includes("x".repeat(200))) throw new Error("the typing did not reach the element");
  if (texts < 100) throw new Error(`only ${texts} text posts for 200 keystrokes: the check cannot tell`);
  if (patches !== 0) throw new Error(`${patches} patch messages while typing`);
  await p.keyboard.press("Escape"); await settle(700);
  return `${texts} text posts, ${patches} patches`;
});

// Per-event cost: the gap between the window's capture phase and its bubble phase, which spans every handler the
// bridge has on the document (Event Timing only lists events slower than 16 ms, so it cannot give a p95 of fast ones).
const installTimer = (f) => f.evaluate(() => {
  const t = { keydown: [], beforeinput: [], input: [], key: [] };
  const open = {};
  for (const type of Object.keys(t).filter((x) => x !== "key")) {
    window.addEventListener(type, () => { open[type] = performance.now(); }, true);
    window.addEventListener(type, () => { t[type].push(performance.now() - open[type]); }, false);
  }
  window.addEventListener("keydown", () => { open.k = performance.now(); }, true);
  window.addEventListener("input", () => { t.key.push(performance.now() - open.k); }, false);
  window.__qaT = t;
  window.__qaSlow = [];
  new PerformanceObserver((l) => { for (const e of l.getEntries()) if (e.name === "keydown" || e.name === "input") window.__qaSlow.push(e.processingEnd - e.processingStart); }).observe({ type: "event", durationThreshold: 16, buffered: true });
});
const p95 = (xs) => { const s = [...xs].sort((a, b) => a - b); return s.length ? s[Math.min(s.length - 1, Math.ceil(s.length * 0.95) - 1)] : NaN; };

await check(`keystroke cost on the 60-node page: p95 of keydown and input <= ${P95_MS} ms, next to a bare contenteditable`, async () => {
  const f = await frame();
  await installTimer(f);
  await startEdit(textId);
  await p.keyboard.press("End");
  await p.keyboard.type("abcdefghij".repeat(20), { delay: 15 });
  await settle(300);
  const bridge = await f.evaluate(() => ({ ...window.__qaT, slow: window.__qaSlow }));
  await p.keyboard.press("Escape"); await settle(600);
  // A bare contenteditable in the same frame, typed into the same way.
  await f.evaluate(() => { const d = document.createElement("div"); d.id = "qa-bare"; d.setAttribute("contenteditable", "true"); d.textContent = "bare"; d.style.cssText = "position:fixed;top:0;left:0;background:#fff;z-index:99999"; document.body.append(d); window.__qaT.keydown.length = window.__qaT.input.length = window.__qaT.beforeinput.length = window.__qaT.key.length = 0; });
  await f.focus("#qa-bare");
  await p.keyboard.press("End");
  await p.keyboard.type("abcdefghij".repeat(20), { delay: 15 });
  await settle(300);
  const bare = await f.evaluate(() => { const t = window.__qaT; document.getElementById("qa-bare").remove(); return t; });
  if (bridge.keydown.length < 150 || bridge.input.length < 150) throw new Error(`only ${bridge.keydown.length} keydown and ${bridge.input.length} input events measured`);
  if (bare.input.length < 150) throw new Error("the bare editor measured nothing");
  const r = (xs) => p95(xs).toFixed(2);
  const msg = `bridge keydown ${r(bridge.keydown)} input ${r(bridge.input)} keystroke ${r(bridge.key)} ms; bare keydown ${r(bare.keydown)} input ${r(bare.input)} ms; slow events ${bridge.slow.length}`;
  for (const k of ["keydown", "input"]) if (!(p95(bridge[k]) <= P95_MS)) throw new Error(`p95 ${k} ${r(bridge[k])} ms > ${P95_MS} ms (${msg})`);
  if (bridge.slow.some((x) => x > P95_MS)) throw new Error(`an event over 16 ms spent ${Math.max(...bridge.slow).toFixed(1)} ms in handlers (${msg})`);
  return msg;
});

await check("axe is clean with the formatting bar and the link dialog open", async () => {
  await startEdit(headId);
  await waitBar();
  await dragSelect(headId, "Rich");
  await barButton("link").click();
  await p.locator("#hm-fmt-href").waitFor({ state: "visible", timeout: 2000 });
  await p.evaluate(axe);
  const r = await p.evaluate((t) => window.axe.run(document, { runOnly: { type: "tag", values: t } }), tags);
  if (r.violations.length) throw new Error("axe: " + r.violations.map((v) => `${v.id} (${v.nodes[0].target.join(" ")})`).join("; "));
  await p.keyboard.press("Escape"); await settle(150);
  await p.keyboard.press("Escape"); await settle(700);
  return "0 violations";
});

await check("inspector editor, keyboard only: select two letters, Ctrl+B, the doc and the canvas show it", async () => {
  const from = errors.length;
  await p.locator(TREE).first().click();
  await row(headId).click();
  await settle(900);
  const ed = p.locator('[data-hm-bs-inspector] [contenteditable="true"], .hm-bs-inspector [contenteditable="true"], [aria-label="Text"][contenteditable="true"]').first();
  await ed.waitFor({ state: "visible", timeout: 5000 }).catch(() => { throw new Error("no inspector rich editor for the heading"); });
  await ed.focus();
  await p.keyboard.press("Control+Home");
  await p.keyboard.press("Shift+ArrowRight");
  await p.keyboard.press("Shift+ArrowRight");
  await settle(300); // the editor reads its selection on selectionchange: a key pressed in the same tick sees the old one
  await p.keyboard.press("Control+b");
  await settle(1200);
  const html = (await byId(headId))?.p.html ?? "";
  if (!/<strong>[^<]{1,3}<\/strong>/.test(html)) throw new Error("the doc did not get the inspector bold: " + html);
  const canvas = (await elState(headId))?.html ?? "";
  if (!/<strong>[^<]{1,3}<\/strong>/.test(canvas)) throw new Error("the canvas does not show it: " + canvas);
  noErrors(from);
  return canvas.slice(0, 100);
});

await check("inspector, keyboard only: tree Enter lands on the Text editor, Ctrl+A and typing replace it as plain text", async () => {
  const from = errors.length;
  await row(headId).click();
  await settle(700);
  await row(headId).focus();
  await p.keyboard.press("Enter");
  await settle(500); // Text comes before Level now: Enter lands on the Text editor itself, not on its toolbar
  const at = await p.evaluate(() => { const a = document.activeElement; return { editable: a?.getAttribute("contenteditable") === "true", name: a?.getAttribute("aria-label") ?? a?.tagName, labelled: !!a?.getAttribute("aria-labelledby") && !!document.getElementById(a.getAttribute("aria-labelledby")) }; });
  if (!at.editable || !at.labelled) throw new Error("Enter on the row did not reach the labelled Text editor: " + JSON.stringify(at));
  await p.keyboard.press("Control+a");
  await p.keyboard.type("Third section heading");
  await settle(1200);
  const n = await byId(headId);
  if (n.p.text !== "Third section heading" || n.p.html) throw new Error("doc: " + JSON.stringify(n.p));
  noErrors(from);
  return JSON.stringify(n.p);
});

await check("inspector editor at phone width, keyboard only: the Inspector sheet shows the editor, Ctrl+B bolds two letters", async () => {
  const from = errors.length;
  await p.setViewportSize({ width: 390, height: 844 });
  try {
    return await onPhone(from);
  } finally {
    await p.setViewportSize({ width: 1440, height: 900 });
  }
});
async function onPhone(from) {
  await p.goto(base + pageHref, { waitUntil: "load" });
  await p.waitForFunction(() => !!window.__hmBuilder, null, { timeout: 8000 });
  await settle(800);
  // The phone dock: Navigator opens the tree as a sheet; tapping a row opens the Inspector sheet by itself.
  await p.locator('[data-hm-bs-dock="nav"]').click();
  await settle(300);
  await row(headId).click();
  await settle(500);
  if (process.env.QA_VERBOSE) console.log("  dock", await p.evaluate(() => [...document.querySelectorAll("[data-hm-bs-dock]")].map((t) => t.textContent.trim() + ":" + t.getAttribute("aria-selected")).join(" | ")));
  if ((await p.evaluate(() => document.querySelector("[data-hm-builder]").dataset.hmBsTab)) !== "insp") { await p.locator('[data-hm-bs-dock="insp"]').click(); await settle(500); }
  const ed = p.locator('.hm-bs-panel [contenteditable="true"][aria-label="Text"]').first();
  await ed.waitFor({ state: "visible", timeout: 5000 }).catch(() => { throw new Error("the editor is not reachable at phone width"); });
  const box = await ed.boundingBox();
  if (box.width > 390) throw new Error(`the editor is ${box.width}px wide on a 390px screen`);
  await ed.focus();
  await p.keyboard.press("Control+Home");
  await p.keyboard.press("Shift+ArrowRight");
  await p.keyboard.press("Shift+ArrowRight");
  await settle(300);
  await p.keyboard.press("Control+b");
  await settle(1000);
  const html = (await byId(headId))?.p.html ?? "";
  if (!/^<strong>Ri<\/strong>ch /.test(html)) throw new Error("the doc did not get the bold: " + html);
  noErrors(from);
  return html.slice(0, 60);
}

await check("inspector plain field (the editor kit did not load) on a formatted heading writes text and clears html, so the canvas shows it", async () => {
  const from = errors.length;
  const cut = /editor\/kit/;
  await p.route(cut, (r) => r.abort());
  try {
    await update();
    await open();
    await row(headId).click();
    await settle(800);
    const field = p.locator('.hm-bs-panel').getByLabel("Text", { exact: true }).first();
    await field.waitFor({ state: "visible", timeout: 5000 });
    if (!(await byId(headId)).p.html) throw new Error("the heading has no html to replace");
    await field.fill("Plain again");
    await field.blur();
    await settle(800);
    const n = await byId(headId);
    if (n.p.text !== "Plain again" || n.p.html) throw new Error("doc: " + JSON.stringify(n.p));
    const shown = (await elState(headId)).html;
    if (!/Plain again/.test(shown) || /<strong>|<a /.test(shown)) throw new Error("canvas: " + shown);
    noErrors(from);
    return shown;
  } finally {
    await p.unroute(cut);
  }
});

await check("the text chunk fails to load: a visible message, and what was typed is kept as plain text", async () => {
  const from = errors.length;
  const plainText = (await nodes()).find((n) => n.t === "text" && n.id !== textId && !n.p.html && n.p.text);
  if (!plainText) throw new Error("no plain text node");
  const cut = /inlinehtml|fmtbar/;
  await update(); // a published page keeps edits in the browser: save before the reload
  await p.route(cut, (r) => r.abort());
  try {
    await open();
    await startEdit(plainText.id);
    await p.keyboard.press("End");
    await p.keyboard.type("Zq");
    await p.keyboard.press("Enter");
    await settle(800);
    const said = await p.evaluate(() => document.querySelector("[data-hm-bs-state]")?.textContent ?? "");
    const live = await p.evaluate(() => [...document.querySelectorAll('[role="status"]')].map((e) => e.textContent).join(" | "));
    if (!/did not load/i.test(said + live)) throw new Error(`no message; status "${said}", live "${live}"`);
    const now = await byId(plainText.id);
    if (now.p.text !== plainText.p.text + "Zq") throw new Error(`the typing was lost: "${now.p.text}"`);
  } finally {
    await p.unroute(cut);
  }
  // put it back (the published page keeps the typing in the browser only until it is saved)
  await update();
  await open();
  await startEdit(plainText.id);
  await p.keyboard.press("End");
  await p.keyboard.press("Backspace");
  await p.keyboard.press("Backspace");
  await p.keyboard.press("Enter");
  await settle(800);
  if ((await byId(plainText.id)).p.text !== plainText.p.text) throw new Error("could not undo the typing");
  noErrors(from);
  return "message shown, text kept";
});

const visibleCmds = () => p.locator(`${BAR} .hm-pe__fmt-row button[data-cmd]`).evaluateAll((bs) => bs.filter((b) => b.offsetParent !== null).map((b) => b.dataset.cmd));
await check("the bar on a heading is B, I, Link and Clear; on a text all seven; the link panel is at most 360 px wide and offers the site's pages", async () => {
  await startEdit(headId);
  await waitBar();
  const head = (await visibleCmds()).join(",");
  if (head !== "bold,italic,link,clear") throw new Error("heading bar: " + head);
  await barButton("link").click();
  await p.locator("#hm-fmt-href").waitFor({ state: "visible", timeout: 2000 });
  const w = (await p.locator("#hm-fmt-link").boundingBox()).width;
  if (w > 360.5) throw new Error(`the link panel is ${w}px wide`);
  const pages = await p.locator("#hm-fmt-pages option").evaluateAll((os) => os.map((o) => o.value));
  if (!pages.length || !pages.some((v) => v.startsWith("/"))) throw new Error("no page in the link list: " + JSON.stringify(pages));
  if ((await p.locator("#hm-fmt-href").getAttribute("list")) !== "hm-fmt-pages") throw new Error("the address field is not tied to the list");
  await p.keyboard.press("Escape"); await settle(200);
  await p.keyboard.press("Escape"); await settle(500);
  await startEdit(textId);
  await waitBar();
  const text = (await visibleCmds()).join(",");
  if (text !== "bold,italic,underline,strike,code,link,clear") throw new Error("text bar: " + text);
  await p.keyboard.press("Escape"); await settle(500);
  return `heading: ${head}; text: ${text}; link panel ${Math.round(w)}px, ${pages.length} choices (e.g. ${pages[0]})`;
});

const hasStrong = async (id) => /<strong>/.test((await elState(id)).html);
await check("Ctrl+Z inside a text edit undoes the last format (button or Ctrl+B) as well as typing; typing after a format is undone first (native), the format next", async () => {
  await startEdit(textId);
  await p.keyboard.press("Home");
  await p.keyboard.press("Shift+ArrowRight"); await p.keyboard.press("Shift+ArrowRight"); await p.keyboard.press("Shift+ArrowRight");
  await waitBar();
  const was = (await elState(textId)).html;
  await barButton("bold").click(); await settle(200);
  const bolded = (await elState(textId)).html;
  if (bolded === was) throw new Error("the bar's Bold changed nothing");
  await p.keyboard.press("Control+z"); await settle(300);
  if ((await elState(textId)).html !== was) throw new Error("Ctrl+Z did not undo the bar's Bold: " + (await elState(textId)).html);
  await p.keyboard.press("Control+b"); await settle(200);
  if ((await elState(textId)).html === was) throw new Error("Ctrl+B changed nothing");
  await p.keyboard.press("Control+z"); await settle(300);
  if ((await elState(textId)).html !== was) throw new Error("Ctrl+Z did not undo Ctrl+B: " + (await elState(textId)).html);
  // the format is undone, the selection is back: format again, type over the selection, undo twice
  await p.keyboard.press("Control+b"); await settle(200);
  const again = (await elState(textId)).html;
  await p.keyboard.type("Q"); await settle(200);
  if (!/Q/.test((await elState(textId)).html)) throw new Error("typing after the format did not show");
  await p.keyboard.press("Control+z"); await settle(300);
  const mid = (await elState(textId)).html;
  if (/Q/.test(mid)) throw new Error("the first Ctrl+Z did not take the typing back: " + mid);
  if (mid !== again) throw new Error("after undoing the typing the bold is gone too: " + mid + " vs " + again);
  await p.keyboard.press("Control+z"); await settle(300);
  if ((await elState(textId)).html !== was) throw new Error("the second Ctrl+Z did not undo the format: " + (await elState(textId)).html);
  const doc1 = (await byId(textId)).p;
  if (/<strong>/.test(String(doc1.html ?? "")) && !/<strong>/.test(was)) throw new Error("the doc kept the undone bold: " + doc1.html);
  await p.keyboard.press("Escape"); await settle(500);
  return "bar Bold and Ctrl+B undone by Ctrl+Z; typing undone before the format";
});

await check("while typing the status chip says the page is not saved and the inspector Text field shows the typed text", async () => {
  await startEdit(textId);
  await p.keyboard.press("End");
  await p.keyboard.type("LIVE");
  await settle(250);
  const chip = (await p.locator("[data-hm-bs-state]").innerText()).replace(/\s+/g, " ");
  if (!/Not saved|Unsaved|Saving/.test(chip)) throw new Error("status chip while typing: " + chip);
  const field = p.locator('[data-hm-bs-panel] [contenteditable="true"][aria-label="Text"], [data-hm-bs-panel] textarea').first();
  const inField = await field.evaluate((e) => e.value ?? e.textContent).catch(() => null);
  await p.keyboard.press("Escape"); await settle(400);
  if (inField === null) return `chip "${chip}"; inspector Text field not open for this node (nothing to check)`;
  if (!/LIVE/.test(inField)) throw new Error("the inspector Text field did not show the typed text: " + inField);
  return `chip "${chip}"; inspector Text shows the typed text`;
});

// One click selects (the frame has the focus); Enter then starts the edit: the keyboard path into the canvas text.
async function clickSelect(id) {
  if (await p.locator("[data-hm-pe-toolbar]:not([hidden])").count()) await p.locator("[data-hm-pe-strip] .hm-pe__crumb").first().click().catch(() => {});
  for (let i = 0; i < 6; i++) {
    const at = await nodePoint(id);
    if (at) {
      await settle(200);
      await p.mouse.click(at.x, at.y);
      for (let j = 0; j < 10; j++) { if ((await p.evaluate(() => window.__hmBuilder.selected())) === id) return; await settle(100); }
    }
    await settle(300);
  }
  throw new Error(`a click never selected ${id}`);
}
await check("Enter on a selected text starts the edit, Enter finishes it (typed text kept); Esc cancels and keeps the selection; the toolbar says so", async () => {
  await clickSelect(textId);
  if (await (await frame()).evaluate(`!!${EL(textId)}.hasAttribute('contenteditable')`)) throw new Error("a single click started the edit");
  await p.keyboard.press("Enter");
  for (let j = 0; j < 10 && !(await elState(textId))?.editing; j++) await settle(100);
  if (!(await elState(textId))?.editing) throw new Error("Enter did not start the edit");
  const hint = await p.locator("[data-hm-pe-toolbar] .hm-pe__thint").innerText();
  if (!/Enter to finish/.test(hint) || !/Esc to cancel/.test(hint)) throw new Error("toolbar hint while editing: " + hint);
  await p.keyboard.type("ZZ");
  await endEdit(textId);
  if (!/ZZ/.test(String((await byId(textId)).p.html ?? (await byId(textId)).p.text))) throw new Error("the typed text was not kept");
  await clickSelect(textId);
  await p.keyboard.press("Enter");
  for (let j = 0; j < 10 && !(await elState(textId))?.editing; j++) await settle(100);
  const before = await sig();
  await p.keyboard.type("QQ");
  await p.keyboard.press("Escape");
  for (let j = 0; j < 20 && (await elState(textId))?.editing; j++) await settle(100);
  await settle(300);
  if ((await elState(textId)).editing) throw new Error("Esc left the edit open");
  if (await sig() !== before) throw new Error("Esc did not cancel what was typed");
  if ((await p.evaluate(() => window.__hmBuilder.selected())) !== textId) throw new Error("Esc dropped the selection");
  return "click selects, Enter edits (hint shown), Enter keeps ZZ, Esc cancels QQ and the selection stays";
});

await check("Ctrl+K with the frame focused and no text edit opens the command palette, not the browser search; Esc closes it", async () => {
  await clickSelect(textId);
  await p.keyboard.press("Control+k");
  await p.locator("#hm-bs-cmdk").waitFor({ state: "visible", timeout: 3000 }).catch(() => { throw new Error("Ctrl+K from the frame did not open the command palette"); });
  await p.keyboard.press("Escape");
  await p.locator("#hm-bs-cmdk").waitFor({ state: "hidden", timeout: 3000 });
  return "palette opened from the frame and closed with Esc";
});

await check("the page is left as found: the pasted section is deleted, the page saved as it was", async () => {
  const kid = await byId(headId);
  const parent = (await nodes()).find((n) => (n.c ?? []).some((c) => c.id === kid.id));
  await p.evaluate(() => document.activeElement?.blur?.());
  await row(parent.id).click({ button: "right" });
  await p.waitForSelector(MENU, { timeout: 3000 });
  await p.locator(`${MENU} [role="menuitem"]`, { hasText: /^Delete/ }).first().click();
  await settle(600);
  if ((await sig()) !== start) throw new Error("deleting the pasted section did not give back the page found");
  await update();
  await open();
  if ((await sig()) !== start) throw new Error("the reloaded page differs from the one found");
  return "restored, saved and reloaded";
});

await b.close();
if (bad) { console.log(`${bad} check(s) failed`); process.exit(1); }
console.log("builder-inline: all checks passed");
