// AT-16 to AT-21: can a script on a public page, or a scanner, find or confirm the admin address?
// Run against the server pinned to a RANDOM address (ADMIN_ADDR=/<random>, same database as the
// pinned /admin run). The page is not told the address. With the pin at /admin most of these
// would be meaningless (everything under /admin passes the address filter), hence this run.
import { launch, newOwner, base, addr, raw, record, finish } from "./lib.mjs";

if (addr === "/admin") throw new Error("attacks-discovery needs a random ADMIN_ADDR, not /admin");
const segment = addr.slice(1);
const nav = { "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "document", "Sec-Fetch-Site": "none", "Sec-Fetch-User": "?1" };

const browser = await launch();
const { context, page: admin } = await newOwner(browser);
const pub = await context.newPage();
const loadPublic = async (path = "/about") => {
  await pub.goto(base + path);
  await pub.waitForFunction(() => window.__atk?.executed === true);
};
await loadPublic();
const atk = (fn, ...arg) => pub.evaluate(({ fn, arg }) => window.__atk[fn](...arg), { fn, arg });
const adminToken = async () => {
  await admin.goto(`${base}${addr}/`);
  return admin.getAttribute('meta[name="csrf-token"]', "content");
};
const realAdmin = (await adminToken()) !== "";

// ---- AT-16: Referer and document.referrer after leaving the admin ---------------------------
const seenReferers = [];
context.on("request", async (r) => {
  const u = new URL(r.url());
  if (u.origin === new URL(base).origin && (u.pathname === "/" || u.pathname === "/about") && r.isNavigationRequest()) {
    // A request that finished before its headers were read must not stop the run.
    seenReferers.push((await r.allHeaders().catch(() => ({}))).referer ?? "");
  }
});
await admin.goto(`${base}${addr}/`);
const popupP = admin.waitForEvent("popup");
await admin.click('a[href="/"][target="_blank"], a[target="_blank"][href^="http"]');
const popup = await popupP;
await popup.waitForLoadState("load");
const leftRef = await popup.evaluate(() => document.referrer);
const leftOpener = await popup.evaluate(() => window.opener === null);
await popup.close();
// The same move in the admin's own tab (rel and target do not matter to the policy): follow a public link.
await admin.goto(`${base}${addr}/`);
await admin.$eval('a[target="_blank"]', (a) => a.removeAttribute("target"));
await Promise.all([admin.waitForURL(`${base}/`), admin.click('a[href="/"]')]);
const sameTabRef = await admin.evaluate(() => document.referrer);
// Control: a link followed on a public page does carry a referrer.
await loadPublic("/about");
const link = await pub.$eval('a[href^="/"]:not([href^="//"]):not([href="/about"])', (a) => a.getAttribute("href"));
await pub.click(`a[href="${link}"]`);
await pub.waitForLoadState("load");
const publicRef = await pub.evaluate(() => document.referrer);
record("AT-16", "no Referer and no document.referrer after leaving the admin", {
  ok: leftRef === "" && sameTabRef === "" && seenReferers.every((h) => !h.includes(segment)),
  // seenReferers.every is vacuous on an empty list: the listener must have seen both navigations.
  control: /\/about$/.test(publicRef) && realAdmin && seenReferers.length >= 2,
  detail: `seen=${seenReferers.length} popup=${JSON.stringify(leftRef)} sameTab=${JSON.stringify(sameTabRef)} publicLink=${JSON.stringify(publicRef)}`,
});

// A window the admin opens onto the public site has no way back to the admin tab.
record("AT-16b", "a window the admin opens onto the public site has window.opener === null", {
  ok: leftOpener === true,
  control: /\/$/.test(new URL(popup.url()).pathname) && leftRef === "" && realAdmin,
  detail: `opener=${leftOpener ? "null" : "set"}`,
});

// ---- AT-16c: the signed-in owner's public pages carry no admin address --------------------------
// The page DOM, the HTML, the feeds and the search results, fetched with the owner's session cookie.
const ownerFetch = async (path) => {
  const r = await context.request.get(base + path, { headers: nav, maxRedirects: 0 });
  return { path, status: r.status(), body: await r.text() };
};
const publicPaths = ["/", "/about", "/feed.xml", "/atom.xml", "/search?q=about", "/sitemap.xml"];
const owned = [];
for (const path of publicPaths) owned.push(await ownerFetch(path));
await pub.goto(base + "/about");
const dom = await pub.evaluate(() => document.documentElement.outerHTML);
const withSegment = [...owned.filter((o) => o.body.includes(segment)).map((o) => o.path), ...(dom.includes(segment) ? ["DOM /about"] : [])];
// Control: the owner is signed in (the admin page carries its own address) and every public page answered.
const adminHtml = await (await context.request.get(`${base}${addr}/`, { headers: nav })).text();
record("AT-16c", "a signed-in owner's public HTML, DOM, feeds and search carry no admin address", {
  ok: withSegment.length === 0,
  control: realAdmin && adminHtml.includes(segment) && owned.every((o) => o.status === 200 && o.body.length > 100) && dom.length > 100,
  detail: withSegment.length ? "address found in " + withSegment.join(", ") : `${owned.length} responses and the DOM clean, statuses ${owned.map((o) => o.status)}`,
});

// ---- AT-17, AT-18: the edit link /-/edit?id=<uuid> ---------------------------------------------
const bearerTok = await (await import("./lib.mjs")).bearer();
const list = await (await fetch(`${base}/api/v1/entities?type=page&limit=5`, { headers: { Authorization: `Bearer ${bearerTok}` } })).json();
const id = (list.entities ?? list.data ?? list.items ?? list).find?.((e) => e.id)?.id;
if (!id) throw new Error("no entity id from the API: " + JSON.stringify(list).slice(0, 200));
const edit = `/-/edit?id=${id}`;
const real = await context.request.get(base + edit, { headers: nav, maxRedirects: 0 });
const realOk = real.status() === 303 && real.headers().location === `${addr}/entities/${id}/edit`;
await loadPublic();
const viaFetch = await atk("fetchManual", edit);
record("AT-17", "fetch of the edit link /-/edit", { ok: viaFetch.status === 404, control: realOk, detail: `fetch=${viaFetch.status}/${viaFetch.type} navigation=${real.status()} ${real.headers().location ?? ""}` });
const frameStatuses = [];
pub.on("response", (r) => { if (r.request().resourceType() === "document" && r.frame() !== pub.mainFrame() && r.url().includes("/-/edit")) frameStatuses.push(r.status()); });
const framed = await atk("frame", edit);
record("AT-18", "iframe of the edit link /-/edit", { ok: frameStatuses.length === 1 && frameStatuses[0] === 404 && !framed.text.includes(segment), control: realOk, detail: `frame=${frameStatuses}` });

// ---- AT-19: robots.txt and sitemap.xml ----------------------------------------------------------
const robots = await atk("fetchRaw", "/robots.txt");
const sitemap = await atk("fetchRaw", "/sitemap.xml");
const full = await raw("/sitemap.xml");
record("AT-19", "robots.txt and sitemap.xml do not mention the admin", {
  ok: !robots.body.includes(segment) && !/admin/i.test(robots.body) && !full.body.includes(segment),
  control: robots.status === 200 && /user-agent/i.test(robots.body) && sitemap.status === 200 && /<(urlset|sitemapindex)/.test(full.body),
  detail: `robots=${robots.status} sitemap=${sitemap.status}`,
});

// ---- AT-20: error pages ---------------------------------------------------------------------------
const rnd = () => "/zz-" + Math.random().toString(36).slice(2, 10);
// The page echoes the path that was asked for (canonical URL), so that path is removed before looking.
const asked = [rnd(), "/admin/anything", `${addr}/no-such-page-${Date.now()}`];
const pages = [];
for (const path of asked) {
  const r = await atk("fetchRaw", path);
  pages.push({ ...r, body: r.body.replaceAll(path, "") });
}
record("AT-20", "error pages carry no admin address or token", {
  ok: pages.every((p) => p.status === 404 && !p.body.includes(segment) && !/name="csrf-token" content="[^"]+"/.test(p.body)),
  control: (await adminToken()) !== "",
  detail: `statuses=${pages.map((p) => p.status)}`,
});

// ---- AT-21: /admin and its neighbours answer exactly what a random path answers ---------------
// Compared byte for byte (status, header names and order, header values, body) with only the Date
// header, nonces and the page's own URL (canonical, og:url) masked.
const cookie = (await context.cookies(base)).map((c) => `${c.name}=${c.value}`).join("; ");
const mask = (r, path) => ({
  status: r.status,
  headers: r.raw.map((v, i) => (i % 2 === 0 ? v.toLowerCase() : /^(date|x-request-id)$/i.test(r.raw[i - 1]) ? "<varies>" : v)),
  // The only request-derived bytes are the page's own URL (canonical link, og:url), normalised by the server.
  body: r.body.replaceAll(path, "<path>").replace(/(rel="canonical" href|property="og:url" content)="[^"]*"/g, '$1="<url>"').replace(/nonce="[^"]*"/g, 'nonce=""').replace(/(nonce-)[A-Za-z0-9+/=_-]+/g, "$1N"),
});
const diffs = [];
for (const withCookie of [false, true]) {
  const h = withCookie ? { cookie } : {};
  const rp = rnd();
  const ref = mask(await raw(rp, { headers: h }), rp);
  for (const p of ["/admin", "/admin/", "/admin/login", "/admin/entities", "/admin/settings", "/Admin", "/admin%2flogin", "/admin/.env"]) {
    const got = mask(await raw(p, { headers: h }), p);
    if (JSON.stringify(got) !== JSON.stringify(ref)) diffs.push(`${p} cookie=${withCookie}`);
  }
}
const realLogin = await raw(`${addr}/login`, { headers: nav });
const refNoCookie = mask(await raw(rnd()), "");
record("AT-21", "/admin and neighbours are byte-identical to a random 404", {
  ok: diffs.length === 0,
  control: realLogin.status === 200 && JSON.stringify(mask(realLogin, "")) !== JSON.stringify(refNoCookie),
  detail: diffs.length ? "differs: " + diffs.join(", ") : `${16} probes identical, real login ${realLogin.status}`,
});

await browser.close();
finish();
