// The community policy panel on Settings > Comments, by click paths: a content type made through the admin
// ("Notes"), its card under Content types, the policy switch with the confirm dialog (Turn on stays disabled
// until the understand switch is ticked), the three numbers (reply depth, edit window, slow mode) and the order
// choice saved and read back, the plain-page confirm without JavaScript (a 422 without the tick), and the editor's
// read-only panel plus the refused POST. Screenshots go to shots-out/comments-policy/ (panel-desktop.png and
// panel-mobile.png) for the comparison with the owner's mockup. The type is deleted, the policy and order reset,
// comments switched off and the site address put back at the end, even after a failure.
// node scripts/admin-qa/comments-policy.mjs   Exit 1 on a failed check, a console error or a page error.
import fs from "node:fs";
import path from "node:path";
import { launch, newSession, base, admin, here } from "./lib.mjs";

const OFF_HELP = "Links and new commenters are held.";
const ON_WARNING = "Links and first-time commenters are posted at once, not held.";
const CONFIRM = "Are you sure? With the community policy on, comments with links and first comments on this content type are posted at once, without review. Spam checks, blocked commenters, duplicates, captcha and surge mode still hold comments.";
const TICK = "I understand comments on this type will not wait for review";
const SPAM_TICK = "I understand this will invite spam";
const settings = `${admin}/settings/comments`;
const general = `${admin}/settings/general`;
const slug = "qa-notes";
const shots = process.env.SHOTS || path.join(here, "shots-out", "comments-policy");
fs.mkdirSync(shots, { recursive: true });

const b = await launch();
let bad = 0;
const ok = (c, m) => {
  console.log(`${c ? "ok  " : "FAIL"} ${m}`);
  if (!c) bad++;
};
const norm = (t) => t.replace(/\s+/g, " ").trim();
const errors = [];
const watch = (p) => {
  p.on("pageerror", (e) => errors.push(e.message));
  p.on("console", (m) => {
    // The no-JS 422 and the editor's refused POST log their status as a console error.
    if (m.type() === "error" && !(watch.expect4xx && /status of 4\d\d/.test(m.text()))) errors.push(m.text());
  });
  p.on("dialog", (d) => d.accept().catch(() => {}));
};
const text = (p) => p.locator("body").innerText().then(norm);
const card = (p) => p.locator(`form:has(#comments-type-${slug})`);
const policySwitch = (p) => p.locator(`#comments-type-${slug}-policy`);
const field = (p, name) => p.locator(`#comments-type-${slug}-${name}`);
const overflow = (p) => p.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
const csrf = (p) => p.$eval('meta[name="csrf-token"]', (m) => m.content);
async function setAddress(p, url) {
  await p.goto(base + general);
  await p.fill("#url", url);
  await p.getByRole("button", { name: "Save identity" }).click();
  await p.waitForLoadState("load");
}
// The native select behind the styled one: set it and let the form see the change.
const choose = (p, name, value) =>
  field(p, name).evaluate((el, v) => {
    el.value = v;
    el.dispatchEvent(new Event("change", { bubbles: true }));
  }, value);
async function editorSession() {
  const p = await newSession(b, { signedIn: false });
  watch(p);
  await p.goto(`${base}${admin}/login`);
  await p.fill("#email", process.env.QA_EDITOR_EMAIL || "editor@qa.test");
  await p.fill("#password", process.env.QA_PASSWORD || "qa-password-123");
  await Promise.all([p.waitForURL((u) => !u.pathname.endsWith(`${admin}/login`)), p.click(`form[action="${admin}/login"] button[type=submit]`)]);
  return p;
}

