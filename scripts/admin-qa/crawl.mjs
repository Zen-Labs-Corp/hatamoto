// Unknown-route crawl: every /admin page reachable from the sidebar, sub navs and tab rows
// must be in urls.mjs (or in its crawlIgnore list with a reason), so a new page cannot escape
// the QA harness. As owner and editor at 1440, BFS from /admin/entities that collects links on every
// scanned page (bounded by MAX_PAGES, and by the finite set of nav links). Exits 1 on any
// reachable path missing from the inventory, or when nothing was scanned.
// BASE=http://127.0.0.1:8191 node scripts/admin-qa/crawl.mjs
// node scripts/admin-qa/crawl.mjs --self-test   (no server: a link to /admin/ghost must be reported)
// error-404 is deliberately an unknown route (status 404): it is never a crawl target.
import { launch, newSession, base, password } from "./lib.mjs";
import { screens, crawlIgnore } from "./urls.mjs";

const selfTest = process.argv.includes("--self-test");
const EDITOR_EMAIL = process.env.QA_EDITOR_EMAIL || "editor@qa.test";
const OWNER_EMAIL = process.env.QA_EMAIL || "owner@qa.test";
const MAX_PAGES = 200; // the nav link set is finite; this only stops a runaway (e.g. a link that mints new paths)
const LINKS = "nav#hm-sidebar a.hm-nav-link, a.hm-sidebar__me, nav.hm-subnav a.hm-subnav__link, nav.hm-tabs a[href]";
const KEEP = new Set(["type", "tab", "form"]);
const UUID = /[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/g;

// The comparison key of a path: ids (UUIDs and {token}s) become {id}, only type|tab|form stay in the query.
export function normalise(href, origin = "http://qa.test") {
  const raw = href.replace(/\{\w+\}/g, "00000000-0000-0000-0000-000000000000");
  const u = new URL(raw, origin);
  if (u.origin !== new URL(origin).origin || !/^\/admin(\/|$)/.test(u.pathname) || u.pathname === "/admin/logout") return null;
  const q = [...u.searchParams].filter(([k]) => KEEP.has(k)).map(([k, v]) => `${k}=${v.replace(UUID, "{id}")}`).sort().join("&");
  return u.pathname.replace(UUID, "{id}").replace(/\/$/, "") + (q ? "?" + q : "");
}

const known = new Set(screens.filter((s) => s.status !== 404).map((s) => normalise(s.path)));
// A type=/tab= variant of an inventoried path is the same route: tabs and type filters are not new pages.
const isKnown = (k) => known.has(k) || (/\?(?:type|tab)=[^&]*$/.test(k) && known.has(k.replace(/\?.*$/, "")));
const ignored = (key) => crawlIgnore.find((i) => i.re.test(key));
// Paths that are neither inventoried nor ignored. error-404 is not a target: it is unknown on purpose.
export const unknownPaths = (keys) => [...new Set(keys)].filter((k) => !isKnown(k) && !ignored(k)).sort();

async function links(page) {
  return page.$$eval(LINKS, (as) => as.map((a) => a.getAttribute("href")).filter((h) => h && !h.startsWith("#") && !h.startsWith("mailto:")));
}

if (selfTest) {
  const b = await launch();
  const page = await (await b.newContext()).newPage();
  await page.setContent('<nav id="hm-sidebar"><a class="hm-nav-link" href="/admin/ghost">Ghost</a><a class="hm-nav-link" href="/admin/entities">Entities</a><a class="hm-nav-link" href="/admin/logout">Out</a><a class="hm-nav-link" href="https://example.com/admin/x">Ext</a></nav>');
  const keys = (await links(page)).map((h) => normalise(h)).filter(Boolean);
  // BFS over a fake three-level site: sidebar -> settings -> subnav page -> tab row page.
  const O = "http://qa.test", nav = (cls, hrefs) => `<nav class="${cls}">${hrefs.map((h) => `<a href="${h}" class="hm-nav-link hm-subnav__link">x</a>`).join("")}</nav>`;
  const site = {
    "/admin/entities": '<nav id="hm-sidebar"><a class="hm-nav-link" href="/admin/settings/general">S</a></nav>',
    "/admin/settings/general": nav("hm-subnav", ["/admin/settings/redirects"]),
    "/admin/settings/redirects": nav("hm-tabs", ["/admin/settings/redirects/explain"]),
    "/admin/settings/redirects/explain": "<p>leaf</p>",
  };
  const ctx = await b.newContext();
  await ctx.route(`${O}/**`, (route) => {
    const p = new URL(route.request().url()).pathname;
    return site[p] ? route.fulfill({ status: 200, contentType: "text/html", body: site[p] }) : route.fulfill({ status: 404, body: "" });
  });
  const crawled = await crawlFrom(await ctx.newPage(), O, "/admin/entities", () => {});
  await b.close();
  const got = unknownPaths(keys);
  const checks = [
    [got.join() === "/admin/ghost", `expected exactly /admin/ghost, got ${got.join() || "nothing"}`],
    [normalise("/admin/entities/3f2b8c1e-0000-4000-8000-000000000001/edit?outline=1") === "/admin/entities/{id}/edit", "uuid and query normalisation"],
    [normalise("/admin/settings/submissions?form={form}") === normalise("/admin/settings/submissions?form=3f2b8c1e-0000-4000-8000-000000000001"), "token and uuid agree"],
    [crawled.scanned === 4 && crawled.broken === 0, `BFS should scan 4 pages across three nav levels, got ${crawled.scanned} (${crawled.broken} broken)`],
    [crawled.seen.has("/admin/settings/redirects/explain") && crawled.reached.has("/admin/settings/redirects/explain"), "a tab row on a depth-2 page must be followed"],
    [crawlIgnore.every((i) => i.re instanceof RegExp && i.reason), "crawlIgnore entries need re and reason"],
    [unknownPaths([normalise("/admin/this-page-does-not-exist")]).length === 1, "error-404 is not a known route: a link to it must be reported"],
  ];
  const bad = checks.filter(([ok]) => !ok);
  for (const [, msg] of bad) console.log(`self-test FAILED: ${msg}`);
  if (bad.length) process.exit(1);
  console.log("self-test ok");
  process.exit(0);
}

async function signIn(page, email) {
  await page.goto(`${base}/admin/login`);
  await page.fill("#email", email);
  await page.fill("#password", password);
  await Promise.all([page.waitForURL((u) => !u.pathname.endsWith("/admin/login")), page.click('form[action="/admin/login"] button[type=submit]')]);
  if (page.url().includes("/admin/login")) throw new Error(`login failed for ${email}`);
}

// crawlFrom walks the navigation from `start`, collecting links on every scanned page so tab rows
// on sub pages are followed. Returns the visited keys, the first href that reached each key,
// and the scanned/broken counts. Throws past MAX_PAGES.
export async function crawlFrom(page, origin, start, log = console.log, role = "") {
  const seen = new Set(), reached = new Map(); // key -> first href that reached it
  let scanned = 0, broken = 0;
  const queue = [start];
  while (queue.length) {
    const href = queue.shift();
    const key = normalise(href, origin);
    if (!key || seen.has(key)) continue;
    if (seen.size >= MAX_PAGES) throw new Error(`crawl exceeded ${MAX_PAGES} pages; is a link minting new paths?`);
    seen.add(key);
    const res = await page.goto(origin + href, { waitUntil: "load" });
    // A redirect (/admin/settings) is judged by where it lands.
    const landed = normalise(page.url(), origin);
    if (landed !== key) { reached.delete(key); seen.add(landed); reached.set(landed, href); }
    if (!res || res.status() >= 400) { broken++; log(`FAIL ${role}: ${href} answered ${res?.status() ?? "nothing"}`); continue; } // a failing page has no links to follow
    scanned++;
    for (const h of await links(page)) {
      const k = normalise(h, origin);
      if (!k || seen.has(k)) continue;
      const u = new URL(h, origin + href);
      queue.push(u.pathname + u.search);
      if (!reached.has(k)) reached.set(k, h);
    }
  }
  return { seen, reached, scanned, broken };
}

const b = await launch();
let scanned = 0, missing = 0, broken = 0;
for (const [role, email] of [["owner", OWNER_EMAIL], ["editor", EDITOR_EMAIL]]) {
  const page = await newSession(b, { width: 1440, signedIn: false });
  await signIn(page, email);
  const r = await crawlFrom(page, base, "/admin/entities", console.log, role);
  scanned += r.scanned; broken += r.broken;
  const unknown = unknownPaths([...r.reached.keys()]);
  for (const k of unknown) { missing++; console.log(`MISSING ${role}: ${k} (reached via ${r.reached.get(k) || "start"}) is not in urls.mjs; add a screen or a crawlIgnore entry`); }
  console.log(`${role}: ${r.seen.size} paths visited, ${unknown.length} missing`);
  await page.context().close();
}
await b.close();
console.log(`base=${base}: ${scanned} pages scanned, ${missing} missing, ${broken} failing`);
if (scanned === 0) console.log("no pages were scanned");
process.exit(scanned > 0 && missing === 0 && broken === 0 ? 0 : 1);
