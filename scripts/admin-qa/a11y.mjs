// axe-core (WCAG 2.0/2.1/2.2 A+AA) on every admin screen at 1440 light, 1440 dark
// and 390, plus the open popover/dialog states. Exits 1 on any violation or any
// screen that does not answer 200.
// BASE=http://127.0.0.1:8191 node scripts/admin-qa/a11y.mjs [screen-name ...]
import fs from "node:fs";
import { launch, newSession, visit, writeResult, base, role } from "./lib.mjs";
import { inventory } from "./urls.mjs";

const axe = fs.readFileSync(new URL("./vendor/axe.min.js", import.meta.url), "utf8");
const only = process.argv.slice(2);
const tags = ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"];
const screens = inventory({ only });
const b = await launch();
const out = [];
let total = 0, bad = 0, skipped = 0;

async function scan(p) {
  if (!(await p.evaluate(() => typeof axe !== "undefined"))) await p.addScriptTag({ content: axe });
  return p.evaluate(async (tags) => {
    const res = await axe.run(document, { runOnly: { type: "tag", values: tags }, resultTypes: ["violations", "incomplete"] });
    return {
      v: res.violations.map((v) => ({ id: v.id, impact: v.impact, n: v.nodes.length, ex: v.nodes.slice(0, 3).map((n) => n.target.join(" ") + " :: " + (n.failureSummary || "").split("\n").slice(1, 2).join("")) })),
      review: res.incomplete.filter((v) => v.id === "color-contrast").reduce((a, v) => a + v.nodes.length, 0),
    };
  }, tags);
}

function report(label, r) {
  const n = r.v.reduce((a, v) => a + v.n, 0);
  total += n;
  out.push({ screen: label, violations: r.v, contrastNeedsReview: r.review });
  console.log(`${label.padEnd(52)} ${n} violations  (contrast needs review: ${r.review})`);
  for (const v of r.v) console.log(`   ${v.impact} ${v.id} x${v.n}\n     ${v.ex.join("\n     ")}`);
}

for (const width of [1440, 390]) for (const theme of ["light", "dark"]) {
  const p = await newSession(b, { theme, width, signedIn: true, bypassCSP: true });
  for (const s of screens) {
    const label = `${s.name} ${width} ${theme}`;
    if (s.auth === false) {
      // signed-out screens need a clean context
      const anon = await newSession(b, { theme, width, signedIn: false, bypassCSP: true });
      const v = await visit(anon, s);
      if (!v.ok) { bad++; console.log(`${label.padEnd(52)} FAIL status ${v.status} for ${v.url}`); await anon.context().close(); continue; }
      report(label, await scan(anon));
      await anon.context().close();
      continue;
    }
    const v = await visit(p, s);
    if (v.skipped) { skipped++; console.log(`${label.padEnd(52)} skipped (no ${s.path.match(/\{(\w+)\}/)?.[1]} data seeded)`); if (!v.ok) bad++; continue; }
    if (!v.ok) { bad++; console.log(`${label.padEnd(52)} FAIL status ${v.status} for ${v.url}`); continue; }
    await p.waitForTimeout(250);
    report(label, await scan(p));
    if (width !== 1440 || theme !== "light") continue;
    // An earlier script may have left an unsaved copy on the server (it follows the person, not the browser): the scan above covered the banner, the states below need the canvas clear.
    const discard = p.locator('[data-banner="recovery"] button:has-text("Discard them")');
    if (await discard.count()) await discard.click();
    for (const st of s.states || []) {
      const t = p.locator(st.click).first();
      if (!(await t.count())) { skipped++; if (process.env.QA_STRICT === "1") bad++; console.log(`${(s.name + " " + st.name).padEnd(52)} skipped (no trigger ${st.click})`); continue; }
      await t.click();
      if (st.press) await p.keyboard.press(st.press);
      await p.waitForTimeout(250);
      report(`${s.name} [${st.name}] ${width} ${theme}`, await scan(p));
      await p.keyboard.press("Escape");
    }
  }
  await p.context().close();
}

writeResult("a11y.json", out);
console.log(`\nrole=${role} base=${base}: ${out.length} scans, ${total} violations, ${bad} screen failures, ${skipped} skipped`);
console.log(total === 0 && bad === 0 ? "0 violations" : "FAILED");
await b.close();
process.exit(total === 0 && bad === 0 ? 0 : 1);