const page = await newSession(b, { width: 1440 });
watch(page);
let nojs;
let originalAddress = null;
let typeID = "";
try {
  // ============ 1. comments on, and a second content type ============
  await page.goto(base + general);
  originalAddress = await page.inputValue("#url");
  if (originalAddress === "") await setAddress(page, base);
  await page.goto(base + settings);
  const enabled = page.getByRole("switch").first();
  if (!(await enabled.isChecked())) {
    await enabled.click();
    const dlg = page.locator("dialog#comments-enable");
    await dlg.waitFor({ state: "visible" });
    await dlg.getByLabel(SPAM_TICK).check();
    await Promise.all([page.waitForURL((u) => u.pathname === settings), dlg.getByRole("button", { name: "Turn on" }).click()]);
  }
  await page.goto(`${base}${admin}/settings/types/new`);
  await page.fill('[name="name"]', "Notes");
  await page.fill('[name="slug"]', slug);
  await page.locator('button[name="op"][type="submit"]:visible').last().click();
  await page.waitForLoadState("load");

  // ============ 2. the cards, policy off ============
  await page.goto(base + settings);
  ok((await card(page).count()) === 1, "panel: the Notes content type has a card");
  const action = await card(page).getAttribute("action");
  typeID = action.split("/").pop();
  ok((await text(page)).includes("Comments: on"), "panel: the card says Comments: on");
  ok((await card(page).innerText().then(norm)).includes(OFF_HELP), "off: the help says links and new commenters are held");
  ok((await card(page).locator('input[type="number"]').count()) === 0, "off: no number fields");
  ok(!(await policySwitch(page).isChecked()), "off: the policy switch is off");

  // ============ 3. JS: the confirm dialog ============
  await policySwitch(page).click();
  const dlg = page.locator(`dialog#comments-policy-${slug}`);
  await dlg.waitFor({ state: "visible" });
  ok((await dlg.innerText().then(norm)).includes(CONFIRM), "dialog: the confirm text, verbatim");
  const turnOn = dlg.getByRole("button", { name: "Turn on" });
  ok(await turnOn.isDisabled(), "dialog: Turn on is disabled until the switch is ticked");
  await dlg.getByLabel(TICK).check();
  ok(await turnOn.isEnabled(), "dialog: ticking the understand switch enables Turn on");
  await Promise.all([page.waitForURL((u) => u.pathname === settings), turnOn.click()]);
  await page.goto(base + settings);
  ok((await policySwitch(page).isChecked()) && (await card(page).innerText().then(norm)).includes(ON_WARNING), "on: the switch is on and the callout shows");
  const vals = async () => [await field(page, "max-depth").inputValue(), await field(page, "edit-window-minutes").inputValue(), await field(page, "slow-mode-seconds").inputValue()];
  ok((await vals()).join("/") === "10/15/0", `on: the numbers start at 10/15/0 (${(await vals()).join("/")})`);

  // ============ 4. numbers and order saved and read back ============
  await field(page, "max-depth").fill("32");
  await field(page, "edit-window-minutes").fill("120");
  await field(page, "slow-mode-seconds").fill("30");
  await choose(page, "order", "oldest");
  await Promise.all([page.waitForURL((u) => u.pathname === settings), card(page).getByRole("button", { name: "Save" }).click()]);
  await page.goto(base + settings);
  ok((await vals()).join("/") === "32/120/30", `saved: the numbers persist (${(await vals()).join("/")})`);
  ok((await field(page, "order").inputValue()) === "oldest", "saved: the order persists");
  await card(page).scrollIntoViewIfNeeded();
  await page.screenshot({ path: path.join(shots, "panel-desktop.png"), fullPage: true });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto(base + settings);
  ok((await overflow(page)) <= 0, "390px: no horizontal scroll on the panel");
  await page.screenshot({ path: path.join(shots, "panel-mobile.png"), fullPage: true });
  await page.setViewportSize({ width: 1440, height: 900 });

  // ============ 5. no JS: the policy needs the tick ============
  await page.goto(base + settings);
  await policySwitch(page).uncheck();
  await Promise.all([page.waitForURL((u) => u.pathname === settings), card(page).getByRole("button", { name: "Save" }).click()]);
  nojs = await newSession(b, { javaScriptEnabled: false });
  watch(nojs);
  await nojs.goto(base + settings);
  ok(!(await policySwitch(nojs).isChecked()), "no JS: the policy is off after the owner turned it off");
  await policySwitch(nojs).check();
  watch.expect4xx = true;
  const res = await Promise.all([nojs.waitForResponse((r) => r.request().method() === "POST"), card(nojs).getByRole("button", { name: "Save" }).click()]).then((a) => a[0]);
  ok(res.status() === 422 && (await text(nojs)).includes(CONFIRM), `no JS: saving without the tick is a 422 with the confirm text (${res.status()})`);
  await nojs.waitForTimeout(200);
  watch.expect4xx = false;
  await nojs.goto(base + settings);
  ok(!(await policySwitch(nojs).isChecked()), "no JS: nothing was written without the tick");

  // ============ 6. the editor ============
  const ed = await editorSession();
  await ed.goto(base + settings);
  ok(await card(ed).locator("fieldset").first().evaluate((f) => f.disabled), "editor: the card is read-only");
  ok((await ed.locator(`dialog#comments-policy-${slug}`).count()) === 0, "editor: no confirm dialog");
  const token = await csrf(ed);
  watch.expect4xx = true;
  const st = await ed.evaluate(
    async ({ url, token }) => (await fetch(url, { method: "POST", body: new URLSearchParams({ policy: "true", understand: "1", _csrf: token }), credentials: "same-origin", headers: { "X-CSRF-Token": token } })).status,
    { url: `${base}${settings}/types/${typeID}`, token },
  );
  ok(st === 403, `editor: POST to the type answers 403 (${st})`);
  await ed.waitForTimeout(200);
  watch.expect4xx = false;
  await ed.context().close();
} finally {
  // ============ 7. clean up ============
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto(base + settings);
  if (typeID) {
    if (await policySwitch(page).isChecked()) {
      await policySwitch(page).uncheck();
      await card(page).getByRole("button", { name: "Save" }).click();
      await page.waitForLoadState("load");
      await page.goto(base + settings);
    }
    if ((await field(page, "order").inputValue()) !== "") {
      await choose(page, "order", "");
      await card(page).getByRole("button", { name: "Save" }).click();
      await page.waitForLoadState("load");
    }
    const token = await csrf(page);
    await page.evaluate(
      async ({ url, token }) => fetch(url, { method: "POST", body: new URLSearchParams({ _csrf: token }), credentials: "same-origin", headers: { "X-CSRF-Token": token } }),
      { url: `${base}${admin}/settings/types/${typeID}/delete`, token },
    );
    await page.goto(base + settings);
    ok((await card(page).count()) === 0, "cleanup: the Notes type is gone");
  }
  const enabled = page.getByRole("switch").first();
  if (await enabled.isChecked()) {
    await enabled.uncheck();
    await page.getByRole("button", { name: "Save" }).first().click();
    await page.waitForLoadState("load");
  }
  await page.goto(base + settings);
  ok(!(await page.getByRole("switch").first().isChecked()), "cleanup: comments are off again");
  if (originalAddress !== null) {
    await page.goto(base + general);
    if ((await page.inputValue("#url")) !== originalAddress) await setAddress(page, originalAddress);
  }
  await nojs?.context().close();
}
ok(errors.length === 0, `no console or page error${errors.length ? ": " + errors.slice(0, 3).join(" | ") : ""}`);
await b.close();
console.log(bad ? `${bad} check(s) failed` : "all checks passed");
process.exit(bad ? 1 : 0);
