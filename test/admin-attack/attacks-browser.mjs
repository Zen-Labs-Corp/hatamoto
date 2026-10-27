// AT-01 to AT-09: a script with site.scripts on a public page attacks the admin through the
// browser, in a context that holds a real owner session (the owner browsing their own site).
// The attack code lives in fixtures/plugins/attackplug/assets/plugin.js; this file triggers it,
// reads the result and runs the positive control that proves the probe could have seen success.
// Run against the server pinned at /admin, seeded, with attackplug granted site.scripts.
import { launch, newOwner, base, addr, record, finish } from "./lib.mjs";

const browser = await launch();
const { context, page: admin } = await newOwner(browser);
const pub = await context.newPage();
const pubUrl = `${base}/about#addr=${encodeURIComponent(addr)}`;
await pub.goto(pubUrl);
await pub.waitForFunction(() => window.__atk?.executed === true);
const atk = (fn, arg) => pub.evaluate(({ fn, arg }) => window.__atk[fn](...arg), { fn, arg });
// What only a real admin document carries: a non-empty token meta, the admin shell.
const adminMarkup = /name="csrf-token" content="[^"]+"|class="hm-shell"|class="hm-sidebar"/;

// Every document response for a frame request, as the browser saw it (status of the iframe load).
const frameStatuses = [];
for (const p of [admin, pub]) {
  p.on("response", (r) => {
    if (r.request().resourceType() === "document" && r.frame() !== p.mainFrame()) frameStatuses.push({ url: new URL(r.url()).pathname, status: r.status(), page: p === admin ? "admin" : "public" });
  });
}
const statusOf = (pathPrefix, from) => frameStatuses.filter((s) => s.page === from && s.url.startsWith(pathPrefix)).map((s) => s.status);

// A plain navigation to the admin works: the control for every "the admin refused" claim.
const nav = await admin.goto(`${base}${addr}/`);
const navOk = nav.status() === 200 && adminMarkup.test(await nav.text());
const token = await admin.getAttribute('meta[name="csrf-token"]', "content");
record("AT-00", "the granted plugin script runs on a public page", { ok: true, control: (await pub.evaluate(() => window.__atk.executed)) === true });

// AT-01
const names = (await atk("cookie", [])).split("; ").map((c) => c.split("=")[0]);
const visible = await context.cookies(base);
const hidden = visible.filter((c) => c.httpOnly).map((c) => c.name);
record("AT-01", "document.cookie shows no session or csrf cookie", {
  ok: !names.some((n) => /^(s|__Host-s|hm_pre|hatamoto_csrf|__Host-c|c)$/.test(n) || /csrf/i.test(n)),
  control: (await atk("ownCookie", [])) === true && hidden.length > 0,
  detail: `readable=[${names}] httpOnly=[${hidden}]`,
});

// AT-02
const html = await atk("fetchRaw", [`${addr}/`]);
record("AT-02", "credentialed fetch of the admin HTML", { ok: html.status === 404 && !adminMarkup.test(html.body), control: navOk, detail: `status=${html.status}` });

// AT-03: an admin route (htmx style) and a cookie-authenticated API route
const htmx = await atk("fetchRaw", [`${addr}/entities`, { "HX-Request": "true" }]);
const api = await atk("fetchRaw", [`/api/v1/entities`]);
const htmxCtl = await admin.evaluate(async ({ u, t }) => (await fetch(u, { credentials: "include", headers: { "HX-Request": "true", "X-CSRF-Token": t } })).status, { u: `${addr}/entities`, t: token });
const bearerTok = await (await import("./lib.mjs")).bearer();
const apiCtl = (await fetch(`${base}/api/v1/entities`, { headers: { Authorization: `Bearer ${bearerTok}` } })).status;
record("AT-03", "credentialed fetch of an admin route and an API route", {
  ok: htmx.status === 404 && api.status === 401 && !adminMarkup.test(htmx.body),
  control: htmxCtl === 200 && apiCtl === 200,
  detail: `admin=${htmx.status} api=${api.status} ctl=${htmxCtl}/${apiCtl}`,
});

// AT-04: after an admin navigation, nothing admin is in the HTTP cache
await admin.goto(`${base}${addr}/`);
const cached = await atk("onlyIfCached", [`${addr}/`]);
await pub.goto(pubUrl);
await pub.waitForFunction(() => window.__atk?.executed === true);
const cachedPublic = await atk("onlyIfCached", ["/about"]);
record("AT-04", "only-if-cached fetch of the admin", { ok: cached.status !== 200 && !adminMarkup.test(cached.body), control: cachedPublic.status === 200 && /<html/i.test(cachedPublic.body), detail: `status=${cached.status} ${cached.error ?? ""}`.trim() });

