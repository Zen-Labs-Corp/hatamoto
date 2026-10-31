// Tab and pill rows never overflow vertically (a 1px overflow with
// overflow-x: auto shows a scrollbar on every row). On
// analytics-crawlers, settings-types, settings-redirects and plugins, at 1440
// and 390, light and dark: no .hm-tabs__list (underline or segmented) has
// scrollHeight > clientHeight. The plugins detail pages (Settings, Permissions) are checked on the
// frameplug fixture, which run-local.sh loads for this script (HATAMOTO_PLUGIN_DIR).
// node scripts/admin-qa/tabs.mjs      Exit 1 when a row overflows.
import { launch, newSession, writeResult, base, role } from "./lib.mjs";
import { screens } from "./urls.mjs";

const names = ["analytics-crawlers", "settings-types", "settings-redirects", "plugins"];
const b = await launch("chromium");
// The seeded instance has no plugin with a settings page: these paths need fixtures/frame-plugin.
const extra = [
  { name: "plugin-settings", path: "/admin/plugins/frameplug/settings" },
  { name: "plugin-permissions", path: "/admin/plugins/frameplug/permissions" },
];
const results = [];
const targets = [...names.map((n) => ({ name: n, path: screens.find((x) => x.name === n).path })), ...extra];
let bad = 0;
for (const theme of ["light", "dark"]) {
  for (const width of [1440, 390]) {
    const p = await newSession(b, { theme, width });
    for (const { name: n, path: pth } of targets) {
      await p.goto(base + pth, { waitUntil: "load" });
      const rows = await p.$$eval(".hm-tabs__list", (els) => els.map((e) => ({ h: e.scrollHeight, c: e.clientHeight })));
      const over = rows.filter((r) => r.h > r.c);
      // A plugin page with no tab row means the fixture did not load (a 404), not a pass.
      if (extra.some((e) => e.name === n) && !rows.length) over.push({ h: 0, c: 0, missing: "no .hm-tabs__list" });
      bad += over.length;
      results.push({ screen: n, theme, width, rows: rows.length, over });
      console.log(`${over.length ? "FAIL" : "ok  "} ${n} ${width} ${theme}: ${rows.length} rows${over.length ? ", overflowing " + JSON.stringify(over) : ""}`);
    }
    await p.context().close();
  }
}
await b.close();
writeResult("tabs.json", { role, base, results });
console.log(`\n${bad} overflowing rows`);
process.exit(bad ? 1 : 0);
