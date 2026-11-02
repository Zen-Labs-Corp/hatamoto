// Live shell matches the approved preview: background, centred 1440 group, fonts, no top bar,
// sidebar toggle and counts, icon-free subnav, plugins filter row, 3-up swatches, neutral callout.
// node scripts/admin-qa/shell-match.mjs [chromium|firefox|webkit]
// An asserted element that is missing fails the run (it never passes by default).
// Manual checks the script cannot make: the Colours corner previews draw their radius while a
// radius field is edited, the 390px sidebar top bar looks right with counts hidden, and
// the collapsed rail tooltips show on hover.
import { launch, newSession, visit, writeResult, sizes } from "./lib.mjs";

const engine = ["chromium", "firefox", "webkit"].includes(process.argv[2]) ? process.argv[2] : "chromium";
sizes[2200] = { width: 2200, height: 900 };
const b = await launch(engine);
let failed = false;
try {
  const bad = [];
  const check = (ok, msg) => { if (!ok) bad.push(msg); };
  // find runs in the page; an element a probe needs but cannot find throws, and that is a failure.
  const probe = async (page, label, fn, arg) => {
    try { return await page.evaluate(fn, arg); } catch (e) { bad.push(`${label}: ${String(e.message).split("\n")[0]}`); return {}; }
  };

  for (const width of [2200, 1440, 390]) {
    const p = await newSession(b, { width });
    await visit(p, { path: "/admin/settings/general" });
    const r = await probe(p, `${width} general`, () => {
      const need = (s) => document.querySelector(s) ?? (() => { throw new Error("missing " + s); })();
      const g = need(".hm-settings").getBoundingClientRect(), side = need(".hm-sidebar").getBoundingClientRect();
      return {
        bg: getComputedStyle(document.body).backgroundColor,
        topbar: !!document.querySelector(".hm-topbar"),
        inter: document.fonts.check("14px Inter"),
        subnavIcons: document.querySelectorAll(".hm-subnav .hm-icon").length,
        subnavLinks: document.querySelectorAll(".hm-subnav__link").length,
        subnavCurrent: document.querySelectorAll(".hm-subnav__link[aria-current=page]").length,
        navCurrent: document.querySelectorAll(".hm-nav-link[aria-current=page]").length,
        groupW: g.width, left: g.left - side.right, right: innerWidth - g.right,
        toggle: !!document.querySelector("[data-hm-sidebar-toggle]"),
      };
    });
    check(r.bg === "rgb(250, 250, 249)", `${width}: body background ${r.bg}`);
    check(r.topbar === false, `${width}: .hm-topbar present`);
    check(r.inter, `${width}: Inter not available`);
    check(r.subnavLinks > 0 && r.subnavIcons === 0, `${width}: subnav has ${r.subnavLinks} links and ${r.subnavIcons} icons`);
    check(r.subnavCurrent === 1 && r.navCurrent === 1, `${width}: aria-current on ${r.subnavCurrent} subnav and ${r.navCurrent} sidebar links, want 1 each`);
    if (width >= 1440) {
      check(r.groupW <= 1440, `${width}: group ${r.groupW}px > 1440`);
      check(Math.abs(r.left - r.right) <= 2, `${width}: group not centred (${r.left} vs ${r.right})`);
    }
    if (width >= 769) check(r.toggle, `${width}: no sidebar toggle`);
    await p.close();
  }

  const p = await newSession(b, { width: 1440 });

  // Inter comes from the admin's own origin (document.fonts.check is also true for a locally installed Inter).
  await visit(p, { path: "/admin/entities" });
  const fonts = await probe(p, "fonts", async () => {
    await document.fonts.load("14px Inter");
    return performance.getEntriesByType("resource").filter((e) => /Inter-Variable\.[^/]*\.woff2/.test(e.name)).map((e) => new URL(e.name).origin === location.origin);
  });
  check(fonts.length > 0 && fonts.every(Boolean), "Inter woff2 was not fetched from the admin origin");

  // Entities: a count in the sidebar, and the segmented counts sit on their label's baseline.
  const ent = await probe(p, "entities", () => {
    const need = (s) => document.querySelector(s) ?? (() => { throw new Error("missing " + s); })();
    need(".hm-segmented__count");
    const entLink = [...document.querySelectorAll(".hm-nav-link")].find((a) => a.textContent.includes("Entities"));
    if (!entLink) throw new Error("missing the Entities nav link");
    const off = [...document.querySelectorAll(".hm-segmented__count")].map((c) => {
      const btn = c.closest(".hm-segmented__btn"), r = document.createRange();
      r.selectNodeContents(btn.firstChild);
      return Math.abs(r.getBoundingClientRect().bottom - c.getBoundingClientRect().bottom);
    });
    return { entCount: entLink.querySelector(".hm-nav-link__badge")?.textContent.trim() ?? "", maxOff: Math.max(...off) };
  });
  check(/^\d+$/.test(ent.entCount ?? ""), `Entities nav item has no count (${ent.entCount})`);
  check(ent.maxOff <= 1, `segmented counts are ${ent.maxOff}px off their label baseline`);

  // Sidebar toggle: click and Enter collapse and expand it, aria-expanded follows.
  const toggleState = () => p.evaluate(() => ({ exp: document.querySelector("[data-hm-sidebar-toggle]").getAttribute("aria-expanded"), side: document.querySelector(".hm-shell").dataset.sidebar ?? "" }));
  const t0 = await toggleState();
  await p.click("[data-hm-sidebar-toggle]");
  const t1 = await toggleState();
  await p.focus("[data-hm-sidebar-toggle]");
  await p.keyboard.press("Enter");
  const t2 = await toggleState();
  check(t0.exp === "true" && t1.exp === "false" && t1.side === "collapsed", `toggle click: ${JSON.stringify([t0, t1])}`);
  check(t2.exp === "true" && t2.side !== "collapsed", `toggle Enter: ${JSON.stringify(t2)}`);

  // Collapsed rail: links keep a tooltip, and the theme, sign-out and profile controls stay reachable.
  await p.click("[data-hm-sidebar-toggle]");
  const rail = await probe(p, "rail", () => {
    const vis = (e) => { const r = e.getBoundingClientRect(); return r.width > 0 && r.height > 0 && getComputedStyle(e).visibility !== "hidden"; };
    const links = [...document.querySelectorAll(".hm-sidebar .hm-nav-link")];
    const names = ["[data-hm-theme-toggle]", 'form[action$="/logout"] button', ".hm-sidebar__me"];
    return { untitled: links.filter((a) => !(a.title || a.dataset.tooltip)).length, hidden: names.filter((s) => { const e = document.querySelector(s); return !e || !vis(e) || !(e.getAttribute("aria-label") || e.textContent.trim()); }) };
  });
  check(rail.untitled === 0, `${rail.untitled} collapsed rail links have no tooltip`);
  check((rail.hidden ?? ["?"]).length === 0, `rail controls not reachable: ${rail.hidden}`);
  await p.click("[data-hm-sidebar-toggle]");

  // Plugins: filter row at the top of the card, above the heading, "/" focuses the search, counts in the nav. The seeded
  // install has no plugin, and the page then shows an empty state with no filter row: that is
  // reported as skipped (see the manual checks above), not passed.
  await visit(p, { path: "/admin/plugins" });
  const skipped = [];
  if (await p.$("#installed")) {
    const pl = await probe(p, "plugins", () => {
      const need = (s) => document.querySelector(s) ?? (() => { throw new Error("missing " + s); })();
      const f = need("#installed > .hm-pluginlist__filters"), h = need("#installed .hm-card__head");
      if (!(f.compareDocumentPosition(h) & Node.DOCUMENT_POSITION_FOLLOWING)) throw new Error("filter row is not above the Installed plugins heading");
      const link = [...document.querySelectorAll(".hm-nav-link")].find((a) => a.textContent.includes("Plugins"));
      return { badge: link?.querySelector(".hm-nav-link__badge")?.textContent.trim() ?? "", keys: need("#plugins-q").getAttribute("aria-keyshortcuts"), button: !!document.querySelector(".hm-pluginlist__search button") };
    });
    check(/^\d+$/.test(pl.badge ?? ""), `Plugins nav item has no count (${pl.badge})`);
    check(pl.keys === "/" && pl.button === false, "plugins search lacks aria-keyshortcuts or has a Filter button");
    await p.keyboard.press("/");
    check(await p.evaluate(() => document.activeElement?.id === "plugins-q"), "/ did not focus the plugins search");
  } else {
    skipped.push("plugins filter row, / shortcut and Plugins count: no plugin installed in this seed");
    console.warn("skipped: " + skipped[0]);
  }

  // Colours and fonts: 3 swatches a row, neutral callout, font sample, corner previews.
  await visit(p, { path: "/admin/theme/colours" });
  const col = await probe(p, "colours", () => {
    const need = (s) => document.querySelector(s) ?? (() => { throw new Error("missing " + s); })();
    const tops = [...document.querySelectorAll(".hm-swatchrow")].map((e) => Math.round(e.getBoundingClientRect().top));
    need(".hm-fontsample"); need(".hm-cornersample__box");
    return { n: tops.length, perRow: tops.filter((t) => t === tops[0]).length, edge: getComputedStyle(need(".hm-callout--neutral")).borderInlineStartWidth, sampleFont: getComputedStyle(need(".hm-fontsample__heading")).fontFamily };
  });
  check(col.n > 0 && col.perRow === 3, `swatches per row ${col.perRow} of ${col.n}, want 3`);
  check(col.edge === "1px", `neutral callout edge ${col.edge}`);
  check(!!col.sampleFont, "font sample has no font");

  writeResult("shell-match.json", { engine, problems: bad, skipped });
  failed = bad.length > 0;
  if (failed) console.error(bad.join("\n"));
  else console.log("shell-match ok");
} catch (e) {
  failed = true;
  console.error(e);
} finally {
  await b.close();
}
process.exit(failed ? 1 : 0);
