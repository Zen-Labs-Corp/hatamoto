// The form Settings tab, by click paths with real pointer and keyboard events. It makes its own
// form, then: checks the shared editor bar, adds recipients as chips (Enter, comma, paste with an
// invalid address, Backspace, the Remove button), edits subject, trap field, rate, words and
// retention, undoes and redoes typed runs and chips, saves, reloads and compares with the stored
// form, checks the Build tab is intact, the leave guard, the 409 conflict state and Duplicate.
// Both forms are deleted at the end.
// node scripts/admin-qa/forms-settings.mjs   Exit 1 on a failed check, a console error or a page error.
import fs from "node:fs";
import path from "node:path";
import { launch, newSession, base, admin, outDir } from "./lib.mjs";

const b = await launch();
let bad = 0;
const ok = (c, m) => {
  console.log(`${c ? "ok  " : "FAIL"} ${m}`);
  if (!c) bad++;
};
const same = (a, c) => JSON.stringify(a) === JSON.stringify(c);

const page = await newSession(b, { theme: "light", width: 1440 });
const errors = [];
page.on("pageerror", (e) => errors.push(e.message));
page.on("console", (m) => {
  if (m.type() === "error" && !m.text().includes("409") && !m.text().includes("422")) errors.push(m.text()); // the 409 and 422 checks provoke failed requests on purpose
});
page.on("dialog", (d) => d.accept().catch(() => {}));

const csrf = () => page.$eval('meta[name="csrf-token"]', (m) => m.content);
const doc = () => page.evaluate(() => window.__hmForms.getDoc());
const settings = async () => (await doc()).settings;
const chips = () => page.locator("[data-hm-ti-chips] .hm-chip [data-hm-ti-text]").allInnerTexts();
const state = () => page.locator(".hm-savestate").first().getAttribute("data-state");
const stateText = () => page.locator(".hm-savestate__text").first().innerText();
const tiInput = page.locator("#fs-notify");
const waitSaved = () => page.waitForFunction(() => document.querySelector(".hm-savestate")?.dataset.state === "saved" && document.querySelector(".hm-savestate__text")?.textContent.startsWith("Saved"));

async function open(id) {
  await page.goto(`${base}${admin}/forms/design/${id}/settings?qa=1`);
  await page.waitForSelector("[data-hm-fs-root]:not([hidden])");
}

/** The form as the server holds it: the boot JSON of a fresh Build page load. */
async function saved(id) {
  return page.evaluate(async (u) => {
    const html = await (await fetch(u, { credentials: "same-origin" })).text();
    return JSON.parse(new DOMParser().parseFromString(html, "text/html").querySelector("script[data-hm-fd-boot]").textContent);
  }, `${base}${admin}/forms/design/${id}`);
}

/** PUTs the stored definition as a second writer would, with patch.fields, patch.nameSuffix and patch.settings applied; returns the status. */
async function put(id, patch) {
  const token = await csrf();
  return page.evaluate(
    async ({ admin, id, token, patch }) => {
      const html = await (await fetch(`${admin}/forms/design/${id}`, { credentials: "same-origin" })).text();
      const boot = JSON.parse(new DOMParser().parseFromString(html, "text/html").querySelector("script[data-hm-fd-boot]").textContent);
      const body = { version: boot.version, name: boot.name, description: boot.description ?? "", status: boot.status, fields: boot.fields, settings: boot.settings };
      if (patch.fields) body.fields = patch.fields;
      if (patch.nameSuffix) body.name += patch.nameSuffix;
      Object.assign(body.settings, patch.settings);
      const res = await fetch(`${admin}/forms/${id}/definition`, { method: "PUT", credentials: "same-origin", headers: { "Content-Type": "application/json", "X-CSRF-Token": token }, body: JSON.stringify(body) });
      return res.status;
    },
    { admin, id, token, patch },
  );
}

// -- a form of our own, with three fields ------------------------------------

