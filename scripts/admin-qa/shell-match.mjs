// Live shell matches the approved preview: background, centred 1440 group, fonts, no top bar,
// sidebar toggle and counts, icon-free subnav, plugins filter row, 3-up swatches, neutral callout.
// node scripts/admin-qa/shell-match.mjs [chromium|firefox|webkit]
import { launch, newSession, visit, writeResult, sizes } from "./lib.mjs";

const engine = ["chromium", "firefox", "webkit"].includes(process.argv[2]) ? process.argv[2] : "chromium";
sizes[2200] = { width: 2200, height: 900 };
const b = await launch(engine);
const bad = [];
const check = (ok, msg) => { if (!ok) bad.push(msg); };

for (const width of [2200, 1440, 390]) {
  const p = await newSession(b, { width });
  await visit(p, { path: "/admin/settings/general" });
  const r = await p.evaluate(() => {
    const rect = (s) => document.querySelector(s)?.getBoundingClientRect();
    const g = rect(".hm-settings"), side = rect(".hm-sidebar");
    return {
      bg: getComputedStyle(document.body).backgroundColor,
      topbar: !!document.querySelector(".hm-topbar"),
      inter: document.fonts.check("14px Inter"),
      subnavIcons: document.querySelectorAll(".hm-subnav .hm-icon").length,
      groupW: g?.width, left: g && g.left - (side?.right ?? 0), right: g && innerWidth - g.right,
      toggle: !!document.querySelector("[data-hm-sidebar-toggle]"),
    };
  });
  check(r.bg === "rgb(250, 250, 249)", `${width}: body background ${r.bg}`);
  check(!r.topbar, `${width}: .hm-topbar present`);
  check(r.inter, `${width}: Inter not available`);
  check(r.subnavIcons === 0, `${width}: subnav has icons`);
  if (width >= 1440) {
    check(r.groupW <= 1440, `${width}: group ${r.groupW}px > 1440`);
    check(Math.abs(r.left - r.right) <= 2, `${width}: group not centred (${r.left} vs ${r.right})`);
  }
  if (width >= 769) check(r.toggle, `${width}: no sidebar toggle`);
  await p.close();
}

const p = await newSession(b, { width: 1440 });
await visit(p, { path: "/admin/plugins" });
const pl = await p.evaluate(() => ({
  counts: [...document.querySelectorAll(".hm-nav-link__badge")].map((e) => e.textContent.trim()),
  filterInCard: !!document.querySelector("#installed .hm-card__body > .hm-pluginlist__filters"),
}));
check(pl.counts.length > 0, "sidebar shows no counts");
check(pl.filterInCard, "plugins filter row is not inside the Installed plugins card");
await visit(p, { path: "/admin/theme/colours" });
const col = await p.evaluate(() => {
  const tops = [...document.querySelectorAll(".hm-swatchrow")].map((e) => Math.round(e.getBoundingClientRect().top));
  return { perRow: tops.filter((t) => t === tops[0]).length, edge: getComputedStyle(document.querySelector(".hm-callout--neutral") ?? document.body).borderInlineStartWidth };
}).catch(() => ({}));
check(col.perRow === 3, `swatches per row ${col.perRow}, want 3`);
check(col.edge === "1px", `neutral callout edge ${col.edge}`);

writeResult("shell-match.json", { engine, problems: bad });
if (bad.length) { console.error(bad.join("\n")); process.exit(1); }
console.log("shell-match ok");
