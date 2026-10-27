// Chrome families check: for each kind of control (selected segmented, table header,
// stat value, ...) the computed styles must be ONE value set across every screen.
// Runs on every owner screen in urls.mjs at 1440 and 390, light and dark; groups the
// measured value sets per family, theme and width; exits 1 when a family has more than
// one set, or when a family was never found (ALLOW_EMPTY lists the families that may be absent; it is empty now:
// script probes reach the media picker's selected tile and the filter chip).
// BASE=http://127.0.0.1:8191 node scripts/admin-qa/consistency-families.mjs [screen ...]
// node scripts/admin-qa/consistency-families.mjs --self-test   (no server: two differing
// segmented controls in a static page must be reported)
// Writes families.json (screen, theme, width, family, value set) to the results dir.
import { launch, newSession, login, visit, writeResult, base } from "./lib.mjs";
import { screens as allScreens } from "./urls.mjs";

const FAMILIES = {
  segmentedSelected: { sel: '.hm-segmented__btn[aria-pressed="true"], .hm-segmented__input:checked + .hm-segmented__btn, .hm-tabs[data-variant="segmented"] [aria-current="page"]', props: ["backgroundColor", "color", "borderTopLeftRadius", "boxShadow"] },
  segmentedHeight: { sel: '.hm-segmented__btn, .hm-tabs[data-variant="segmented"] .hm-tabs__tab', props: ["height"] },
  segmentedUnselected: { sel: '.hm-segmented__btn[aria-pressed="false"], .hm-segmented__input:not(:checked) + .hm-segmented__btn, .hm-tabs[data-variant="segmented"] .hm-tabs__tab:not([aria-current="page"])', props: ["backgroundColor", "color", "fontWeight", "borderTopLeftRadius"] },
  segmentedTrack: { sel: ".hm-segmented, .hm-tabs[data-variant=segmented] .hm-tabs__list", props: ["borderTopWidth", "borderTopColor", "borderTopLeftRadius", "backgroundColor"] },
  theadTh: { sel: ".hm-table thead th", props: ["textTransform", "fontSize", "fontWeight", "letterSpacing", "color"] },
  statValue: { sel: ".hm-statcard__value", props: ["fontSize", "fontWeight"] },
  statLabel: { sel: ".hm-statcard__label", props: ["color"] },
  sidebarCurrent: { sel: '.hm-sidebar .hm-nav-link[aria-current="page"]', props: ["backgroundColor", "fontWeight"] },
  mediaSelected: { sel: ".hm-mediatile--selected, .hm-pickertile[aria-pressed=true]", props: ["boxShadow", "borderTopColor"] },
  tabSelected: { sel: '.hm-tabs:not([data-variant="segmented"]) .hm-tabs__tab[aria-current="page"], .hm-tablist__tab[aria-selected="true"]', props: ["color"], pseudo: "::after", pseudoProps: ["backgroundColor", "height"] },
  chip: { sel: ".hm-filter-chip, .hm-msel__chip", props: ["height", "borderTopLeftRadius"] },
};
// Families that may legitimately not appear on any scanned screen (no seedable state).
const ALLOW_EMPTY = new Set();

// States only a script action reaches, run after the plain screens: an active filter chip, and the
// media picker open on an editor with one image already chosen (its tile shows pressed).
const PROBES = [
  { name: "entities-filter-chip", path: "/admin/entities?from=2020-01-01" },
  {
    name: "media-picker-selected",
    path: async (p) => { await p.goto(base + "/admin/entities?type=post"); return (await p.locator('a[href$="/edit"]').first().getAttribute("href")); },
    act: async (p) => {
      const open = async () => { await p.locator("[data-hm-media-picker]:visible").first().click(); await p.locator("#hm-media-picker [data-hm-pick]").first().waitFor(); };
      if (!(await p.locator("[data-hm-media-picker]:visible").count())) return; // phone: the side panel holding it is a closed drawer
      await open();
      await p.locator("#hm-media-picker [data-hm-pick]").first().click(); // chooses it and closes the dialog
      await open(); // reopened: the chosen tile is aria-pressed
    },
  },
];

// Runs in the page. Returns [{ family, set, el }] for every visible element of every family.
function measure(families) {
  const out = [];
  const visible = (el) => {
    if (!el.getClientRects().length) return false;
    const cs = getComputedStyle(el);
    return cs.visibility !== "hidden" && cs.display !== "none" && !el.closest("[hidden], dialog:not([open]), [popover]:not(:popover-open)");
  };
  for (const [family, f] of Object.entries(families)) {
    for (const el of document.querySelectorAll(f.sel)) {
      if (!visible(el)) continue;
      const cs = getComputedStyle(el);
      const set = f.props.map((p) => `${p}:${cs[p]}`);
      if (f.pseudo) { const ps = getComputedStyle(el, f.pseudo); set.push(...f.pseudoProps.map((p) => `${f.pseudo} ${p}:${ps[p]}`)); }
      const label = el.tagName.toLowerCase() + (el.classList.length ? "." + [...el.classList].slice(0, 2).join(".") : "");
      out.push({ family, set: set.join("; "), el: label });
    }
  }
  return out;
}

