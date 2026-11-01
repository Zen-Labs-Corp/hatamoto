// Builder top bar status and the Update indicator, on a throwaway local instance (this script SAVES: it publishes an edit,
// so it refuses any base URL that is not loopback; builder-chrome-prod.mjs is the one that never saves).
//
//   PG_BIN=<pg bin dir> bash scripts/admin-qa/run-local.sh builder-chrome-status.mjs      (QA_SHOTS=<dir> keeps the screenshots)
//
// Draft page (created here with the CLI): an edit shows "Saving…" then "Draft saved · not live yet", the time is in the title,
// there is no Update button and "Save draft" / "Publish" keep their names. Published page (the seeded "About", QA_PAGE_NAME):
// an edit with autosave off (a published page keeps only a recovery copy) reads "Draft saved · not live yet", the Update button
// carries data-hm-bs-dirty and the name "Update, unpublished changes"; Update then reads "Published · Saved" and the indicator
// clears. Offline shows the "Not saved" reason line. At 390 px the short line and the second row are in place. Axe on the
// top bar in both themes. The bar is measured at 360, 390, 414, 640, 1000, 1100, 1280, 1300 and 1600 wide (no overflow from 1100 up, 1000 is reported; Undo and the
// devices on their own row up to 414; the phone layout alone at 640). Screenshots: status-1600.png, status-390.png, bar-<width>.png.
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { launch, base, admin, login } from "./lib.mjs";

if (!/^https?:\/\/(127\.0\.0\.1|localhost|\[::1\])(:\d+)?$/.test(base)) {
  console.error(`builder-chrome-status: refusing ${base}: this script saves and is local only`);
  process.exit(2);
}
const NAME = process.env.QA_PAGE_NAME || "About";
const DRAFT = `QA Status Draft ${Date.now()}`;
const SHOTS = process.env.QA_SHOTS || "";
const axe = fs.readFileSync(new URL("./vendor/axe.min.js", import.meta.url), "utf8");
if (SHOTS) fs.mkdirSync(SHOTS, { recursive: true });

const b = await launch("chromium");
const ctx = await b.newContext({ viewport: { width: 1600, height: 950 } });
await ctx.addCookies([{ name: "hm_theme", value: "light", url: base }]);
const p = await ctx.newPage();
await login(p);
p.on("dialog", (d) => d.accept().catch(() => {}));
let bad = 0;
const errors = [];
p.on("pageerror", (e) => errors.push(e.message));

const TREE = '[data-hm-bs-tree] [role="treeitem"]';
const settle = (ms = 150) => p.waitForTimeout(ms);
const statusText = () => p.locator("[data-hm-bs-state]").first().innerText().then((t) => t.replace(/\s+/g, " ").trim());
const title = () => p.locator("[data-hm-bs-state] .hm-savestate").first().getAttribute("title");
const waitStatus = (re, ms = 15000) => p.waitForFunction((src) => new RegExp(src).test(document.querySelector("[data-hm-bs-state]")?.textContent.replace(/\s+/g, " ") ?? ""), re.source, { timeout: ms });
const shot = async (name) => { if (SHOTS) await p.screenshot({ path: path.join(SHOTS, name) }); };
const update = () => p.locator('[data-hm-bs-save="save"]');

async function check(name, fn) {
  try { console.log(`ok   ${name}: ${await fn()}`); }
  catch (e) {
    bad++; console.log(`FAIL ${name}: ${String(e.message || e).split("\n").slice(0, process.env.QA_VERBOSE ? 30 : 1).join(" | ")}`);
    await ctx.setOffline(false).catch(() => {});
  }
}

async function idOf(name) {
  await p.goto(`${base}${admin}/entities?q=${encodeURIComponent(name)}`);
  const hrefs = await p.$$eval(`a[href^="${admin}/entities/"]`, (as, t) => as.filter((a) => a.textContent.includes(t)).map((a) => a.getAttribute("href")), name);
  const id = hrefs.map((h) => h.match(/entities\/([0-9a-f-]{36})/)?.[1]).find(Boolean);
  if (!id) throw new Error(`no page named ${name}`);
  return id;
}
async function open(id, qs = "") {
  await p.goto(`${base}${admin}/entities/${id}/edit?qa=1${qs}`, { waitUntil: "load" });
  await p.waitForFunction(() => !!window.__hmBuilder, null, { timeout: 15000 }); // a draft page starts empty: no Navigator rows
  await settle(800);
}
const addHeading = async () => {
  const n = (await p.evaluate(() => window.__hmBuilder.doc().nodes)).length;
  await p.evaluate(() => window.__hmBuilder.insertAt({ parent: "", index: 0 }, "heading"));
  await p.waitForFunction((k) => window.__hmBuilder.doc().nodes.length > k, n, { timeout: 5000 });
};
/** Records every state text the bar shows from now on, so a short-lived "Saving…" is not missed. */
const record = () => p.evaluate(() => {
  const el = document.querySelector("[data-hm-bs-state]");
  window.__seen = [];
  new MutationObserver(() => { const t = el.textContent.replace(/\s+/g, " ").trim(); if (window.__seen.at(-1) !== t) window.__seen.push(t); }).observe(el, { subtree: true, childList: true, characterData: true });
});
const seen = () => p.evaluate(() => window.__seen);

