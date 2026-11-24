// Comments in the admin, by click paths: the owner-only two-warning enable flow (callout, dialog with the
// understand switch, and the plain-page path with JavaScript off), the editor's read-only settings, the sidebar
// item and badge, the audit entry, and the moderation basics (held reasons, approve from the row menu, bulk trash),
// on a desktop and a 390px viewport. Comments are switched off again at the end, even after a failure.
// First it checks the no-address state: the warning, the disabled switch, no enable dialog and the link to
// Settings > General; a screenshot goes to shots-out/comments/. When the instance has no address the warning must
// be there (a missing warning fails); when one is already set the step is skipped with a note. The script sets an
// address for the rest of the run and puts the original one back at the end.
// Needs QA_SEED_COMMENTS=1 for checks 4 (badge) and 6 (moderation); without it they are skipped with a note.
// node scripts/admin-qa/comments-admin.mjs   Exit 1 on a failed check, a console error or a page error.
import fs from "node:fs";
import path from "node:path";
import { launch, newSession, base, admin, here } from "./lib.mjs";

const seeded = process.env.QA_SEED_COMMENTS === "1";
const CALLOUT = "Turning comments on will invite spam. Bots and spammers find comment forms within days. Every comment with a link and every first comment will wait in your moderation queue.";
const WARNING = "Are you sure? Turning comments on will invite spam to this site. You will need to check the moderation queue regularly.";
const UNDERSTAND = "I understand this will invite spam";
const NO_ADDRESS = "Comments need your site's public address to send confirmation emails.";
const settings = `${admin}/settings/comments`;
const general = `${admin}/settings/general`;
const shots = process.env.SHOTS || path.join(here, "shots-out", "comments");
fs.mkdirSync(shots, { recursive: true });

const b = await launch();
let bad = 0;
const ok = (c, m) => {
  console.log(`${c ? "ok  " : "FAIL"} ${m}`);
  if (!c) bad++;
};
const skip = (m) => console.log(`skip ${m} (QA_SEED_COMMENTS=1 not set)`);
const norm = (t) => t.replace(/\s+/g, " ").trim();
const errors = [];
const watch = (p) => {
  p.on("pageerror", (e) => errors.push(e.message));
  p.on("console", (m) => {
    // The editor check below sends a POST that must be refused: its 403 is the expected console line.
    if (m.type() === "error" && !(watch.expect403 && /status of 403/.test(m.text()))) errors.push(m.text());
  });
  p.on("dialog", (d) => d.accept().catch(() => {}));
};
const text = (p) => p.locator("body").innerText().then(norm);
// Sets (or, with "", clears) the site address on Settings > General.
async function setAddress(p, url) {
  await p.goto(base + general);
  await p.fill("#url", url);
  await p.getByRole("button", { name: "Save identity" }).click();
  await p.waitForLoadState("load");
}
const sw = (p) => p.getByRole("switch").first();
const overflow = (p) => p.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
const commentsLink = (p) => p.locator(`.hm-nav-link[href$="/comments"]`).first();
const badge = (p) => commentsLink(p).locator(".hm-nav-link__badge [aria-hidden]").first().innerText({ timeout: 500 }).then((t) => Number(t), () => 0);
// The status filter is the segmented control #comments-seg; each option carries its count in #comments-seg-<status>-n.
const tabCount = async (p, status) => Number(await p.locator(`#comments-seg-${status}-n`).innerText());

