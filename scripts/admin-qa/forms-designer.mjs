// The form designer, by click paths (A1 to A4) and the save conflict, with real pointer and
// keyboard events. It makes its own form (Forms > New), builds fields by palette click and
// drag, reorders, renames inline and in the inspector, deletes, saves with Ctrl+S and
// compares the saved definition with what the page showed; then it checks the shortcuts,
// the shared validation table in the browser, the tab history and the 409 on a stale
// version. The form is deleted at the end.
// node scripts/admin-qa/forms-designer.mjs   Exit 1 on a failed check, a console error or a page error.
import fs from "node:fs";
import path from "node:path";
import { launch, newSession, base, admin, here } from "./lib.mjs";

const b = await launch();
let bad = 0;
const ok = (c, m) => {
  console.log(`${c ? "ok  " : "FAIL"} ${m}`);
  if (!c) bad++;
};
const same = (a, c) => JSON.stringify(a) === JSON.stringify(c);

const page = await newSession(b, { theme: "light", width: 1440 });
// the 30 s "Saved N min ago" timer, callable on demand
await page.addInitScript(() => {
  const si = window.setInterval;
  window.setInterval = (fn, ms, ...a) => {
    if (ms === 30000) window.__hmTick30 = fn;
    return si(fn, ms, ...a);
  };
});
const errors = [];
page.on("pageerror", (e) => errors.push(e.message));
page.on("console", (m) => {
  if (m.type() === "error" && !m.text().includes("409")) errors.push(m.text()); // the 409 check provokes a failed request on purpose
});
page.on("dialog", (d) => d.accept().catch(() => {})); // leaving a page with unsaved edits asks first
let navs = 0;
page.on("framenavigated", (f) => {
  if (f === page.mainFrame()) navs++;
});

// -- helpers -----------------------------------------------------------------

const csrf = () => page.$eval('meta[name="csrf-token"]', (m) => m.content);
const doc = () => page.evaluate(() => window.__hmForms.getDoc());
const labels = async () => (await doc()).fields.map((f) => f.label);
const types = async () => (await doc()).fields.map((f) => f.type);
const selected = () => page.evaluate(() => window.__hmForms.getSelected());
const row = (id) => page.locator(`[data-hm-fd-canvas] [data-id="${id}"]`);
const rowAt = async (i) => row((await doc()).fields[i].id);
// A field's own padding: the control inside an empty field has no size, and the label is an input.
const EDGE = { position: { x: 3, y: 3 } };
const state = () => page.locator(".hm-savestate").first().getAttribute("data-state");
const stateText = () => page.locator(".hm-savestate__text").first().innerText();
const tile = (type) => page.locator(`[data-hm-fd-palette] .hm-fd-tile[data-type="${type}"]`).first();

async function open(id, tab = "") {
  await page.goto(`${base}${admin}/forms/design/${id}${tab}?qa=1`);
  if (!tab) await page.waitForSelector("[data-hm-fd-palette] .hm-fd-tile");
}

/** The definition as the server holds it: the boot JSON of a fresh page load. */
async function saved(id) {
  return page.evaluate(async (u) => {
    const html = await (await fetch(u, { credentials: "same-origin" })).text();
    return JSON.parse(new DOMParser().parseFromString(html, "text/html").querySelector("script[data-hm-fd-boot]").textContent);
  }, `${base}${admin}/forms/design/${id}`);
}
const shape = (fields) => fields.map((f) => [f.type, f.label]);

async function waitSaved() {
  await page.waitForFunction(() => document.querySelector(".hm-savestate")?.dataset.state === "saved" && document.querySelector(".hm-savestate__text")?.textContent.startsWith("Saved"));
}

// -- a form of our own -------------------------------------------------------

await page.goto(`${base}${admin}/forms`);
const made = await page.evaluate(
  async ({ admin, token }) => {
    const res = await fetch(`${admin}/forms`, { method: "POST", body: new URLSearchParams({ name: "QA designer " + Math.random().toString(36).slice(2, 7), _csrf: token }), credentials: "same-origin", headers: { "X-CSRF-Token": token } });
    return { status: res.status, url: res.url };
  },
  { admin, token: await csrf() },
);
const formId = made.url.match(/\/forms\/design\/([0-9a-f-]{36})/)?.[1];
ok(!!formId, `a new form lands on Build (${made.url.replace(base, "")})`);
if (!formId) {
  await b.close();
  process.exit(1);
}

