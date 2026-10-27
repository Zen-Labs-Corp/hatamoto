// AT-22: a script with site.scripts drives the admin editor from inside an editor render.
// The attack code lives in fixtures/plugins/attackplug/assets/plugin.js (it runs only when the URL is
// an editor preview: builder canvas, blog preview frame, no-JS preview tab) and posts select, Delete
// and save messages to its parent and opener. The owner only opens the editors.
// Expected: no save, the published page byte for byte unchanged, the editor document unchanged.
// Controls: the same plugin script runs on the public page, and the real bridge selects a section.
// Run against the server pinned at /admin, seeded, with attackplug granted site.scripts.
import { launch, newOwner, raw, base, addr, record, finish } from "./lib.mjs";

const browser = await launch();
const { context, page: admin } = await newOwner(browser);
const pluginScript = "/p/attackplug/assets/plugin.js";

// Every write the owner's tab sends, and every editor render it receives.
const posts = [];
admin.on("request", (r) => { if (r.method() === "POST") posts.push(new URL(r.url()).pathname); });
const renders = [];
admin.on("response", async (r) => {
  if (r.request().resourceType() !== "document" || !/\/edit\/preview(\/|$)/.test(new URL(r.url()).pathname)) return;
  const headers = await r.allHeaders().catch(() => ({}));
  renders.push({ path: new URL(r.url()).pathname, status: r.status(), csp: headers["content-security-policy"] ?? "", body: await r.text().catch(() => "") });
});
const saves = () => posts.filter((p) => p.endsWith("/builder/save"));

const idOf = async (q) => {
  await admin.goto(`${base}${addr}/entities?q=${encodeURIComponent(q)}`);
  const hrefs = await admin.$$eval('a[href*="/entities/"]', (as, t) => as.filter((a) => a.textContent.includes(t)).map((a) => a.getAttribute("href")), q);
  return hrefs.map((h) => h.match(/entities\/([0-9a-f-]{36})/)?.[1]).find(Boolean);
};
const aboutId = await idOf("About");
const postId = await idOf("QA Post 1");

// Control for every part: the plugin script is on the public page and runs there.
const pub = await context.newPage();
await pub.goto(`${base}/about`);
const ran = await pub.waitForFunction(() => window.__atk?.executed === true, null, { timeout: 8000 }).then(() => true, () => false);
const aboutBefore = await raw("/about");
const scriptOnPublic = aboutBefore.body.includes(pluginScript);
const pluginRuns = ran && scriptOnPublic;
await pub.close();

