// PDF widget: upload a PDF, put a pdf widget on a page through the builder (the inspector's "Choose PDF" opens a picker
// that lists documents only), publish, then open the public page at desktop and phone width. Desktop shows the PDF in
// a frame (the PDF response allows same-site framing); the phone shows the download link and no frame.
// node scripts/admin-qa/pdf-widget.mjs   (local throwaway instance only: it saves and publishes the page it edits)
//   PG_BIN=<pg bin dir> bash scripts/admin-qa/run-local.sh pdf-widget.mjs
// Screenshots: pdf-widget-desktop.png, pdf-widget-phone.png, pdf-widget-picker.png in the results dir. Exit 1 on a failed check.
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { launch, newSession, base, admin, outDir } from "./lib.mjs";

if (!/^https?:\/\/(127\.0\.0\.1|localhost)(:|\/|$)/.test(base)) {
  console.log("pdf-widget: refusing to edit and publish a page on a non-local instance");
  process.exit(2);
}
const b = await launch();
const p = await newSession(b, { theme: "light", width: 1440 });
let bad = 0;
const ok = (c, m) => { console.log(`${c ? "ok  " : "FAIL"} ${m}`); if (!c) bad++; };
const settle = (ms = 300) => p.waitForTimeout(ms);
fs.mkdirSync(outDir, { recursive: true });

// A one-page PDF with a line of text.
const stream = "BT /F1 28 Tf 72 700 Td (Hatamoto QA brochure) Tj ET";
const pdf = [
  "%PDF-1.4",
  "1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj",
  "2 0 obj<</Type/Pages/Kids[3 0 R]/Count 1>>endobj",
  "3 0 obj<</Type/Page/Parent 2 0 R/MediaBox[0 0 612 792]/Contents 4 0 R/Resources<</Font<</F1 5 0 R>>>>>>endobj",
  `4 0 obj<</Length ${stream.length}>>stream\n${stream}\nendstream endobj`,
  "5 0 obj<</Type/Font/Subtype/Type1/BaseFont/Helvetica>>endobj",
  "trailer<</Root 1 0 R/Size 6>>",
  "%%EOF",
].join("\n");
const dir = fs.mkdtempSync(path.join(os.tmpdir(), "hm-pdf-"));
const file = path.join(dir, "qa-brochure.pdf");
fs.writeFileSync(file, pdf);

await p.goto(`${base}${admin}/settings/media/new`);
await p.setInputFiles("input[type=file][name=file]", file);
await Promise.all([p.waitForNavigation(), p.locator('form[action$="/media/upload"] button[type=submit]').first().click()]);
ok((await p.textContent("body")).includes("qa-brochure"), "the PDF uploads to the library");

// The page: the seeded About page, found by its title.
await p.goto(`${base}${admin}/entities?q=About`);
const hrefs = await p.$$eval(`a[href^="${admin}/entities/"]`, (as) => as.filter((a) => a.textContent.includes("About")).map((a) => a.getAttribute("href")));
const id = hrefs.map((h) => h.match(/entities\/([0-9a-f-]{36})/)?.[1]).find(Boolean);
if (!id) throw new Error("no About page to edit");
await p.goto(`${base}${admin}/entities/${id}/edit?qa=1`, { waitUntil: "load" });
await p.waitForSelector('[data-hm-bs-tree] [role="treeitem"]', { timeout: 15000 });
await p.waitForFunction(() => !!window.__hmBuilder, null, { timeout: 8000 });
await settle(800);

await p.evaluate(() => window.__hmBuilder.insertAt({ parent: "", index: 0 }, "pdf"));
await settle(800);
const flat = (ns) => ns.flatMap((n) => [n, ...flat(n.c ?? [])]);
const node = flat(await p.evaluate(() => window.__hmBuilder.doc().nodes)).find((n) => n.t === "pdf");
ok(!!node, "the palette inserts a pdf widget");
await p.locator(`[data-hm-bs-tree] [role="treeitem"][data-id="${node.id}"]`).click();
await settle(600);
await p.locator('[role="tab"][data-tab="content"]').first().click().catch(() => {});
await settle(300);

