// Frame tickets (A3): every admin document that loads in a frame needs a one-time, 60 second,
// session-bound ticket. Happy paths first (builder canvas, blog preview, a plugin page, each with a
// screenshot in SHOTS), then the attacks (no ticket, a reused ticket, from an admin page and from a
// public page) which must answer the admin 404.
//
// Plugin part: the seeded instance has no plugin with an admin page, so run-local.sh points
// HATAMOTO_PLUGIN_DIR at fixtures/frame-plugin when this script is requested (a UI-only plugin
// whose plugin.wasm is the 8 byte empty module; the dev override loads it as an unsigned package).
//
// Run: SHOTS=<dir> bash scripts/admin-qa/run-local.sh frame-tickets.mjs   (SHOTS: where screenshots go)   Exit 1 on any problem.
import fs from "node:fs";
import path from "node:path";
import { launch, newSession, base } from "./lib.mjs";

const SHOTS = process.env.SHOTS || "";
if (SHOTS) fs.mkdirSync(SHOTS, { recursive: true });
const shot = (page, name) => (SHOTS ? page.screenshot({ path: path.join(SHOTS, name) }) : null);

let bad = 0;
const check = (ok, msg, detail = "") => {
  console.log(`${ok ? "PASS" : "FAIL"}  ${msg}${!ok && detail ? "  " + detail : ""}`);
  if (!ok) bad++;
};

const b = await launch("chromium");
const p = await newSession(b, { width: 1440, bypassCSP: true });

// Every frame document response, by status (a page's own admin navigations are not counted).
const seen = [];
const watch = (page) =>
  page.on("response", (r) => {
    const u = new URL(r.url());
    if (/\/edit\/preview\/[^/]+$/.test(u.pathname) || u.pathname.startsWith("/plugin-ui/") && /\.html?$/.test(u.pathname))
      seen.push({ path: u.pathname + u.search, status: r.status(), dest: r.frame() === page.mainFrame() ? "document" : "iframe" });
  });
watch(p);
const docStatuses = () => seen.filter((x) => x.dest === "iframe").map((x) => x.status);

const pageId = async (name) => {
  await p.goto(`${base}/admin/entities?q=${encodeURIComponent(name)}`);
  const hrefs = await p.$$eval('a[href^="/admin/entities/"]', (as, t) => as.filter((a) => a.textContent.includes(t)).map((a) => a.getAttribute("href")), name);
  return hrefs.map((h) => h.match(/entities\/([0-9a-f-]{36})/)?.[1]).find(Boolean);
};
const csrf = (page) => page.evaluate(() => document.querySelector('meta[name="csrf-token"]')?.content ?? "");

// ---- (a) builder canvas ----
const TREE = '[data-hm-bs-tree] [role="treeitem"]';
const aboutId = await pageId(process.env.QA_PAGE_NAME || "About");
check(!!aboutId, "found the About page");
await p.goto(`${base}/admin/entities/${aboutId}/edit?qa=1`, { waitUntil: "load" });
await p.waitForSelector(TREE, { timeout: 10000 });
await p.waitForFunction(() => !!window.__hmBuilder, null, { timeout: 5000 });
await p.waitForTimeout(1500);
const canvasFrame = async (page) => {
  for (const f of page.frames()) {
    if (f === page.mainFrame() || f.url() === "about:blank") continue;
    if (await (await f.frameElement()).isVisible()) return f;
  }
  return null;
};
const cf = await canvasFrame(p);
check(!!cf, "the builder canvas frame is visible");
const canvasText = async (f) => (await f?.evaluate(() => document.body?.innerText ?? "").catch(() => "")) ?? "";
check((await canvasText(cf)).trim().length > 20, "the builder canvas rendered the page");
check(docStatuses().length > 0 && docStatuses().every((c) => c === 200), "builder: every frame document answered 200", JSON.stringify(seen));
await shot(p, "builder-canvas.png");

// An edit restages through a new ticket and the canvas keeps rendering.
const before = seen.length;
await p.evaluate(() => window.__hmBuilder.doc());
await p.locator(TREE).first().click();
await p.keyboard.press("Escape");
await p.waitForTimeout(500);
check(seen.slice(before).every((x) => x.status === 200), "builder: later frame loads are ticketed too");