let draftId = "", pubId = "";

await check("setup: a draft page (CLI) and the published seeded page", async () => {
  execFileSync(process.env.HATAMOTO_BIN || "hatamoto", ["entity", "create", "--schema", "page", "--name", DRAFT, "--as", process.env.QA_EMAIL || "owner@qa.test"], { stdio: "pipe" });
  draftId = await idOf(DRAFT);
  pubId = await idOf(NAME);
  return `draft ${draftId}, published ${pubId}`;
});

await check("draft page: an edit shows Saving… then Draft saved · not live yet; title holds the time; no Update button", async () => {
  await open(draftId);
  const st = await statusText();
  await record();
  await addHeading();
  await waitStatus(/Draft saved · not live yet/);
  const s = await seen();
  if (!s.some((t) => /^Saving…/.test(t))) throw new Error(`no "Saving…" among ${JSON.stringify(s)}`);
  const ti = await title();
  if (!/^Saved at \d/.test(ti ?? "")) throw new Error(`title is ${JSON.stringify(ti)}`);
  if (await p.getByRole("button", { name: /^Update/ }).count()) throw new Error("a draft page has an Update button");
  for (const n of ["Save draft", "Publish"]) if (!(await p.getByRole("button", { name: n, exact: true }).count())) throw new Error(`no "${n}" button`);
  if (await p.locator("[data-hm-bs-dirty]").count()) throw new Error("a dirty indicator on a draft page");
  return `${st} -> ${JSON.stringify(s)}, title "${ti}"`;
});

await check("published page: an edit with autosave off keeps a copy: Draft saved · not live yet, Update marked, named Update, unpublished changes", async () => {
  await open(pubId);
  const st = await statusText();
  if (!/Published/.test(st)) throw new Error(`the page starts as "${st}"`);
  await addHeading();
  await waitStatus(/Draft saved · not live yet/);
  const btn = update();
  if ((await btn.getAttribute("data-hm-bs-dirty")) === null) throw new Error("Update has no data-hm-bs-dirty");
  const name = await p.getByRole("button", { name: "Update, unpublished changes", exact: true }).count();
  if (!name) throw new Error(`Update's accessible name is not "Update, unpublished changes" (aria-label ${await btn.getAttribute("aria-label")})`);
  await shot("status-1600.png");
  return `"${await statusText()}", Update marked and named`;
});

await check("offline: an edit shows the Not saved reason line", async () => {
  await ctx.setOffline(true);
  try {
    await addHeading();
    await waitStatus(/^Not saved/);
    const t = await statusText();
    if (!/offline/i.test(t)) throw new Error(`no reason in "${t}"`);
    return `"${t}"`;
  } finally {
    await ctx.setOffline(false);
  }
});

await check("Update saves: Published · Saved, the indicator clears, the name is Update again", async () => {
  await update().click();
  await waitStatus(/Published · Saved/);
  await settle(300);
  if ((await update().getAttribute("data-hm-bs-dirty")) !== null) throw new Error("data-hm-bs-dirty is still set");
  if ((await update().getAttribute("aria-label")) !== null) throw new Error(`aria-label is still ${await update().getAttribute("aria-label")}`);
  if (!(await p.getByRole("button", { name: "Update", exact: true }).count())) throw new Error('no button named exactly "Update"');
  return `"${await statusText()}"`;
});

/** Layout numbers of the top bar at the current viewport: overflow of the bar, of the page, and the rows of title, Undo and the device switch. */
const barBox = () => p.evaluate(() => {
  const bar = document.querySelector(".hm-editor-bar");
  const r = (sel) => document.querySelector(sel)?.getBoundingClientRect();
  const kids = [...bar.querySelectorAll("*")].filter((e) => e.offsetParent && e.getBoundingClientRect().width);
  const right = Math.max(...kids.map((e) => e.getBoundingClientRect().right));
  const left = Math.min(...kids.map((e) => e.getBoundingClientRect().left));
  const bb = bar.getBoundingClientRect();
  return {
    barOver: Math.max(0, Math.round(Math.max(right - bb.right, bb.left - left, bar.scrollWidth - bar.clientWidth))),
    pageOver: document.documentElement.scrollWidth - innerWidth,
    titleTop: r("[data-hm-bs-title]")?.top, undoTop: r("[data-hm-bs-undo]")?.top, devTop: r(".hm-bs__devices")?.top,
    grid: getComputedStyle(bar).display,
  };
});
const WIDTHS = [360, 390, 414, 640, 1000, 1100, 1280, 1300, 1600];