// ---- AT-22a: builder canvas of a published page --------------------------------------------------
// The visible canvas frame (the builder keeps a second, hidden one for the next render).
const frameOf = async () => {
  for (const f of admin.frames()) {
    if (f === admin.mainFrame() || !/\/edit\/preview\//.test(f.url())) continue;
    if (await (await f.frameElement()).isVisible()) return f;
  }
  return undefined;
};
await admin.goto(`${base}${addr}/entities/${aboutId}/edit?qa=1`, { waitUntil: "load" });
await admin.waitForSelector('[data-hm-bs-tree] [role="treeitem"]', { timeout: 15000 });
await admin.waitForFunction(() => !!window.__hmBuilder, null, { timeout: 15000 });
const docOf = () => admin.evaluate(() => JSON.stringify(window.__hmBuilder.doc()));
const selOf = () => admin.evaluate(() => JSON.stringify(window.__hmBuilder.selected()));
const docBefore = await docOf();
const selBefore = await selOf();
const savesBefore = saves().length;
await admin.waitForTimeout(5000); // longer than the attack timers in plugin.js
const aboutAfter = await raw("/about");
const docAfter = await docOf();
const selAfter = await selOf();
const frame = await frameOf();
const canvasRender = renders.find((r) => /\/edit\/preview\/[^/]+$/.test(r.path) && r.path.includes(aboutId));
const injected = frame ? await frame.evaluate(() => ({ marker: window.__canvasAtk !== undefined || document.documentElement.dataset.atk !== undefined })).catch(() => ({ marker: false })) : { marker: false };

// Control: the real bridge works. A click on a section in the frame selects it in the editor.
let bridgeWorks = false;
if (frame) {
  // The frame is scaled to fit its pane, so the click point is computed in page space.
  const box = await (await frame.frameElement()).boundingBox();
  const at = await frame.evaluate(() => {
    const e = document.querySelector("[data-hm-section]");
    e.scrollIntoView({ block: "center" });
    const b = e.getBoundingClientRect();
    return { x: b.x + b.width / 2, y: b.y + b.height / 2, w: innerWidth };
  });
  const k = box.width / at.w;
  await admin.mouse.click(box.x + at.x * k, box.y + at.y * k);
  bridgeWorks = await admin.waitForFunction((b) => JSON.stringify(window.__hmBuilder.selected()) !== b, selBefore, { timeout: 5000 }).then(() => true, () => false);
}
record("AT-22", "a plugin script in the builder canvas cannot select, delete or save", {
  ok: saves().length === savesBefore && aboutAfter.body === aboutBefore.body && docAfter === docBefore && selAfter === selBefore && !injected.marker && !canvasRender?.body.includes(pluginScript),
  control: pluginRuns && !!frame && canvasRender?.status === 200 && bridgeWorks,
  detail: `saves=${saves().length - savesBefore} aboutSame=${aboutAfter.body === aboutBefore.body} docSame=${docAfter === docBefore} selectionSame=${selAfter === selBefore} pluginInFrame=${injected.marker} bridgeSelect=${bridgeWorks}`,
});

// ---- AT-22b: blog preview frame and the no-JS "Open preview in a new tab" ------------------------
await admin.goto(`${base}${addr}/entities/${postId}/edit`, { waitUntil: "load" });
await admin.waitForSelector("#hm-be");
const formState = () => admin.evaluate(() => Object.fromEntries([...new FormData(document.getElementById("hm-be")).entries()].filter(([, v]) => typeof v === "string" && !/csrf/i.test(v) && v.length < 2000)));
const formBefore = JSON.stringify(await formState());
await admin.locator("[data-hm-be-preview-toggle]").click();
await admin.waitForSelector("[data-hm-be-stage] iframe.hm-be__frame", { timeout: 10000 });
await admin.waitForTimeout(5000);
const blogRender = renders.find((r) => r.path.includes(postId) && /\/edit\/preview\/[^/]+$/.test(r.path));
const blogWrites = posts.filter((p) => p.includes(postId) && /\/edit$/.test(p));
const blogFrame = admin.frames().find((f) => f !== admin.mainFrame() && f.url().includes(`${postId}/edit/preview/`));
const blogMarker = blogFrame ? await blogFrame.evaluate(() => window.__canvasAtk !== undefined || document.documentElement.dataset.atk !== undefined).catch(() => false) : false;
const blogFormAfter = JSON.stringify(await formState());
record("AT-22b", "a plugin script in the blog preview frame cannot drive the editor", {
  ok: blogWrites.length === 0 && blogFormAfter === formBefore && !blogMarker && !blogRender?.body.includes(pluginScript) && /sandbox/.test(blogRender?.csp ?? ""),
  control: pluginRuns && blogRender?.status === 200 && !!blogFrame && blogRender.body.includes("QA Post 1"),
  detail: `writes=${blogWrites.length} formSame=${blogFormAfter === formBefore} pluginInFrame=${blogMarker} csp=${/sandbox/.test(blogRender?.csp ?? "")}`,
});

// No-JS preview: the form posts to a new tab, the response is a top-level document.
await admin.goto(`${base}${addr}/entities/${postId}/edit`, { waitUntil: "load" });
await admin.waitForSelector("#hm-be");
const tabResponse = context.waitForEvent("response", (r) => r.request().method() === "POST" && new URL(r.url()).pathname === `${addr}/entities/${postId}/edit/preview`, { timeout: 10000 });
const popupP = admin.waitForEvent("popup", { timeout: 10000 });
await admin.locator('button[name="_op"][value="preview"]').evaluate((b) => b.click());
const tabRes = await tabResponse;
const tab = { csp: (await tabRes.allHeaders())["content-security-policy"] ?? "" };
const tabBody = await tabRes.text();
const popup = await popupP;
await popup.waitForLoadState("load");
await popup.waitForTimeout(3000);
const tabState = await popup.evaluate(() => ({ origin: self.origin, marker: window.__canvasAtk !== undefined || document.documentElement.dataset.atk !== undefined, text: document.body.innerText.includes("QA Post 1") })).catch((e) => ({ error: String(e) }));
const tabWrites = posts.filter((p) => /\/edit$/.test(p) || p.endsWith("/builder/save"));
await popup.close();
record("AT-22c", "the no-JS preview tab runs no plugin script and is sandboxed", {
  ok: tabRes.status() === 200 && /sandbox/.test(tab.csp) && !tabBody.includes(pluginScript) && tabState.marker === false && tabState.origin === "null" && tabWrites.length === 0,
  control: pluginRuns && tabRes.status() === 200 && tabBody.includes("QA Post 1") && tabState.text === true,
  detail: `status=${tabRes.status()} csp=${/sandbox/.test(tab.csp)} origin=${tabState.origin} pluginInTab=${tabState.marker} writes=${tabWrites.length}`,
});

await browser.close();
finish();
