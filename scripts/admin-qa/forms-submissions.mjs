// A form's Submissions tab, by click paths with real pointer and keyboard events. It makes its own
// form and sends five submissions through the public endpoint, then checks: the stats strip and the
// tracker, search, the status filter, the export (only the rows that match), the side drawer (a row
// click marks it read, Mark unread puts it back, a ?s= address opens it on load, j and k move between submissions from inside
// the open drawer without closing it, Esc closes it and focus returns to the row), the bulk actions
// (Mark read, Mark as spam, Delete with its confirmation), the old addresses, the sidebar badge and
// a 390px viewport. The form is deleted at the end.
// node scripts/admin-qa/forms-submissions.mjs   Exit 1 on a failed check, a console error or a page error.
import fs from "node:fs";
import path from "node:path";
import { launch, newSession, base, admin, outDir } from "./lib.mjs";

const b = await launch();
let bad = 0;
const ok = (c, m) => {
  console.log(`${c ? "ok  " : "FAIL"} ${m}`);
  if (!c) bad++;
};

const page = await newSession(b, { theme: "light", width: 1440 });
const errors = [];
page.on("pageerror", (e) => errors.push(e.message));
page.on("console", (m) => {
  if (m.type() === "error") errors.push(m.text());
});
page.on("dialog", (d) => d.accept().catch(() => {}));

const csrf = () => page.$eval('meta[name="csrf-token"]', (m) => m.content);
const rows = page.locator("tr[data-hm-ss-row]");
const unread = page.locator("tr[data-hm-ss-row].hm-ss__row--unread");
const drawer = page.locator("dialog:has([data-hm-ss-drawer-body])");
const drawerId = () => page.locator("[data-hm-ss-drawer-body] [data-hm-ss-id]").getAttribute("data-hm-ss-id");
const rowText = () => rows.locator("th").allInnerTexts().then((a) => a.map((t) => t.replace(/\s*New\s*$/, "").trim()));
const badge = () => page.locator('.hm-nav-link[href$="/forms"] .hm-nav-link__badge [aria-hidden]').first().innerText({ timeout: 500 }).then((t) => Number(t), () => 0);
let tab;
const open = async (qs = "") => {
  await page.goto(`${base}${tab}${qs}`);
  await page.waitForSelector("[data-hm-ss-drawer-body]", { state: "attached" });
};

// -- a form of our own, with three fields and five submissions ---------------

await page.goto(`${base}${admin}/forms`);
const name = "QA submissions " + Math.random().toString(36).slice(2, 7);
const made = await page.evaluate(
  async ({ admin, token, name }) => {
    const res = await fetch(`${admin}/forms`, { method: "POST", body: new URLSearchParams({ name, _csrf: token }), credentials: "same-origin", headers: { "X-CSRF-Token": token } });
    return { status: res.status, url: res.url };
  },
  { admin, token: await csrf(), name },
);
const formId = made.url.match(/\/forms\/design\/([0-9a-f-]{36})/)?.[1];
ok(!!formId, `a new form is made (${made.url.replace(base, "")})`);
if (!formId) {
  await b.close();
  process.exit(1);
}
tab = `${admin}/forms/design/${formId}/submissions`;
const endpoint = await page.evaluate(
  async ({ admin, id, token }) => {
    const html = await (await fetch(`${admin}/forms/design/${id}`, { credentials: "same-origin" })).text();
    const boot = JSON.parse(new DOMParser().parseFromString(html, "text/html").querySelector("script[data-hm-fd-boot]").textContent);
    const body = {
      version: boot.version, name: boot.name, description: boot.description ?? "", status: "active", settings: { ...boot.settings, rate_limit_per_hour: 1000 },
      fields: [
        { id: "f_a", type: "text", label: "Name", name: "name", position: 0 },
        { id: "f_b", type: "email", label: "Email", name: "email", position: 1 },
        { id: "f_c", type: "textarea", label: "Message", name: "message", position: 2 },
      ],
    };
    const res = await fetch(`${admin}/forms/${id}/definition`, { method: "PUT", credentials: "same-origin", headers: { "Content-Type": "application/json", "X-CSRF-Token": token }, body: JSON.stringify(body) });
    return res.status === 200 ? boot.endpoint : "";
  },
  { admin, id: formId, token: await csrf() },
);
ok(!!endpoint, "three fields are saved and the form is active");

const people = [["Ada", "alpha zebra"], ["Ben", "beta"], ["Cara", "gamma zebra"], ["Dev", "delta"], ["Eli", "epsilon"]];
// The public form limiter lets 5 through, then one every 10 seconds per address (the seed used some): wait on a 429.
let sent = 0;
for (const [who, message] of people) {
  for (let tries = 0; tries < 4; tries++) {
    const res = await page.request.post(endpoint.startsWith("/") ? base + endpoint : endpoint, { data: { name: who, email: `${who.toLowerCase()}@qa.test`, message } });
    if (res.ok()) {
      sent++;
      break;
    }
    if (res.status() !== 429) break;
    await page.waitForTimeout(10500);
  }
}
ok(sent === 5, `five submissions are sent through the public endpoint (${sent})`);

