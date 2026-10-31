// The Translations tab on Details: add two locales, re-tag a copy through the Locale select, link it from the
// original's tab through the dialog, read the s4 page (desktop and phone width), make the copy the original and
// back, edit the original and expect "Out of date" on the copy, then unlink. Uses the seeded "QA Post 1" and
// "QA Post 2" (same type); the locales are removed again at the end.
// Run: scripts/admin-qa/run-local.sh locales-translations.mjs   Output: SHOTS_OUT (default results/shots). Exit 1 on a failure.
import fs from "node:fs";
import path from "node:path";
import { launch, newSession, base, here } from "./lib.mjs";

const OUT = process.env.SHOTS_OUT || path.join(here, "results", "shots");
fs.mkdirSync(OUT, { recursive: true });
const b = await launch();
let bad = 0;
const ok = (c, m) => { console.log(`${c ? "ok  " : "FAIL"} ${m}`); if (!c) bad++; };
const shot = (p, name) => p.screenshot({ path: path.join(OUT, `translations-${name}.png`), fullPage: true });
const p = await newSession(b, { theme: "light", width: 1440 });

async function idOf(name) {
  await p.goto(`${base}/admin/entities?q=${encodeURIComponent(name)}`);
  const href = await p.locator(`a[href^="/admin/entities/"]:text-is("${name}")`).first().getAttribute("href");
  return href.match(/entities\/([0-9a-f-]{36})/)[1];
}
async function addLocale(tag, name) {
  await p.goto(`${base}/admin/settings/locales`);
  const t = await p.locator("#add-locale-form input[name=_csrf]").inputValue();
  const res = await p.evaluate(async ([tag, name, t]) => (await fetch("/admin/settings/locales/add", { method: "POST", headers: { "Content-Type": "application/x-www-form-urlencoded" }, body: new URLSearchParams({ _csrf: t, tag, name, public: "true" }) })).status, [tag, name, t]);
  ok(res === 200, `locale ${tag} added (${res})`);
}
async function removeLocale(tag) {
  await p.goto(`${base}/admin/settings/locales/remove/${tag}`);
  const btn = p.locator("form[action$='/remove/" + tag + "'] button[type=submit]").first();
  if (await btn.count()) await Promise.all([p.waitForLoadState(), btn.click()]);
}

const first = await idOf("QA Post 1");
const second = await idOf("QA Post 2");

// Single-locale site (A6): no tab, no select, an empty state with a way to add a locale.
await p.goto(`${base}/admin/entities/${first}`);
ok((await p.getByRole("link", { name: /^Translations/ }).count()) === 0, "single-locale: no Translations tab");
ok((await p.locator("#locale").count()) === 0, "single-locale: no Locale select");
await p.goto(`${base}/admin/entities/${first}/translations`);
ok((await p.getByText("This site has one language").count()) === 1 && (await p.getByRole("link", { name: "Add a locale" }).count()) === 1, "single-locale: direct visit shows the empty state");
await shot(p, "single-locale");

await addLocale("fr", "French");
await addLocale("es", "Spanish");

// Re-tag the second post to French on Details.
await p.goto(`${base}/admin/entities/${second}`);
ok((await p.locator("#locale option[value=und]").count()) === 0, "the Locale select never offers und");
await p.selectOption("#locale", "fr");
await Promise.all([p.waitForLoadState(), p.locator('.hm-savebar button.hm-btn--primary:has-text("Save")').first().click()]);
await p.goto(`${base}/admin/entities/${second}`);
ok((await p.locator("#locale").inputValue()) === "fr", "the copy is now French");

// Link it from the first post's tab through the dialog.
await p.goto(`${base}/admin/entities/${first}/translations`);
ok((await p.getByRole("link", { name: "Translations 1/3" }).count()) === 1, "tab count before linking is 1/3");
await p.getByRole("button", { name: "Link an existing page" }).or(p.getByRole("link", { name: "Link an existing page" })).first().click();
await p.locator("dialog#link-copy[open]").waitFor();
await p.fill("#link-copy-page", "QA Post 2");
await p.locator("#link-copy-page-list [role=option]").first().click();
await Promise.all([p.waitForLoadState(), p.locator('dialog#link-copy button[form="link-copy-form"]').click()]);
ok((await p.getByRole("link", { name: "Translations 2/3" }).count()) === 1, "tab count after linking is 2/3");
ok((await p.getByText("no copy").count()) >= 1, "Spanish shows no copy, and no Create button");
ok((await p.getByRole("button", { name: /^Create/ }).count()) === 0, "no Create button");
await shot(p, "desktop");

// Edit the original's content: the copy turns Out of date.
await p.goto(`${base}/admin/entities/${first}`);
const field = p.locator('textarea[name^="attr."]:visible, input[name^="attr."][type=text]:visible').first();
ok((await field.count()) === 1, "the post has a text field to edit");
await field.fill("Edited after the translation " + Date.now());
await Promise.all([p.waitForLoadState(), p.locator('.hm-savebar button.hm-btn--primary:has-text("Save")').first().click()]);
await p.goto(`${base}/admin/entities/${first}/translations`);
ok((await p.getByText("Out of date").count()) >= 1, "the French copy is Out of date");

// Make the French copy the original, then back.
await p.locator("#row-fr .hm-table__actions button").first().click();
await Promise.all([p.waitForLoadState(), p.getByRole("menuitem", { name: "Make original" }).first().click()]);
ok((await p.locator("#row-fr").getByText("Original").count()) === 1, "French is the original");
await p.locator("#row-en .hm-table__actions button").first().click();
await Promise.all([p.waitForLoadState(), p.getByRole("menuitem", { name: "Make original" }).first().click()]);
ok((await p.locator("#row-en").getByText("Original").count()) === 1, "English is the original again");

// Phone width.
const phone = await newSession(b, { theme: "light", width: 390 });
await phone.goto(`${base}/admin/entities/${first}/translations`);
ok(await phone.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1), "phone: the page does not scroll sideways");
await shot(phone, "phone");

// Unlink the copy (the confirmation page is the no-JS path; here the dialog confirms).
p.on("dialog", (d) => d.accept().catch(() => {}));
await p.locator("#row-fr .hm-table__actions button").first().click();
await p.getByRole("menuitem", { name: "Unlink" }).first().click();
const confirm = p.locator("dialog#hm-confirm[open] button.hm-btn--primary, dialog#hm-confirm[open] button[data-hm-confirm-ok]").first();
if (await confirm.count()) await Promise.all([p.waitForLoadState(), confirm.click()]);
await p.waitForLoadState();
await p.goto(`${base}/admin/entities/${first}/translations`);
ok((await p.getByRole("link", { name: "Translations 1/3" }).count()) === 1, "after unlinking the tab is 1/3 again");

await removeLocale("es");
await removeLocale("fr");
await b.close();
process.exit(bad ? 1 : 0);
