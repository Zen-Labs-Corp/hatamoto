// Settings > Hosting on the SFTP target (QA_SEED_HOSTING=1 QA_HOSTING_TARGET=sftp QA_HOSTING_LIVE_ORIGIN= so the
// seed saves an SFTP target with a trusted host key and no live origin): SFTP is the chosen target, its fields
// show and the folder and S3 fields are hidden, switching Sign in with to Private key swaps the password field
// for the key textarea, and ?setup=apache opens the Apache / cPanel tab with the .htaccess preview. The seed
// stores a placeholder credential that cannot be opened, so the page shows the "can no longer be read" help. The run sets https://example.com on Settings > General
// first (the setup card needs a site address) and puts the original back at the end. Screenshots of the target
// card and the setup card go to $SHOTS (default shots-out/hosting-sftp).
// QA_SEED_HOSTING=1 QA_HOSTING_TARGET=sftp QA_HOSTING_LIVE_ORIGIN= scripts/admin-qa/run-local.sh hosting-sftp.mjs
import path from "node:path";
import fs from "node:fs";
import { launch, newSession, base, admin, here } from "./lib.mjs";

const shots = process.env.SHOTS || path.join(here, "shots-out", "hosting-sftp");
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
ok(await page.locator('input[name="target"][value="sftp"]').isChecked(), "SFTP is the chosen target");
for (const f of ["sftp_host", "sftp_port", "sftp_user", "sftp_dir"]) {
  ok(await page.locator(`[name="${f}"]`).isVisible(), `${f} is visible`);
}
ok((await page.inputValue('[name="sftp_host"]')) === "ssh.example.com", "the saved host is shown");
ok(await page.locator('[name="sftp_password"]').isVisible(), "the password field is shown for password sign-in");
ok(!(await page.locator('[name="sftp_key"]').isVisible()), "the key textarea is hidden for password sign-in");
ok(!(await page.locator('[name="dir"]').isVisible()), "the folder field is hidden");
ok(!(await page.locator('[name="bucket"]').isVisible()), "the S3 bucket field is hidden");
ok(await page.locator('#hosting-sftp-htaccess').isChecked(), "Write .htaccess is on");
ok(/SHA256:qa-example/.test(await page.locator("#hosting-target-card").innerText()), "the trusted host key is shown");

await page.locator('input[name="sftp_auth"][value="key"]').check();
ok(await page.locator('[name="sftp_key"]').isVisible(), "the key textarea shows for private key sign-in");
ok(!(await page.locator('[name="sftp_password"]').isVisible()), "the password field is hidden for private key sign-in");
await page.locator("#hosting-target-card").screenshot({ path: path.join(shots, "sftp-fields.png") });

await page.goto(`${url}?setup=apache`);
const tabs = (await page.locator("#hosting-setup .hm-tabs__tab").allInnerTexts()).map((t) => t.trim());
ok(tabs.includes("Apache / cPanel"), `tabs: ${tabs.join(" | ")}`);
ok((await page.locator('#hosting-setup .hm-tabs__tab[aria-current="page"]').innerText()).trim() === "Apache / cPanel", "the Apache / cPanel tab is active");
ok((await page.locator("#hosting-setup-config").count()) === 1, "the .htaccess preview is shown");
await page.locator("#hosting-setup").screenshot({ path: path.join(shots, "apache-tab.png") });

await setAddress(originalAddress);
ok(errors.length === 0, `no console or page errors${errors.length ? ": " + errors.slice(0, 3).join(" | ") : ""}`);
await b.close();
console.log(bad ? `${bad} check(s) failed` : "all checks passed");
process.exit(bad ? 1 : 0);