await check("top bar fit at 360, 390, 414, 640, 1000, 1100, 1280, 1300 and 1600 px (published page with an unpublished edit: the widest bar)", async () => {
  await open(pubId);
  await addHeading();
  await waitStatus(/Draft saved · not live yet/);
  const out = [], fails = [];
  for (const w of WIDTHS) {
    await p.setViewportSize({ width: w, height: 844 });
    await settle(500);
    const b = await barBox();
    await shot(`bar-${w}.png`);
    out.push(`${w}:bar+${b.barOver}/page+${b.pageOver}/${b.grid === "grid" ? "grid" : "wrap"}${w <= 640 ? `/undo y${Math.round(b.undoTop)} dev y${Math.round(b.devTop)} title y${Math.round(b.titleTop)}` : ""}`);
    if (w >= 1100 && (b.barOver > 1 || b.pageOver > 1)) fails.push(`the bar overflows at ${w}px: ${JSON.stringify(b)}`);
    if (w <= 640) {
      if (b.grid === "grid") fails.push(`at ${w}px the bar is still the grid layout (the phone rules must win)`);
      if (b.pageOver > 1) fails.push(`the page scrolls sideways by ${b.pageOver}px at ${w}px`);
      if (w <= 414 && (!(b.devTop > b.titleTop + 8) || Math.abs(b.undoTop - b.devTop) > 8)) fails.push(`at ${w}px Undo (y ${b.undoTop}) and the devices (y ${b.devTop}) are not their own row below the title (y ${b.titleTop})`);
    }
  }
  await p.setViewportSize({ width: 1600, height: 950 });
  if (fails.length) throw new Error(fails.join("; ") + " [" + out.join(" ") + "]");
  return out.join(" ") + " (1000 is reported, not asserted: the SEO score makes it wide, as it was before the chrome work)";
});

await check("390 px: the short line and the second row layout", async () => {
  await open(draftId);
  await p.setViewportSize({ width: 390, height: 844 });
  await settle(600);
  const r = await p.evaluate(() => {
    const st = document.querySelector("[data-hm-bs-state] .hm-savestate");
    const t = document.querySelector("[data-hm-bs-title]").getBoundingClientRect();
    const d = document.querySelector(".hm-bs__devices").getBoundingClientRect();
    return { short: st.dataset.short, after: getComputedStyle(st, "::after").content, titleTop: t.top, devTop: d.top, over: document.documentElement.scrollWidth - innerWidth };
  });
  if (!r.short) throw new Error("no data-short on the status");
  if (!r.after.includes(r.short)) throw new Error(`the phone bar shows ${r.after}, wanted ${r.short}`);
  if (!(r.devTop > r.titleTop + 8)) throw new Error(`the device switch (y ${r.devTop}) is not on a second row below the title (y ${r.titleTop})`);
  if (r.over > 1) throw new Error(`the page scrolls sideways by ${r.over}px`);
  await shot("status-390.png");
  await p.setViewportSize({ width: 1600, height: 950 });
  return `short "${r.short}", second row at y ${Math.round(r.devTop)}`;
});

for (const theme of ["light", "dark"]) {
  await check(`axe: the top bar has no violations (${theme})`, async () => {
    await ctx.addCookies([{ name: "hm_theme", value: theme, url: base }]);
    try {
      await open(draftId);
      if ((await p.locator("html").getAttribute("data-admin-theme")) !== theme) throw new Error(`the page is not in the ${theme} theme`);
      await p.evaluate(axe);
      const v = await p.evaluate(() => axe.run(document.querySelector(".hm-editor-bar"), { runOnly: { type: "tag", values: ["wcag2a", "wcag2aa", "wcag21aa"] } }).then((r) => r.violations.map((x) => x.id + "×" + x.nodes.length + " " + x.nodes.slice(0, 3).map((n) => n.target.join(" ")).join(" | "))));
      if (v.length) throw new Error(v.join(", "));
      return "0 violations";
    } finally {
      await ctx.addCookies([{ name: "hm_theme", value: "light", url: base }]);
    }
  });
}

if (errors.length) { bad++; console.log("FAIL page errors: " + errors.slice(0, 3).join(" | ")); }
await b.close();
if (bad) { console.log(`${bad} check(s) failed`); process.exit(1); }
console.log("builder-chrome-status: all checks passed");
