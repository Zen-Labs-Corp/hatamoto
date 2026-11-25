// Query loop attribute sort and filters, driven like a user: insert a Query loop over a generic `event` schema, check the
// Sort by attribute and Filters rows in the inspector (attribute list, operators per type, at most three filters) and that
// the canvas preview lists only today and later entries, soonest first.
//
// Runs locally only (it creates a schema and five published entries through the API):
//   PG_BIN=<pg bin dir> bash scripts/admin-qa/run-local.sh builder-loop-filters.mjs     (seeded throwaway instance)
// Screenshots (1600x950, only with QA_SHOTS=<dir>): loop-filters-inspector.png and loop-filters-canvas.png.
// Nothing is saved: every POST to the save / autosave / recovery endpoints is aborted in the browser. Not in the CI default
// list. Exit 1 on any problem.
import fs from "node:fs";
import path from "node:path";

const { launch, base, admin, login, email, password } = await import("./lib.mjs");

const SHOTS = process.env.QA_SHOTS || "";
if (SHOTS) fs.mkdirSync(SHOTS, { recursive: true });

// Setup through the API: schema `event` and five published entries around today (the server's UTC date; the site zone is UTC).
const lr = await fetch(`${base}/api/v1/auth/login`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ email, password }) });
if (!lr.ok) throw new Error(`api login failed: ${lr.status}`);
const bearer = { Authorization: `Bearer ${(await lr.json()).session_token}`, "Content-Type": "application/json" };
const api = async (method, url, body) => {
  const r = await fetch(base + url, { method, headers: bearer, body: body ? JSON.stringify(body) : undefined });
  if (!r.ok) throw new Error(`${method} ${url}: ${r.status} ${await r.text()}`);
  return r.json();
};
const day = (n) => new Date(Date.now() + n * 86400000).toISOString().slice(0, 10);
const schema = await api("POST", "/api/v1/schemas", {
  name: "Event",
  slug: "event",
  schema: {
    type: "object",
    properties: {
      start_date: { type: "string", format: "date", title: "Start date" },
      status: { type: "string", enum: ["scheduled", "cancelled"], title: "Status" },
      capacity: { type: "integer", title: "Capacity" },
    },
  },
});
const events = [["Yesterday event", -1, "scheduled"], ["Today event", 0, "scheduled"], ["Cancelled event", 1, "cancelled"], ["Soon event", 3, "scheduled"], ["Later event", 20, "scheduled"]];
for (const [name, d, status] of events) {
  const e = await api("POST", "/api/v1/entities", { schema_id: schema.id, name, attributes: { start_date: day(d), status, capacity: 10 } });
  await api("PATCH", `/api/v1/entities/${e.id}/status`, { status: "published" });
}

const b = await launch("chromium");
const ctx = await b.newContext({ viewport: { width: 1600, height: 950 } });
await ctx.addCookies([{ name: "hm_theme", value: "light", url: base }]);
const p = await ctx.newPage();
await login(p);
let bad = 0;
const errors = [];
p.on("pageerror", (e) => errors.push(e.message));
p.on("dialog", (d) => d.accept().catch(() => {}));
const blocked = [];
await ctx.route(/\/builder\/(save|autosave|recovery)(\/discard)?$/, (r) => { blocked.push(r.request().method()); return r.abort("failed"); });

const TREE = '[data-hm-bs-tree] [role="treeitem"]';
const settle = (ms = 150) => p.waitForTimeout(ms);
const shot = async (name) => { if (SHOTS) await p.screenshot({ path: path.join(SHOTS, name) }); };
const doc = () => p.evaluate(() => window.__hmBuilder.doc());
const flat = (ns) => ns.flatMap((n) => [n, ...flat(n.c ?? [])]);
async function check(name, fn) {
  try { console.log(`ok   ${name}: ${await fn()}`); }
  catch (e) {
    bad++; console.log(`FAIL ${name}: ${String(e.message || e).split("\n").slice(0, process.env.QA_VERBOSE ? 30 : 1).join(" | ")}`);
  }
}
const same = (got, want, what) => { if (JSON.stringify(got) !== JSON.stringify(want)) throw new Error(`${what}: ${JSON.stringify(got)}, wanted ${JSON.stringify(want)}`); };
const options = (loc) => loc.locator("option").allInnerTexts().then((t) => t.map((s) => s.trim()));
const sorted = (xs) => [...xs].sort(); // the schema's attributes come in the stored key order, which is not the form order
const pick = async (loc, label) => { await loc.selectOption({ label }); await settle(400); };

