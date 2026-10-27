// AT-10 to AT-15: forged and replayed POSTs, planted recovery copies, service workers.
// Same setup as attacks-browser.mjs: the attacks live in fixtures/plugins/attackplug/assets/plugin.js
// and run on a public page in a context that holds an owner session.
// The unsafe admin route is POST <admin>/entities/{id}/edit/recovery/discard (a real, harmless
// admin action: it answers 204 when the guard lets it through, 403 when the token rule blocks it).
import { launch, newOwner, base, addr, record, finish } from "./lib.mjs";

const browser = await launch();
const { context, page: admin } = await newOwner(browser);
const pub = await context.newPage();
const pubUrl = `${base}/about#addr=${encodeURIComponent(addr)}`;
const loadPublic = async () => {
  await pub.goto(pubUrl);
  await pub.waitForFunction(() => window.__atk?.executed === true);
};
await loadPublic();
const atk = (fn, ...arg) => pub.evaluate(({ fn, arg }) => window.__atk[fn](...arg), { fn, arg });

const idOf = async (q) => {
  await admin.goto(`${base}${addr}/entities?q=${encodeURIComponent(q)}`);
  const hrefs = await admin.$$eval('a[href*="/entities/"]', (as, t) => as.filter((a) => a.textContent.includes(t)).map((a) => a.getAttribute("href")), q);
  return hrefs.map((h) => h.match(/entities\/([0-9a-f-]{36})/)?.[1]).find(Boolean);
};
const postId = await idOf("QA Post 1");
const aboutId = await idOf("About");
const target = `${addr}/entities/${postId}/edit/recovery/discard`;
const metaToken = (p) => p.getAttribute('meta[name="csrf-token"]', "content");

// ---- AT-10 to AT-12: POST without, with a guessed and with a replayed token ---------------------
await admin.goto(`${base}${addr}/entities/${postId}/edit`);
const ownToken = await metaToken(admin);
const ctl = await admin.evaluate(async ({ u, t }) => (await fetch(u, { method: "POST", credentials: "same-origin", headers: { "X-CSRF-Token": t } })).status, { u: target, t: ownToken });
const ctlOk = ctl === 204;

const none = await atk("post", target, null);
record("AT-10", "same-origin POST to an admin action without a token", { ok: none.status === 403, control: ctlOk, detail: `status=${none.status} control=${ctl}` });

const guesses = ["0".repeat(43), "AAAA".repeat(11), "' OR 1=1 --", ownToken.slice(0, -1) + (ownToken.endsWith("A") ? "B" : "A")];
const guessed = [];
for (const g of guesses) guessed.push((await atk("post", target, g)).status);
record("AT-11", "POST with guessed tokens", { ok: guessed.every((s) => s === 403), control: ctlOk, detail: `statuses=${guessed}` });

// The token of another owner session is a valid token, for that session only.
const other = await newOwner(browser);
await other.page.goto(`${base}${addr}/entities/${postId}/edit`);
const otherToken = await metaToken(other.page);
const replay = await atk("post", target, otherToken);
const otherCtl = await other.page.evaluate(async ({ u, t }) => (await fetch(u, { method: "POST", credentials: "same-origin", headers: { "X-CSRF-Token": t } })).status, { u: target, t: otherToken });
record("AT-12", "POST replaying a token taken from another session", { ok: replay.status === 403 && otherToken !== ownToken, control: ctlOk && otherCtl === 204, detail: `status=${replay.status} otherSessionControl=${otherCtl}` });

// ---- AT-13: planted recovery copies --------------------------------------------------------------
// The copies the browser-storage autosave used to restore: key hm:blog:<user>:<entity>.
await admin.goto(`${base}${addr}/entities/${postId}/edit`);
const { user, id } = await admin.$eval("#hm-be", (f) => ({ user: f.dataset.hmUser, id: f.dataset.hmEntity }));
await admin.goto(`${base}${addr}/entities/${aboutId}/edit?qa=1`);
await admin.waitForFunction(() => !!window.__hmBuilder, null, { timeout: 15000 });
const boot = await admin.evaluate(() => JSON.parse(document.getElementById("hm-builder-boot").textContent));
const blogKey = `hm:blog:${user}:${id}`;
const builderKey = `hm:blog:${boot.user}:${boot.entity}`;
await loadPublic();
await atk("plant", [
  [blogKey, JSON.stringify({ content: "<p>PLANTED COPY</p>", version: 1, name: "Planted title", excerpt: "", at: Date.now() })],
  [builderKey, JSON.stringify({ content: JSON.stringify({ v: 2, nodes: [] }), version: boot.version, name: "Planted page", at: Date.now() })],
]);
const plantedKeys = (await atk("stored", "hm:blog:")).length;
await admin.goto(`${base}${addr}/entities/${postId}/edit`);
await admin.waitForSelector("#hm-be");
await admin.waitForTimeout(800);
const blogBanner = await admin.locator("[data-hm-be-recover]").isHidden();
const blogText = await admin.inputValue("#hm-be [name=content]");
const blogName = await admin.inputValue("#hm-be [name=name]");
const blogKeyLeft = await admin.evaluate((k) => localStorage.getItem(k), blogKey);
await admin.goto(`${base}${addr}/entities/${aboutId}/edit?qa=1`);
await admin.waitForFunction(() => !!window.__hmBuilder, null, { timeout: 15000 });
await admin.waitForTimeout(1000);
const builderBanner = (await admin.locator("text=kept for you").count()) === 0;
const builderNodes = await admin.evaluate(() => window.__hmBuilder.doc().nodes.length);
const builderName = await admin.inputValue("[data-hm-bs-title]");
const builderKeyLeft = await admin.evaluate((k) => localStorage.getItem(k), builderKey);

