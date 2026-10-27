// S-1: every well-known scanner path answers byte for byte what a random unknown path answers
// (status, header names in order, header values, body), with and without an owner session.
// Black box over raw HTTP against the server pinned to a RANDOM admin address. Only values that
// differ per request are masked: Date, X-Request-Id, nonces and the page's own URL (canonical,
// og:url), which the server derives from the path asked for. Sends no proxy headers.
import { launch, newOwner, base, raw, record, finish } from "./lib.mjs";
import { scannerPaths, MIN_PATHS } from "./scanner-paths.mjs";

const paths = scannerPaths();
const mask = (r, p) => ({
  status: r.status,
  headers: r.raw.map((v, i) => (i % 2 === 0 ? v.toLowerCase() : /^(date|x-request-id)$/i.test(r.raw[i - 1]) ? "<varies>" : v)),
  body: r.body.replaceAll(p, "<path>").replace(/(rel="canonical" href|property="og:url" content)="[^"]*"/g, '$1="<url>"').replace(/nonce="[^"]*"/g, 'nonce=""'),
});
const same = (a, b) => JSON.stringify(a) === JSON.stringify(b);
const rnd = () => "/zz-" + Math.random().toString(36).slice(2, 12);

const browser = await launch();
const { context } = await newOwner(browser);
const session = (await context.cookies(base)).map((c) => `${c.name}=${c.value}`).join("; ");
await browser.close();
if (!session) throw new Error("no session cookie to send");

async function compare(p, method, headers) {
  const r = rnd();
  const probe = mask(await raw(p, { method, headers }), p);
  const ref = mask(await raw(r, { method, headers }), r);
  return same(probe, ref) ? null : `${method} ${p}`;
}

const states = { "no cookie": {}, "owner session cookie": { cookie: session } };
const sample = paths.filter((_, i) => i % Math.ceil(paths.length / 20) === 0);
let sent = 0;
for (const [label, headers] of Object.entries(states)) {
  const failures = [];
  for (const p of paths) {
    const f = await compare(p, "GET", headers);
    sent++;
    if (f) failures.push(f);
  }
  for (const method of ["HEAD", "POST", "OPTIONS"]) {
    for (const p of sample) {
      const f = await compare(p, method, headers);
      sent++;
      if (f) failures.push(f);
    }
  }
  for (const f of failures) console.log(`  differs: ${f} (${label})`);
  // Control: the comparator can see a difference (robots.txt is a real page, not a 404).
  const robots = mask(await raw("/robots.txt", { headers }), "/robots.txt");
  const rp = rnd();
  const canSee = !same(robots, mask(await raw(rp, { headers }), rp));
  record("S-1", `${paths.length} scanner paths answer the random-path 404 (${label})`, {
    ok: failures.length === 0 && paths.length >= MIN_PATHS,
    control: canSee,
    detail: `GET ${paths.length} + HEAD/POST/OPTIONS ${sample.length} each, differing=${failures.length}`,
  });
}
console.log(`scanner: ${sent} requests`);
finish();
