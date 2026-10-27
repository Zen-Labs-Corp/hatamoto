// Generates the fictional images the seed uploads: six avatars (initials on a soft
// gradient) and one featured image (abstract gradient). No photos, no real people.
// Usage: node gen-assets.mjs OUTDIR
import path from "node:path";
import sharp from "sharp";

const out = process.argv[2];
const people = [["ML", "#c7d2fe", "#818cf8"], ["AT", "#fde68a", "#f59e0b"], ["JO", "#a7f3d0", "#10b981"], ["SR", "#fbcfe8", "#ec4899"], ["DK", "#bae6fd", "#0ea5e9"], ["EV", "#ddd6fe", "#8b5cf6"]];
const svg = (w, h, body) => Buffer.from(`<svg xmlns="http://www.w3.org/2000/svg" width="${w}" height="${h}">${body}</svg>`);
for (const [i, [ini, a, b]] of people.entries()) {
  await sharp(svg(256, 256, `<defs><linearGradient id="g" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="${a}"/><stop offset="1" stop-color="${b}"/></linearGradient></defs><rect width="256" height="256" fill="url(#g)"/><text x="128" y="150" font-family="sans-serif" font-size="88" font-weight="700" fill="#fff" text-anchor="middle">${ini}</text>`))
    .png().toFile(path.join(out, `avatar-${i + 1}.png`));
}
await sharp(svg(1200, 630, `<defs><linearGradient id="g" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="#1e3a8a"/><stop offset="0.55" stop-color="#3b82f6"/><stop offset="1" stop-color="#f0abfc"/></linearGradient></defs><rect width="1200" height="630" fill="url(#g)"/><circle cx="930" cy="190" r="210" fill="#fff" fill-opacity="0.14"/><circle cx="1010" cy="470" r="130" fill="#fff" fill-opacity="0.12"/><path d="M0 520 C300 420 600 640 1200 450 L1200 630 L0 630Z" fill="#0b1b4d" fill-opacity="0.45"/>`))
  .png().toFile(path.join(out, "featured-release.png"));