// Control: a copy the admin itself kept on the server is offered in a second session.
await admin.goto(`${base}${addr}/entities/${postId}/edit`);
await admin.waitForSelector("#hm-be");
const wrote = admin.waitForResponse((r) => r.url().includes("/edit/recovery") && r.request().method() === "POST" && !r.url().endsWith("/discard"), { timeout: 10000 });
await admin.fill("#hm-be [name=name]", "Kept by the server");
const writeStatus = (await wrote).status();
const second = await newOwner(browser);
await second.page.goto(`${base}${addr}/entities/${postId}/edit`);
const offered = await second.page.locator("[data-hm-be-recover]").waitFor({ state: "visible", timeout: 5000 }).then(() => true, () => false);
if (offered) await second.page.click("[data-hm-be-recover-drop]");
record("AT-13", "planted recovery copies are not offered and are deleted", {
  ok: plantedKeys === 2 && blogBanner && !blogText.includes("PLANTED COPY") && blogName !== "Planted title" && blogKeyLeft === null && builderBanner && builderNodes > 0 && builderName !== "Planted page" && builderKeyLeft === null,
  control: plantedKeys === 2 && writeStatus === 204 && offered,
  detail: `planted=${plantedKeys} blogBannerHidden=${blogBanner} blogKeyLeft=${blogKeyLeft !== null} builderKeyLeft=${builderKeyLeft !== null} serverCopyOffered=${offered}`,
});

// ---- AT-14, AT-15: service workers ---------------------------------------------------------------
// Observed (Chromium, this suite): see the detail on the PASS lines. The assertion is on state:
// no registration may exist whose scope is the site root or lies under the admin address.
const covers = (scope) => {
  const p = new URL(scope).pathname;
  return p === "/" || p === addr || p.startsWith(addr + "/");
};
await loadPublic();
const asset = "/p/attackplug/assets/sw.js";
const swFetch = await atk("fetchRaw", asset);
const swCtl = swFetch.status === 200 && /addEventListener/.test(swFetch.body);
const noScripts = await atk("registerSw", "/sw.js", "/");
const rootScope = await atk("registerSw", asset, "/");
const adminScope = await atk("registerSw", asset, `${addr}/`);
const states = [noScripts, rootScope, adminScope];
record("AT-14", "service worker registered with scope / or the admin address", {
  ok: states.every((s) => s.outcome !== "registered" && !s.scopes.some(covers)),
  control: swCtl,
  detail: `/sw.js=${noScripts.outcome} asset@/=${rootScope.outcome} asset@admin=${adminScope.outcome}`,
});
const own = await atk("registerSw", asset, undefined);
await atk("unregisterAll");
const ref = await context.request.get(`${base}${addr}/`, { headers: { "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "document", "Sec-Fetch-Site": "none", "Sec-Fetch-User": "?1" }, maxRedirects: 0 });
const swHdr = await context.request.get(`${base}${addr}/`, { headers: { "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "document", "Sec-Fetch-Site": "none", "Sec-Fetch-User": "?1", "Service-Worker": "script" }, maxRedirects: 0 });
record("AT-15", "service worker at its own asset path, and a worker fetch of the admin", {
  ok: !own.scopes.some(covers) && swHdr.status() === 404,
  control: swCtl && ref.status() === 200 && Array.isArray(own.scopes),
  detail: `own=${own.outcome} scopes=${own.scopes.join(",")} serviceWorkerHeaderOnAdmin=${swHdr.status()} withoutHeader=${ref.status()}`,
});

await browser.close();
finish();
