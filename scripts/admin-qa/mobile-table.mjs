// 390px table layout: bulk checkbox is the hit target at its own centre, and hand-written
// tables keep their column headers. Run: scripts/admin-qa/run-local.sh mobile-table.mjs
import { launch, newSession, base } from "./lib.mjs";
const b = await launch();
let bad = 0;
const ok = (c, m) => { console.log(`${c ? "ok  " : "FAIL"} ${m}`); if (!c) bad++; };
const p = await newSession(b, { theme: "light", width: 390 });
await p.goto(`${base}/admin/entities`);
const hit = await p.evaluate(() => {
  const cb = document.querySelector(".hm-table--cards td.hm-table__check input");
  if (!cb) return null;
  const r = cb.getBoundingClientRect();
  const el = document.elementFromPoint(r.x + r.width / 2, r.y + r.height / 2);
  return { onCheck: el === cb, tag: el && el.tagName };
});
ok(hit !== null, "entities list has a bulk row checkbox");
ok(hit?.onCheck === true, `checkbox centre hit-tests to the checkbox (got ${hit?.tag})`);
const cards = await p.evaluate(() => [...document.querySelectorAll(".hm-table--cards tbody tr")].map((r) => r.getBoundingClientRect().height));
ok(cards.length > 0 && Math.max(...cards) < 110, `entities cards are compact, at most ~3 text lines (max ${Math.max(...cards)}px, n=${cards.length})`);
const wide = await p.evaluate(() => [...document.querySelectorAll(".hm-table--cards td.hm-col-wide")].every((td) => getComputedStyle(td).display === "none"));
ok(wide, "WideOnly cells are hidden in cards");
const head = await p.evaluate(() => {
  const t = document.createElement("table");
  t.className = "hm-table";
  t.innerHTML = "<thead><tr><th>Alpha</th><th>Beta</th></tr></thead><tbody><tr><td>1</td><td>2</td></tr></tbody>";
  document.body.append(t);
  const th = t.querySelector("th").getBoundingClientRect();
  const display = getComputedStyle(t.querySelector("tr")).display;
  t.remove();
  return { w: th.width, h: th.height, display };
});
ok(head.display === "table-row" && head.w > 8 && head.h > 8, `plain .hm-table keeps headers at 390px (${JSON.stringify(head)})`);
const stack = await p.evaluate(() => document.querySelector(".hm-toasts")?.getBoundingClientRect().height);
ok(stack === 0, `the empty toast stack takes no space and covers nothing (height ${stack})`);
await b.close();
process.exit(bad ? 1 : 0);