const choose = p.getByRole("button", { name: "Choose PDF" });
ok(await choose.isVisible(), "the inspector offers Choose PDF for the document field");
await choose.click();
await p.locator("#hm-media-picker[open] [data-hm-pick]").first().waitFor({ timeout: 8000 });
const picks = await p.$$eval("#hm-media-picker [data-hm-pick]", (bs) => bs.map((x) => x.dataset.hmPick));
ok(picks.length > 0 && picks.every((u) => u.endsWith(".pdf")), `the picker lists only PDFs (${picks.length})`);
ok((await p.textContent("#hm-media-picker-title")).includes("document"), "the picker title says document");
await p.screenshot({ path: path.join(outDir, "pdf-widget-picker.png") });
await p.locator("#hm-media-picker [data-hm-pick]", { hasText: "qa-brochure" }).first().click();
await settle(600);
const src = flat(await p.evaluate(() => window.__hmBuilder.doc().nodes)).find((n) => n.id === node.id)?.p?.src ?? "";
ok(src.startsWith("/media/") && src.endsWith(".pdf"), `the picked PDF is the widget source (${src})`);

// The editor preview is served in a CSP sandbox (opaque origin): the PDF must be a link there, not a blocked frame.
const doc = await p.evaluate(() => window.__hmBuilder.doc());
const prev = await p.evaluate(async (doc) => {
  const r = await fetch("/api/v1/render/preview", {
    method: "POST", credentials: "same-origin",
    headers: { "Content-Type": "application/json", "X-CSRF-Token": document.querySelector('meta[name="csrf-token"]')?.content ?? "" },
    body: JSON.stringify({ schema: "page", attributes: { page_doc: doc } }),
  });
  return { status: r.status, html: await r.text() };
}, doc);
ok(prev.status === 200 && prev.html.includes('class="hm-pdf__link"'), "preview: the PDF link renders");
ok(!prev.html.includes("hm-pdf__frame"), "preview: no PDF frame in the sandboxed preview");

await p.locator('[data-hm-bs-save="publish"]:visible, [data-hm-bs-save="save"]:visible').first().click();
await settle(2500);

// The public page, desktop and phone.
async function visit(width, height, shot) {
  const ctx = await b.newContext({ viewport: { width, height } });
  const q = await ctx.newPage();
  const seen = [];
  q.on("response", (r) => { if (r.url().endsWith(".pdf")) seen.push(r); });
  await q.goto(`${base}/about`, { waitUntil: "load" });
  await q.waitForTimeout(2500);
  const out = { q, ctx, seen, frame: q.locator("iframe.hm-pdf__frame"), link: q.locator("a.hm-pdf__link") };
  await q.screenshot({ path: path.join(outDir, shot) });
  return out;
}
const d = await visit(1280, 900, "pdf-widget-desktop.png");
ok(await d.frame.isVisible(), "desktop: the PDF frame is visible");
ok(d.seen.length > 0 && d.seen[0].status() < 300, `desktop: the PDF loads in the frame (${d.seen[0]?.status()})`);
const hd = d.seen[0] ? await d.seen[0].allHeaders() : {};
ok(hd["x-frame-options"] === "SAMEORIGIN" && (hd["content-security-policy"] || "").includes("frame-ancestors 'self'"), "desktop: the PDF response allows same-site framing");
ok(await d.link.isVisible(), "desktop: the download link is visible too");
ok((await d.q.locator("main script, .hm-pdf script").count()) === 0, "no script inside the widget");
await d.ctx.close();

const m = await visit(390, 844, "pdf-widget-phone.png");
ok(!(await m.frame.isVisible()), "phone: the frame is hidden");
ok(await m.link.isVisible(), "phone: the download link is visible");
await m.ctx.close();

await b.close();
fs.rmSync(dir, { recursive: true, force: true });
if (bad) { console.log(`${bad} check(s) failed`); process.exit(1); }
console.log("pdf-widget: all checks passed");