// ============ A1: no page load while building, one save, the definition matches ============
await open(formId);
navs = 0;

// the empty form: a drop target with a hint, and no listbox without options
{
  const sheet = page.locator("[data-hm-fd-sheet]");
  ok((await sheet.locator(".hm-fd-empty").innerText()).includes("Drag your first field here") && !(await sheet.locator(".hm-fd-empty").innerText()).includes("left") && (await sheet.getAttribute("role")) === null, "empty form: the hint shows and the sheet is not an empty listbox");
  const t = await tile("text").boundingBox();
  const to = await sheet.locator(".hm-fd-empty").boundingBox();
  await page.mouse.move(t.x + t.width / 2, t.y + t.height / 2);
  await page.mouse.down();
  await page.mouse.move(t.x + t.width / 2 + 12, t.y + t.height / 2 + 12, { steps: 4 });
  await page.mouse.move(to.x + to.width / 2, to.y + to.height / 2, { steps: 10 });
  await page.mouse.up();
  ok(same(await types(), ["text"]) && (await sheet.getAttribute("role")) === "listbox" && (await sheet.locator(".hm-fd-empty").count()) === 0, "empty form: dropping a tile on the hint adds the first field and the list returns");
  await (await rowAt(0)).locator('[data-act="delete"]').click({ force: true });
  ok((await sheet.locator(".hm-fd-empty").count()) === 1, "empty form: the hint returns when the last field is deleted");
}
await tile("text").click();
await tile("email").click();
// a drag from the palette onto the lower half of the last row
{
  const t = await tile("textarea").boundingBox();
  const last = await (await rowAt(1)).boundingBox();
  await page.mouse.move(t.x + t.width / 2, t.y + t.height / 2);
  await page.mouse.down();
  await page.mouse.move(t.x + t.width / 2 + 12, t.y + t.height / 2 + 12, { steps: 4 });
  await page.mouse.move(last.x + last.width / 2, last.y + last.height - 4, { steps: 10 });
  await page.mouse.up();
}
ok(same(await types(), ["text", "email", "textarea"]), "palette click and drag add three fields in order");

// a field dragged to the top
{
  const f3 = await (await rowAt(2)).boundingBox();
  const first = await (await rowAt(0)).boundingBox();
  await page.mouse.move(f3.x + 3, f3.y + 3);
  await page.mouse.down();
  await page.mouse.move(f3.x + 8, f3.y - 12, { steps: 4 });
  await page.mouse.move(first.x + first.width / 2, first.y + 2, { steps: 10 });
  await page.mouse.up();
}
ok(same(await types(), ["textarea", "text", "email"]), "dragging a field to the top reorders it");

// Alt+Down moves the selected field
{
  await (await rowAt(0)).click(EDGE);
  await page.keyboard.press("Alt+ArrowDown");
}
ok(same(await types(), ["text", "textarea", "email"]), "Alt+Down moves the selected field");

// inline label edits (blur commits), then the inspector
const inline = async (i, text) => {
  const input = (await rowAt(i)).locator("[data-hm-fd-label]");
  await input.fill(text);
  await input.press("Enter");
};
await inline(0, "Full name");
await inline(1, "Notes");
{
  await (await rowAt(2)).click(EDGE);
  const ins = page.locator('[data-hm-fd-inspector] [data-fd-key="label"]');
  await ins.waitFor();
  await ins.fill("Work email");
}
ok(same(await labels(), ["Full name", "Notes", "Work email"]), "labels edited inline and in the inspector reach the document");

// a label edit whose blur comes from a click on the row's own button: the click must land
{
  const n = (await doc()).fields.length;
  const input = (await rowAt(0)).locator("[data-hm-fd-label]");
  await input.fill("Edited then duplicated");
  await (await rowAt(0)).locator('[data-act="duplicate"]').click();
  const d = await doc();
  ok(d.fields.length === n + 1 && d.fields[0].label === "Edited then duplicated", "a click on Duplicate right after a label edit lands (the edit and the click both count)");
  await page.keyboard.press("Control+z");
  await page.keyboard.press("Control+z");
  ok((await labels())[0] === "Full name" && (await rowAt(0)).locator("[data-hm-fd-label]") && (await (await rowAt(0)).locator("[data-hm-fd-label]").inputValue()) === "Full name", "undo puts the label back in the row's input");
}

