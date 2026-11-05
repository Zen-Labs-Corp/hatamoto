// Fonts: Settings > Colours and fonts > Fonts. Upload a .woff2 with the keyboard, see it listed, pick it as the
// body font, check the public page serves it from the site (no third party host), refuse removal while in use,
// then remove it. axe finds nothing on the three pages. The Google download is not exercised (it needs the network).
// Run: scripts/admin-qa/run-local.sh fonts.mjs
import fs from "node:fs";
import { launch, newSession, base, admin, outDir } from "./lib.mjs";

const axeSrc = fs.readFileSync(new URL("./vendor/axe.min.js", import.meta.url), "utf8");
const b = await launch();
let bad = 0;
const ok = (c, m) => { console.log(`${c ? "ok  " : "FAIL"} ${m}`); if (!c) bad++; };
const tags = ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"];
fs.mkdirSync(outDir, { recursive: true });

const p = await newSession(b, { theme: "light", width: 1440, bypassCSP: true });
const axe = async (label) => {
  if (!(await p.evaluate(() => typeof axe !== "undefined"))) await p.addScriptTag({ content: axeSrc });
  const res = await p.evaluate((t) => axe.run(document, { runOnly: { type: "tag", values: t } }), tags);
  ok(res.violations.length === 0, `axe: ${label} has ${res.violations.length} violations ${res.violations.map((x) => x.id).join(",")}`);
};

// The tab is reachable from Colours and fonts.
await p.goto(`${base}${admin}/theme/colours`);
await p.getByRole("link", { name: "Fonts", exact: true }).first().click();
await p.waitForURL((u) => u.pathname.endsWith("/theme/fonts"));
ok((await p.locator("h1").innerText()).includes("Colours and fonts"), "the Fonts tab sits under Colours and fonts");
await p.screenshot({ path: `${outDir}/fonts-list.png`, fullPage: true });
await axe("the list (no fonts)");

// Keyboard path through the upload form.
await p.getByRole("link", { name: "Upload a font" }).focus();
await p.keyboard.press("Enter");
await p.waitForURL((u) => u.pathname.endsWith("/theme/fonts/upload"));
await axe("the upload form");
await p.setInputFiles("#upload_file", { name: "brand.woff2", mimeType: "font/woff2", buffer: Buffer.concat([Buffer.from("wOF2"), Buffer.alloc(256)]) });
await p.fill("#upload_family", "Brand Sans");
await p.focus("#upload_style");
await p.keyboard.press("Tab");
ok(await p.evaluate(() => document.activeElement.tagName === "BUTTON" || document.activeElement.closest("footer, form") !== null), "Tab leaves the style field for the buttons");
await Promise.all([p.waitForURL((u) => u.pathname.endsWith("/theme/fonts")), p.getByRole("button", { name: "Upload font" }).click()]);
ok((await p.locator("table").innerText()).includes("Brand Sans"), "the uploaded font is listed");

// A bad file is refused with a reason.
await p.goto(`${base}${admin}/theme/fonts/upload`);
await p.setInputFiles("#upload_file", { name: "bad.woff2", mimeType: "font/woff2", buffer: Buffer.from("not a font") });
await p.fill("#upload_family", "Bad Font");
await p.getByRole("button", { name: "Upload font" }).click();
await p.waitForSelector(".hm-field__error");
ok((await p.locator(".hm-field__error").first().innerText()).includes("not a .woff2"), "a non-woff2 file is refused with a reason");
await axe("the upload form with an error");

await p.goto(`${base}${admin}/theme/fonts/google`);
await axe("the Google form");
await p.screenshot({ path: `${outDir}/fonts-google.png`, fullPage: true });
await p.getByLabel("Google font name").fill("");
await p.keyboard.press("Tab");
ok(await p.evaluate(() => document.activeElement.closest(".hm-field__chips") !== null), "Tab moves from the name to the weights");

// Choose it as the body font and read the public page as a visitor.
await p.goto(`${base}${admin}/theme/colours`);
await p.selectOption("#font_choice", { label: "Brand Sans" });
await Promise.all([p.waitForURL((u) => u.pathname.endsWith("/theme/colours")), p.getByRole("button", { name: "Save colours and fonts" }).click()]);
await p.goto(`${base}${admin}/theme/colours`);
ok((await p.locator("#font").inputValue()).includes("Brand Sans"), "the chosen font is stored as a stack");
await axe("Colours and fonts with the pickers");

const v = await (await b.newContext({ viewport: { width: 1440, height: 900 } })).newPage();
const resp = await v.goto(`${base}/`);
const html = await resp.text();
ok(html.includes('@font-face{font-family:"Brand Sans"') && html.includes("font-display:swap"), "the public page declares the face with swap");
ok((html.match(/rel="preload"[^>]*as="font"/g) || []).length === 1, "exactly one font preload");
ok(!/googleapis|gstatic/.test(html), "the page names no Google host");
const file = /\/fonts\/([0-9a-f]{64})\.woff2/.exec(html);
const fr = file && (await v.request.get(`${base}/fonts/${file[1]}.woff2`));
ok(!!fr && fr.status() === 200 && fr.headers()["content-type"] === "font/woff2" && /immutable/.test(fr.headers()["cache-control"]), "the font file is served as font/woff2 and immutable");

// In use: refused. Not in use: removed.
await p.goto(`${base}${admin}/theme/fonts`);
await p.getByRole("button", { name: "Remove Brand Sans" }).click();
await p.waitForSelector(".hm-formerrors, [role=alert]");
ok((await p.locator("body").innerText()).includes("Brand Sans is the body font"), "removing the body font is refused");
await p.goto(`${base}${admin}/theme/colours`);
await p.selectOption("#font_choice", { label: "Custom stack" });
await p.fill("#font", "Inter, sans-serif");
await Promise.all([p.waitForURL((u) => u.pathname.endsWith("/theme/colours")), p.getByRole("button", { name: "Save colours and fonts" }).click()]);
await p.goto(`${base}${admin}/theme/fonts`);
await Promise.all([p.waitForURL((u) => u.pathname.endsWith("/theme/fonts")), p.getByRole("button", { name: "Remove Brand Sans" }).click()]);
ok((await p.locator("main").innerText()).includes("No fonts yet"), "an unused font is removed");

await b.close();
process.exit(bad ? 1 : 0);
