// The plugin/theme details panel: one request per card per page (kept until reload), a modified click is a
// plain link (new tab, no dialog), a failed request falls back to the full page, focus moves in and back.
// The local QA server has no registry, so there are no cards: one is injected with the template's attributes
// (pages/plugins/browse.html, browse-card) and its /about is answered from the test, so the panel's own code
// is what is exercised.
// node scripts/admin-qa/panels-plugin.mjs   (run-local.sh panels-plugin.mjs)  Exit 1 on a failed check or error.
import { launch, newSession, base, admin } from "./lib.mjs";

const b = await launch();
let bad = 0;
const ok = (c, m) => {
  console.log(`${c ? "ok  " : "FAIL"} ${m}`);
  if (!c) bad++;
};

const page = await newSession(b, { width: 1440 });
const errors = [];
page.on("pageerror", (e) => errors.push(e.message));
page.on("console", (m) => {
  if (m.type() === "error") errors.push(m.text());
});

const about = `${admin}/plugins/qa-panel/about`;
let hits = 0;
let fail = false;
await page.route(`**${about}`, (route) => {
  if (route.request().headers()["hx-request"]) {
    hits++;
    return route.fulfill(fail ? { status: 500, body: "" } : { status: 200, contentType: "text/html", body: '<h3>QA panel</h3><p><a href="#x">More</a></p>' });
  }
  return route.fulfill({ status: 200, contentType: "text/html", body: "<!doctype html><title>QA full page</title><p>full</p>" });
});

const inject = async () => {
  await page.goto(`${base}${admin}/plugins/browse`);
  await page.waitForSelector("#plugin-panel", { state: "attached" });
  await page.evaluate((href) => {
    const a = Object.assign(document.createElement("a"), { className: "hm-plugincard", href, textContent: "QA panel" });
    // the attribute list of pages/plugins/browse.html "browse-card"
    for (const [k, v] of Object.entries({ "data-hm-open": "plugin-panel", "hx-get": href, "hx-target": "#plugin-panel-body", "hx-swap": "innerHTML", "hx-sync": "#plugin-panel-body:replace", "hx-indicator": "#plugin-panel-body" })) a.setAttribute(k, v);
    document.querySelector("main").prepend(a);
    window.htmx.process(a);
  }, about);
  return page.locator(`a[data-hm-open="plugin-panel"][href$="${about}"]`);
};
const isOpen = () => page.evaluate(() => document.getElementById("plugin-panel").open);

let card = await inject();
await card.click();
await page.waitForSelector("#plugin-panel[open] #plugin-panel-body h3");
ok((await page.locator("#plugin-panel-body").innerText()).includes("QA panel"), "click: the panel opens with the fragment");
ok(await page.evaluate(() => document.getElementById("plugin-panel").contains(document.activeElement)), "focus is inside the panel after opening");
await page.keyboard.press("Escape");
await page.waitForFunction(() => !document.getElementById("plugin-panel").open);
ok(await card.evaluate((el) => el === document.activeElement), "Esc: focus returns to the card");
await card.click();
await page.waitForSelector("#plugin-panel[open] #plugin-panel-body h3");
ok((await page.locator("#plugin-panel-body").innerText()).includes("QA panel"), "second click: the panel opens with the content");
ok(hits === 1, `second open sends no request (${hits} request)`);
ok(await page.evaluate(() => !document.getElementById("plugin-panel-body").hasAttribute("aria-busy")), "second open: the panel body is not left aria-busy");
await page.keyboard.press("Escape");
await page.waitForFunction(() => !document.getElementById("plugin-panel").open);

const [tab] = await Promise.all([page.context().waitForEvent("page"), card.click({ modifiers: ["Control"] })]);
ok(!(await isOpen()), "Ctrl-click: the panel stays closed on this page");
ok(!!tab, "Ctrl-click: the link opens in a new page");
await tab.close();

fail = true;
hits = 0;
card = await inject();
await card.click();
await page.waitForURL(`**${about}`);
ok(((await page.locator("body").innerText()) || "").includes("full"), "a failed request falls back to the full page");
ok(hits === 1, `the failed request was the panel's (${hits})`);

// What the 500 step logs by design: the browser's resource line, htmx's own report of the 500 and the
// view transition it abandons when the page navigates away.
const expected = ["Failed to load resource", `Response Status Error Code 500 from ${about}`, "Transition was aborted because of invalid state"];
const unexpected = errors.filter((e) => !expected.some((x) => e.includes(x)));
ok(unexpected.length === 0, `no console or page errors${unexpected.length ? ": " + unexpected.slice(0, 3).join(" | ") : ""}`);
await b.close();
console.log(bad ? `${bad} check(s) failed` : "all checks passed");
process.exit(bad ? 1 : 0);