// add one more, delete it by its button
await tile("number").click();
ok((await doc()).fields.length === 4, "a fourth field is added");
await page.locator('[data-hm-fd-canvas] [aria-selected="true"] [data-act="delete"]').click();
ok(same(await labels(), ["Full name", "Notes", "Work email"]), "the Delete button removes the selected field");

// Ctrl+S saves with no navigation
await page.keyboard.press("Control+s");
await waitSaved();
ok(navs === 0, `building and saving made no page load (${navs})`);
{
  // the relative-time tick must not be announced: the live region is off for it, and back on for a real state change
  const live = () => page.locator(".hm-savestate").first().getAttribute("aria-live");
  await page.evaluate(() => window.__hmTick30());
  ok((await live()) === "off", "the 30 s relative-time tick is silent for screen readers");
  await page.keyboard.press("Control+z");
  await page.waitForFunction(() => document.querySelector(".hm-savestate")?.dataset.state !== "saved");
  ok((await live()) === "polite", "a save-state change is announced again");
  await page.keyboard.press("Control+Shift+z");
  await page.keyboard.press("Control+s");
  await waitSaved();
}
{
  const want = shape((await doc()).fields);
  const got = shape((await saved(formId)).fields);
  ok(same(want, got), `the saved definition matches the canvas (${JSON.stringify(got)})`);
}
await page.reload();
await page.waitForSelector("[data-hm-fd-canvas] [data-id]");
ok(same(await labels(), ["Full name", "Notes", "Work email"]), "after a reload the fields persist");

// ============ A2: the browser validator agrees with the shared table ============
{
  const table = JSON.parse(fs.readFileSync(path.resolve(here, "../../backend/internal/form/testdata/validation_cases.json"), "utf8"));
  const out = await page.evaluate(async (t) => {
    const m = await import("@hm/forms/validate");
    const free = JSON.parse(document.querySelector("script[data-hm-fd-boot]").textContent).freeEmail ?? [];
    const eq = (a, c) => JSON.stringify(a) === JSON.stringify(c);
    const canon = (o) => (o && typeof o === "object" && !Array.isArray(o) ? Object.fromEntries(Object.keys(o).sort().map((k) => [k, canon(o[k])])) : o);
    const fails = [];
    for (const c of t.cases) {
      const r = m.validate(c.fields, c.data, c.context, free);
      const wantErr = Object.keys(c.want_errors ?? {}).length > 0;
      const good = wantErr ? r.data === null && eq(canon(r.errors), canon(c.want_errors)) : Object.keys(r.errors).length === 0 && eq(canon(r.data), canon(c.want_data ?? {}));
      if (!good) fails.push(c.name);
    }
    for (const c of t.visibility) if (!eq(canon(m.visible(c.fields, c.data)), canon(c.want_visible))) fails.push("visibility: " + c.name);
    return { cases: t.cases.length, visibility: t.visibility.length, fails };
  }, table);
  ok(out.fails.length === 0, `A2: ${out.cases} validation and ${out.visibility} visibility cases match the server table${out.fails.length ? " (first mismatch: " + out.fails[0] + ")" : ""}`);
}

