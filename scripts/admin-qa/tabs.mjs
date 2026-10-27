// Tab and pill rows never overflow vertically (a 1px overflow with
// overflow-x: auto shows a scrollbar on every row). On
// analytics-crawlers, settings-types, settings-redirects and plugins, at 1440
// and 390, light and dark: no .hm-tabs__list (underline or segmented) has
// scrollHeight > clientHeight.
// node scripts/admin-qa/tabs.mjs      Exit 1 when a row overflows.
import { launch, newSession, writeResult, base, role } from "./lib.mjs";
import { screens } from "./urls.mjs";

const names = ["analytics-crawlers", "settings-types", "settings-redirects", "plugins"];
const b = await launch("chromium");
const results = [];
let bad = 0;
for (const theme of ["light", "dark"]) {
  for (const width of [1440, 390]) {
    const p = await newSession(b, { theme, width });
    for (const n of names) {
      const s = screens.find((x) => x.name === n);
      await p.goto(base + s.path, { waitUntil: "load" });
      const rows = await p.$$eval(".hm-tabs__list", (els) => els.map((e) => ({ h: e.scrollHeight, c: e.clientHeight })));
      const over = rows.filter((r) => r.h > r.c);
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
