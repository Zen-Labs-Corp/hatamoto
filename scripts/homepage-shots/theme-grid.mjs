// Composes the theme gallery for the README: up to four theme screenshots in a 2x2 grid with
// their names, as one WebP 1600 px wide. The screenshots are the ones the theme registry
// publishes (registry/screenshots/, filled by registry/build-community-themes.sh).
//   node theme-grid.mjs SCREENSHOTS_DIR OUT.webp
// Env: CHROME.
import fs from "node:fs";
import path from "node:path";
import sharp from "sharp";
import { chromium } from "playwright-core";
import { fontFile } from "./frame.mjs";

const [dir, dest] = process.argv.slice(2);
if (!dir || !dest) throw new Error("usage: node theme-grid.mjs SCREENSHOTS_DIR OUT.webp");
const files = fs.readdirSync(dir).filter((f) => /\.(png|webp|jpe?g)$/i.test(f)).sort().slice(0, 4);
if (files.length < 4) throw new Error(`theme-grid: need 4 screenshots in ${dir}, found ${files.length}`);

const W = 1600;
// Each screenshot is cropped to 16:10 from the top and inlined, so the page has no external fetches.
const cells = [];
for (const f of files) {
  const buf = await sharp(path.join(dir, f)).resize(1440, 900, { fit: "cover", position: "top" }).webp({ quality: 85 }).toBuffer();
  cells.push({ name: path.parse(f).name, src: `data:image/webp;base64,${buf.toString("base64")}` });
}
const title = (s) => s.charAt(0).toUpperCase() + s.slice(1);
const html = `<!doctype html><meta charset="utf-8"><style>
@font-face{font-family:Geist;src:url(data:font/woff2;base64,${fontFile("Geist-Variable.woff2")});font-weight:100 900}
*{box-sizing:border-box}html,body{margin:0;width:${W}px;background:#f4f4f2;font-family:Geist,sans-serif}
main{display:grid;grid-template-columns:1fr 1fr;gap:28px 32px;padding:40px 48px}
figure{margin:0}
img{display:block;width:100%;aspect-ratio:16/10;object-fit:cover;object-position:top;border-radius:10px;border:1px solid #dcdcd6;box-shadow:0 1px 2px rgba(0,0,0,.04),0 8px 24px rgba(0,0,0,.06)}
figcaption{margin-top:10px;font-size:17px;font-weight:550;color:#1b1b1d;letter-spacing:-.01em}
</style><main>${cells.map((c) => `<figure><img src="${c.src}" alt=""><figcaption>${title(c.name)}</figcaption></figure>`).join("")}</main>`;

const browser = await chromium.launch({ executablePath: process.env.CHROME || "/usr/bin/google-chrome" });
const page = await browser.newPage({ viewport: { width: W, height: 800 }, deviceScaleFactor: 1 });
await page.setContent(html, { waitUntil: "load" });
await page.evaluate(() => document.fonts.ready);
const png = await page.screenshot({ fullPage: true });
await browser.close();
await sharp(png).webp({ quality: 82 }).toFile(dest);
console.log(`theme-grid: ${files.map((f) => path.parse(f).name).join(", ")} -> ${dest}`);
