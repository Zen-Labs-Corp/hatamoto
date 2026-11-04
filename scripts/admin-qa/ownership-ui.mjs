// Who owns a page: an imported page shows "Imported, tracked by <plugin>", a structural edit makes
// it "Yours", and the hand-back button makes it "Imported, tracked" again.
// Needs the frameplug fixture (run-local.sh loads it for this script).
// Run: SHOTS=<dir> bash scripts/admin-qa/run-local.sh ownership-ui.mjs   Exit 1 on any problem.
import fs from "node:fs";
import path from "node:path";
import { execFileSync } from "node:child_process";
import { launch, newSession, base } from "./lib.mjs";

const psql = (sql) => execFileSync(process.env.PG_BIN ? path.join(process.env.PG_BIN, "psql") : "psql", [process.env.DATABASE_URL, "-v", "ON_ERROR_STOP=1", "-At", "-c", sql], { encoding: "utf8" }).trim();
const SHOTS = process.env.SHOTS || "";
if (SHOTS) fs.mkdirSync(SHOTS, { recursive: true });
const shot = (page, name) => (SHOTS ? page.screenshot({ path: path.join(SHOTS, name), fullPage: true }) : null);

let bad = 0;
const check = (ok, msg, detail = "") => {
  console.log(`${ok ? "PASS" : "FAIL"}  ${msg}${!ok && detail ? "  " + detail : ""}`);
  if (!ok) bad++;
};

// One page the fixture plugin published: origin, its source key and an applied changeset item.
const id = psql(`
  WITH sc AS (SELECT id FROM hatamoto_entity_schemas WHERE slug = 'post'),
  e AS (
    INSERT INTO hatamoto_entities (schema_id, name, slug, status, attributes, origin)
    SELECT id, 'QA Owned Page', 'qa-owned-page', 'draft', '{"content":"<p>Imported text.</p>"}', 'plugin:frameplug' FROM sc
    RETURNING id
  ), run AS (
    INSERT INTO hatamoto_runs (plugin, name, state, phase, hosts, progress, steps)
    VALUES ('frameplug', 'import', 'finished', 'build', '{example.com}', '{}', 1) RETURNING id
  ), cs AS (
    INSERT INTO hatamoto_changesets (run_id, plugin, state, expires_at, published_at)
    SELECT id, 'frameplug', 'published', now() + interval '1 day', now() FROM run RETURNING id
  ), sk AS (
    INSERT INTO hatamoto_changeset_source_keys (origin, kind, source_key, target_id)
    SELECT 'plugin:frameplug', 'entity', 'old.example.com, post 123', e.id::text FROM e
  ), it AS (
    INSERT INTO hatamoto_changeset_items (changeset_id, seq, kind, op, source_key, target_id, result)
    SELECT cs.id, 1, 'entity', 'upsert', 'old.example.com, post 123', e.id::text, 'applied' FROM cs, e
  )
  SELECT id FROM e`).split("\n")[0];
check(/^[0-9a-f-]{36}$/.test(id), "seeded an imported page", id);
const origin = () => psql(`SELECT origin FROM hatamoto_entities WHERE id = '${id}'`);

const b = await launch("chromium");
const p = await newSession(b, { width: 1440 });
const errors = [];
p.on("pageerror", (e) => errors.push(String(e)));
p.on("console", (m) => m.type() === "error" && errors.push(m.text()));
const page = `${base}/admin/entities/${id}`;
const card = p.locator("#own");
const badge = () => card.locator(".hm-badge").innerText();

await p.goto(page, { waitUntil: "load" });
check(/Imported, tracked by/.test(await badge()), "an imported page is tracked", await badge().catch(() => "no card"));
check((await card.innerText()).includes("old.example.com, post 123"), "the card shows where the page came from");
check((await card.innerText()).includes("Changeset #"), "the card shows the last import");
check((await p.locator("#own-never").count()) === 1, "the card of things a plugin can never own is there");
check((await card.getByRole("button").count()) === 0, "nothing to hand back while it is tracked");
await shot(p, "ownership-imported.png");

// A structural edit (the slug) takes the page over.
await p.fill("#slug", "qa-owned-page-edited");
await Promise.all([p.waitForLoadState("load"), p.locator(".hm-savebar button[type=submit]").click()]);
check(origin() === "owner", "editing the slug hands the page over", origin());
check((await badge()) === "Yours", "the badge says Yours", await badge());
check((await card.innerText()).includes("imports no longer change it"), "the card explains it");
await shot(p, "ownership-yours.png");

// Hand it back with the card's button.
await Promise.all([p.waitForLoadState("load"), card.getByRole("button", { name: /track this again/ }).click()]);
check(origin() === "plugin:frameplug", "the hand-back gives the page to the plugin", origin());
check(/Imported, tracked by/.test(await badge()), "the badge is Imported again", await badge());
check((await card.getByRole("button").count()) === 0, "the button is gone again");

check(errors.length === 0, "no console errors", errors.join(" | "));
await b.close();
console.log(bad ? `\n${bad} problem(s)` : "\nall passed");
process.exit(bad ? 1 : 0);
