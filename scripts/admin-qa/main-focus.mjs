// A mousedown on plain text inside <main> must not focus <main> (a static tabindex=-1 did, and
// Chrome scrolled the page to the top of main between mousedown and mouseup). The skip link
// still moves focus to main. node scripts/admin-qa/main-focus.mjs   Exit 1 on a failure.
import { launch, newSession, base } from "./lib.mjs";

const b = await launch();
let bad = 0;
const ok = (c, m) => { console.log(`${c ? "ok  " : "FAIL"} ${m}`); if (!c) bad++; };
const p = await newSession(b, { theme: "light", width: 1440 });

for (const path of ["/admin/analytics/crawlers", "/admin/settings"]) {
  await p.goto(base + path);
  await p.evaluate(() => { document.querySelector("#hm-main").insertAdjacentHTML("beforeend", '<div style="height:3000px"></div><p id="qa-text" style="margin:0">plain text</p><div style="height:3000px"></div>'); });
  ok(await p.evaluate(() => !document.querySelector("#hm-main").hasAttribute("tabindex")), `${path}: main has no static tabindex`);
  await p.evaluate(() => { window.__clicks = 0; document.addEventListener("click", () => window.__clicks++); });
  await p.locator("#qa-text").scrollIntoViewIfNeeded();
  const before = await p.evaluate(() => scrollY);
  const r = await p.locator("#qa-text").boundingBox();
  await p.mouse.move(r.x + 5, r.y + r.height / 2);
  await p.mouse.down();
  const mid = await p.evaluate(() => scrollY);
  await p.mouse.up();
  const after = await p.evaluate(() => scrollY);
  ok(before > 1000 && mid === before && after === before, `${path}: scrollY unchanged by mousedown/mouseup (${before} ${mid} ${after})`);
  ok(await p.evaluate(() => window.__clicks === 1 && document.activeElement.id !== "hm-main"), `${path}: click fires, main not focused`);

  await p.evaluate(() => scrollTo(0, 0));
  await p.keyboard.press("Tab");
  ok(await p.evaluate(() => document.activeElement.classList.contains("hm-skip")), `${path}: skip link is the first Tab stop`);
  await p.keyboard.press("Enter");
  ok(await p.evaluate(() => document.activeElement.id === "hm-main"), `${path}: skip link moves focus to main`);
  await p.keyboard.press("Tab");
  ok(await p.evaluate(() => !document.querySelector("#hm-main").hasAttribute("tabindex") && document.querySelector("#hm-main").contains(document.activeElement)), `${path}: Tab leaves main into its content; tabindex dropped`);

  // After the skip link, a mousedown on plain text inside main (main still focused) drops the
  // tabindex before it can focus main, so the page does not jump.
  await p.goto(base + path);
  await p.evaluate(() => { document.querySelector("#hm-main").insertAdjacentHTML("beforeend", '<div style="height:3000px"></div><p id="qa-text" style="margin:0">plain text</p><div style="height:3000px"></div>'); });
  await p.keyboard.press("Tab");
  await p.keyboard.press("Enter");
  ok(await p.evaluate(() => document.activeElement.id === "hm-main"), `${path}: skip link moves focus to main again`);
  await p.locator("#qa-text").scrollIntoViewIfNeeded();
  const y0 = await p.evaluate(() => scrollY);
  const r2 = await p.locator("#qa-text").boundingBox();
  await p.mouse.move(r2.x + 5, r2.y + r2.height / 2);
  await p.mouse.down();
  const y1 = await p.evaluate(() => scrollY);
  ok(await p.evaluate(() => !document.querySelector("#hm-main").hasAttribute("tabindex")), `${path}: a mousedown after the skip link drops main's tabindex`);
  await p.mouse.up();
  const y2 = await p.evaluate(() => scrollY);
  ok(y0 > 1000 && y1 === y0 && y2 === y0 && (await p.evaluate(() => document.activeElement.id !== "hm-main")), `${path}: no scroll jump or focus on main (${y0} ${y1} ${y2})`);
}
await b.close();
process.exit(bad ? 1 : 0);
