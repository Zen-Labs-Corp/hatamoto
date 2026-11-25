// A plugin page starts a run: the frameplug fixture's page calls hatamoto.runInputs("import", {...}).
// Without a click the call is refused and no dialog opens. A click opens core's confirm (outside the
// frame) naming the run and every input; Cancel returns "cancelled" and sends no request; Start returns
// {ok:true, run_id} and shows a success toast. The run is cancelled afterwards. The Runs tab keeps its form.
// Run: SHOTS=<dir> bash scripts/admin-qa/run-local.sh page-run-start.mjs   (SHOTS: where screenshots go)   Exit 1 on any problem.
import fs from "node:fs";
import path from "node:path";
import { launch, newSession, base } from "./lib.mjs";

const SHOTS = process.env.SHOTS || "";
if (SHOTS) fs.mkdirSync(SHOTS, { recursive: true });
const shot = (page, name) => (SHOTS ? page.screenshot({ path: path.join(SHOTS, name) }) : null);

let bad = 0;
const check = (ok, msg, detail = "") => {
  console.log(`${ok ? "PASS" : "FAIL"}  ${msg}${!ok && detail ? "  " + detail : ""}`);
  if (!ok) bad++;
};

const b = await launch("chromium");
const p = await newSession(b, { width: 1440 });
const pageUrl = `${base}/admin/plugins/frameplug/settings`;
const pframe = () => p.frames().find((f) => f.url().includes("/plugin-ui/frameplug/index.html"));
const startPosts = [];
p.on("request", (req) => {
  if (req.method() === "POST" && /\/runs\/import\/start$/.test(req.url())) startPosts.push(req.url());
});

async function open(url = pageUrl) {
  await p.goto(url, { waitUntil: "load" });
  await p.waitForFunction(() => document.querySelector("iframe.hm-pluginframe")?.contentWindow, null, { timeout: 10000 });
  for (let i = 0; i < 50 && !(await pframe()?.locator("#state").textContent().catch(() => ""))?.startsWith("bridge ready"); i++) await p.waitForTimeout(200);
}
const out = async () => JSON.parse((await pframe().locator("#out").textContent()) || "{}");
const waitOut = async (before = "") => {
  await pframe().waitForFunction((x) => document.getElementById("out").textContent !== x, before, { timeout: 15000 });
  return out();
};

await open();
check((await p.locator("[data-hm-pluginframe][data-runs]").count()) === 1, "the owner's frame host carries data-runs");

// ---- no click: refused, no dialog ----
// The evaluate itself counts as a gesture, so wait it out first.
const noClick = await pframe().evaluate(() => new Promise((r) => setTimeout(r, 6000)).then(() => hatamoto.runInputs("import", { source: "https://example.com/" })).then(() => "resolved", (e) => String(e.message)));
check(/click/.test(noClick) && (await p.locator("dialog[open]").count()) === 0, "no click: refused, no dialog", noClick);

// ---- click, then Cancel ----
await pframe().locator("#startrun").click();
await p.waitForSelector("#hm-confirm[open]", { timeout: 10000 });
const msg = (await p.locator("#hm-confirm-message").textContent()) || "";
check(msg.includes('Start run "Import"') && msg.includes('"source": "https://example.com/"') && msg.includes('"limit": "3"'), "the confirm quotes the run and every input", msg);
check(/It may reach ("[^"]+"|no other site)/.test(msg), "the confirm names the host", msg);
await shot(p, "page-run-start-confirm.png");
await p.locator("[data-hm-confirm-cancel]").click();
let r = await waitOut();
check(r.ok === false && r.error === "cancelled", "Cancel: the page gets cancelled", JSON.stringify(r));
check(startPosts.length === 0, "Cancel: no request to the start route", startPosts.join(","));

// ---- right after a Cancel the frame is cooled down ----
const afterCancel = (await pframe().locator("#out").textContent()) || "";
await pframe().locator("#startrun").click();
r = await waitOut(afterCancel);
check(r.ok !== true && /cancelled/.test(JSON.stringify(r)), "after a Cancel the next start is refused", JSON.stringify(r));
check((await p.locator("dialog[open]").count()) === 0, "the cooldown opens no dialog");
await p.waitForTimeout(3200);

// ---- click, then Start ----
const before = await pframe().locator("#out").textContent();
await pframe().locator("#startrun").click();
await p.waitForSelector("#hm-confirm[open]", { timeout: 10000 });
await p.waitForTimeout(700); // Start is ignored for the first 500 ms (double-click guard)
await p.locator("[data-hm-confirm-ok]").click();
r = await waitOut(before || "");
check(r.ok === true && !!r.run_id && String(r.url).endsWith("/runs/" + r.run_id), "Start: the page gets {ok:true, run_id, url}", JSON.stringify(r));
check(startPosts.length === 1, "Start: exactly one request to the start route", startPosts.join(","));
await p.waitForSelector("#hm-toasts .hm-toast", { timeout: 5000 }).catch(() => {});
check(((await p.locator("#hm-toasts").textContent()) || "").includes("Started the Import run"), "Start: a success toast");
check((await p.locator("dialog[open]").count()) === 0, "Start: the dialog is closed");
await shot(p, "page-run-start-started.png");

// ---- clean up: cancel the run ----
if (r.run_id) {
  const status = await p.evaluate(async (id) => {
    const csrf = document.querySelector('meta[name="csrf-token"]')?.content ?? "";
    const res = await fetch(`/admin/plugins/frameplug/runs/${id}/cancel`, { method: "POST", credentials: "same-origin", headers: { "X-CSRF-Token": csrf } });
    return res.status;
  }, r.run_id);
  check(status < 400, "the started run is cancelled afterwards", String(status));
}

// ---- legacy: the Runs tab keeps its start form ----
await open(`${base}/admin/plugins/frameplug/runs`);
check((await p.locator("[data-hm-run-form]").count()) >= 1, "the Runs tab still renders its start form");

await b.close();
console.log(bad ? `\n${bad} problem(s)` : "\nall good");
process.exit(bad ? 1 : 0);