// the sidebar count is cached for a while: wait until it has caught up with the new submissions
let before = 0;
for (let i = 0; i < 20 && before < 5; i++) {
  await page.goto(`${base}${admin}/forms`);
  before = await badge();
  if (before < 5) await page.waitForTimeout(1500);
}
ok(before >= 5, `the sidebar Forms badge counts the new submissions (${before})`);

// ============ the page, the stats strip and the tracker ============
await open();
ok((await rows.count()) === 5 && (await unread.count()) === 5, "five rows, all unread");
ok(page.url().endsWith(`/forms/design/${formId}/submissions`), "the tab address");
ok((await page.locator(".hm-statgrid .hm-statcard").count()) === 4, "stats strip: four cards");
const stats = await page.locator(".hm-statgrid .hm-statcard").allInnerTexts();
ok(stats.some((t) => /Unread\s*5/.test(t.replace(/\s+/g, " "))) && stats.some((t) => /Submissions\s*5/.test(t.replace(/\s+/g, " "))), "stats strip: 5 submissions, 5 unread");
ok((await page.locator(".hm-ss__tracker").count()) === 1 && (await page.locator(".hm-ss__tracker").innerHTML()).length > 100, "the 30-day tracker is drawn");
ok((await page.locator("tr[data-hm-ss-row] a[data-hm-ss-open]").count()) === 5, "every row has an open link");

// ============ search ============
await page.locator("#ss-q").fill("zebra");
await page.waitForFunction(() => document.querySelectorAll("tr[data-hm-ss-row]").length === 2);
ok(same((await rowText()).sort(), ["Ada", "Cara"]) && page.url().includes("q=zebra"), "search narrows the rows and the address");
ok((await page.locator("#ss-status").innerText()).startsWith("2 submissions"), "search: the live region says 2 submissions");
await page.locator("#ss-q").fill("nothing-matches-this");
await page.waitForFunction(() => document.querySelectorAll("tr[data-hm-ss-row]").length === 0);
ok((await page.getByText("No submissions match").count()) > 0, "search: an empty result says so");
await page.locator("#ss-q").fill("");
await page.waitForFunction(() => document.querySelectorAll("tr[data-hm-ss-row]").length === 5);
ok(true, "search: clearing it brings all rows back");

// ============ the status filter ============
await page.locator('#ss-seg label:has-text("Spam")').click();
await page.waitForFunction(() => document.querySelectorAll("tr[data-hm-ss-row]").length === 0);
ok(page.url().includes("status=spam"), "filter: Spam shows none yet");
await page.locator('#ss-seg label:has-text("Unread")').click();
await page.waitForFunction(() => document.querySelectorAll("tr[data-hm-ss-row]").length === 5);
ok(page.url().includes("status=new") && (await page.locator('#ss-seg input[value="new"]').isChecked()), "filter: Unread shows the five and is the pressed segment");
await page.locator('#ss-seg label:has-text("All")').click();
await page.waitForFunction(() => /status=(&|$)/.test(location.search) || !location.search.includes("status="));
ok((await rows.count()) === 5 && (await page.locator('#ss-seg input[value=""]').isChecked()), "filter: All again");

// ============ the export carries the filters and downloads only matching rows ============
const exportHref = (qs) => page.locator('a[href*="export.csv"]').first().getAttribute("href");
await open("?q=zebra&status=new");
const href = await exportHref();
ok(href.includes("q=zebra") && href.includes("status=new") && href.includes(`form=${formId}`), "export: the link carries the form, q and status");
const csv = await page.evaluate(async (u) => (await fetch(u, { credentials: "same-origin" })).text(), href);
ok(csv.includes("Ada") && csv.includes("Cara") && !csv.includes("Ben") && !csv.includes("Dev") && !csv.includes("Eli"), "export: the CSV holds only the matching rows");
await open();
await page.locator("#ss-q").fill("zebra");
await page.waitForFunction(() => document.querySelectorAll("tr[data-hm-ss-row]").length === 2);
const liveHref = (await exportHref()) ?? "";
ok(liveHref.includes("q=zebra") && liveHref.includes(`form=${formId}`), "export: the link follows a live search without a reload");
await page.locator('#ss-seg label:has-text("Unread")').click();
await page.waitForFunction(() => location.search.includes("status=new"));
await page.waitForFunction(() => document.querySelector("#ss-export")?.getAttribute("href")?.includes("status=new"));
const segHref = (await exportHref()) ?? "";
ok(segHref.includes("q=zebra") && segHref.includes("status=new"), "export: the link follows a segment change too");
await page.locator("#ss-q").fill("");
await page.waitForFunction(() => document.querySelectorAll("tr[data-hm-ss-row]").length === 5);

