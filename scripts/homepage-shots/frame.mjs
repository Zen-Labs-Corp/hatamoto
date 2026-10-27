// Window frame for the homepage shots: a light title bar (three dots, URL pill)
// and a hairline border, composed with sharp into one WebP. No baked shadow: the
// homepage slot CSS adds radius and shadow already.
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import sharp from "sharp";

export const W = 2880, H = 1800, BAR = 86, INNER_H = H - BAR;
const here = path.dirname(fileURLToPath(import.meta.url));
const fontsDir = path.resolve(here, "../../backend/internal/render/themes/default/assets/fonts");
export const fontFile = (name) => fs.readFileSync(path.join(fontsDir, name)).toString("base64");

// renderBar draws the 2880x86 title bar in Chrome, so it uses the same Geist as the shots.
export async function renderBar(browser, urlLabel) {
  const ctx = await browser.newContext({ viewport: { width: W / 2, height: BAR / 2 }, deviceScaleFactor: 2 });
  const page = await ctx.newPage();
  const esc = urlLabel.replace(/[&<>]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;" })[c]);
  await page.setContent(`<!doctype html><style>
@font-face{font-family:Geist;src:url(data:font/woff2;base64,${fontFile("Geist-Variable.woff2")});font-weight:100 900}
*{box-sizing:border-box}html,body{margin:0;height:100%}
body{background:#f4f4f2;border-bottom:1px solid #e4e4e0;display:flex;align-items:center;padding:0 18px;font-family:Geist,sans-serif}
.d{display:flex;gap:8px;width:90px}.d i{width:12px;height:12px;border-radius:50%;display:block}
.p{position:absolute;left:50%;transform:translateX(-50%);width:420px;height:26px;border-radius:7px;background:#fff;border:1px solid #e4e4e0;color:#5c5c58;font-size:12.5px;display:flex;align-items:center;justify-content:center;gap:6px}
</style><div class="d"><i style="background:#ff5f57"></i><i style="background:#febc2e"></i><i style="background:#28c840"></i></div><div class="p">${esc}</div>`);
  await page.evaluate(() => document.fonts.ready);
  const png = await page.screenshot();
  await ctx.close();
  return png;
}

// frame composes bar + content into the final WebP and checks the size.
export async function frame(barPng, contentPng, dest, bg = "#ffffff") {
  // contain: a clip that could not grow to the frame's aspect (page smaller than it) is centred on the page colour.
  const content = await sharp(contentPng).resize(W, INNER_H, { fit: "contain", background: bg }).png().toBuffer();
  const border = Buffer.from(`<svg xmlns="http://www.w3.org/2000/svg" width="${W}" height="${H}"><rect x="1" y="1" width="${W - 2}" height="${H - 2}" fill="none" stroke="#d9d9d4" stroke-width="2"/></svg>`);
  await sharp({ create: { width: W, height: H, channels: 3, background: "#ffffff" } })
    .composite([{ input: barPng, top: 0, left: 0 }, { input: content, top: BAR, left: 0 }, { input: border, top: 0, left: 0 }])
    .webp({ quality: 82 })
    .toFile(dest);
  const m = await sharp(dest).metadata();
  if (m.width !== W || m.height !== H || m.format !== "webp") throw new Error(`${dest}: got ${m.width}x${m.height} ${m.format}, want ${W}x${H} webp`);
}
