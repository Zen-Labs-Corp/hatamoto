// One-step review (A1/A2/A4 in a browser): with review on, an author's change to their published post is
// staged, not written; an editor sees it in the Review queue, sends it back with a comment; the author
// sees the comment, sends a new version; the editor approves and the change goes live.
// The seed (review-edit-seed.sh, run by run-local.sh) makes the author, the post and turns review on.
// Run: SHOTS=<dir> bash scripts/admin-qa/run-local.sh review-edit-flow.mjs   Exit 1 on any problem.
import fs from "node:fs";
import path from "node:path";
import { execFileSync } from "node:child_process";
import { launch, base, admin, password, sizes } from "./lib.mjs";

const psql = (...args) => execFileSync(process.env.PG_BIN ? path.join(process.env.PG_BIN, "psql") : "psql", [process.env.DATABASE_URL, "-v", "ON_ERROR_STOP=1", "-At", ...args], { encoding: "utf8" }).trim();
const SHOTS = process.env.SHOTS || "";
if (SHOTS) fs.mkdirSync(SHOTS, { recursive: true });
// The admin cross-fades between pages: let it settle before the shot.
const shot = async (page, name) => {
  if (!SHOTS) return;
  await page.waitForTimeout(600);
  await page.screenshot({ path: path.join(SHOTS, name), fullPage: true });
};

let bad = 0;
const check = (ok, msg, detail = "") => {
  console.log(`${ok ? "PASS" : "FAIL"}  ${msg}${!ok && detail ? "  " + detail : ""}`);
  if (!ok) bad++;
};

const AUTHOR = process.env.QA_AUTHOR_EMAIL || "author@qa.test";
const EDITOR = process.env.QA_EDITOR_EMAIL || "editor@qa.test";
const OWNER = process.env.QA_EMAIL || "owner@qa.test";
const TITLE = "QA Review Post";

const b = await launch("chromium");
const errors = [];
// A signed-in page for one person, in its own browser context (its own cookies).
async function session(email) {
  const ctx = await b.newContext({ viewport: sizes[1440] });
  const page = await ctx.newPage();
  page.on("pageerror", (e) => errors.push(`${email}: ${e}`));
  await page.goto(`${base}${admin}/login`);
  await page.fill("#email", email);
  await page.fill("#password", password);
  await Promise.all([page.waitForURL((u) => !u.pathname.endsWith(`${admin}/login`)), page.click(`form[action="${admin}/login"] button[type=submit]`)]);
  if (page.url().includes("/profile/complete")) {
    // A new account completes its profile once: a name is enough (no public byline).
    await page.fill("#name", "QA " + email[0].toUpperCase() + email.slice(1, email.indexOf("@")));
    await Promise.all([page.waitForURL((u) => !u.pathname.endsWith("/profile/complete")), page.getByRole("button", { name: "Save and continue" }).click()]);
  }
  if (page.url().includes("/login") || page.url().includes("/profile/complete")) throw new Error(`sign-in of ${email} ended on ${page.url()}`);
  return page;
}
const text = (page) => page.locator("main").innerText();
const flash = async (page) => (await page.locator(".hm-flash, .hm-toast").allInnerTexts()).join(" ");
const live = (id) => psql("-c", `SELECT name FROM hatamoto_entities WHERE id = '${id}'`);

const id = psql("-c", `SELECT id FROM hatamoto_entities WHERE name = '${TITLE}' LIMIT 1`);
check(/^[0-9a-f-]{36}$/.test(id), "the seed made the author's post", id);
const editPath = `${base}${admin}/entities/${id}/edit`;
const sendButton = (p) => p.getByRole("button", { name: "Send for review" }).first();

// A1: the author's change waits.
const author = await session(AUTHOR);
await author.goto(editPath, { waitUntil: "load" });
check(await sendButton(author).isVisible(), "the author's editor offers Send for review");
await author.fill("#name", `${TITLE} (revised)`);
await Promise.all([author.waitForNavigation({ waitUntil: "load" }), sendButton(author).click()]);
check(/Sent for review/.test(await flash(author)), "the author is told it was sent", await flash(author));
check(/Waiting for review since/.test(await text(author)), "the editor shows the waiting banner");
check(live(id) === TITLE, "the live post is unchanged", live(id));
await shot(author, "editor-waiting.png");