// ============ the drawer: a row click ============
await open();
const ids = await rows.evaluateAll((a) => a.map((r) => r.dataset.hmSsId));
const first = rows.first();
const firstName = (await rowText())[0];
await first.locator("a[data-hm-ss-open]").click();
await page.waitForFunction(() => document.querySelector("dialog[open] [data-hm-ss-drawer-body] [data-hm-ss-id]"));
ok(await drawer.evaluate((d) => d.open), "click: the drawer opens");
ok((await drawer.innerText()).includes(firstName) && (await drawer.innerText()).includes("Message"), `click: the drawer shows ${firstName}'s answers`);
ok((await drawerId()) === ids[0] && page.url().includes(`s=${ids[0]}`), "click: the address names the open submission");
ok(!(await first.evaluate((r) => r.classList.contains("hm-ss__row--unread"))) && (await unread.count()) === 4, "click: its unread styling is gone");

// ============ a drawer action: Mark unread ============
await page.getByRole("button", { name: "Mark unread" }).click();
await page.waitForLoadState("load");
await page.waitForFunction((t) => location.pathname === t, tab);
ok((await page.getByText("Marked as new").count()) > 0 && (await unread.count()) === 5 && (await rows.nth(0).evaluate((r) => r.classList.contains("hm-ss__row--unread"))), "drawer: Mark unread puts the submission back in Unread and returns to the tab");
await open();
await first.locator("a[data-hm-ss-open]").click();
await page.waitForFunction(() => document.querySelector("dialog[open] [data-hm-ss-drawer-body] [data-hm-ss-id]"));

// ============ j and k with the drawer open ============
await drawer.evaluate((d) => { d.dataset.qaSame = "1"; });
const focusInside = await page.evaluate(() => !!document.activeElement?.closest("dialog[open]"));
ok(focusInside, "keys: focus is inside the open drawer");
await page.keyboard.press("j");
await page.waitForFunction((id) => document.querySelector("[data-hm-ss-drawer-body] [data-hm-ss-id]")?.dataset.hmSsId === id, ids[1]);
ok((await drawer.evaluate((d) => d.open && d.dataset.qaSame === "1")), "j: the next submission loads and the drawer stays open (same dialog)");
ok((await drawerId()) === ids[1] && page.url().includes(`s=${ids[1]}`) && (await rows.nth(1).getAttribute("aria-selected")) === "true", "j: body, address and selected row moved to the second");
ok((await unread.count()) === 3, "j: the second one is read too");
await page.keyboard.press("j");
await page.waitForFunction((id) => document.querySelector("[data-hm-ss-drawer-body] [data-hm-ss-id]")?.dataset.hmSsId === id, ids[2]);
await page.keyboard.press("k");
await page.waitForFunction((id) => document.querySelector("[data-hm-ss-drawer-body] [data-hm-ss-id]")?.dataset.hmSsId === id, ids[1]);
ok(await drawer.evaluate((d) => d.open && d.dataset.qaSame === "1"), "k: moves back and the drawer stays open");
await page.keyboard.press("k");
await page.waitForFunction((id) => document.querySelector("[data-hm-ss-drawer-body] [data-hm-ss-id]")?.dataset.hmSsId === id, ids[0]);
await page.keyboard.press("k");
ok((await drawerId()) === ids[0] && (await drawer.evaluate((d) => d.open)), "k: stops at the first row");

// ============ Esc closes, focus returns ============
await page.keyboard.press("Escape");
await page.waitForFunction(() => !document.querySelector("dialog[open]") && !location.search.includes("s="));
ok(!page.url().includes("s="), "Esc: the drawer closes and ?s= leaves the address");
ok(await page.evaluate(() => document.activeElement?.matches("tr[data-hm-ss-row] a[data-hm-ss-open]") && document.activeElement.closest("tr").getAttribute("aria-selected") === "true"), "Esc: focus returns to the selected row's link");

// j and k with the drawer closed only move the selection
await page.keyboard.press("j");
ok((await rows.nth(1).getAttribute("aria-selected")) === "true" && !(await drawer.evaluate((d) => d.open)), "j with the drawer closed selects the next row and opens nothing");