// The editor signs in with the same password; lib.mjs logs in as one role per run.
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
// The Site address the instance had before the run; the cleanup puts back exactly this.
let originalAddress = null;
try {
  await page.goto(base + general);
  originalAddress = await page.inputValue("#url");
  // ============ 0. no site address: warned and blocked ============
  await page.goto(base + settings);
  if (originalAddress === "") {
    ok((await text(page)).includes(NO_ADDRESS), "no address: settings say comments need the public address");
    ok(await sw(page).isDisabled(), "no address: the switch is disabled");
    ok((await page.locator("dialog#comments-enable").count()) === 0, "no address: there is no enable dialog");
    ok((await page.locator('a[href$="/settings/general#identity"]').count()) > 0, "no address: a link leads to Settings > General");
    await page.screenshot({ path: path.join(shots, "settings-comments-no-address.png"), fullPage: true });
    await setAddress(page, base);
    await page.goto(base + settings);
    ok(!(await text(page)).includes(NO_ADDRESS), "address set: the warning is gone");
  } else console.log(`skip no-address checks (the instance already has a site address: ${originalAddress})`);

  // ============ 1. the callout and the switch, off ============
  await page.goto(base + settings);
  ok((await text(page)).includes(CALLOUT), "settings: the callout carries the first warning verbatim");
  ok(!(await sw(page).isChecked()), "settings: the switch is off");

  // ============ 2. JS: the enable dialog ============
  await sw(page).click();
  const dlg = page.locator("dialog#comments-enable");
  await dlg.waitFor({ state: "visible" });
  ok((await dlg.innerText().then(norm)).includes(WARNING), "dialog: the second warning, verbatim");
  const turnOn = dlg.getByRole("button", { name: "Turn on" });
  ok(await turnOn.isDisabled(), "dialog: Turn on is disabled until the switch is ticked");
  await dlg.getByLabel(UNDERSTAND).check();
  ok(await turnOn.isEnabled(), "dialog: ticking the understand switch enables Turn on");
  await dlg.getByRole("button", { name: "Cancel" }).click();
  await dlg.waitFor({ state: "hidden" });
  ok(!(await sw(page).isChecked()), "dialog: Cancel closes it and the switch is still off");

  // ============ 3. no JS: the plain page ============
  nojs = await newSession(b, { javaScriptEnabled: false });
  await nojs.goto(base + settings);
  await sw(nojs).check();
  await Promise.all([nojs.waitForURL(/\/settings\/comments\/enable/), nojs.getByRole("button", { name: "Save" }).first().click()]);
  ok(nojs.url().includes(`${settings}/enable`), "no JS: ticking the switch and saving lands on the enable page");
  const res = await Promise.all([nojs.waitForResponse((r) => r.request().method() === "POST"), nojs.getByRole("button", { name: "Turn on" }).click()]).then((a) => a[0]);
  ok(res.status() === 422 && /understand/i.test(await text(nojs)), `no JS: submitting without the tick is a 422 with the error (${res.status()})`);
  await nojs.getByLabel(UNDERSTAND).check();
  await Promise.all([nojs.waitForURL((u) => u.pathname === settings), nojs.getByRole("button", { name: "Turn on" }).click()]);
  const after = await text(nojs);
  ok(nojs.url().includes(settings) && /comments.*(on|enabled)/i.test(after), "no JS: Turn on returns to settings with a success flash");
  ok(!after.includes(CALLOUT) && (await sw(nojs).isChecked()), "no JS: the callout is gone and the switch is on");

  // ============ 4. sidebar item, badge, audit ============
  await page.goto(base + settings);
  ok((await commentsLink(page).count()) > 0, "sidebar: the Comments item shows right after turning comments on");
  if (seeded) {
    const n = await badge(page);
    ok(n > 0, `sidebar: the Comments badge shows pending (${n})`);
  } else skip("sidebar badge");
  await page.goto(`${base}${admin}/audit`);
  ok((await text(page)).includes("Turned comments on"), "audit log: lists Turned comments on");

  // ============ 5. the editor ============
  const ed = await editorSession();
  await ed.goto(base + settings);
  ok(await sw(ed).isDisabled(), "editor: the switch on settings is disabled");
  await ed.goto(base + settings);
  const token = await ed.$eval('meta[name="csrf-token"]', (m) => m.content);
  watch.expect403 = true;
  const st = await ed.evaluate(
    async ({ url, token }) => (await fetch(url, { method: "POST", body: new URLSearchParams({ understand: "1", _csrf: token }), credentials: "same-origin", headers: { "X-CSRF-Token": token } })).status,
    { url: `${base}${settings}/enable`, token },
  );
  ok(st === 403, `editor: POST enable answers 403 (${st})`);
  await ed.waitForTimeout(200);
  watch.expect403 = false;
  await ed.context().close();

  // ============ 6. moderation ============
  if (seeded) {
    await page.goto(`${base}${admin}/comments?status=pending`);
    ok((await text(page)).includes("Held: contains a link, first comment"), "queue: a held row says why");
    const rows = page.locator("table tbody tr");
    const approved = await tabCount(page, "approved");
    await rows.first().locator('[popovertarget^="menu-"]').click();
    await page.getByRole("menuitem", { name: "Approve", exact: true }).click();
    await page.waitForLoadState("load");
    await page.goto(`${base}${admin}/comments?status=pending`);
    ok((await tabCount(page, "approved")) === approved + 1, "queue: approving one raises the Approved count by 1");
    const trash = await tabCount(page, "trash");
    const sel = page.locator("input[data-hm-row-select]");
    await sel.nth(0).check();
    await sel.nth(1).check();
    await page.locator('[data-hm-bulk-toolbar] button:has-text("Trash")').click();
    await page.waitForLoadState("load");
    await page.goto(`${base}${admin}/comments?status=pending`);
    ok((await tabCount(page, "trash")) === trash + 2, "queue: bulk trash of two raises the Trash count by 2");

    // Dead tab: one comment a plugin marked dead; Restore moves it to Approved.
    await page.goto(`${base}${admin}/comments?status=dead`);
    ok((await tabCount(page, "dead")) === 1, "dead tab: the Dead count is 1");
    const dead = page.locator("table tbody tr");
    const deadText = await dead.first().innerText();
    ok(deadText.includes("Dead") && deadText.includes("Marked dead by plugin example."), "dead tab: the row has the Dead badge and the plugin line");
    await dead.first().locator('[popovertarget^="menu-"]').click();
    for (const name of ["Restore", "Trash", "Delete"]) {
      ok(await page.getByRole("menuitem", { name, exact: true }).isVisible(), `dead tab: the row offers ${name}`);
    }
    await page.screenshot({ path: path.join(shots, "comments-dead.png"), fullPage: true });
    const approvedBefore = await tabCount(page, "approved");
    await page.getByRole("menuitem", { name: "Restore", exact: true }).click();
    await page.waitForLoadState("load");
    await page.goto(`${base}${admin}/comments?status=dead`);
    ok((await tabCount(page, "dead")) === 0 && (await tabCount(page, "approved")) === approvedBefore + 1, "dead tab: Restore empties Dead and raises Approved by 1");
  } else skip("moderation");

  // ============ a phone ============
  await page.setViewportSize({ width: 390, height: 844 });
  for (const p of [settings, `${admin}/comments`]) {
    await page.goto(base + p);
    ok((await overflow(page)) <= 0, `390px: no horizontal scroll on ${p.replace(admin, "")}`);
  }
  await page.goto(base + settings);
  ok(!(await text(page)).includes(CALLOUT) && (await sw(page).isChecked()), "390px: settings show comments on");
} finally {
  // ============ 7. leave comments off ============
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto(base + settings);
  if (await sw(page).isChecked()) {
    await sw(page).uncheck();
    await page.getByRole("button", { name: "Save" }).first().click();
    await page.waitForLoadState("load");
  }
  await page.goto(base + settings);
  ok(!(await sw(page).isChecked()), "cleanup: comments are off again");
  if (originalAddress !== null) {
    await page.goto(base + general);
    if ((await page.inputValue("#url")) !== originalAddress) await setAddress(page, originalAddress);
  }
  await nojs?.context().close();
}
ok(errors.length === 0, `no console or page errors${errors.length ? ": " + errors.slice(0, 3).join(" | ") : ""}`);
await b.close();
console.log(bad ? `${bad} check(s) failed` : "all checks passed");
process.exit(bad ? 1 : 0);
