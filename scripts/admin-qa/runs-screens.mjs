// Plugin runs (A4): the Runs tab and the progress page of a running run, at 1440.
// The host ticks of the start form belong to core, so they sit in the admin page and never in
// the plugin's sandboxed frame; Start is enabled; the progress page shows both rooms; no console errors.
// Needs the frameplug fixture (run-local.sh loads it for this script) and seed-changeset.sql.
// Run: SHOTS=<dir> bash scripts/admin-qa/run-local.sh runs-screens.mjs   Exit 1 on any problem.
import fs from "node:fs";
import path from "node:path";
import { execFileSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { launch, newSession, base } from "./lib.mjs";

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

psql("-v", "running=1", "-f", path.join(here, "seed-changeset.sql"));
const runId = psql("-c", "SELECT id FROM hatamoto_runs WHERE plugin = 'frameplug' AND state = 'running'");
check(/^[0-9a-f-]{36}$/.test(runId), "seeded a running run", runId);

const b = await launch("chromium");
const p = await newSession(b, { width: 1440 });
const errors = [];
p.on("pageerror", (e) => errors.push(String(e)));
p.on("console", (m) => m.type() === "error" && errors.push(m.text()));

await p.goto(`${base}/admin/plugins/frameplug/runs`, { waitUntil: "load" });
await p.waitForTimeout(800);
check((await p.title()).length > 0 && (await p.$("#run-form")) !== null, "the Runs tab shows the start form");
const start = p.locator('#run-form button[type="submit"]');
check((await start.count()) === 1 && (await start.isEnabled()), "Start is enabled");
const hostsInPage = await p.locator('#run-form [name="hosts"]').count();
check(hostsInPage > 0, "the host ticks are in the admin page", `${hostsInPage}`);
const inFrames = (await Promise.all(p.frames().filter((f) => f !== p.mainFrame()).map((f) => f.locator('[name="hosts"]').count().catch(() => 0)))).reduce((a, n) => a + n, 0);
check(inFrames === 0, "no host tick inside the plugin frame", `${inFrames}`);
await shot(p, "runs-tab-1440.png");

await p.goto(`${base}/admin/plugins/frameplug/runs/${runId}`, { waitUntil: "load" });
check((await p.locator("#room-fetch").count()) === 1 && (await p.locator("#room-build").count()) === 1, "both rooms are on the progress page");
const progress = await p.locator("#room-build [role=progressbar]").first().getAttribute("aria-valuenow");
check(Math.round(Number(progress)) === 62, "the build room shows 37 of 60", String(progress));
await shot(p, "run-progress-1440.png");

check(errors.length === 0, "no console errors", errors.join(" | "));
await b.close();
console.log(bad ? `\n${bad} problem(s)` : "\nall passed");
process.exit(bad ? 1 : 0);