let loop = null;
await check("setup: open a page, insert a Query loop and pick the schema", async () => {
  await p.goto(`${base}${admin}/entities?q=About`);
  const id = (await p.$$eval(`a[href^="${admin}/entities/"]`, (as) => as.map((a) => a.getAttribute("href")))).map((h) => h.match(/entities\/([0-9a-f-]{36})/)?.[1]).find(Boolean);
  if (!id) throw new Error("no About page");
  await p.goto(`${base}${admin}/entities/${id}/edit?qa=1`, { waitUntil: "load" });
  await p.waitForSelector(TREE, { timeout: 15000 });
  await p.waitForFunction(() => !!window.__hmBuilder, null, { timeout: 8000 });
  await settle(800);
  await p.evaluate(() => window.__hmBuilder.insertAt({ parent: "", index: 0 }, "container"));
  await settle(700);
  const sec = (await doc()).nodes[0];
  await p.evaluate((sid) => window.__hmBuilder.insertAt({ parent: sid, index: 0 }, "loop"), sec.id);
  await settle(900);
  loop = flat((await doc()).nodes).find((n) => n.t === "loop");
  if (!loop) throw new Error("no loop in the document");
  await p.locator(`${TREE}[data-id="${loop.id}"]`).click();
  await settle(500);
  await p.locator('[role="tab"][data-tab="content"]').first().click().catch(() => {});
  await settle(400);
  const schemaSel = p.getByLabel("Schema", { exact: true }).first();
  await schemaSel.waitFor({ state: "visible", timeout: 4000 });
  if ((await schemaSel.evaluate((el) => el.tagName)) === "SELECT") await pick(schemaSel, "Event");
  else { await schemaSel.fill("event"); await schemaSel.press("Enter"); await settle(500); }
  await p.locator('[aria-label="Filter 1 attribute"], .hm-bs-attrs').first().waitFor({ state: "attached", timeout: 4000 });
  return `loop ${loop.id}`;
});

const rowsEl = p.locator(".hm-bs-frow");
const add = p.getByRole("button", { name: "Add filter" });
const fAttr = (n) => p.locator(`[aria-label="Filter ${n} attribute"]`);
const fOp = (n) => p.locator(`[aria-label="Filter ${n} operator"]`);
const stored = async () => flat((await doc()).nodes).find((n) => n.id === loop.id)?.p ?? {};

await check("Sort and Filters sit between Order and Limit, as in the mockup", async () => {
  const labels = await p.locator(".hm-bs-content .hm-field__label").allInnerTexts();
  const at = (t) => labels.findIndex((l) => l.trim().startsWith(t));
  if (!(at("Order") >= 0 && at("Order") < at("Sort by attribute") && at("Sort by attribute") < at("Filters") && at("Filters") < at("Limit"))) throw new Error(`label order: ${labels.join(" / ")}`);
  return "Order, Sort by attribute, Filters, Limit";
});

await check("A4: Sort by attribute and the filter attribute list offer exactly Start date, Status, Capacity", async () => {
  same(sorted(await options(p.getByLabel("Sort by attribute (optional)"))), sorted(["None", "Start date", "Status", "Capacity"]), "sort options");
  await add.click();
  await settle(300);
  same(sorted(await options(fAttr(1))), sorted(["Start date", "Status", "Capacity"]), "filter attribute options");
  return "Start date / Status / Capacity";
});

