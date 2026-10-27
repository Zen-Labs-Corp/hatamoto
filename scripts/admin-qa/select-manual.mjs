// Scratch check for the styled Select (not part of the suite).
import { launch, newSession, base, resolvePath } from "./lib.mjs";
import path from "node:path";
const SHOTS = process.env.SHOTS || "/tmp";
const b = await launch();
let bad = 0;
const ok = (c, m) => { console.log(`${c ? "ok  " : "FAIL"} ${m}`); if (!c) bad++; };
const trig = (p, sel) => p.locator(`${sel} .hm-select__trigger`).first();

for (const theme of ["light", "dark"]) {
  const p = await newSession(b, { theme, width: 1440 });
  if (theme === "light") {
    // 1. entities list status filter fires the htmx request on pick, by keyboard only.
    await p.goto(`${base}/admin/entities`);
    const t = p.locator(".hm-filterbar .hm-select__trigger").first();
    ok((await t.count()) === 1, "filterbar select is enhanced");
    const sel = p.locator(".hm-filterbar select").first();
    const before = await sel.inputValue();
    await t.focus();
    const req = p.waitForRequest((r) => r.headers()["hx-request"] === "true" && r.url().includes("/admin/entities"), { timeout: 5000 }).catch(() => null);
    await p.keyboard.press("ArrowDown");
    ok((await t.getAttribute("aria-expanded")) === "true", "ArrowDown opens the list");
    ok((await t.getAttribute("aria-activedescendant")) !== null, "active descendant set");
    await p.keyboard.press("ArrowDown");
    await p.keyboard.press("Enter");
    const r = await req;
    ok(!!r, `pick triggered the htmx request ${r?.url()}`);
    await p.waitForTimeout(600);
    const after = await p.locator(".hm-filterbar select").first().inputValue();
    ok(after !== before, `native value changed '${before}' -> '${after}' (after swap)`);
    ok((await p.locator(".hm-filterbar .hm-select__trigger").count()) >= 1, "filterbar re-enhanced after outerHTML swap");
    const fid = await p.evaluate(() => document.activeElement?.className || document.activeElement?.tagName);
    ok(String(fid).includes("hm-select__trigger"), `focus stays on the filter trigger after the swap (got ${fid})`);

    // 2. parent select: no seeded type has a nested URL pattern, so mount the same markup the
    // entity edit form uses (search input, #parent-select, innerHTML swap) on a live page.
    await p.goto(`${base}${await resolvePath(p, "/admin/entities/{post}/edit")}`);
    await p.evaluate(async () => {
      const html = await (await fetch("/admin/entities/parent-options?parent_q=", { headers: { "HX-Request": "true", "HX-Target": "parent-select" } })).text();
      document.body.insertAdjacentHTML("beforeend", `<form id="pf" style="position:fixed;top:0;left:0;z-index:9999;background:#fff"><label for="parent">Parent</label><input type="search" id="parent_q" name="parent_q" hx-get="/admin/entities/parent-options" hx-trigger="input changed delay:300ms" hx-target="#parent-select" hx-swap="innerHTML" hx-include="#parent, #parent-exclude"><input type="hidden" id="parent-exclude" name="exclude" value=""><div id="parent-select">${html}</div></form>`);
      window.htmx.process(document.getElementById("pf"));
      document.body.dispatchEvent(new CustomEvent("htmx:afterSettle", { bubbles: true }));
    });
    ok((await p.locator("#parent-select .hm-select__trigger").count()) === 1, "parent select enhanced on first render");
    await p.fill("#parent_q", "QA");
    await p.waitForResponse((r) => r.url().includes("parent-options") && r.url().includes("QA"), { timeout: 5000 }).catch(() => {});
    await p.waitForTimeout(500);
    const pt = p.locator("#parent-select .hm-select__trigger");
    ok((await pt.count()) === 1, "parent select re-enhanced after the swap");
    await pt.focus();
    await p.keyboard.press("Enter");
    const n = await p.locator("#parent-select .hm-select__option").count();
    ok(n > 1, `parent list built from the swapped options (${n})`);
    await p.keyboard.press("ArrowDown");
    await p.keyboard.press("Enter");
    const v = await p.locator("#parent").inputValue();
    const txt = await p.locator("#parent-select .hm-select__value").textContent();
    ok(v !== "" && txt.trim() !== "", `parent value '${v.slice(0, 8)}' label '${txt.trim()}'`);
    await p.evaluate(() => document.getElementById("pf").remove());

    // 3. required select blocks submit and focuses the trigger.
    await p.evaluate(() => {
      document.body.insertAdjacentHTML("beforeend", '<form id="tf" style="position:fixed;top:0;left:0;z-index:9999;background:#fff"><label for="zz">Zed</label><div class="hm-select" data-hm-select><select class="hm-field__control" id="zz" name="zz" required><option value="">Pick one</option><option value="a">A · hint</option><option value="b" disabled>B off</option><option value="c">Cat</option></select></div><button type="submit" id="tfs">go</button></form>');
      document.body.dispatchEvent(new CustomEvent("htmx:afterSettle", { bubbles: true }));
    });
    let submitted = false;
    await p.evaluate(() => document.getElementById("tf").addEventListener("submit", (e) => { e.preventDefault(); window.__sub = true; }));
    await p.click("#tfs");
    submitted = await p.evaluate(() => !!window.__sub);
    ok(!submitted, "empty required select blocks submit");
    ok(await p.evaluate(() => document.activeElement?.id === "zz-trigger"), "focus lands on the trigger");
    // 4. keyboard-only behaviour on the scratch select.
    const z = p.locator("#zz-trigger");
    await p.keyboard.press("Enter");
    ok((await z.getAttribute("aria-expanded")) === "true", "Enter opens");
    await p.keyboard.press("End");
    ok((await p.locator("#zz-list [data-active]").textContent()).startsWith("Cat"), "End goes to the last enabled option (skips disabled B? last is Cat)");
    await p.keyboard.press("ArrowUp");
    ok((await p.locator("#zz-list [data-active]").textContent()).startsWith("A"), "ArrowUp skips the disabled option");
    await p.keyboard.press("Home");
    ok((await p.locator("#zz-list [data-active]").textContent()).startsWith("Pick"), "Home goes to the first");
    await p.keyboard.type("cat");
    ok((await p.locator("#zz-list [data-active]").textContent()).startsWith("Cat"), "typeahead cat -> Cat");
    await p.keyboard.press("Escape");
    ok((await z.getAttribute("aria-expanded")) === "false" && (await p.evaluate(() => document.activeElement.id)) === "zz-trigger", "Esc closes and keeps focus on the trigger");
    await p.waitForTimeout(800);
    await p.keyboard.press("Space");
    await p.keyboard.type("c");
    await p.keyboard.press("Tab");
    ok((await p.locator("#zz").inputValue()) === "c", "Tab commits the highlighted option");
    let changed = 0;
    await p.evaluate(() => { document.getElementById("zz").addEventListener("change", () => (window.__ch = (window.__ch || 0) + 1)); });
    await p.locator("#zz-trigger").click();
    await p.locator("#zz-opt-1").click();
    ok((await p.locator("#zz").inputValue()) === "a" && (await p.evaluate(() => window.__ch)) === 1, "mouse pick sets value and fires one change");
    ok((await p.locator("#zz-trigger").innerText()).trim() === "A", "trigger label shows the name without the hint");
    await p.evaluate(() => document.getElementById("tf").remove());
  }

  // 5. schema edit: kind select shows/hides data-hm-for fields.
  if (process.env.SCHEMA) {
    // No seeded type is editable: start a new one and add a field (htmx swap, so the new selects also prove re-init).
    await p.goto(`${base}/admin/settings/types/new`);
    await p.getByRole("button", { name: "Add field" }).click();
    await p.waitForSelector("select[data-hm-kind]", { state: "attached" });
    await p.waitForTimeout(300);
    await p.evaluate(() => document.querySelectorAll("details").forEach((d) => (d.open = true)));
    const kind = p.locator("select[data-hm-kind]:enabled").first();
    if (await kind.count()) {
      const t = p.locator("[data-hm-select]:has(select[data-hm-kind]:enabled) .hm-select__trigger").first();
      const opts = await kind.locator("option").evaluateAll((os) => os.map((o) => o.value));
      const seen = {};
      for (const v of opts) {
        const i = opts.indexOf(v);
        await t.focus(); await p.keyboard.press("Enter"); await p.locator("[data-hm-select]:has(select[data-hm-kind]:enabled) .hm-select__option").nth(i).click();
        seen[v] = await p.evaluate(() => [...document.querySelectorAll("[data-hm-for]")].filter((e) => !e.hidden && e.offsetParent !== null).map((e) => e.dataset.hmFor).join("|"));
      }
      console.log("kind -> visible data-hm-for", JSON.stringify(seen));
      ok(new Set(Object.values(seen)).size > 1, "kind select changes which fields show");
    } else console.log("note: no kind select");
  }

  // screenshots: an open Select in the entity edit form (status).
  for (const w of [1440, 390]) {
    if (theme === "dark" && w === 390) continue;
    const q = await newSession(b, { theme, width: w });
    await q.goto(`${base}${await resolvePath(q, "/admin/entities/{post}")}`);
    const t = q.locator("[data-hm-select] .hm-select__trigger").first();
    await t.scrollIntoViewIfNeeded();
    await t.click();
    await q.waitForTimeout(150);
    await q.screenshot({ path: path.join(SHOTS, `select-${w}-${theme}.png`) });
    await q.context().close();
  }
  await p.context().close();
}
await b.close();
console.log(bad ? `${bad} FAILED` : "all ok");
process.exit(bad ? 1 : 0);