await page.goto(`${base}${admin}/forms`);
const name = "QA settings " + Math.random().toString(36).slice(2, 7);
const made = await page.evaluate(
  async ({ admin, token, name }) => {
    const res = await fetch(`${admin}/forms`, { method: "POST", body: new URLSearchParams({ name, _csrf: token }), credentials: "same-origin", headers: { "X-CSRF-Token": token } });
    return { status: res.status, url: res.url };
  },
  { admin, token: await csrf(), name },
);
const formId = made.url.match(/\/forms\/design\/([0-9a-f-]{36})/)?.[1];
ok(!!formId, `a new form is made (${made.url.replace(base, "")})`);
if (!formId) {
  await b.close();
  process.exit(1);
}
ok(
  (await put(formId, {
    fields: [
      { id: "f_a", type: "text", label: "First name", name: "first_name", position: 0 },
      { id: "f_b", type: "email", label: "Email", name: "email", position: 1 },
      { id: "f_c", type: "textarea", label: "Message", name: "message", position: 2 },
    ],
  })) === 200,
  "three fields are saved through the definition endpoint",
);

// ============ the page and its editor bar ============
await open(formId);
ok((await page.locator("[data-hm-fd-title]").inputValue()) === name, "bar: the form name");
ok(await page.locator("[data-hm-fd-status]").isVisible(), "bar: the status select");
ok((await stateText()).startsWith("Saved"), "bar: Saved N ago");
ok((await page.locator("[data-hm-fd-undo]").isVisible()) && (await page.locator("[data-hm-fd-redo]").isVisible()) && (await page.locator("[data-hm-fd-save]").isVisible()), "bar: Undo, Redo and Save show");
ok((await page.locator("[data-hm-fd-undo]").isDisabled()) && (await page.locator("[data-hm-fd-save]").isDisabled()), "bar: nothing to undo or save yet");
ok((await page.getByText("View on site").count()) === 0, "bar: no View on site (forms have no public page)");
ok((await page.locator("[data-hm-fs-root] .hm-card").count()) === 7, "seven cards");

// ============ recipients as chips ============
await tiInput.click();
await page.keyboard.type("a@example.com");
await page.keyboard.press("Enter");
await page.keyboard.type("b@example.com,");
ok(same(await chips(), ["a@example.com", "b@example.com"]), "chips: Enter and comma each commit an address");
ok((await settings()).notify_email === "a@example.com, b@example.com", "chips: the stored value is the comma string");
ok((await page.locator('[data-hm-ti-chips] [aria-label="Remove a@example.com"]').count()) === 1 && (await page.locator("[data-hm-ti-chips]").getAttribute("role")) === "list", "chips: Remove buttons are named and the list is a list");
await page.evaluate(() => {
  const input = document.querySelector("#fs-notify");
  const data = new DataTransfer();
  data.setData("text", "c@example.com, d@example.com\nnot-an-email");
  input.dispatchEvent(new ClipboardEvent("paste", { clipboardData: data, bubbles: true, cancelable: true }));
});
ok(same(await chips(), ["a@example.com", "b@example.com", "c@example.com", "d@example.com"]), "paste: one chip per valid address");
ok((await tiInput.inputValue()) === "not-an-email" && (await tiInput.getAttribute("aria-invalid")) === "true", "paste: the invalid address stays in the input, marked invalid");
{
  const errId = await page.locator("[data-hm-ti-err]").getAttribute("id");
  ok(((await tiInput.getAttribute("aria-describedby")) ?? "").split(" ").includes(errId) && (await page.locator("[data-hm-ti-err]").isVisible()), "paste: the error is visible and tied to the input");
  ok((await page.locator("[data-hm-ti-live]").innerText()).includes("not-an-email"), "paste: the live region announces it");
}
await tiInput.fill("");
await page.keyboard.press("Backspace");
ok(same(await chips(), ["a@example.com", "b@example.com", "c@example.com"]), "Backspace on the empty input removes the last chip");
await page.locator('[aria-label="Remove c@example.com"]').click();
ok(same(await chips(), ["a@example.com", "b@example.com"]), "the Remove button removes its chip");
ok((await page.locator("[data-hm-ti-live]").innerText()) === "Removed c@example.com", "the removal is announced");
ok((await settings()).notify_email === "a@example.com, b@example.com", "chips: the stored value follows");