// ============ A3: shortcuts ============
await open(formId);
{
  const count = async () => (await doc()).fields.length;
  const base0 = await count(); // 3, saved above
  await (await rowAt(1)).click(EDGE);
  const second = (await doc()).fields[1].id;
  await page.keyboard.press("Alt+ArrowUp");
  ok((await doc()).fields[0].id === second, "A3: Alt+Up moves the field up");
  await page.keyboard.press("Alt+ArrowDown");
  ok((await doc()).fields[1].id === second, "A3: Alt+Down moves it back");
  await page.keyboard.press("Control+d");
  ok((await count()) === base0 + 1, "A3: Ctrl+D duplicates the selected field");
  await page.keyboard.press("Delete");
  ok((await count()) === base0 && (await page.locator(".hm-toast").count()) > 0, "A3: Delete removes the field and shows the undo toast");
  await page.keyboard.press("Control+z");
  ok((await count()) === base0 + 1, "A3: Ctrl+Z undoes the delete");
  await page.keyboard.press("Control+Shift+z");
  ok((await count()) === base0, "A3: Ctrl+Shift+Z redoes it");
  await page.keyboard.press("Control+z");
  await page.keyboard.press("Control+y");
  ok((await count()) === base0, "A3: Ctrl+Y redoes too");
  await page.keyboard.press("Escape");
  ok((await selected()) === null, "A3: Esc deselects");
  await (await rowAt(0)).click(EDGE);
  await page.keyboard.press("ArrowDown");
  ok((await selected()) === (await doc()).fields[1].id, "A3: Down selects the next field");
  await page.keyboard.press("ArrowUp");
  ok((await selected()) === (await doc()).fields[0].id, "A3: Up selects the previous field");

  // keyboard only: focusing a row selects it, and the arrows pick a field when none is selected
  await page.keyboard.press("Escape");
  ok((await selected()) === null && (await page.evaluate(() => document.activeElement?.dataset.id != null)), "A3: Esc deselects and leaves focus on a row");
  await page.keyboard.press("ArrowDown");
  ok((await selected()) === (await doc()).fields[0].id, "A3: Down with nothing selected picks the first field");
  await page.keyboard.press("Escape");
  await (await rowAt(1)).focus();
  ok((await selected()) === (await doc()).fields[1].id, "A3: focusing a row selects it");
  // Tab from the selected row must reach the inspector without selecting the rows after it
  const pick = (await doc()).fields[1].id;
  for (let i = 0; i < 12 && !(await page.evaluate(() => document.activeElement?.closest("[data-hm-fd-inspector]") != null)); i++) await page.keyboard.press("Tab");
  ok(await page.evaluate(() => document.activeElement?.closest("[data-hm-fd-inspector]") != null), "A3: Tab from the canvas reaches the inspector");
  ok((await selected()) === pick, "A3: tabbing to the inspector keeps the selected field selected");
  await (await rowAt(1)).focus();
  const second1 = (await doc()).fields[1].id;
  await page.keyboard.press("Alt+ArrowUp");
  ok((await doc()).fields[0].id === second1, "A3: Alt+Up then reorders it, with no mouse");
  await page.keyboard.press("Alt+ArrowDown");

  // Try it hides the canvas: nothing is selected, and the editing keys do nothing
  await (await rowAt(0)).click(EDGE);
  const tryDoc = JSON.stringify(await doc());
  await page.locator('[data-hm-fd-mode="try"]').click();
  await page.locator("[data-hm-fd-sheet][hidden]").waitFor({ state: "attached" });
  await page.evaluate(() => document.activeElement?.blur());
  for (const k of ["Delete", "Backspace", "Control+d", "Control+z", "Alt+ArrowDown"]) await page.keyboard.press(k);
  ok((await selected()) === null && JSON.stringify(await doc()) === tryDoc, "A3: in Try it nothing is selected and the editing keys change nothing");
  ok((await page.locator("[data-hm-fd-undo]").isDisabled()) && (await page.locator("[data-hm-fd-redo]").isDisabled()), "A3: Undo and Redo are off in Try it");
  await page.locator('[data-hm-fd-mode="edit"]').click();
  ok(!(await page.locator("[data-hm-fd-undo]").isDisabled()), "A3: Undo is back after Try it");

  // typing in a field's label keeps every shortcut to itself
  const before = JSON.stringify(await doc());
  const input = (await rowAt(0)).locator("[data-hm-fd-label]");
  await input.click();
  await page.keyboard.type("x");
  const typed = JSON.stringify((await doc()).fields.map((f) => f.id));
  for (const k of ["Alt+ArrowDown", "Delete", "Backspace", "Control+z", "Control+d", "ArrowDown"]) await page.keyboard.press(k);
  ok(JSON.stringify((await doc()).fields.map((f) => f.id)) === typed && (await count()) === base0, "A3: shortcuts are inert while typing in the label");
  await input.press("Escape");
  ok(JSON.stringify(await doc()) === before, "A3: Esc in the label puts the text back");
}