// The editor decides.
const editor = await session(EDITOR);
await editor.goto(`${base}${admin}/review`, { waitUntil: "load" });
check((await text(editor)).includes(`${TITLE} (revised)`), "the queue lists the change");
// The sidebar item is a cached count (up to 30 s old, filled in the background): look again until it shows.
const badge = () => editor.locator(`#hm-sidebar a[href="${admin}/review"] .hm-nav-link__badge [aria-hidden]`).innerText({ timeout: 500 }).then((t) => t.trim(), () => "");
let shown = "";
for (let i = 0; i < 20 && shown !== "1"; i++) {
  if (i) await editor.waitForTimeout(2000);
  await editor.reload({ waitUntil: "load" });
  shown = await badge();
}
check(shown === "1", "the sidebar shows Review to the editor with the waiting count", shown);
await shot(editor, "review-queue.png");
await Promise.all([editor.waitForURL(/\/review\/\d+$/), editor.getByRole("link", { name: "Open", exact: true }).first().click()]);
const detail = editor.url();
const body = await text(editor);
check(/\/review\/\d+$/.test(detail), "Open leads to the change", detail);
check(body.includes("name") && body.includes(`${TITLE} (revised)`), "the page shows the difference");
check(await editor.getByRole("button", { name: "Approve and publish" }).isVisible(), "Approve and publish is offered");
await shot(editor, "review-detail.png");

// Send back needs a comment.
await Promise.all([editor.waitForNavigation({ waitUntil: "load" }), editor.getByRole("button", { name: "Send back" }).click()]);
check(/Add a comment so the author knows what to change\./.test(await text(editor)), "an empty send back is refused with a hint");
await editor.fill("#review-comment", "Please keep the original title.");
await Promise.all([editor.waitForURL(/\/review$/), editor.getByRole("button", { name: "Send back" }).click()]);
check(/Sent back/.test(await flash(editor)), "the send back is confirmed", await flash(editor));
check(live(id) === TITLE, "the live post is still unchanged", live(id));

// The author sees the comment and sends a new version.
await author.goto(editPath, { waitUntil: "load" });
check(/Sent back by QA Editor: Please keep the original title\./.test(await text(author)), "the author sees the comment");
check((await author.inputValue("#name")) === `${TITLE} (revised)`, "the editor still holds the author's version");
await shot(author, "editor-sent-back.png");
await author.fill("#name", `${TITLE} v2`);
await Promise.all([author.waitForNavigation({ waitUntil: "load" }), sendButton(author).click()]);
check(/Waiting for review since/.test(await text(author)), "the new version waits");

// Approve: the change goes live.
await editor.goto(`${base}${admin}/review`, { waitUntil: "load" });
await Promise.all([editor.waitForURL(/\/review\/\d+$/), editor.getByRole("link", { name: "Open", exact: true }).first().click()]);
await Promise.all([editor.waitForURL(/\/review$/), editor.getByRole("button", { name: "Approve and publish" }).click()]);
check(/Approved\. The change is live\./.test(await flash(editor)), "the approval is confirmed", await flash(editor));
check(live(id) === `${TITLE} v2`, "the live post has the approved title", live(id));
await editor.goto(`${base}${admin}/review`, { waitUntil: "load" });
check(/Nothing is waiting for review\./.test(await text(editor)), "the queue is empty again");

// The owner sees the switch on.
const owner = await session(OWNER);
await owner.goto(`${base}${admin}/settings/review`, { waitUntil: "load" });
check(await owner.locator('input[name="review_enabled"][role="switch"]').isChecked(), "Settings shows review on");
await shot(owner, "settings-review.png");

check(errors.length === 0, "no script errors", errors.join(" | "));
await b.close();
if (bad) {
  console.log(`${bad} check(s) failed`);
  process.exit(1);
}
console.log("review-edit-flow: all checks passed");