// ============ a deep link opens the drawer on load ============
await open(`?s=${ids[3]}`);
await page.waitForSelector("dialog[open]");
ok((await drawerId()) === ids[3] && (await rows.nth(3).getAttribute("aria-selected")) === "true", "deep link: ?s= opens that submission and marks its row");
ok(!(await rows.nth(3).evaluate((r) => r.classList.contains("hm-ss__row--unread"))), "deep link: it is read");
await page.keyboard.press("Escape");
await page.waitForFunction(() => !document.querySelector("dialog[open]"));

// ============ old addresses end on the tab ============
await page.goto(`${base}${admin}/forms/submissions?form=${formId}`);
ok(new URL(page.url()).pathname === tab && (await rows.count()) === 5, "old list address: ends on the tab");
await page.goto(`${base}${admin}/forms/submissions/${ids[4]}`);
ok(new URL(page.url()).pathname === tab, `old single address: ends on the tab (${page.url().replace(base, "")})`);

// ============ bulk actions ============
await open();
await rows.nth(2).locator("input[data-hm-row-select]").check();
await rows.nth(4).locator("input[data-hm-row-select]").check();
await page.locator('[data-hm-bulk-toolbar] button:has-text("Mark read")').click();
await page.waitForLoadState("load");
ok(new URL(page.url()).pathname === tab, "bulk Mark read: back on the tab");
ok((await page.getByText("2 submissions marked as read").count()) > 0, "bulk Mark read: the flash says 2 marked");
await page.locator('#ss-seg label:has-text("Unread")').click();
await page.waitForFunction(() => document.querySelectorAll("tr[data-hm-ss-row]").length === 0);
ok(true, "bulk Mark read: nothing is left in the Unread list");
await open();

await page.locator(`tr[data-hm-ss-id="${ids[2]}"] input[data-hm-row-select]`).check();
await page.locator('[data-hm-bulk-toolbar] button:has-text("Mark as spam")').click();
await page.waitForLoadState("load");
ok((await page.getByText("1 submission marked as spam").count()) > 0, "bulk Spam: the flash says 1 marked");
await page.locator('#ss-seg label:has-text("Spam")').click();
await page.waitForFunction(() => document.querySelectorAll("tr[data-hm-ss-row]").length === 1);
ok((await rows.first().getAttribute("data-hm-ss-id")) === ids[2], "bulk Spam: it is in the Spam list");

await open();
await page.locator(`tr[data-hm-ss-id="${ids[4]}"] input[data-hm-row-select]`).check();
await page.locator('[data-hm-bulk-toolbar] button:has-text("Delete")').click();
const confirm = page.locator('dialog[open] button:has-text("Delete"), main button:has-text("Delete")').last();
await confirm.waitFor();
await page.screenshot({ path: path.join(outDir, "forms-submissions-delete-confirm.png") });
await confirm.click();
await page.waitForLoadState("load");
await page.waitForFunction((t) => location.pathname === t, tab);
ok((await page.locator(`tr[data-hm-ss-id="${ids[4]}"]`).count()) === 0 && (await page.getByText("1 submission deleted").count()) > 0, "bulk Delete: asks first, then the row is gone");
await open();
ok((await rows.count()) === 4, "bulk Delete: four rows are left");

// ============ the sidebar badge falls as submissions are read ============
// The count is cached for 30 seconds and refreshed behind a render: reload until it drops.
let after = before;
for (let i = 0; i < 40 && after >= before; i++) {
  await page.goto(`${base}${admin}/forms`);
  after = await badge();
  if (after >= before) await page.waitForTimeout(2000);
}
ok(after < before, `sidebar badge: Forms went from ${before} to ${after}`);

// ============ a phone ============
await page.setViewportSize({ width: 390, height: 844 });
await open();
const over = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
ok(over <= 0, `390px: no horizontal scroll on the page (${over}px over)`);
fs.mkdirSync(outDir, { recursive: true });
await page.screenshot({ fullPage: true, path: path.join(outDir, "forms-submissions-390.png") });
await page.setViewportSize({ width: 1440, height: 900 });
await open(`?s=${ids[0]}`);
await page.waitForSelector("dialog[open]");
await page.screenshot({ path: path.join(outDir, "forms-submissions.png") });

// -- clean up and report -----------------------------------------------------
await page.goto(`${base}${admin}/forms`);
const token = await csrf();
await page.evaluate(
  async ({ admin, id, token }) => {
    await fetch(`${admin}/forms/delete/${id}`, { method: "POST", body: new URLSearchParams({ _csrf: token }), credentials: "same-origin", headers: { "X-CSRF-Token": token } });
  },
  { admin, id: formId, token },
);
ok(errors.length === 0, `no console or page errors${errors.length ? ": " + errors.slice(0, 3).join(" | ") : ""}`);
await b.close();
console.log(bad ? `${bad} check(s) failed` : "all checks passed");
process.exit(bad ? 1 : 0);

function same(a, c) {
  return JSON.stringify(a) === JSON.stringify(c);
}
