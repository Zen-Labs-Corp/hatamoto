// Settings > Kits by click paths, with the registry switched off (run-local.sh sets HATAMOTO_REGISTRY_URL=off
// for this script): the list says "The registry is unavailable." and offers no Review link, Kits is in the
// Settings sidebar, a review address answers a readable error page with a way back instead of a crash, the
// editor gets 403, and the list does not overflow on a 390px viewport. The review and apply screens need a
// registry that lists a kit; the server's registry client refuses loopback and private addresses, so a
// fixture registry cannot run here and those screens stay with their Go tests and goldens.
// Screenshots go to shots-out/kits/.
// node scripts/admin-qa/kits-admin.mjs   Exit 1 on a failed check, a console error or a page error.
import fs from "node:fs";
import path from "node:path";
import { launch, newSession, base, admin, here } from "./lib.mjs";

const list = `${admin}/settings/kits`;
const shots = process.env.SHOTS || path.join(here, "shots-out", "kits");
fs.mkdirSync(shots, { recursive: true });

const b = await launch();
let bad = 0;
const ok = (c, m) => {
  console.log(`${c ? "ok  " : "FAIL"} ${m}`);
  if (!c) bad++;
};
const norm = (t) => t.replace(/\s+/g, " ").trim();
const errors = [];
const watch = (p) => {
  p.on("pageerror", (e) => errors.push(e.message));
  p.on("console", (m) => {
    // The refused pages (403, 502) are expected: the browser logs their status as a console error.
    if (m.type() === "error" && !/status of (403|502)/.test(m.text())) errors.push(m.text());
  });
};
const text = (p) => p.locator("body").innerText().then(norm);
const shot = (p, name) => p.screenshot({ path: path.join(shots, `${name}.png`), fullPage: true });

try {
  // ============ 1. the list, registry off ============
  const page = await newSession(b, { width: 1440 });
  watch(page);
  await page.goto(base + list);
  let t = await text(page);
  ok(t.includes("Applied") && t.includes("No kit applied yet"), "list: the Applied card says nothing is applied");
  ok(t.includes("Available from the registry") && t.includes("The registry is unavailable."), "list: the registry card says it is unavailable");
  ok((await page.getByRole("link", { name: "Review" }).count()) === 0, "list: no Review link without a registry");
  ok((await page.locator("table").count()) === 0, "list: no table with row actions");
  ok((await page.locator(`a[href$="${list}"]`).count()) > 0, "list: Kits is in the Settings sidebar");
  await shot(page, "list-registry-off");

  // ============ 2. a review address without a registry ============
  const res = await page.goto(`${base}${list}/community`);
  t = await text(page);
  ok(res.status() === 502 && t.includes("The kit could not be read"), "review: a readable error page, not a crash");
  ok((await page.getByRole("link", { name: "Back to kits" }).count()) === 1, "review: the error page links back to the kits");
  await shot(page, "review-unavailable");

  // ============ 3. the phone layout ============
  const phone = await newSession(b, { width: 390 });
  watch(phone);
  await phone.goto(base + list);
  const overflow = await phone.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  ok(overflow <= 0, "phone: the list does not overflow sideways");
  await shot(phone, "list-phone");

  // ============ 4. the editor is refused ============
  const ed = await newSession(b, { signedIn: false });
  watch(ed);
  await ed.goto(`${base}${admin}/login`);
  await ed.fill("#email", process.env.QA_EDITOR_EMAIL || "editor@qa.test");
  await ed.fill("#password", process.env.QA_PASSWORD || "qa-password-123");
  await Promise.all([ed.waitForURL((u) => !u.pathname.endsWith(`${admin}/login`)), ed.click(`form[action="${admin}/login"] button[type=submit]`)]);
  ok((await ed.goto(base + list)).status() === 403, "editor: Settings > Kits answers 403");
  ok((await ed.goto(`${base}${list}/community`)).status() === 403, "editor: a kit review answers 403");
} finally {
  await b.close();
}
ok(errors.length === 0, `no console or page errors${errors.length ? ": " + errors.join(" | ") : ""}`);
process.exit(bad ? 1 : 0);
