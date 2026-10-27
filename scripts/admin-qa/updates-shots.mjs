// Screenshots of every Settings > Updates status and the owner banner. Runs inside
// run-local.sh (DATABASE_URL and BASE are set): for each variant it writes the
// update status row with psql, waits out the 30 s banner cache where the banner is
// shot, then calls shots.mjs and renames its output.
//   QA_VERSION=v1.4.1 [QA_VARIANTS="up_to_date ..."] scripts/admin-qa/run-local.sh updates-shots.mjs
// (run-local.sh treats arguments as scripts, so the variants go in QA_VARIANTS; the default is every variant but the two server-level ones.) Variants: up_to_date scheduled blocked major_available rolled_back rollback_pending off check_failed
// notify no_check_yet pinned history banner_rolled_back banner_rollback_pending banner_blocked
// legacy. env_locked needs HATAMOTO_UPDATES=auto and env_locked_canary needs
// HATAMOTO_UPDATE_CHANNEL=canary in the server's environment and unavailable needs HATAMOTO_UPDATE_URL=not-a-url (see updates-shots.sh).
// Env: SHOTS (output dir, default scripts/admin-qa/shots-out).
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { here } from "./lib.mjs";

const out = process.env.SHOTS || path.join(here, "shots-out");
fs.mkdirSync(out, { recursive: true });
const ok = { last_result: "ok", decision: "up_to_date", reason: "running the newest release offered" };
const row = (o) => ({ mode: "auto", pin: "", defer_days: 0, last_check_at: "now() - interval '12 minutes'", last_result: "ok", decision: "", reason: "", target: "", available_major: "", off_since: "NULL", ...o });
const states = {
  up_to_date: row(ok),
  scheduled: row({ decision: "scheduled", target: "v1.4.2", reason: "v1.4.2 is rolling out (bucket 37, ramp 20%)" }),
  blocked: row({ decision: "blocked", target: "v1.5.0", reason: "plugin hello needs SDK ^0.8 (v1.5.0 has 0.10.0)" }),
  blocked_long: row({ decision: "blocked", target: "v1.5.0", reason: "plugin hello needs SDK ^0.8 (v1.5.0 has 0.10.0); plugin gallery-pro needs SDK ^0.7 (v1.5.0 has 0.10.0); theme studio needs SDK ^0.9 (v1.5.0 has 0.10.0); plugin seo-extras needs SDK ^0.6 (v1.5.0 has 0.10.0)" }),
  major_available: row({ decision: "available_major", available_major: "v2.0.0" }),
  update_failed: row({ last_result: "error", decision: "update_failed", target: "v1.5.0", reason: "download v1.5.0: Get \"https://hatamoto.dev/releases/v1.5.0/hatamoto-linux-amd64\": connection reset by peer" }),
  rolled_back: row({ decision: "rolled_back", target: "v1.4.2", reason: "smoke failed: GET / returned 500" }),
  rollback_pending: row({ decision: "rollback_pending", target: "v1.4.0", reason: "v1.4.1 was withdrawn (bad build); rolling back to v1.4.0; check only, not switching" }),
  off: row({ ...ok, mode: "off", off_since: "now() - interval '3 days'" }),
  check_failed: row({ last_result: "error", reason: "fetch channel manifest: timeout" }),
  check_failed_long: row({ last_result: "error", reason: "fetch channel stable: Get \"https://hatamoto.dev/releases/channels/stable.json\": context deadline exceeded (Client.Timeout exceeded while awaiting headers)" }),
  notify: row({ mode: "notify", decision: "notify", target: "v1.5.0", reason: "v1.5.0 is available; mode notify, not installing" }),
  no_check_yet: row({ last_check_at: "NULL", last_result: "" }),
  pinned: row({ ...ok, pin: "v1.4", defer_days: 3 }),
  history: row(ok),
  env_locked: row(ok),
  env_locked_canary: row(ok),
  unavailable: row(ok),
  banner_rolled_back: row({ decision: "rolled_back", target: "v1.4.2", reason: "smoke failed: GET / returned 500" }),
  banner_rolled_back_yanked: row({ decision: "rolled_back_yanked", target: "v1.4.0", reason: "v1.4.1 was withdrawn (bad build); rolling back to v1.4.0" }),
  banner_rollback_pending: row({ decision: "rollback_pending", target: "v1.4.0", reason: "v1.4.1 was withdrawn (bad build); rolling back to v1.4.0; check only, not switching" }),
  banner_blocked: row({ decision: "blocked", target: "v1.5.0", reason: "plugin hello needs SDK ^0.8 (v1.5.0 has 0.10.0)" }),
  legacy: row(ok),
};
const screenOf = (v) => (v.startsWith("banner_") ? "settings-general" : v === "legacy" ? "settings-performance" : "settings-updates");
const prefix = (v) => (v.startsWith("banner_") ? v.replace("_", "-") : v === "legacy" ? "legacy-performance" : `updates-${v.replaceAll("_", "-")}`);

