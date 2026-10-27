// Screenshots of every admin screen at 1440 and 390, light and dark. For a
// screen with a mockup pairing the mockup is shot live from the mockup server
// and a composite is written with the mockup on the left, the admin on the right.
//   node scripts/admin-qa/shots.mjs [screen ...]      shoot (all screens by default)
//   node scripts/admin-qa/shots.mjs --table           print the markdown table for docs/qa
// Env: SHOTS (output dir, default scripts/admin-qa/shots-out, gitignored),
// NOMOCK=1 (shoot the admin only), MOCK_BASE (default http://127.0.0.1:8097), MOCK_SURFACE (default calm, the mockup surface compared against), FULL=1 (full-page shots on both sides).
// Output per screen: <name>-<1440|390>-<light|dark>.png (admin),
// mock/<mock>-<w>-<theme>.png and <name>-<w>-<theme>-sbs.png for paired screens.
// Exits 1 when any screen does not answer 200 (a missing data token is "skipped").
import fs from "node:fs";
import path from "node:path";
import { launch, newSession, visit, here, role, sizes } from "./lib.mjs";
import { inventory } from "./urls.mjs";

const mockBase = (process.env.MOCK_BASE || "http://127.0.0.1:8097").replace(/\/$/, "");
const mockSurface = process.env.MOCK_SURFACE || "calm";
const out = process.env.SHOTS || path.join(here, "shots-out");
const widths = [1440, 390], themes = ["light", "dark"];
const args = process.argv.slice(2);
const file = (s, w, t) => `${s.name}-${w}-${t}.png`;

if (args.includes("--table")) {
  const rows = inventory().map((s) => {
    const cell = (w, t) => `[png](screens/${file(s, w, t)})`;
    return `| ${s.name} | ${s.mock ?? "none (solo)"} | ${cell(1440, "light")} | ${cell(1440, "dark")} | ${cell(390, "light")} | ${cell(390, "dark")} | pending |`;
  });
  console.log("| Screen | Mock | 1440 light | 1440 dark | 390 light | 390 dark | matches: yes/no/accepted delta + reason |\n|---|---|---|---|---|---|---|\n" + rows.join("\n"));
  process.exit(0);
}

fs.mkdirSync(path.join(out, "mock"), { recursive: true });
const full = process.env.FULL === "1";
const b = await launch();
let failed = 0, skipped = 0, written = 0;

async function mockShot(s, w, t) {
  const ctx = await b.newContext({ viewport: sizes[w], deviceScaleFactor: w === 390 ? 2 : 1 });
  const p = await ctx.newPage();
  const res = await p.goto(`${mockBase}/${s.mock}.html?theme=${t}&surface=${mockSurface}`).catch(() => null);
  if (!res || res.status() !== 200) { await ctx.close(); return null; }
  await p.waitForTimeout(200);
  const f = path.join(out, "mock", `${s.mock}-${w}-${t}.png`);
  await p.screenshot({ path: f, fullPage: full });
  await ctx.close();
  return f;
}

async function sideBySide(left, right, dest, w) {
  const img = (f) => `data:image/png;base64,${fs.readFileSync(f).toString("base64")}`;
  const ctx = await b.newContext({ viewport: { width: 1000, height: 600 } });
  const p = await ctx.newPage();
  await p.setContent(`<body style="margin:0;background:#888;font:600 14px sans-serif;color:#fff"><div style="display:flex;gap:12px;align-items:flex-start;width:max-content"><figure style="margin:0"><figcaption style="padding:4px">Mockup</figcaption><img src="${img(left)}" style="display:block;width:${w}px"></figure><figure style="margin:0"><figcaption style="padding:4px">Admin</figcaption><img src="${img(right)}" style="display:block;width:${w}px"></figure></div></body>`);
  await p.locator("div").first().screenshot({ path: dest });
  await ctx.close();
}

for (const w of widths) for (const t of themes) {
  const session = await newSession(b, { theme: t, width: w });
  for (const s of inventory({ only: args })) {
    const label = `${s.name} ${w} ${t}`;
    let page = session;
    if (s.auth === false) page = await newSession(b, { theme: t, width: w, signedIn: false });
    const v = await visit(page, s);
    if (v.skipped) { skipped++; console.log(`skip ${label} (no data for ${s.path})`); continue; }
    if (!v.ok) { failed++; console.log(`FAIL ${label}: status ${v.status} for ${v.url}`); continue; }
    await page.waitForTimeout(250);
    const f = path.join(out, file(s, w, t));
    await page.screenshot({ path: f, fullPage: full });
    written++;
    if (s.mock && process.env.NOMOCK !== "1") {
      const m = await mockShot(s, w, t);
      if (!m) { failed++; console.log(`FAIL ${label}: mockup ${s.mock} unavailable at ${mockBase}`); continue; }
      await sideBySide(m, f, path.join(out, `${s.name}-${w}-${t}-sbs.png`), w);
    }
    if (s.auth === false) await page.context().close();
  }
  await session.context().close();
}
await b.close();
console.log(`\nrole=${role}: ${written} admin shots in ${out}, ${skipped} skipped, ${failed} failed`);
process.exit(failed ? 1 : 0);