// AT-05
const f1 = await atk("frame", [`${addr}/`]);
const f1ctl = await atk("frame", ["/about"]);
record("AT-05", "iframe of the admin", { ok: !(f1.readable && adminMarkup.test(f1.text)) && !statusOf(`${addr}/`, "public").includes(200), control: navOk && f1ctl.readable && /<html/i.test(f1ctl.text) && !adminMarkup.test(f1ctl.text), detail: `readable=${f1.readable} statuses=${statusOf(`${addr}/`, "public")}` });

// Entity ids the frame attacks need.
const idOf = async (q) => {
  await admin.goto(`${base}${addr}/entities?q=${encodeURIComponent(q)}`);
  const hrefs = await admin.$$eval('a[href*="/entities/"]', (as, t) => as.filter((a) => a.textContent.includes(t)).map((a) => a.getAttribute("href")), q);
  return hrefs.map((h) => h.match(/entities\/([0-9a-f-]{36})/)?.[1]).find(Boolean);
};
const aboutId = await idOf("About");
const postId = await idOf("QA Post 1");

// AT-06: plugin UI frame without a ticket; control: the real frame of the plugin page loads with its ticket
await admin.goto(`${base}${addr}/plugins/uiplug/settings`);
await admin.waitForSelector("iframe.hm-pluginframe", { state: "attached" });
await admin.waitForTimeout(1000);
const uiCtl = statusOf("/plugin-ui/uiplug/index.html", "admin").includes(200);
const f6 = await atk("frame", ["/plugin-ui/uiplug/index.html"]);
const f6b = await atk("frame", ["/plugin-ui/uiplug/index.html?t=" + "A".repeat(32)]);
const s6 = statusOf("/plugin-ui/uiplug/index.html", "public");
record("AT-06", "iframe of a plugin UI frame without a ticket", { ok: s6.length === 2 && s6.every((s) => s === 404) && !/ui frame loaded/.test(f6.text + f6b.text), control: uiCtl, detail: `statuses=${s6}` });

// AT-07: builder canvas; the real canvas frame loads with its ticket, a copy of that URL (spent) and a made-up ticket do not
const canvasUrls = [];
admin.on("framenavigated", (f) => { if (/\/edit\/preview\//.test(f.url())) canvasUrls.push(new URL(f.url()).pathname); });
await admin.goto(`${base}${addr}/entities/${aboutId}/edit`, { waitUntil: "load" });
await admin.waitForSelector('[data-hm-bs-tree] [role="treeitem"]', { timeout: 15000 });
await admin.waitForTimeout(2000);
const canvasCtl = canvasUrls.length > 0 && statusOf(`${addr}/entities/${aboutId}/edit/preview/`, "admin").every((s) => s === 200);
const spent = canvasUrls[0] ?? `${addr}/entities/${aboutId}/edit/preview/x`;
const guess = `${addr}/entities/${aboutId}/edit/preview/${"A".repeat(32)}`;
await atk("frame", [guess]);
await atk("frame", [spent]);
const s7 = statusOf(`${addr}/entities/${aboutId}/edit/preview/`, "public");
record("AT-07", "iframe of the builder canvas without a valid ticket", { ok: s7.length === 2 && s7.every((s) => s === 404), control: canvasCtl, detail: `statuses=${s7}` });

// AT-08: blog preview, the same ticket route for a post
await admin.goto(`${base}${addr}/entities/${postId}/edit`, { waitUntil: "load" });
await admin.locator("[data-hm-be-preview-toggle]").click();
await admin.waitForSelector("[data-hm-be-stage] iframe.hm-be__frame", { timeout: 10000 });
await admin.waitForTimeout(1500);
const previewCtl = statusOf(`${addr}/entities/${postId}/edit/preview/`, "admin").includes(200);
await atk("frame", [`${addr}/entities/${postId}/edit/preview/${"A".repeat(32)}`]);
const s8 = statusOf(`${addr}/entities/${postId}/edit/preview/`, "public");
record("AT-08", "iframe of the blog preview without a ticket", { ok: s8.length === 1 && s8[0] === 404, control: previewCtl, detail: `statuses=${s8}` });

// AT-09: window.open from a click, then read the handle
async function openAndRead(url) {
  await pub.goto(pubUrl);
  await pub.waitForFunction(() => window.__atk?.executed === true);
  await pub.evaluate((u) => { window.__atk.openUrl = u; window.__atk.win = null; }, url);
  const popup = pub.waitForEvent("popup");
  await pub.click("#atk-open");
  const p = await popup;
  await p.waitForLoadState("load"); // read only after the popup finished loading, never on a timer
  const out = await pub.evaluate(() => window.__atk.readOpened());
  await p.close();
  return out;
}
const w = await openAndRead(`${base}${addr}/`);
const wctl = await openAndRead(`${base}/about`);
record("AT-09", "window.open of the admin, read document and location", { ok: w.document !== "readable" && !/csrf|login|entities/.test(String(w.location)) && (w.closed === true || w.document === "SecurityError"), control: wctl.document === "readable" && /\/about/.test(wctl.location), detail: JSON.stringify(w) });

await browser.close();
finish();