// undo and redo re-sync the chips
await tiInput.click();
await page.keyboard.type("e@example.com");
await page.keyboard.press("Enter");
ok(same(await chips(), ["a@example.com", "b@example.com", "e@example.com"]), "a third address is added");
await page.locator("[data-hm-fd-undo]").click();
ok(same(await chips(), ["a@example.com", "b@example.com"]) && (await settings()).notify_email === "a@example.com, b@example.com", "Undo takes the chip back out");
await page.locator("[data-hm-fd-redo]").click();
ok(same(await chips(), ["a@example.com", "b@example.com", "e@example.com"]), "Redo puts it back");
await page.locator('[aria-label="Remove e@example.com"]').click();

// ============ the other controls ============
await page.locator("#fs-notify-subject").fill("New enquiry from {{first_name}}");
ok((await settings()).notify_subject === "New enquiry from {{first_name}}", "subject: typed");
ok((await page.locator("[data-hm-fs-subject-preview]").innerText()).includes("New enquiry from First name"), "subject: the preview fills the token");
await page.locator('[data-hm-fs-token="{{email}}"]').click();
ok((await settings()).notify_subject === "New enquiry from {{first_name}}{{email}}", "subject: a token button inserts at the cursor");
await page.locator("#fs-reply-to").selectOption("email");
ok((await settings()).notify_reply_to === "email", "reply-to: the email field");

// a typed run is one undo step, and the control shows the earlier text
await page.locator("#fs-notify-subject").fill("Base");
await page.locator("#fs-notify-subject").click();
await page.keyboard.press("End");
await page.keyboard.type(" and more");
ok((await settings()).notify_subject === "Base and more", "typing: a run in the subject");
await page.locator("[data-hm-fd-undo]").click();
ok((await settings()).notify_subject === "New enquiry from {{first_name}}{{email}}" && (await page.locator("#fs-notify-subject").inputValue()) === "New enquiry from {{first_name}}{{email}}", "Undo reverts the whole typed run and the control follows");
await page.locator("[data-hm-fd-redo]").click();
ok((await page.locator("#fs-notify-subject").inputValue()) === "Base and more", "Redo restores it");
await page.locator("#fs-notify-subject").fill("New enquiry from {{first_name}}");

await page.locator('label[for="fs-honeypot"]').click();
ok((await settings()).honeypot_off === true, "trap field: off writes honeypot_off");
await page.locator("#fs-rate").fill("5");
ok((await settings()).rate_limit_per_hour === 5, "rate: 5 an hour");
await page.locator("#fs-rate").fill("lots");
ok((await page.locator('[data-hm-fs-err="settings.rate_limit_per_hour"]').isVisible()) && (await settings()).rate_limit_per_hour === 5, "rate: a non-number is refused inline and not stored");
await page.locator("#fs-rate").fill("5");
ok(!(await page.locator('[data-hm-fs-err="settings.rate_limit_per_hour"]').isVisible()), "rate: the message goes when the value is fixed");
await page.locator("#fs-words").fill("casino\nexample.net");
ok(same((await settings()).blocked_words, ["casino", "example.net"]), "words: one per line");
await page.locator('[data-hm-fs-seg="retention_days"] [data-value="90"]').click();
ok((await settings()).retention_days === 90 && (await page.locator('[data-hm-fs-seg="retention_days"] [data-value="90"]').getAttribute("aria-pressed")) === "true", "retention: 90 days");