// Phone width: switching panes must not reload (and so 404) the canvas.
await p.setViewportSize({ width: 390, height: 844 });
const n0 = seen.length;
for (const tab of ["nav", "insp", "canvas", "nav", "canvas"]) {
  await p.locator(`[data-hm-bs-tabs] [data-hm-bs-tab="${tab}"]`).click();
  await p.waitForTimeout(300);
}
await p.waitForTimeout(1200);
check(seen.slice(n0).every((x) => x.status === 200), "builder at 390px: switching panes causes no refused frame load", JSON.stringify(seen.slice(n0)));
const cf2 = await canvasFrame(p);
check((await canvasText(cf2)).trim().length > 20, "builder at 390px: the canvas is not blank after switching panes");
await shot(p, "builder-canvas-390.png");
await p.setViewportSize({ width: 1440, height: 900 });

// ---- (b) blog preview ----
const hrefs = await p.goto(`${base}/admin/entities?q=QA%20Post`).then(() => p.$$eval('a[href^="/admin/entities/"]', (as) => as.map((a) => a.getAttribute("href"))));
const postId = hrefs.map((h) => h.match(/entities\/([0-9a-f-]{36})\/edit/)?.[1]).find(Boolean) ?? hrefs.map((h) => h.match(/entities\/([0-9a-f-]{36})/)?.[1]).find(Boolean);
check(!!postId, "found a blog post");
await p.goto(`${base}/admin/entities/${postId}/edit`, { waitUntil: "load" });
await p.locator("[data-hm-be-preview-toggle]").click();
await p.waitForSelector("[data-hm-be-stage] iframe.hm-be__frame", { timeout: 10000 });
await p.waitForTimeout(1500);
const bf = p.frames().find((f) => /\/edit\/preview\//.test(f.url()));
check(!!bf, "the blog preview frame loaded a ticketed URL");
check((await canvasText(bf)).trim().length > 20, "the blog preview rendered the post");
check(docStatuses().at(-1) === 200, "blog preview: the frame document answered 200", JSON.stringify(seen.at(-1)));
await shot(p, "blog-preview.png");

// ---- (c) plugin page ----
await p.goto(`${base}/admin/plugins/frameplug/settings`);
const holder = p.locator("[data-hm-pluginframe]");
check((await holder.count()) === 1, "the plugin page renders its frame holder");
const pluginSrc = (await holder.getAttribute("data-src").catch(() => "")) || "";
check(/^\/plugin-ui\/frameplug\/index\.html\?t=[A-Za-z0-9_-]{32}$/.test(pluginSrc), "the plugin frame URL carries a 32 character ticket", pluginSrc);
const pframe = () => p.frames().find((f) => f.url().includes("/plugin-ui/frameplug/index.html"));
await p.waitForFunction(() => document.querySelector("iframe.hm-pluginframe") !== null);
await p.waitForTimeout(1000);
check((await pframe()?.locator("#state").textContent().catch(() => "")) === "bridge ready: frameplug", "the plugin page loaded and its bridge answered");
check(docStatuses().at(-1) === 200, "plugin page: the frame document answered 200", JSON.stringify(seen.at(-1)));
await shot(p, "plugin-page.png");

// ---- attacks ----
// Load url in a new iframe of `page` and report the document response of that load.
async function frameLoad(page, url) {
  const abs = new URL(url, base).href;
  const got = page.waitForResponse((r) => r.url() === abs && r.request().resourceType() === "document", { timeout: 8000 }).catch(() => null);
  await page.evaluate((u) => {
    const f = document.createElement("iframe");
    f.src = u;
    document.body.append(f);
  }, abs);
  const r = await got;
  if (!r) return { status: 0, body: "", xfo: "" };
  return { status: r.status(), body: await r.text().catch(() => ""), xfo: r.headers()["x-frame-options"] ?? "" };
}
// A refused frame is a 404 whose headers forbid framing (the admin's refusal carries frame-ancestors
// 'none' and X-Frame-Options DENY, so Chrome shows its own block page inside the frame and the body
// seen by the page is that page, not ours). The body itself is compared in refusedBody below, over
// the request API with the same Sec-Fetch headers a frame sends.
const isAdmin404 = (r) => r.status === 404 && /deny/i.test(r.xfo) && !/preview|bridge ready|Frame fixture/.test(r.body.slice(0, 4000));

// The refusal body, as a frame request (same-origin iframe navigation with the session cookie) gets it.
const norm = (t) => t.replace(/[A-Za-z0-9_+/=-]{16,}/g, "N");
const frameHeaders = { "Sec-Fetch-Dest": "iframe", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Site": "same-origin" };
async function refusedBody(url) {
  const res = await p.context().request.get(new URL(url, base).href, { headers: frameHeaders, maxRedirects: 0 });
  return { status: res.status(), body: norm(await res.text()) };
}
const standard404 = await refusedBody(`/admin/zz-no-such-page-${Date.now()}`);
check(standard404.status === 404 && standard404.body.length > 200, "baseline: an unknown admin address answers a 404 page", String(standard404.status));

// Mint a preview ticket the way the blog editor does.
await p.goto(`${base}/admin/entities/${postId}/edit`, { waitUntil: "load" });
const form = await p.evaluate(() => {
  const f = document.querySelector("form[data-hm-blog-editor]");
  return { url: f.dataset.hmPreview, body: new URLSearchParams([...new FormData(f)].filter(([k, v]) => typeof v === "string" && k !== "_op")).toString() };
});
const mint = async () => {
  const token = await csrf(p);
  return p.evaluate(async ({ url, body, token }) => {
    const r = await fetch(url, { method: "POST", credentials: "same-origin", headers: { "Content-Type": "application/x-www-form-urlencoded", "X-CSRF-Token": token, "X-HM-Stage": "1" }, body });
    return (await r.json()).url;
  }, { ...form, token });
};
const adminPage = `${base}/admin/profile`;
const publicPage = `${base}/`;

for (const [where, url] of [["an admin page", adminPage], ["a public page", publicPage]]) {
  await p.goto(url);
  const real = await mint();
  const noTicket = real.replace(/[^/]+$/, "A".repeat(32));
  const rb = await refusedBody(noTicket);
  check(rb.status === 404 && rb.body === standard404.body, `preview without a ticket: the body is the same refusal as an unknown admin address`);
  const r1 = await frameLoad(p, noTicket);
  check(isAdmin404(r1), `preview without a ticket from ${where}: the admin 404`, JSON.stringify({ s: r1.status }));
  const garbage = await frameLoad(p, real.replace(/[^/]+$/, "x"));
  check(isAdmin404(garbage), `preview with a malformed ticket from ${where}: the admin 404`, String(garbage.status));
  const first = await frameLoad(p, real);
  check(first.status === 200, `preview with its ticket from ${where}: loads once`, String(first.status));
  const second = await frameLoad(p, real);
  check(isAdmin404(second), `preview with a reused ticket from ${where}: the admin 404`, String(second.status));
}

// A ticket in a context with no session is useless, and the refused attempt does not spend it.
{
  await p.goto(adminPage);
  const real = await mint();
  const anon = await b.newContext({ bypassCSP: true });
  const res = await anon.request.get(new URL(real, base).href, { headers: { "Sec-Fetch-Dest": "iframe", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Site": "same-origin" } });
  check(res.status() === 404, "a preview ticket without the session cookie: 404", String(res.status()));
  await anon.close();
  const owner = await frameLoad(p, real);
  check(owner.status === 200, "the same ticket still loads for its own session (a refusal without a session spends nothing)", String(owner.status));
}

// Plugin UI documents: no ticket, then a spent ticket (the real frame consumed pluginSrc).
for (const [where, url] of [["an admin page", adminPage], ["a public page", publicPage]]) {
  await p.goto(url);
  const rb = await refusedBody("/plugin-ui/frameplug/index.html");
  check(rb.status === 404 && rb.body === standard404.body, "plugin document without a ticket: the same refusal body");
  const none = await frameLoad(p, "/plugin-ui/frameplug/index.html");
  check(isAdmin404(none), `plugin document without a ticket from ${where}: the admin 404`, String(none.status));
  const junk = await frameLoad(p, "/plugin-ui/frameplug/index.html?t=" + "A".repeat(32));
  check(isAdmin404(junk), `plugin document with a garbage ticket from ${where}: the admin 404`, String(junk.status));
  const used = await frameLoad(p, pluginSrc);
  check(isAdmin404(used), `plugin document with the spent ticket from ${where}: the admin 404`, String(used.status));
}
// A fresh ticket loads once, and its scripts and styles never need one.
await p.goto(`${base}/admin/plugins/frameplug/settings`);
const src2 = (await p.locator("[data-hm-pluginframe]").getAttribute("data-src")) || "";
await p.waitForTimeout(1000); // the real frame spends src2
const again = await frameLoad(p, src2);
check(isAdmin404(again), "a plugin ticket already used by its real frame: the admin 404", String(again.status));
const asset = await p.evaluate(
  () => new Promise((res) => {
    const s = document.createElement("script");
    s.src = "/plugin-ui/frameplug/app.js";
    s.onload = () => res("loaded");
    s.onerror = () => res("refused");
    document.head.append(s);
  }),
);
check(asset === "loaded", "plugin scripts stay public (no ticket)", asset);

await b.close();
console.log(bad ? `\n${bad} problem(s)` : "\nok");
process.exit(bad ? 1 : 0);
