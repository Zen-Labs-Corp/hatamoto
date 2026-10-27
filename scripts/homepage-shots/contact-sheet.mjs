// Writes contact-sheet.png (all shots in a 4 column grid with slot labels) and tiles-500.png
// (the tile slots at the 500 px width they have on the homepage) next to the shots.
// Usage: node contact-sheet.mjs OUTDIR
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import sharp from "sharp";

const here = path.dirname(fileURLToPath(import.meta.url));
const out = path.resolve(process.argv[2] || "scripts/homepage-shots/out");
const shots = JSON.parse(fs.readFileSync(path.join(here, "shots.json"), "utf8"));
const label = (w, h, text) => Buffer.from(`<svg xmlns="http://www.w3.org/2000/svg" width="${w}" height="${h}"><rect width="${w}" height="${h}" fill="#ffffff"/><text x="8" y="${h - 12}" font-family="sans-serif" font-size="18" fill="#222">${text}</text></svg>`);

// sheet lays shots out in a grid of `cols` columns, each cell tw wide (16:10) with a label strip.
async function sheet(list, cols, tw, dest) {
  const th = Math.round((tw * 1800) / 2880), lh = 36, gap = 12;
  const rows = Math.ceil(list.length / cols);
  const layers = [];
  for (const [i, s] of list.entries()) {
    const x = gap + (i % cols) * (tw + gap), y = gap + Math.floor(i / cols) * (th + lh + gap);
    const file = path.join(out, s.file);
    if (!fs.existsSync(file)) throw new Error(`missing ${s.file}`);
    layers.push({ input: await sharp(file).resize(tw, th).png().toBuffer(), left: x, top: y + lh });
    layers.push({ input: label(tw, lh, `${i + 1}. ${s.slot}  (${s.file})`), left: x, top: y });
  }
  await sharp({ create: { width: gap + cols * (tw + gap), height: gap + rows * (th + lh + gap), channels: 3, background: "#d9d9d4" } })
    .composite(layers).png().toFile(dest);
}

await sheet(shots, 4, 640, path.join(out, "contact-sheet.png"));
await sheet(shots.filter((s) => s.slot.startsWith("hm-n-t-")), 3, 500, path.join(out, "tiles-500.png"));
console.log(`contact-sheet: ${shots.length} shots in ${out}`);