// the store switch hides retention; no-IP stays
await page.locator('label[for="fs-store"]').click();
ok((await settings()).store_off === true && !(await page.locator('[data-hm-fs-seg="retention_days"]').isVisible()) && (await page.locator('label[for="fs-noip"]').isVisible()), "store off: written as store_off, retention hides, no-IP stays");
await page.locator('label[for="fs-store"]').click();
ok((await page.locator('[data-hm-fs-seg="retention_days"]').isVisible()) && (await settings()).retention_days === 90, "store on again: retention shows with its value");
await page.locator('label[for="fs-noip"]').click();
ok((await settings()).no_ip === true, "no IP: on");

// "Then": a page instead of a message
await page.locator('[data-hm-fs-seg="after"] [data-value="redirect"]').click();
await page.locator("#fs-redirect").fill("/thank-you");
ok((await settings()).success_redirect_url === "/thank-you" && !(await page.locator("#fs-success").isVisible()), "after: go to a page");

// confirmation preview
await page.locator('label[for="fs-confirm-on"]').click();
await page.locator("#fs-confirm-subject").fill("Got it");
ok((await page.locator("[data-hm-fs-mail-subject]").innerText()) === "Got it" && (await page.locator("[data-hm-fs-mail-to]").innerText()) === "visitor@example.com", "confirmation: live preview");

// ============ save, reload, compare ============
await page.locator("[data-hm-fd-save]").click();
await waitSaved();
const stored = await saved(formId);
ok(stored.settings.notify_email === "a@example.com, b@example.com" && stored.settings.notify_subject === "New enquiry from {{first_name}}" && stored.settings.honeypot_off === true, "saved: recipients, subject and trap field");
ok(stored.settings.rate_limit_per_hour === 5 && same(stored.settings.blocked_words, ["casino", "example.net"]) && stored.settings.retention_days === 90 && stored.settings.no_ip === true && stored.settings.notify_reply_to === "email", "saved: rate, words, retention, no IP, reply-to");
ok(same(stored.fields.map((f) => f.label), ["First name", "Email", "Message"]), "saved: the Build tab's fields are intact");
await open(formId);
ok(same(await chips(), ["a@example.com", "b@example.com"]) && (await page.locator("#fs-notify-subject").inputValue()) === "New enquiry from {{first_name}}", "reload: chips and subject");
ok(!(await page.locator("#fs-honeypot").isChecked()) && (await page.locator("#fs-rate").inputValue()) === "5" && (await page.locator("#fs-words").inputValue()) === "casino\nexample.net", "reload: trap field, rate and words");
ok((await page.locator('[data-hm-fs-seg="retention_days"] [data-value="90"]').getAttribute("aria-pressed")) === "true" && (await page.locator("#fs-reply-to").inputValue()) === "email", "reload: retention and reply-to");
ok((await state()) === "saved" && (await page.locator("[data-hm-fd-undo]").isDisabled()), "reload: clean, nothing to undo");

// ============ an untouched legacy value is written back as it was ============
ok((await put(formId, { settings: { notify_email: "a@example.com,b@example.com" } })) === 200, "a recipient string in a different shape is stored");
await open(formId);
ok(same(await chips(), ["a@example.com", "b@example.com"]), "legacy: it shows as chips");
await page.locator("#fs-rate").fill("6");
await page.locator("[data-hm-fd-save]").click();
await waitSaved();
ok((await saved(formId)).settings.notify_email === "a@example.com,b@example.com", "legacy: saving another setting leaves the string byte-identical");

