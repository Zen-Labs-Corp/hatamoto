// Comments in the admin, by click paths: the owner-only two-warning enable flow (callout, dialog with the
// understand switch, and the plain-page path with JavaScript off), the editor's read-only settings, the sidebar
// item and badge, the audit entry, and the moderation basics (held reasons, approve from the row menu, bulk trash),
// on a desktop and a 390px viewport. Comments are switched off again at the end, even after a failure.
// Needs QA_SEED_COMMENTS=1 for checks 4 (badge) and 6 (moderation); without it they are skipped with a note.
// node scripts/admin-qa/comments-admin.mjs   Exit 1 on a failed check, a console error or a page error.
import { launch, newSession, base, admin } from "./lib.mjs";

const seeded = process.env.QA_SEED_COMMENTS === "1";
const CALLOUT = "Turning comments on will invite spam. Bots and spammers find comment forms within days. Every comment with a link and every first comment will wait in your moderation queue.";
const WARNING = "Are you sure? Turning comments on will invite spam to this site. You will need to check the moderation queue regularly.";
const UNDERSTAND = "I understand this will invite spam";
const settings = `${admin}/settings/comments`;

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
    if (m.type() === "error") errors.push(m.text());
  });
  p.on("dialog", (d) => d.accept().catch(() => {}));
};
const text = (p) => p.locator("body").innerText().then(norm);
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
try {
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
  let seen = false;
  for (let i = 0; i < 35 && !seen; i++) {
    await page.goto(base + settings);
    seen = (await commentsLink(page).count()) > 0;
    if (!seen) await page.waitForTimeout(1000);
  }
  ok(seen, "sidebar: the Comments item appears (up to the 30 s cache)");
  if (seeded) {
    let n = 0;
    for (let i = 0; i < 35 && n === 0; i++) {
      n = await badge(page);
      if (n === 0) {
        await page.waitForTimeout(1000);
        await page.reload();
      }
    }
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
  const st = await ed.evaluate(
    async ({ url, token }) => (await fetch(url, { method: "POST", body: new URLSearchParams({ understand: "1", _csrf: token }), credentials: "same-origin", headers: { "X-CSRF-Token": token } })).status,
    { url: `${base}${settings}/enable`, token },
  );
  ok(st === 403, `editor: POST enable answers 403 (${st})`);
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
  await nojs?.context().close();
}
ok(errors.length === 0, `no console or page errors${errors.length ? ": " + errors.slice(0, 3).join(" | ") : ""}`);
await b.close();
console.log(bad ? `${bad} check(s) failed` : "all checks passed");
process.exit(bad ? 1 : 0);
