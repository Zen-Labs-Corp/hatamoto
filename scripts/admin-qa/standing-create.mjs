// Standing approvals with create: the rule card reads "May publish new post entries on its own" before
// the Never line, allowing works through the password page, and the daily summary lists the new entries in
// their own table. Needs the frameplug fixture with the create rule (run-local.sh patches the copy it
// loads for this script). standing-approvals.mjs keeps covering the rule without create.
// Run: SHOTS=<dir> bash scripts/admin-qa/run-local.sh standing-create.mjs   Exit 1 on any problem.
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
const errors = [];
const tab = `${base}/admin/plugins/frameplug/automatic`;
const open = async (width) => {
  const page = await newSession(b, { width });
  page.on("pageerror", (e) => errors.push(String(e)));
  page.on("console", (m) => m.type() === "error" && errors.push(m.text()));
  return page;
};

// The rule card at both widths: the create line sits before the Never line.
const m = await open(390);
await m.goto(tab, { waitUntil: "load" });
await shot(m, "standing-create-390.png");
const p = await open(1440);
await p.goto(tab, { waitUntil: "load" });
const lines = await p.locator("#opt2 .hm-permlist__item").allInnerTexts();
const at = lines.findIndex((l) => /May publish new post entries on its own, up to 20 an hour/.test(l));
const never = lines.findIndex((l) => /^Never /.test(l));
check(lines.length === 5 && at === 3 && never === 4, "the create line sits before the Never line", lines.join(" | "));
check(/May change views and rating/.test(lines[0]) && /Up to 100 changes an hour/.test(lines[2]), "the update lines are unchanged", lines.join(" | "));
check((await p.locator("#opt2 .hm-permlist__item--review").count()) === 1, "the create line uses the review tile");
check((await active()) === "0", "nothing is allowed before the owner says so");
await shot(p, "standing-create-1440.png");

// Allowing asks for the password first, then the tab shows the rule as on.
await Promise.all([p.waitForURL(/\/sudo/), p.getByRole("button", { name: "Sign in again and allow" }).click()]);
await p.fill("#password", password);
await Promise.all([p.waitForURL((u) => !u.pathname.endsWith("/sudo")), p.getByRole("button", { name: "Confirm" }).click()]);
await p.goto(tab, { waitUntil: "load" });
await Promise.all([p.waitForLoadState("load"), p.getByRole("button", { name: "Sign in again and allow" }).click()]);
check((await active()) === "1", "one active approval exists");
check((await p.locator("#opt2").innerText()).includes("Automatic updates are on."), "the tab shows the rule as allowed");

// One automatic run published a new entry and updated another: the summary lists them apart.
psql(`
  WITH run AS (
    INSERT INTO hatamoto_runs (plugin, name, state, phase, hosts, progress, steps, trigger)
    VALUES ('frameplug', 'refresh', 'finished', 'build', '{}', '{}', 3, 'cron') RETURNING id
  ), c AS (
    INSERT INTO hatamoto_changesets (run_id, plugin, state, expires_at, published_at, auto_published, item_count)
    SELECT id, 'frameplug', 'published', now() + interval '70 hours', now(), true, 2 FROM run RETURNING id
  )
  INSERT INTO hatamoto_changeset_items (changeset_id, seq, kind, op, source_key, result, before)
  SELECT id, 1, 'entity', 'upsert', 'qa-new-1', 'applied', NULL FROM c
  UNION ALL
  SELECT id, 2, 'entity', 'upsert', 'qa-old-2', 'applied', '{}'::jsonb FROM c`);
await p.goto(tab, { waitUntil: "load" });
const summary = await p.locator("#summary-card").innerText().catch(() => "");
check(/Daily summary, /.test(summary) && /Automatic runs/.test(summary), "the daily summary lists the run", summary);
check(/New entries published automatically/.test(summary) && (await p.locator("#summary-created").count()) === 1, "the new entries have their own table", summary);
const created = await p.locator("#summary-created tbody tr").allInnerTexts();
check(created.length === 1 && /frameplug/.test(created[0]) && /\b1\b/.test(created[0]), "one new entry is counted for the plugin", created.join(" | "));
await shot(p, "standing-create-summary-1440.png");

check(errors.length === 0, "no page or console errors", errors.join(" | "));
await b.close();
process.exit(bad ? 1 : 0);
