// Media library plus: upload an SVG with a script in it (the notice says what was removed), move it into a folder,
// filter by the folder, replace it with another SVG and screenshot the detail page. The file is deleted at the end.
// node scripts/admin-qa/media-library-plus.mjs   Exit 1 on a failed check.
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { launch, newSession, base, admin, outDir } from "./lib.mjs";

const b = await launch();
const p = await newSession(b, { theme: "light", width: 1440 });
let bad = 0;
const ok = (c, m) => { console.log(`${c ? "ok  " : "FAIL"} ${m}`); if (!c) bad++; };
const dir = fs.mkdtempSync(path.join(os.tmpdir(), "hm-svg-"));
const svg = (fill) => `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"><script>alert(1)</script><rect width="10" height="10" fill="${fill}"/></svg>`;
const first = path.join(dir, "qa-mark.svg");
const second = path.join(dir, "qa-mark-2.svg");
fs.writeFileSync(first, svg("red"));
fs.writeFileSync(second, svg("blue"));

await p.goto(`${base}${admin}/settings/media/new`);
await p.setInputFiles('input[type=file][name=file]', first);
await Promise.all([p.waitForNavigation(), p.locator('form[action$="/media/upload"] button[type=submit]').first().click()]);
ok((await p.textContent("body")).includes("was cleaned"), "the upload says what was removed");

await p.goto(`${base}${admin}/settings/media?q=qa-mark`);
await p.locator('a[href*="/media/"]').filter({ hasText: /qa-mark/ }).first().click();
const detail = p.url();
await p.fill("#new_folder", "QA Folder");
await Promise.all([p.waitForNavigation(), p.locator('button[type=submit]', { hasText: /^Save/ }).first().click()]);
await p.goto(`${base}${admin}/settings/media?folder=QA+Folder`);
ok((await p.textContent("body")).includes("qa-mark"), "the folder filter lists the file");

await p.goto(detail);
await p.setInputFiles("#replace-file", second);
await Promise.all([p.waitForNavigation(), p.locator('button[type=submit]', { hasText: "Replace file" }).click()]);
ok((await p.textContent("body")).includes("The address stays the same"), "replace keeps the address");
fs.mkdirSync(outDir, { recursive: true });
await p.screenshot({ path: path.join(outDir, "media-detail-replace.png"), fullPage: true });

await p.goto(detail.replace(/\/?$/, "/delete"));
await Promise.all([p.waitForNavigation(), p.locator('form button[type=submit]').last().click()]);
fs.rmSync(dir, { recursive: true, force: true });
await b.close();
if (bad) { console.log(`${bad} check(s) failed`); process.exit(1); }
console.log("media-library-plus: all checks passed");