// records: [{ screen, theme, width, family, set, el }]. Returns { rows, bad }.
function analyze(records, { allowEmpty = ALLOW_EMPTY, families = Object.keys(FAMILIES) } = {}) {
  const rows = [], bad = [];
  for (const family of families) {
    const recs = records.filter((r) => r.family === family);
    if (!recs.length && !allowEmpty.has(family)) bad.push(`${family}: no element scanned on any screen`);
    const groups = new Map();
    for (const r of recs) {
      const k = `${r.theme} ${r.width}`;
      if (!groups.has(k)) groups.set(k, new Map());
      const sets = groups.get(k);
      if (!sets.has(r.set)) sets.set(r.set, new Set());
      sets.get(r.set).add(`${r.screen}:${r.el}`);
    }
    let worst = recs.length ? 1 : 0;
    for (const [k, sets] of groups) {
      worst = Math.max(worst, sets.size);
      if (sets.size > 1) for (const [set, where] of sets) bad.push(`${family} [${k}] ${set}  <- ${[...where].slice(0, 4).join(", ")}${where.size > 4 ? ` +${where.size - 4}` : ""}`);
    }
    rows.push({ family, distinct: worst, screens: new Set(recs.map((r) => r.screen)).size });
  }
  return { rows, bad };
}

function printTable(rows) {
  console.log("family".padEnd(20) + "distinct sets".padEnd(15) + "screens");
  for (const r of rows) console.log(r.family.padEnd(20) + String(r.distinct).padEnd(15) + r.screens);
}

if (process.argv.includes("--self-test")) {
  const sb = await launch();
  const page = await (await sb.newContext({ viewport: { width: 1440, height: 900 } })).newPage();
  await page.setContent(`<!doctype html><body>
<div class="hm-segmented"><button class="hm-segmented__btn" aria-pressed="true" style="background:#111;color:#fff;height:32px">A</button></div>
<div class="hm-segmented"><button class="hm-segmented__btn" aria-pressed="true" style="background:#fff;color:#000;height:30px">B</button></div>
<table class="hm-table"><thead><tr><th>X</th></tr></thead></table></body>`);
  const records = (await page.evaluate(measure, FAMILIES)).map((r) => ({ ...r, screen: "fixture", theme: "light", width: 1440 }));
  const { bad } = analyze(records, { allowEmpty: new Set(Object.keys(FAMILIES)) });
  await sb.close();
  if (!bad.some((b) => b.startsWith("segmentedSelected ["))) { console.log("self-test FAILED: two differing segmented controls were not reported"); process.exit(1); }
  if (bad.some((b) => b.startsWith("theadTh"))) { console.log("self-test FAILED: a single table header was reported"); process.exit(1); }
  if (!analyze([], { allowEmpty: new Set() }).bad.some((b) => b.startsWith("statValue: no element"))) { console.log("self-test FAILED: an empty family was not reported"); process.exit(1); }
  if (analyze([], { allowEmpty: new Set(Object.keys(FAMILIES)) }).bad.length) { console.log("self-test FAILED: ALLOW_EMPTY families were reported"); process.exit(1); }
  console.log("self-test ok");
  process.exit(0);
}

const only = process.argv.slice(2);
const unknown = only.filter((n) => !allScreens.some((s) => s.name === n));
if (unknown.length) { console.error(`unknown screen name(s): ${unknown.join(", ")}`); process.exit(1); }
const list = allScreens.filter((s) => s.roles.includes("owner") && s.auth !== false && s.as !== "newcomer").filter((s) => !only.length || only.includes(s.name));

const b = await launch();
const records = [];
let failed = 0;
// QA_JOBS: width x theme sessions at once (default 4); records are grouped per width/theme,
// so the order they arrive in does not matter.
const JOBS = Math.max(1, Number.parseInt(process.env.QA_JOBS || "4", 10) || 1);
const combos = [];
for (const width of [1440, 390]) for (const theme of ["light", "dark"]) combos.push(async () => {
  const p = await newSession(b, { theme, width, signedIn: false });
  await login(p);
  for (const s of list) {
    const v = await visit(p, s);
    if (v.skipped) { if (!v.ok) failed++; continue; }
    if (!v.ok) { failed++; console.log(`FAIL ${s.name} ${width} ${theme}: status ${v.status} for ${v.url}`); continue; }
    await p.waitForTimeout(200);
    for (const r of await p.evaluate(measure, FAMILIES)) records.push({ screen: s.name, theme, width, ...r });
  }
  for (const probe of PROBES) {
    const path = typeof probe.path === "function" ? await probe.path(p) : probe.path;
    await p.goto(base + path);
    if (probe.act) await probe.act(p);
    await p.waitForTimeout(200);
    for (const r of await p.evaluate(measure, FAMILIES)) records.push({ screen: probe.name, theme, width, ...r });
  }
  await p.context().close();
});
let next = 0;
await Promise.all(Array.from({ length: Math.min(JOBS, combos.length) }, async () => { while (next < combos.length) await combos[next++](); }));
await b.close();

const { rows, bad } = analyze(records);
const summary = new Map();
for (const r of records) summary.set(`${r.family}|${r.theme}|${r.width}|${r.screen}|${r.set}`, { screen: r.screen, theme: r.theme, width: r.width, family: r.family, set: r.set });
const key = (x) => `${x.family}|${x.theme}|${String(x.width).padStart(5, "0")}|${x.screen}|${x.set}`;
writeResult("families.json", { base, summary: rows, sets: [...summary.values()].sort((x, y) => (key(x) < key(y) ? -1 : key(x) > key(y) ? 1 : 0)) });
printTable(rows);
for (const line of bad) console.log(line);
console.log(`\nbase=${base}: ${records.length} elements, ${bad.length} problems, ${failed} screen failures`);
const ok = !bad.length && !failed && records.length > 0;
console.log(ok ? "0 violations" : "FAILED");
process.exit(ok ? 0 : 1);
