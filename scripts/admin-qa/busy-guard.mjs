// A plain POST form must not stay locked after a pending submit that never navigates
// (browser Stop, beforeunload "Stay"). Run: scripts/admin-qa/run-local.sh busy-guard.mjs
import { launch, newSession, base } from "./lib.mjs";
const b = await launch();
let bad = 0;
const ok = (c, m) => { console.log(`${c ? "ok  " : "FAIL"} ${m}`); if (!c) bad++; };
const p = await newSession(b, { theme: "light", width: 1440 });
await p.goto(`${base}/admin/entities`);
await p.evaluate(() => {
  document.body.insertAdjacentHTML("beforeend", '<form id="bg" method="post" action="/__busy-guard"><button type="submit" id="bgb">Save</button></form>'
    + '<form id="bgu" method="post" enctype="multipart/form-data" action="/__busy-guard"><button type="submit" id="bgub">Upload</button></form>');
  // Registered after the guard: counts submits the guard let through and cancels them, which is
  // what a beforeunload "Stay" or the browser's Stop leaves behind (page alive, no navigation).
  window.__posts = 0;
  document.addEventListener("submit", (e) => { if (!e.defaultPrevented) { window.__posts++; e.preventDefault(); } });
});
const posts = () => p.evaluate(() => window.__posts);
const submit = (id = "bgb") => p.evaluate((i) => document.getElementById(i).click(), id);
await submit();
await p.waitForTimeout(100);
ok((await posts()) === 1, "first submit posts");
await submit();
await p.waitForTimeout(100);
ok((await posts()) === 1, "second submit while pending is swallowed");
await p.waitForTimeout(10500);
await submit();
await p.waitForTimeout(100);
ok((await posts()) === 2, `form unlocks after the guard window (posts=${await posts()})`);
// A multipart upload is never released by the time-box (a slow transfer must not be resubmitted).
await p.waitForTimeout(10500); // let the re-armed timer of the plain form expire first: it clears every locked button
const before = await posts();
await submit("bgub");
await p.waitForTimeout(100);
ok((await posts()) === before + 1, "upload form posts once");
await p.waitForTimeout(10500);
await submit("bgub");
await p.waitForTimeout(100);
ok((await posts()) === before + 1, `upload form stays locked past the guard window (posts=${await posts()})`);
await b.close();
process.exit(bad ? 1 : 0);
