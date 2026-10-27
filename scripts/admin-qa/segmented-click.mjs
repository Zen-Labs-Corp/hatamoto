// A real pointer click on the Allow/Block segmented control (hm/segmented radio mode) changes the
// checked radio and reveals the save bar, at the page's scrolled position. A click on a label
// whose text is not the input used to focus <main tabindex=-1>, which scrolled the page between
// mousedown and mouseup, so the click never landed (live on hatamoto.dev with hm-aia-seg).
// node scripts/admin-qa/segmented-click.mjs   Exit 1 on a failed click.
import { launch, newSession, base } from "./lib.mjs";

const b = await launch();
let bad = 0;
const ok = (c, m) => { console.log(`${c ? "ok  " : "FAIL"} ${m}`); if (!c) bad++; };
const p = await newSession(b, { theme: "light", width: 1440 });

for (const path of ["/admin/analytics/crawlers", "/admin/analytics/agents/gptbot"]) {
  await p.goto(base + path);
  const first = p.locator(".hm-segmented__input").first();
  const name = await first.getAttribute("name");
  const pick = async (value) => {
    const opt = p.locator(`.hm-segmented__input[name="${name}"][value="${value}"]`).locator("..");
    await opt.scrollIntoViewIfNeeded();
    const r = await opt.boundingBox();
    await p.mouse.click(r.x + r.width / 2, r.y + r.height / 2); // a real pointer click, not locator.click()
    return p.locator(`.hm-segmented__input[name="${name}"][value="${value}"]`).isChecked();
  };
  ok(await pick("deny"), `${path}: a mouse click on Block checks it`);
  ok(await p.locator(".hm-savebar").isVisible(), `${path}: the save bar shows`);
  ok(await pick("allow"), `${path}: a mouse click on Allow checks it again`);
  await p.locator(`.hm-segmented__input[name="${name}"][value="deny"]`).focus();
  await p.keyboard.press("ArrowLeft");
  ok(await p.locator(`.hm-segmented__input[name="${name}"][value="allow"]`).isChecked(), `${path}: ArrowLeft moves the radio to Allow`);
}
await b.close();
process.exit(bad ? 1 : 0);