const psql = (sql) => execFileSync("psql", [process.env.DATABASE_URL, "-X", "-v", "ON_ERROR_STOP=1", "-c", sql], { stdio: ["ignore", "ignore", "inherit"] });
const lit = (k, v) => (["last_check_at", "off_since"].includes(k) ? v : typeof v === "number" ? v : `'${String(v).replaceAll("'", "''")}'`);
function setState(s) {
  const cols = { ...s, mode: s.mode, pin: s.pin || null };
  const sets = Object.entries(cols).map(([k, v]) => `${k} = ${v === null ? "NULL" : lit(k, v)}`).join(", ");
  psql(`INSERT INTO hatamoto_update_settings (id) VALUES (TRUE) ON CONFLICT DO NOTHING; UPDATE hatamoto_update_settings SET ${sets}`);
}
const history = `INSERT INTO hatamoto_updates (event_id, from_version, to_version, phase, error, started_at, finished_at) VALUES
 ('qa-1','v1.4.0','v1.4.1','success','', now() - interval '5 days', now() - interval '5 days'),
 ('qa-2','v1.4.0','v1.4.1','rolled_back','smoke failed: GET / returned 500', now() - interval '8 days', now() - interval '8 days'),
 ('qa-3','v1.3.9','v1.4.0','failed','disk full', now() - interval '20 days', now() - interval '20 days'),
 ('qa-4','v1.3.8','v1.3.9','rolled_back','health check failed: GET /healthz returned 503 after 3 attempts; GET / returned 500; the previous release was restored and the new one is marked bad', now() - interval '40 days', now() - interval '40 days') ON CONFLICT DO NOTHING`;

const want = (process.env.QA_VARIANTS || "").split(/\s+/).filter(Boolean);
const local = Object.keys(states).filter((v) => !v.startsWith("env_locked") && v !== "unavailable");
// The server's own scheduled check can land minutes after start and overwrite the row
// (it fails against the closed port); a variant is shot again when that happened.
const intact = (s) => {
  const got = execFileSync("psql", [process.env.DATABASE_URL, "-X", "-At", "-F", "|", "-c", "SELECT last_result, decision, reason FROM hatamoto_update_settings"], { encoding: "utf8" }).trim();
  return got === [s.last_result, s.decision, s.reason].join("|");
};
async function shoot(v) {
  setState(states[v]);
  psql("DELETE FROM hatamoto_updates WHERE event_id LIKE 'qa-%'");
  if (v === "history") psql(history);
  if (v.startsWith("banner_")) { console.log(`${v}: waiting out the banner cache`); await new Promise((r) => setTimeout(r, 32000)); }
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "hm-upd-shots-"));
  execFileSync("node", [path.join(here, "shots.mjs"), screenOf(v)], { env: { ...process.env, SHOTS: tmp, NOMOCK: "1", FULL: "1" }, stdio: "inherit" });
  for (const f of fs.readdirSync(tmp).filter((f) => f.endsWith(".png"))) fs.copyFileSync(path.join(tmp, f), path.join(out, f.replace(/^[^.]*?-(1440|390)-/, `${prefix(v)}-$1-`)));
  fs.rmSync(tmp, { recursive: true, force: true });
  if (!intact(states[v])) return false;
  console.log(`${v}: done`);
  return true;
}
for (const v of want.length ? want : local) {
  if (!states[v]) { console.error(`unknown variant ${v}`); process.exit(2); }
  let n = 0;
  while (!(await shoot(v))) if (++n > 2) { console.error(`${v}: the row kept changing`); process.exit(1); }
}
