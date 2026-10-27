// axe on shell pages while each owner banner shows (rolled back, blocked, backup failed:
// error, warning and error styles), keyboard on the first, plus the
// Updates page itself. Runs inside run-local.sh; needs a release-stamped build so the
// banner is not suppressed:
//   QA_VERSION=v1.4.1 scripts/admin-qa/run-local.sh updates-banner-a11y.mjs
import { execFileSync } from "node:child_process";
import path from "node:path";
import { here } from "./lib.mjs";

const row = (o) => ({ mode: "'auto'", off_since: "NULL", decision: "''", target: "''", reason: "''", available_major: "''", ...o });
const kinds = {
  rolled_back: row({ decision: "'rolled_back'", target: "'v1.4.2'", reason: "'smoke failed: GET / returned 500'" }),
  blocked: row({ decision: "'blocked'", target: "'v1.5.0'", reason: "'plugin hello needs SDK ^0.8 (v1.5.0 has 0.10.0)'" }),
  backup_failed: row({ decision: "'backup_failed'", target: "'v1.5.0'", reason: "'not enough free disk space'" }),
};
const screens = ["entities", "settings-general", "settings-performance", "settings-updates"];
let first = true;
for (const [kind, o] of Object.entries(kinds)) {
  const set = Object.entries(o).map(([k, v]) => `${k} = ${v}`).join(", ");
  const sql = `INSERT INTO hatamoto_update_settings (id) VALUES (TRUE) ON CONFLICT DO NOTHING; UPDATE hatamoto_update_settings SET last_check_at = now(), last_result = 'ok', ${set}`;
  execFileSync("psql", [process.env.DATABASE_URL, "-X", "-v", "ON_ERROR_STOP=1", "-c", sql], { stdio: ["ignore", "ignore", "inherit"] });
  console.log(`== banner ${kind}`);
  await new Promise((r) => setTimeout(r, 32000)); // the banner is cached for 30 s
  for (const s of first ? ["a11y.mjs", "keyboard.mjs"] : ["a11y.mjs"]) execFileSync("node", [path.join(here, s), ...screens], { stdio: "inherit" });
  first = false;
}
