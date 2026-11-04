// Changeset review (A2/A3/A5): decide the risky items, meet the password page on a suggestion
// approval, reject the suggestions, publish, then undo the run from Settings, Changes.
// Needs the frameplug fixture (run-local.sh loads it for this script) and seed-changeset.sql.
// Run: SHOTS=<dir> bash scripts/admin-qa/run-local.sh review-flow.mjs   Exit 1 on any problem.
import fs from "node:fs";
import path from "node:path";
import { execFileSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { launch, newSession, base, password } from "./lib.mjs";

const here = path.dirname(fileURLToPath(import.meta.url));
const psql = (...args) => execFileSync(process.env.PG_BIN ? path.join(process.env.PG_BIN, "psql") : "psql", [process.env.DATABASE_URL, "-v", "ON_ERROR_STOP=1", "-At", ...args], { encoding: "utf8" }).trim();
const SHOTS = process.env.SHOTS || "";
if (SHOTS) fs.mkdirSync(SHOTS, { recursive: true });
const shot = (page, name) => (SHOTS ? page.screenshot({ path: path.join(SHOTS, name), fullPage: true }) : null);

let bad = 0;
const check = (ok, msg, detail = "") => {
  console.log(`${ok ? "PASS" : "FAIL"}  ${msg}${!ok && detail ? "  " + detail : ""}`);
  if (!ok) bad++;
};

psql("-v", "running=0", "-f", path.join(here, "seed-changeset.sql"));
const csId = psql("-c", "SELECT max(id) FROM hatamoto_changesets WHERE plugin = 'frameplug' AND state = 'staged'");
const sugIds = psql("-c", "SELECT p.id FROM hatamoto_run_proposals p JOIN hatamoto_changesets c ON c.run_id = p.run_id WHERE c.id = " + csId + " ORDER BY p.id").split("\n");
check(/^\d+$/.test(csId) && sugIds.length === 3, "seeded a changeset with 3 suggestions", `${csId} ${sugIds}`);
const tagline = () => psql("-c", "SELECT COALESCE(tagline, '') FROM hatamoto_site_config LIMIT 1");
const taglineBefore = tagline();

const b = await launch("chromium");
const p = await newSession(b, { width: 1440 });
const errors = [];
p.on("pageerror", (e) => errors.push(String(e)));
p.on("console", (m) => m.type() === "error" && errors.push(m.text()));
const review = `${base}/admin/plugins/frameplug/changesets/${csId}`;
const publish = p.locator('button[formaction$="/publish"]');
// A segmented radio is visually hidden: click its label.
const pick = (name, value) => p.locator(`label.hm-segmented__opt:has(input[name="${name}"][value="${value}"])`).click();
const flash = async () => (await p.locator(".hm-flash, .hm-toast").allInnerTexts()).join(" ");

await p.goto(review, { waitUntil: "load" });
check((await p.locator("h1").innerText()).includes(`#${csId}`), "the review page opens");
check(await publish.isDisabled(), "Publish is disabled while decisions are open");
await shot(p, "review-open-1440.png");

// A2: decide every risky item with the Keep/Drop radios.
const risky = await p.locator('#risky input[type="radio"][name^="item_"]').evaluateAll((els) => [...new Set(els.map((e) => e.name))]);
check(risky.length >= 3, "the risky card lists the items", risky.join(","));
for (const [i, name] of risky.entries()) await pick(name, i === 1 ? "dropped" : "approved");
if (await p.locator('input[name="group_redirects"]').count()) await pick("group_redirects", "approved");
await Promise.all([p.waitForLoadState("load"), p.getByRole("button", { name: "Save decisions" }).click()]);
check(/Saved your decisions/.test(await flash()), "the item decisions are saved", await flash());
check(await publish.isDisabled(), "Publish stays disabled while suggestions are open");

// A3: approving a suggestion changes settings, so it asks for the password first.
await pick(`suggestion_${sugIds[0]}`, "approved");
await Promise.all([p.waitForURL(/\/sudo/), p.getByRole("button", { name: "Save suggestion decisions" }).click()]);
check(p.url().includes("/admin/sudo"), "an approval lands on the password page", p.url());
await shot(p, "review-sudo-1440.png");
await p.fill("#password", password);
// The admin sends no Referer, so the password page cannot return to the review: open it again.
await Promise.all([p.waitForURL((u) => !u.pathname.endsWith("/sudo")), p.getByRole("button", { name: "Confirm" }).click()]);
await p.goto(review, { waitUntil: "load" });
check(await publish.isDisabled(), "nothing was decided by the detour");
for (const id of sugIds) await pick(`suggestion_${id}`, "rejected");
await Promise.all([p.waitForLoadState("load"), p.getByRole("button", { name: "Save suggestion decisions" }).click()]);
check(/Saved the suggestion decisions/.test(await flash()), "the suggestions are rejected", await flash());
check(await publish.isEnabled(), "Publish is enabled once everything is decided");
check(tagline() === taglineBefore, "rejecting changed no setting");
await shot(p, "review-decided-1440.png");

// A5: publish, land on Changes, undo the run.
await Promise.all([p.waitForURL(/\/settings\/changes/), publish.click()]);
check(/Published changeset/.test(await flash()), "Publish lands on Changes with a flash", await flash());
const published = async () => {
  await p.goto(`${base}/admin/entities?q=${encodeURIComponent("QA Import Plain")}`, { waitUntil: "load" });
  return p.locator("main").innerText();
};
await p.waitForTimeout(600);
const results = () => psql("-c", `SELECT string_agg(seq || ':' || COALESCE(result, '-') || COALESCE('(' || result_detail || ')', ''), ' ' ORDER BY seq) FROM hatamoto_changeset_items WHERE changeset_id = ${csId}`);
check((await published()).includes("QA Import Plain"), "the approved page is live", results());
await shot(p, "changes-published-1440.png");
await p.goto(`${base}/admin/settings/changes`, { waitUntil: "load" });
await p.locator('[data-hm-open^="undo-"]').first().click();
const dialog = p.locator("dialog[open]");
await dialog.waitFor();
const text = await dialog.innerText();
check(/Undo/.test(text) && /import/i.test(text), "the Undo dialog explains the undo", text.replace(/\s+/g, " "));
await shot(p, "changes-undo-dialog-1440.png");
await Promise.all([p.waitForLoadState("load"), dialog.getByRole("button", { name: "Undo run" }).click()]);
check(/Undid|restored|Undone/i.test(await flash()), "Undo answers with a flash", await flash());
check(!(await published()).includes("QA Import Plain"), "the undone page is gone");
check(tagline() === taglineBefore, "settings are as before");

check(errors.length === 0, "no console errors", errors.join(" | "));
await b.close();
console.log(bad ? `\n${bad} problem(s)` : "\nall passed");
process.exit(bad ? 1 : 0);
