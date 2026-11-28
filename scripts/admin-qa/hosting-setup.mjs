// Settings > Hosting with the same host setup (QA_SEED_HOSTING=1 QA_HOSTING_LIVE_ORIGIN= so the seed saves no
// live origin): Same host is chosen and Advanced is closed, the "Set up your web server" card has the Caddy,
// nginx, S3 + CDN and Apache / cPanel tabs, the config holds the proxy and the admin path, and the page has no console or page
// error. The QA instance has no site address, so the run sets https://example.com on Settings > General first
// (saving it also refreshes the running server's island snapshot, which the seed wrote straight to the
// database; that is what turns the island status into "via proxy") and puts the original back at the end.
// Screenshots of the card go to $SHOTS (default shots-out/hosting-setup). The clipboard is not read: headless
// denies it.
// QA_SEED_HOSTING=1 QA_HOSTING_LIVE_ORIGIN= scripts/admin-qa/run-local.sh hosting-setup.mjs
import path from "node:path";
import fs from "node:fs";
import { launch, newSession, base, admin, here } from "./lib.mjs";

const shots = process.env.SHOTS || path.join(here, "shots-out", "hosting-setup");
fs.mkdirSync(shots, { recursive: true });

const b = await launch();
let bad = 0;
const ok = (c, m) => {
  console.log(`${c ? "ok  " : "FAIL"} ${m}`);
  if (!c) bad++;
};
const page = await newSession(b, { theme: "light", width: 1440 });
const errors = [];
page.on("pageerror", (e) => errors.push(e.message));
page.on("console", (m) => m.type() === "error" && errors.push(m.text()));
const url = `${base}${admin}/settings/hosting`;
const general = `${base}${admin}/settings/general`;
const setAddress = async (value) => {
  await page.goto(general);
  await page.fill("#url", value);
  await page.getByRole("button", { name: "Save identity" }).click();
  await page.waitForLoadState("load");
};

await page.goto(general);
const originalAddress = await page.inputValue("#url");
await setAddress("https://example.com");

await page.goto(url);
await page.click("#hosting-form .hm-btn--primary");
await page.waitForLoadState("load");
ok(page.url() === url && !/There is a problem with this form/.test(await page.locator("main").innerText()), "saving the page unchanged needs no password and is accepted");

await page.goto(url);
ok(await page.locator('input[name="live_mode"][value="same"]').isChecked(), "Same host is chosen");
ok(!(await page.locator("details.hm-disclosure").first().evaluate((d) => d.open)), "Advanced: separate live origin is closed");
ok(await page.locator("#hosting-islands tbody tr").count() > 0, "the island list has rows");
ok((await page.locator("#hosting-islands tbody td[data-label=Status]").first().innerText()).trim() === "via proxy", "the island status reads via proxy");
ok((await page.locator("#hosting-setup").count()) === 1, "the setup card is shown");
const tabs = (await page.locator("#hosting-setup .hm-tabs__tab").allInnerTexts()).map((t) => t.trim());
ok(tabs.join("|") === "Caddy|nginx|S3 + CDN|Apache / cPanel", `tabs: ${tabs.join(" | ")}`);
const config = () => page.locator("#hosting-setup-config").inputValue();
let c = await config();
ok(c.includes("reverse_proxy") && c.includes(admin), "the Caddy config proxies to Hatamoto and passes the admin path");
await page.locator("#hosting-setup").screenshot({ path: path.join(shots, "caddy.png") });

await page.click('#hosting-setup .hm-tabs__tab:text("nginx")');
await page.waitForURL(/setup=nginx/);
c = await config();
ok(c.includes("proxy_pass") && c.includes(admin), "the nginx tab shows the nginx config");
await page.locator("#hosting-setup").screenshot({ path: path.join(shots, "nginx.png") });

await page.click('#hosting-setup .hm-tabs__tab:text("S3")');
await page.waitForURL(/setup=s3/);
ok((await page.locator("#hosting-setup-config").count()) === 0, "the S3 + CDN tab has no config");
ok(/A bucket cannot pass islands/.test(await page.locator("#hosting-setup").innerText()), "the S3 + CDN tab explains the bucket limit");
await page.locator("#hosting-setup").screenshot({ path: path.join(shots, "s3.png") });

await setAddress(originalAddress);
ok(errors.length === 0, `no console or page errors${errors.length ? ": " + errors.slice(0, 3).join(" | ") : ""}`);
await b.close();
console.log(bad ? `${bad} check(s) failed` : "all checks passed");
process.exit(bad ? 1 : 0);
