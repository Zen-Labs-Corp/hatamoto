// The entity parent picker (native <details>): closed it shows the parent chip and "Change";
// open it shows "Done" and hides the chip and "Change". A merged CSS selector once left both
// words showing at all times. Needs a type whose URL pattern has {parent}: the script creates
// "QA guide" (/guides/{parent}/{slug}) and one entry when no such type exists.
// node scripts/admin-qa/parent-picker.mjs   Exit 1 on a failure.
import { launch, newSession, base } from "./lib.mjs";

const b = await launch();
let bad = 0;
const ok = (c, m) => { console.log(`${c ? "ok  " : "FAIL"} ${m}`); if (!c) bad++; };
const p = await newSession(b, { theme: "light", width: 1440 });

const typePath = "/admin/entities/new?type=qa-guide";
await p.goto(base + typePath);
if (!(await p.locator(".hm-parentpick").count())) {
  await p.goto(base + "/admin/settings/types/new");
  await p.fill('[name="name"]', "QA guide");
  await p.fill('[name="slug"]', "qa-guide");
  await p.fill('[name="url_pattern"]', "/guides/{parent}/{slug}");
  await p.locator('button[name="op"][type="submit"]:visible').last().click();
  await p.waitForLoadState();
  await p.goto(base + typePath);
}
await p.fill('[name="name"]', "QA parent picker");
await p.fill('[name="slug"]', "qa-parent-picker-" + Date.now());
await Promise.all([p.waitForURL((u) => !u.search.includes("type=qa-guide")), p.press('[name="name"]', "Enter")]);

const seen = (sel) => p.locator(sel).evaluate((el) => !!el.getClientRects().length && getComputedStyle(el).display !== "none");
const state = () => Promise.all([seen(".hm-parentpick__open"), seen(".hm-parentpick__done"), seen("[data-hm-parent-chip]")]);
ok((await p.locator(".hm-parentpick").count()) === 1, "the entity form has a parent picker");
let [change, done, chip] = await state();
ok(change && !done && chip, `closed: Change shown, Done hidden, chip shown (${change} ${done} ${chip})`);
await p.locator(".hm-parentpick__summary").click();
ok(await p.locator(".hm-parentpick").evaluate((d) => d.open), "the summary opens the picker");
[change, done, chip] = await state();
ok(!change && done && !chip, `open: Done shown, Change hidden, chip hidden (${change} ${done} ${chip})`);
await p.locator(".hm-parentpick__summary").click();
[change, done, chip] = await state();
ok(change && !done && chip, `closed again: Change shown, Done hidden, chip shown (${change} ${done} ${chip})`);
await b.close();
process.exit(bad ? 1 : 0);
