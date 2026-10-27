// A tooltip inside a scrolling table (the agents table's "Ignores robots.txt") must not be clipped
// or grow the scroll box (its scroll size is unchanged on hover), and the tip's own box lies inside the viewport.
// The box is placed from its containing block (the control, or the card row on phones). Checked at 390, 700, 1024 and 1440: content that overflows left or up never adds to a scroll size, so the box is measured too.
// node scripts/admin-qa/tooltip-clip.mjs   Exit 1 on a failure.
import { launch, newSession, base } from "./lib.mjs";

const b = await launch();
let bad = 0;
const ok = (c, m) => { console.log(`${c ? "ok  " : "FAIL"} ${m}`); if (!c) bad++; };
for (const [width, vw] of [[1440, 1440], [390, 390], [390, 700], [1024, 1024]]) {
  const p = await newSession(b, { theme: "light", width });
  await p.setViewportSize({ width: vw, height: 900 });
  await p.goto(base + "/admin/analytics/crawlers");
  const tips = p.locator(".hm-table__scroll [data-tooltip]");
  ok((await tips.count()) > 0, `${vw}px: the agents table has tooltips`);
  const size = () => p.evaluate(() => { const s = [...document.querySelectorAll(".hm-table__scroll")].find((x) => x.querySelector("[data-tooltip]")); return [s.scrollWidth, s.scrollHeight, document.documentElement.scrollWidth].join("x"); });
  for (let i = 0; i < (await tips.count()); i++) {
    await tips.nth(i).scrollIntoViewIfNeeded();
    const rest = await size();
    await tips.nth(i).hover();
    await p.waitForTimeout(600);
    ok((await size()) === rest, `${vw}px: tooltip ${i} leaves the scroll size at ${rest}`);
    const box = () => tips.nth(i).evaluate((el) => { const c = getComputedStyle(el, "::after"); let cb = el; while (cb !== document.body && getComputedStyle(cb).position === "static") cb = cb.parentElement; const l = cb.getBoundingClientRect().left + cb.clientLeft + parseFloat(c.left); return { left: Math.round(l), right: Math.round(l + parseFloat(c.width)), iw: innerWidth }; });
    const tip = await box();
    ok(tip.left >= 0 && tip.right <= tip.iw, `${vw}px: tooltip ${i} spans ${tip.left}..${tip.right} inside 0..${tip.iw}`);
    if (i === 0) {
      // Worst case: the badge alone at the start of its line (it wraps there when the cell's other content is long); simulated with a line break before it, its tip must still fit.
      await tips.nth(0).evaluate((el) => { const row = el.parentElement, br = document.createElement("span"); row.style.flexWrap = "wrap"; br.style.flexBasis = "100%"; row.insertBefore(br, el); });
      await tips.nth(0).hover();
      await p.waitForTimeout(600);
      const start = await box();
      ok(start.left >= 0 && start.right <= start.iw, `${vw}px: tooltip 0 at the start of a line spans ${start.left}..${start.right} inside 0..${start.iw}`);
    }
    await p.mouse.move(0, 0);
  }
  await p.context().close();
}
await b.close();
process.exit(bad ? 1 : 0);