// ============ A4: tabs and the Back button ============
await open(formId);
await page.goto(`${base}${admin}/forms/design/${formId}/settings`);
await page.waitForSelector(".hm-tabs");
await page.locator('.hm-tabs a:has-text("Build")').first().click();
await page.waitForURL((u) => u.pathname.endsWith(`/forms/design/${formId}`));
await page.goBack();
await page.waitForURL((u) => u.pathname.endsWith("/settings"));
ok(await page.locator('.hm-tabs [aria-current="page"]:has-text("Settings")').count().then((n) => n > 0), "A4: Back from Build shows the Settings tab");
await page.goto(`${base}${admin}/forms/edit/${formId}`);
ok(page.url().includes(`/forms/design/${formId}`), `A4: the old edit URL lands on Build (${page.url().replace(base, "")})`);

// ============ 409: a second writer changes the version ============
/** Opens Build, lets a second writer save, then edits and saves here: the page ends in the conflict state. */
async function conflict(label) {
  await open(formId);
  await page.waitForSelector("[data-hm-fd-canvas] [data-id]");
  const token = await csrf();
  const status = await page.evaluate(
    async ({ admin, id, token }) => {
      const html = await (await fetch(`${admin}/forms/design/${id}`, { credentials: "same-origin" })).text();
      const boot = JSON.parse(new DOMParser().parseFromString(html, "text/html").querySelector("script[data-hm-fd-boot]").textContent);
      const res = await fetch(`${admin}/forms/${id}/definition`, {
        method: "PUT",
        credentials: "same-origin",
        headers: { "Content-Type": "application/json", "X-CSRF-Token": token },
        body: JSON.stringify({ version: boot.version, name: boot.name, description: boot.description ?? "", status: boot.status, fields: boot.fields, settings: boot.settings }),
      });
      return res.status;
    },
    { admin, id: formId, token },
  );
  ok(status === 200, `the second writer saves (${status})`);
  await inline(0, label);
  await page.keyboard.press("Control+s");
  await page.waitForFunction(() => document.querySelector(".hm-savestate__text")?.textContent.includes("changed in another tab"));
}

await conflict("Changed here");
{
  ok((await stateText()).includes("changed in another tab") && (await state()) === "error", "409: the bar says the form changed in another tab");
  ok((await page.locator('a:has-text("Reload")').count()) > 0, "409: a Reload link is offered");
  ok((await saved(formId)).fields[0].label !== "Changed here", "409: the stale save did not overwrite");
  const saveBtn = page.locator("[data-hm-fd-save]");
  ok((await saveBtn.isDisabled()) && ((await saveBtn.getAttribute("title")) ?? "").includes("changed in another tab"), "409: Save is disabled and says why");
  await page.evaluate(() => document.activeElement?.blur());
  await page.keyboard.press("Control+s");
  ok((await page.locator("[data-hm-fd] [role=status].hm-visually-hidden").innerText()).includes("Not saved"), "409: Ctrl+S says it did not save");
  // an edit the store refuses (the 60-field cap) must not undo the stopped state
  await (await rowAt(0)).click(EDGE);
  for (let i = 0; i < 62; i++) await page.keyboard.press("Control+d");
  ok((await doc()).fields.length === 60, "409: the field cap is reached");
  ok((await saveBtn.isDisabled()) && (await page.locator('a:has-text("Reload")').count()) === 1 && (await stateText()).includes("changed in another tab"), "409: a refused edit keeps Save off, the Reload link and the message");
  const nav = page.waitForNavigation();
  await page.locator('a:has-text("Reload")').click();
  await nav;
  await page.waitForSelector("[data-hm-fd-canvas] [data-id]");
  ok((await labels())[0] !== "Changed here", "409: Reload leaves the page and shows the saved form");
}
await conflict("Changed again");
{
  const nav = page.waitForNavigation();
  await page.locator('.hm-tabs a:has-text("Settings")').first().click();
  await nav;
  ok(page.url().endsWith("/settings"), "409: a tab link still leaves the page");
}

// -- clean up and report -----------------------------------------------------
await page.evaluate(
  async ({ admin, id, token }) => {
    await fetch(`${admin}/forms/delete/${id}`, { method: "POST", body: new URLSearchParams({ _csrf: token }), credentials: "same-origin", headers: { "X-CSRF-Token": token } });
  },
  { admin, id: formId, token: await csrf() },
);
ok(errors.length === 0, `no console or page errors${errors.length ? ": " + errors.slice(0, 3).join(" | ") : ""}`);
await b.close();
console.log(bad ? `${bad} check(s) failed` : "all checks passed");
process.exit(bad ? 1 : 0);
