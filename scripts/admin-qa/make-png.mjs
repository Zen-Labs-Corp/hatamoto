// Writes a solid-colour PNG: node make-png.mjs WIDTH HEIGHT OUT [R G B]. Test tooling for seed.sh (no dependencies).
import fs from "node:fs";
import zlib from "node:zlib";

const [w, h, out, r = 40, g = 110, b = 200] = process.argv.slice(2);
const W = +w, H = +h;
const chunk = (type, data) => {
  const body = Buffer.concat([Buffer.from(type), data]);
  const len = Buffer.alloc(4); len.writeUInt32BE(data.length);
  const crc = Buffer.alloc(4); crc.writeUInt32BE(zlib.crc32(body));
  return Buffer.concat([len, body, crc]);
};
const ihdr = Buffer.alloc(13); ihdr.writeUInt32BE(W, 0); ihdr.writeUInt32BE(H, 4); ihdr[8] = 8; ihdr[9] = 2;
const row = Buffer.concat([Buffer.from([0]), Buffer.from(Array.from({ length: W }, () => [+r, +g, +b]).flat())]);
const raw = Buffer.concat(Array.from({ length: H }, () => row));
fs.writeFileSync(out, Buffer.concat([Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]), chunk("IHDR", ihdr), chunk("IDAT", zlib.deflateSync(raw)), chunk("IEND", Buffer.alloc(0))]));
