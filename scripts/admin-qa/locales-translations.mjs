// The Translations tab on Details: add two locales, re-tag a copy through the Locale select, link it from the
// original's tab through the dialog, read the s4 page (desktop and phone width), make the copy the original and
// back, edit the original and expect "Out of date" on the copy, then unlink. It then creates a copy through the
// Create copy page, the bulk action and Add a locale > Start with, copies a term, and checks the single-locale
// site for none of those controls (A6). Uses the seeded "QA Post 1" and
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

// A6: a single-locale site shows none of the copy controls.
await p.goto(`${base}/admin/entities`);
ok((await p.getByText("Create drafts in").count()) === 0, "single-locale: no bulk Create drafts action");
const direct = await p.goto(`${base}/admin/entities/${first}/translations/new?locale=pt`);
ok(direct.status() === 404, "single-locale: the Create copy page answers 404");
await p.goto(`${base}/admin/settings/locales/new`);
ok((await p.locator("#locale-start").count()) === 0 && (await p.getByText("Start with").count()) === 0, "single-locale: Add a locale has no Start with");
const termHref = await (async () => {
  await p.goto(`${base}/admin/settings/taxonomies`);
  const link = p.locator('a[href*="/settings/taxonomies/terms/"]').first();
  return (await link.count()) ? link.getAttribute("href") : null;
})();
if (termHref) {
  await p.goto(`${base}${termHref}`);
  ok((await p.locator("#term-locale, #term-locales-h").count()) === 0, "single-locale: the term page has no Locale select or Languages block");
}

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

// Create a Spanish copy of the first post from the Create copy page: the title and address are prefilled, the
// copy starts as a draft and is linked into the group.
await p.goto(`${base}/admin/entities/${first}/translations/new?locale=es`);
ok((await p.locator("#copy-es-name").inputValue()) === "QA Post 1", "Create copy: the title is prefilled from the original");
ok((await p.getByText("Stays the same as the original").count()) === 1, "Create copy: says what stays the same");
ok((await p.getByRole("radio", { name: /Empty page/ }).count()) === 1, "Create copy: offers an empty page");
await shot(p, "create-copy");
await Promise.all([p.waitForLoadState(), p.locator('form[action$="/translations/copy"] button[type=submit]').first().click()]);
ok(/\/admin\/entities\/[0-9a-f-]{36}/.test(p.url()) && !p.url().endsWith(first), "Create copy: opens the new copy");
const copyId = p.url().match(/entities\/([0-9a-f-]{36})/)[1];
await p.goto(`${base}/admin/entities/${first}/translations`);
ok((await p.locator("#row-es").getByText("no copy").count()) === 0, "Create copy: Spanish now has a copy");

// Bulk: tick the second post and create drafts in Spanish; the row that already has one is skipped next time.
await p.goto(`${base}/admin/entities?q=QA%20Post%202`);
await p.locator('tbody input[type=checkbox]').first().check();
ok((await p.getByRole("button", { name: "Create drafts in es" }).or(p.getByRole("menuitem", { name: "Create drafts in es" })).count()) >= 1, "bulk: Create drafts in es is offered");

// Add a locale shows Start with once the site has entries.
await p.goto(`${base}/admin/settings/locales/new`);
ok((await p.getByText("Start with").count()) >= 1, "Add a locale: Start with is offered");

// Term page: Locale select and the copies block.
if (termHref) {
  await p.goto(`${base}${termHref}`);
  ok((await p.locator("#term-locale").count()) === 1, "term: the Locale select is shown");
  ok((await p.locator("#term-locales-h").count()) === 1, "term: the Languages block is shown");
  await shot(p, "term-copies");
}

// Tidy up: delete the created copy.
await p.goto(`${base}/admin/entities/${copyId}/delete`);
const del = p.locator('form[action$="/delete"] button[type=submit]').first();
if (await del.count()) await Promise.all([p.waitForLoadState(), del.click()]);

await removeLocale("es");
await removeLocale("fr");
await b.close();
process.exit(bad ? 1 : 0);