await check("A4: operators follow the attribute type (3 date operators, 5 number operators)", async () => {
  await pick(fAttr(1), "Start date"); // a new row starts on the schema's first attribute
  same(await options(fOp(1)), ["is today or later", "is before today", "is within the next days"], "date operators");
  await pick(fAttr(1), "Capacity");
  same(await options(fOp(1)), ["is", "is less than", "is at most", "is more than", "is at least"], "number operators");
  await pick(fAttr(1), "Status");
  same(await options(fOp(1)), ["is", "is not"], "enum operators");
  return "date 3, number 5, enum 2";
});

await check("A4: three filter rows hide Add filter", async () => {
  await pick(fAttr(1), "Start date");
  await add.click(); await settle(300);
  await add.click(); await settle(300);
  if ((await rowsEl.count()) !== 3) throw new Error(`${await rowsEl.count()} rows`);
  if (await add.isVisible()) throw new Error("Add filter still shown at three rows");
  await rowsEl.first().scrollIntoViewIfNeeded();
  return "3 rows, Add filter hidden";
});

await check("A5: filter Start date is today or later, Status is not cancelled, sort Start date ascending", async () => {
  await pick(fAttr(1), "Start date"); await pick(fOp(1), "is today or later");
  await pick(fAttr(2), "Status"); await pick(fOp(2), "is not");
  await p.locator('[aria-label="Filter 2 value"]').selectOption({ label: "cancelled" });
  await settle(400);
  await p.getByRole("button", { name: "Remove filter 3" }).click();
  await settle(400);
  await pick(p.getByLabel("Sort by attribute (optional)"), "Start date");
  await pick(p.getByLabel("Direction", { exact: true }), "Ascending");
  const pr = await stored();
  same(pr.filters, [{ attr: "start_date", op: "from_today" }, { attr: "status", op: "ne", value: "cancelled" }], "stored filters");
  same(pr.order_by, { attr: "start_date", dir: "asc" }, "stored sort");
  await p.getByLabel("Sort by attribute (optional)").scrollIntoViewIfNeeded();
  await settle(200);
  await shot("loop-filters-inspector.png");
  return "filters and sort stored on the loop";
});

await check("A5: the canvas preview lists only today and later, soonest first, no cancelled", async () => {
  const want = ["Today event", "Soon event", "Later event"];
  let text = "";
  for (let i = 0; i < 20; i++) {
    for (const f of p.frames()) {
      if (f === p.mainFrame() || f.url() === "about:blank") continue;
      text = await f.locator("body").innerText().catch(() => "");
      if (text.includes("Today event")) break;
    }
    if (text.includes("Today event")) break;
    await settle(500);
  }
  const seen = events.map(([n]) => n).filter((n) => text.includes(n)).sort((a, b) => text.indexOf(a) - text.indexOf(b));
  same(seen, want, "entries in the canvas");
  await shot("loop-filters-canvas.png");
  return seen.join(", ");
});

await check("From to archive clears the sort and filters in the same step; one undo brings them back", async () => {
  await pick(p.getByLabel("From", { exact: true }), "archive");
  await settle(400);
  const pr = await stored();
  if (pr.order_by || pr.filters) throw new Error(`sort or filters left on a non-query loop: ${JSON.stringify(pr)}`);
  await p.keyboard.press("Control+z");
  await settle(400);
  const back = await stored();
  same(back.filters?.length, 2, "filters after undo");
  same(back.order_by, { attr: "start_date", dir: "asc" }, "sort after undo");
  return "cleared with From, restored by one undo";
});

await check("nothing was saved and no page error", async () => {
  if (errors.length) throw new Error(`page errors: ${errors.join(" | ")}`);
  return `${blocked.length} save request(s) aborted`;
});

await b.close();
console.log(bad ? `${bad} check(s) failed` : "all checks passed");
process.exit(bad ? 1 : 0);