// ============ the leave guard ============
await page.locator("#fs-notify-subject").fill("Unsaved subject");
ok(await page.evaluate(() => { const e = new Event("beforeunload", { cancelable: true }); window.dispatchEvent(e); return e.defaultPrevented; }), "leave: unsaved edits ask before the page unloads");
await page.keyboard.press("Control+s");
await waitSaved();
ok(!(await page.evaluate(() => { const e = new Event("beforeunload", { cancelable: true }); window.dispatchEvent(e); return e.defaultPrevented; })), "leave: Ctrl+S saves and the guard lets go");
await page.locator("#fs-notify-subject").fill("Saved by the tab link");
const nav = page.waitForNavigation();
await page.locator('.hm-tabs a:has-text("Build")').first().click();
await nav;
ok(page.url().endsWith(`/forms/design/${formId}`) && (await saved(formId)).settings.notify_subject === "Saved by the tab link", "leave: a tab link saves first, then goes");

// ============ conflict: a second writer ============
await open(formId);
ok((await put(formId, { nameSuffix: " (renamed elsewhere)" })) === 200, "the second writer saves");
await page.locator("#fs-notify-subject").fill("Mine");
await page.keyboard.press("Control+s");
await page.waitForFunction(() => document.querySelector(".hm-savestate__text")?.textContent.includes("changed in another tab"));
ok((await state()) === "error" && (await page.locator('a:has-text("Reload")').count()) > 0 && (await page.locator("[data-hm-fd-save]").isDisabled()), "409: the bar says so, offers Reload and turns Save off");
ok((await saved(formId)).settings.notify_subject !== "Mine", "409: the stale save did not overwrite");

// ============ a server refusal shows next to its control ============
await open(formId);
await page.locator('[data-hm-fs-seg="after"] [data-value="redirect"]').click();
await page.locator("#fs-redirect").fill("javascript:alert(1)");
await page.keyboard.press("Control+s");
await page.waitForSelector('[data-hm-fs-err="settings.success_redirect_url"]:not([hidden])');
ok(((await page.locator("#fs-redirect").getAttribute("aria-describedby")) ?? "").includes("fs-err-success_redirect_url"), "422: the message sits under the address and is tied to it");
await page.locator("#fs-redirect").fill("/ok");
ok(!(await page.locator('[data-hm-fs-err="settings.success_redirect_url"]').isVisible()), "422: editing the field clears its message");

// ============ screenshot ============
fs.mkdirSync(outDir, { recursive: true });
await page.screenshot({ fullPage: true, path: path.join(outDir, "forms-settings.png") });

// ============ duplicate ============
await open(formId);
await page.locator('form[action$="/duplicate"] button').click();
await page.waitForURL((u) => /\/forms\/design\/[0-9a-f-]{36}\/settings$/.test(u.pathname) && !u.pathname.includes(formId));
const copyId = page.url().match(/\/forms\/design\/([0-9a-f-]{36})/)[1];
await page.waitForSelector("[data-hm-fs-root]:not([hidden])");
{
  const copy = await saved(copyId);
  ok(copy.name === `Copy of ${(await saved(formId)).name}`.slice(0, 120) && copy.status === "inactive", `duplicate: "${copy.name}" starts paused`);
  ok(copy.fields.length === 3 && copy.settings.notify_email === "a@example.com,b@example.com" && copy.settings.retention_days === 90, "duplicate: fields and settings are copied");
  ok(copyId !== formId && copy.endpoint !== (await saved(formId)).endpoint, "duplicate: a new identifier");
}

// -- clean up and report -----------------------------------------------------
const token = await csrf();
for (const id of [copyId, formId]) {
  await page.evaluate(
    async ({ admin, id, token }) => {
      await fetch(`${admin}/forms/delete/${id}`, { method: "POST", body: new URLSearchParams({ _csrf: token }), credentials: "same-origin", headers: { "X-CSRF-Token": token } });
    },
    { admin, id, token },
  );
}
ok(errors.length === 0, `no console or page errors${errors.length ? ": " + errors.slice(0, 3).join(" | ") : ""}`);
await b.close();
console.log(bad ? `${bad} check(s) failed` : "all checks passed");
process.exit(bad ? 1 : 0);
