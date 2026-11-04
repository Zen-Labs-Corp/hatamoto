// Standing approvals: the Automatic updates tab, the password page before a rule is allowed, the
// waiting-run callout, and the paused state with the daily summary after an update voids the rule.
// Needs the frameplug fixture (run-local.sh loads it for this script). The install prompt needs an
// uploaded package, so its markup stays with the Go golden.
// Run: SHOTS=<dir> bash scripts/admin-qa/run-local.sh standing-approvals.mjs   Exit 1 on any problem.
import fs from "node:fs";
import path from "node:path";
import { execFileSync } from "node:child_process";
import { launch, newSession, base, password } from "./lib.mjs";

const psql = (sql) => execFileSync(process.env.PG_BIN ? path.join(process.env.PG_BIN, "psql") : "psql", [process.env.DATABASE_URL, "-v", "ON_ERROR_STOP=1", "-At", "-c", sql], { encoding: "utf8" }).trim();
const SHOTS = process.env.SHOTS || "";
if (SHOTS) fs.mkdirSync(SHOTS, { recursive: true });
const shot = (page, name) => (SHOTS ? page.screenshot({ path: path.join(SHOTS, name), fullPage: true }) : null);

let bad = 0;
const check = (ok, msg, detail = "") => {
  console.log(`${ok ? "PASS" : "FAIL"}  ${msg}${!ok && detail ? "  " + detail : ""}`);
  if (!ok) bad++;
};
const active = () => psql("SELECT count(*) FROM hatamoto_standing_approvals WHERE plugin = 'frameplug' AND voided_at IS NULL");

const b = await launch("chromium");
const p = await newSession(b, { width: 1440 });
const errors = [];
p.on("pageerror", (e) => errors.push(String(e)));
p.on("console", (m) => m.type() === "error" && errors.push(m.text()));
const tab = `${base}/admin/plugins/frameplug/automatic`;
const flash = async () => (await p.locator(".hm-flash, .hm-toast").allInnerTexts()).join(" ");

// Screen 8: option 1, the rule as a permission list, nothing allowed yet.
await p.goto(tab, { waitUntil: "load" });
check(await p.getByRole("link", { name: "Automatic updates" }).count() > 0, "the plugin has an Automatic updates tab");
check((await p.locator("#opt1").innerText()).includes("Option 1: no approval needed"), "option 1 (the data path) is shown");
const lines = await p.locator("#opt2 .hm-permlist__item").allInnerTexts();
check(lines.length === 4 && /May change views and rating/.test(lines[0]) && /Only on post it created/.test(lines[1]) && /Up to 100 changes an hour/.test(lines[2]) && /^Never /.test(lines[3]), "the rule reads as plain sentences", lines.join(" | "));
check((await active()) === "0", "nothing is allowed before the owner says so");
await shot(p, "standing-s8-1440.png");

// A run that stepped outside the rule waits for review, with a Review link.
const cs = psql(`
  WITH run AS (
    INSERT INTO hatamoto_runs (plugin, name, state, phase, hosts, progress, steps, trigger)
    VALUES ('frameplug', 'refresh', 'finished', 'build', '{}', '{}', 3, 'cron') RETURNING id
  )
  INSERT INTO hatamoto_changesets (run_id, plugin, state, expires_at, item_count)
  SELECT id, 'frameplug', 'staged', now() + interval '70 hours', 15 FROM run RETURNING id`).split("\n")[0];
await p.goto(tab, { waitUntil: "load" });
const outside = await p.locator("#outside").innerText();
check(new RegExp(`run #${cs} is waiting for you`).test(outside) && /15 changes staged/.test(outside), "the waiting run is called out", outside);
await shot(p, "standing-s8-waiting-1440.png");
check((await p.locator(`#outside a[href$="/plugins/frameplug/changesets/${cs}"]`).count()) === 1, "the callout links to the review");

// Allowing asks for the password first, and that POST is not replayed.
await Promise.all([p.waitForURL(/\/sudo/), p.getByRole("button", { name: "Sign in again and allow" }).click()]);
check(p.url().includes("/admin/sudo"), "allowing lands on the password page", p.url());
check((await active()) === "0", "nothing was saved before the password");
await p.fill("#password", password);
await Promise.all([p.waitForURL((u) => !u.pathname.endsWith("/sudo")), p.getByRole("button", { name: "Confirm" }).click()]);
check((await active()) === "0", "the password page did not replay the POST");
await p.goto(tab, { waitUntil: "load" });
await Promise.all([p.waitForLoadState("load"), p.getByRole("button", { name: "Sign in again and allow" }).click()]);
check(/Automatic updates are allowed for frameplug/.test(await flash()), "allowing again saves the rule", await flash());
check((await active()) === "1", "one active approval exists");
check((await p.locator("#opt2").innerText()).includes("Automatic updates are on."), "the tab shows the rule as allowed");

// Screen 9: an update voids the approval; the tab pauses it and the daily summary lists the day.
psql(`
  UPDATE hatamoto_standing_approvals SET voided_at = now(), void_reason = 'plugin updated' WHERE plugin = 'frameplug' AND voided_at IS NULL;
  WITH run AS (
    INSERT INTO hatamoto_runs (plugin, name, state, phase, hosts, progress, steps, trigger)
    VALUES ('frameplug', 'refresh', 'finished', 'build', '{}', '{}', 3, 'cron') RETURNING id
  ), c AS (
    INSERT INTO hatamoto_changesets (run_id, plugin, state, expires_at, published_at, auto_published, item_count)
    SELECT id, 'frameplug', 'published', now() + interval '70 hours', now(), true, 2 FROM run RETURNING id
  )
  INSERT INTO hatamoto_changeset_items (changeset_id, seq, kind, op, source_key, result)
  SELECT id, s, 'entity', 'upsert', 'qa-auto-' || s, 'applied' FROM c, generate_series(1, 2) s`);
await p.goto(tab, { waitUntil: "load" });
const body = await p.locator("main").innerText();
check(/Updated/.test(body) && /Its automatic updates are paused\./.test(body), "the paused callout and the Updated badge show", body.slice(0, 300));
check((await p.getByRole("link", { name: "Review and allow again" }).count()) === 1 && (await p.getByRole("link", { name: "Keep paused" }).count()) === 1, "both choices are offered");
const summary = await p.locator("#summary-card").innerText().catch(() => "");
check((await p.locator("#opt1, #opt2, #outside").count()) === 0, "the paused tab shows no rule cards, only the notice and the summary");
check(/Daily summary, /.test(summary) && /frameplug/.test(summary) && /See runs/.test(summary), "the daily summary lists the automatic run", summary);
await shot(p, "standing-s9-1440.png");
await p.getByRole("link", { name: "Review and allow again" }).click();
check(p.url().endsWith("?review=1") && (await p.locator("#opt2").count()) === 1, "Review and allow again brings the rule back", p.url());

check(errors.length === 0, "no page or console errors", errors.join(" | "));
await b.close();
process.exit(bad ? 1 : 0);
