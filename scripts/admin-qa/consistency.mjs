// Chrome consistency check: computed-style rules UI-D1..UI-D14 (docs/ADMIN_UI_RULES.md)
// on every screen in urls.mjs at 1440 and 390, light and dark, for the owner and
// the editor. Writes <name>-<width>-<theme>.png to OUT and consistency.json to the
// results dir. Exits 1 on any violation or any screen that does not answer.
// BASE=http://127.0.0.1:8191 node scripts/admin-qa/consistency.mjs [screen ...]
// node scripts/admin-qa/consistency.mjs --self-test   (no server: plants one violation
// per rule in a static page and fails unless every rule id fires)
// A run that scans nothing, or names an unknown screen, fails.
// QA_OUT: screenshot dir (default scripts/admin-qa/results/consistency/, gitignored).
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { launch, newSession, login, visit, writeResult, base, here, strict } from "./lib.mjs";
import { screens as allScreens } from "./urls.mjs";

// The component contracts (UI-D14): the one spec the Go test TestDesignContract also reads.
const SPEC = JSON.parse(fs.readFileSync(fileURLToPath(new URL("../../backend/web/admin/components.json", import.meta.url)), "utf8"));
const OUT = process.env.QA_OUT || path.join(here, "results", "consistency");
fs.mkdirSync(OUT, { recursive: true });
const selfTest = process.argv.includes("--self-test");
const only = process.argv.slice(2).filter((a) => a !== "--self-test");
const EDITOR_EMAIL = process.env.QA_EDITOR_EMAIL || "editor@qa.test";
const PASSWORD = process.env.QA_PASSWORD || "qa-password-123";
const NEWCOMER_EMAIL = process.env.QA_NEWCOMER_EMAIL || "newcomer@qa.test";

// Screens that may carry the sticky flat footer (.hm-savebar): entity edit,
// entity model edit, form editor, menu editor (approved, UI-02; plugin settings is not seedable, see urls.mjs).
const STICKY_SCREENS = new Set(["entity-details-post", "entity-edit-post", "entity-edit-page", "settings-types-new", "settings-types-edit", "settings-forms-new", "settings-forms-edit", "theme-menus-edit"]);

// Runs in the page. Returns [rule, selector, message, value] tuples.
function check({ sticky, list, spec }) {
  const out = [];
  const POPUP = ".hm-dialog, .hm-menu, .hm-popover-panel, .hm-slash, .hm-bubble, .hm-tokens__list, .hm-searchselect__list, .hm-select__list, .hm-pe__rich, .hm-toast, .hm-flash, [popover]";
  // Named exception: editor top bar (blogeditor.css, UI-06 allowlist).
  const SHADOW_OK = ".hm-ed__bar";
  const BTN_OK = ".hm-filter-chip, .hm-bubble, .hm-ed__bar";
  // Multi-pick lists and consent gates stay checkboxes (docs/ADMIN_UI_RULES.md rule 4).
  const MULTI = 'table, .hm-mediatile, [data-multipick], .hm-fieldrow__clear, [name="scopes"], [name="events"], [name="understand"], [name^="perm"], [name="types"], [name="terms"], [name="scripts"], [name="tier"], [name="cat"]';
  const visible = (el) => {
    if (!el.getClientRects().length) return false;
    const cs = getComputedStyle(el);
    return cs.visibility !== "hidden" && cs.display !== "none" && !el.closest("[hidden], dialog:not([open]), [popover]:not(:popover-open)");
  };
  const sel = (el) => {
    let s = el.tagName.toLowerCase();
    if (el.getAttribute("id")) return s + "#" + el.getAttribute("id"); // not el.id: a form with a field named id returns that field
    const c = [...el.classList].slice(0, 2).join(".");
    if (c) s += "." + c;
    const n = el.getAttribute("name");
    if (n) s += `[name="${n}"]`;
    const t = (el.textContent || "").trim().replace(/\s+/g, " ").slice(0, 24);
    return t && el.matches(".hm-btn") ? `${s} "${t}"` : s;
  };
  const px = (v) => Math.round(parseFloat(v) * 100) / 100;

  // UI-D1, UI-D5 (footers), UI-D6
  for (const f of document.querySelectorAll(".hm-card__foot, .hm-savebar, .hm-dialog__foot")) {
    if (!visible(f)) continue;
    const cs = getComputedStyle(f);
    const s = sel(f);
    const isDialog = f.matches(".hm-dialog__foot");
    const plain = f.matches(".hm-card__foot--plain");
    const btns = [...f.querySelectorAll(".hm-btn")].filter(visible);
    if (!isDialog) {
      if (cs.display !== "flex") out.push(["UI-D1", s, "display is not flex", cs.display]);
      if (cs.justifyContent !== "flex-end") out.push(["UI-D1", s, "not right-aligned", cs.justifyContent]);
      if (cs.boxShadow !== "none") out.push(["UI-D1", s, "footer has a shadow", cs.boxShadow]);
      if (px(cs.borderTopLeftRadius) !== 0) out.push(["UI-D1", s, "footer has a radius", cs.borderTopLeftRadius]);
      if (!plain && px(cs.borderTopWidth) !== 1) out.push(["UI-D1", s, "top border is not 1px", cs.borderTopWidth]);
      if (f.matches(".hm-savebar") && cs.position === "sticky" && !sticky) out.push(["UI-D1", s, "sticky footer outside the editor screens", cs.position]);
      if (f.matches(".hm-card__foot") && cs.position === "sticky") out.push(["UI-D1", s, "card footer is sticky", cs.position]);
      const par = f.parentElement;
      if (par && btns.length) {
        const pr = par.getBoundingClientRect(), pcs = getComputedStyle(par);
        const edge = pr.right - px(pcs.paddingRight) - px(pcs.borderRightWidth);
        const fr = f.getBoundingClientRect();
        const last = btns[btns.length - 1].getBoundingClientRect();
        // The footer sits at the parent's content edge, or flush with a padded card.
        if (Math.abs(fr.right - pr.right) > 1 && Math.abs(fr.right - edge) > 1) out.push(["UI-D1", s, "right edge off the parent edge", `${fr.right} vs ${edge}`]);
        if (Math.abs(last.right - (fr.right - px(cs.paddingRight) - px(cs.borderRightWidth))) > 1 && cs.justifyContent === "flex-end" && !f.querySelector(".hm-savebar__aside, .hm-card__foot-note"))
          out.push(["UI-D1", s, "last button is not at the footer's right edge", `${last.right} vs ${fr.right - px(cs.paddingRight)}`]);
      }
    }
    if (btns.length > 1) {
      const l = btns[btns.length - 1];
      const hasPrim = btns.some((b) => b.matches(".hm-btn--primary, .hm-btn--danger"));
      if (hasPrim && !l.matches(".hm-btn--primary, .hm-btn--danger")) out.push([isDialog ? "UI-D5" : "UI-D1", sel(l), "last button is not primary or danger", l.className]);
      const ll = l.getBoundingClientRect().left;
      for (const b of btns.slice(0, -1)) {
        if (b.matches(".hm-btn--primary, .hm-btn--danger") && l.matches(".hm-btn--primary, .hm-btn--danger") === false) continue;
        const r = b.getBoundingClientRect();
        if (r.top < l.getBoundingClientRect().bottom && r.bottom > l.getBoundingClientRect().top && r.left >= ll) out.push(["UI-D1", sel(b), "sibling is not left of the last button", `${r.left} >= ${ll}`]);
      }
    }
    // UI-D12: on an editor savebar Delete is the far-left control, inside .hm-savebar__aside.
    // No Delete (new pages) is fine; a Delete outside the aside, or not at the content-box left, is not.
    if (f.matches(".hm-savebar")) {
      const del = [...f.querySelectorAll(".hm-btn--danger-text, .hm-btn--danger")].filter((b) => visible(b) && (/\/delete$/.test(b.getAttribute("href") || "") || /^delete\b/i.test((b.textContent || "").trim())));
      for (const d of del) {
        if (!d.closest(".hm-savebar__aside")) { out.push(["UI-D12", s, "Delete is not in the far-left aside", sel(d)]); continue; }
        const left = f.getBoundingClientRect().left + px(cs.paddingLeft) + px(cs.borderLeftWidth);
        const dl = d.getBoundingClientRect().left;
        if (Math.abs(dl - left) > 1) out.push(["UI-D12", sel(d), "Delete is not at the savebar's left edge", `${dl} vs ${left}`]);
      }
    }
    const prim = btns.filter((b) => b.matches(".hm-btn--primary"));
    if (prim.length > 1) out.push(["UI-D5", s, "more than one primary in the footer", prim.length]);
    // The plain footer is a loose action row that may sit inside a card body.
    if (f.matches(".hm-card__foot:not(.hm-card__foot--plain)")) {
      if (f.closest(".hm-card__body")) out.push(["UI-D6", s, "card footer inside the card body", ""]);
      const card = f.closest(".hm-card");
      if (card) {
        const ccs = getComputedStyle(card);
        const inner = card.getBoundingClientRect().width - px(ccs.borderLeftWidth) - px(ccs.borderRightWidth);
        const w = f.getBoundingClientRect().width;
        if (Math.abs(w - inner) > 1) out.push(["UI-D6", s, "footer width differs from the card inner width", `${w} vs ${inner}`]);
      }
    }
  }
  for (const h of document.querySelectorAll(".hm-pageheader, .hm-section-head")) {
    const prim = [...h.querySelectorAll(".hm-btn--primary")].filter(visible);
    if (prim.length > 1) out.push(["UI-D5", sel(h), "more than one primary in the page header", prim.length]);
  }

  // UI-D2: shadows
  const ring = /^(?:rgba?\([^)]*\)|color\([^)]*\)|#\w+)?\s*0px 0px 0px \d+px|inset/;
  for (const el of document.querySelectorAll("body *")) {
    const bs = getComputedStyle(el).boxShadow;
    if (bs === "none" || !visible(el)) continue;
    const parts = bs.split(/,(?![^(]*\))/).map((p) => p.trim());
    if (parts.every((p) => /\binset\b/.test(p) || /(^|\s)0px 0px 0px \d+px(\s|$)/.test(p))) continue;
    if (el.closest(POPUP) || el.closest(SHADOW_OK)) continue;
    out.push(["UI-D2", sel(el), "shadow outside an popup", bs]);
  }

  // UI-D3: button size
  for (const b of document.querySelectorAll(".hm-btn")) {
    const r0 = b.getBoundingClientRect();
    if (!visible(b) || b.closest(BTN_OK) || r0.height <= 1) continue; // 1px = visually hidden (Enter-key submit)
    const cs = getComputedStyle(b);
    const h = px(cs.height), r = px(cs.borderTopLeftRadius);
    if (!((h === 36 || h === 32) && r === 6)) out.push(["UI-D3", sel(b), "button size/radius is not 36|32 / 6", `${h} / ${r}`]);
  }

  // UI-D4: switches and segmented controls
  for (const c of document.querySelectorAll('input[type="checkbox"]')) {
    if (!visible(c) && !c.closest(".hm-switch")) continue;
    if (c.closest(MULTI)) continue;
    const sw = c.closest(".hm-switch") || c;
    const track = c.matches('[role="switch"]') ? c : null;
    if (!track) { out.push(["UI-D4", sel(c), "checkbox without role=switch", ""]); continue; }
    const cs = getComputedStyle(c);
    const w = px(cs.width), h = px(cs.height), r = px(cs.borderTopLeftRadius);
    if (w !== 32 || h !== 18 || r < 9) out.push(["UI-D4", sel(c), "switch is not 32x18 radius 999", `${w}x${h} r${r}`]);
    void sw;
  }
  for (const g of document.querySelectorAll("fieldset.hm-segmented")) {
    if (!visible(g)) continue;
    const n = g.querySelectorAll('input[type="radio"]').length;
    if (n < 2) out.push(["UI-D4", sel(g), "segmented control with fewer than 2 options", n]);
  }

  // Resolves a token the way the page would, so the checks compare computed values.
  const probe = (prop, value) => {
    const d = document.createElement("div");
    d.style.setProperty(prop, value);
    document.body.appendChild(d);
    const v = getComputedStyle(d).getPropertyValue(prop);
    d.remove();
    return v;
  };

  // UI-D14: component contracts (backend/web/admin/components.json). Every visible element of a
  // component carries the contract values, plus its variant's. Named exceptions are the contexts
  // the Go test allowlists (DC-02): filter chips and bubbles restyle buttons, the editor side card,
  // colour and file inputs, and the states that deliberately recolour a border.
  const CONTRACT_OK = {
    button: BTN_OK,
    input: '[type="color"], [type="file"], [type="hidden"], :disabled, [aria-invalid="true"], :focus, .hm-input-affix *, .hm-field--invalid *',
    card: ".hm-be__side .hm-card, .hm-tablecard", // .hm-tablecard drops the card's border and radius (table.css)
  };
  // The enhanced select trigger is a button that wears the select variant of the input contract (select.css).
  const ALSO = { "input/select": ".hm-select__trigger" };
  const want = new Map();
  const wanted = (prop, value) => { const k = prop + "|" + value; if (!want.has(k)) want.set(k, probe(prop, value)); return want.get(k); };
  for (const [name, c] of Object.entries(spec || {})) {
    for (const el of document.querySelectorAll(c.selector)) {
      if (!visible(el) || el.getBoundingClientRect().height <= 1 || (CONTRACT_OK[name] && el.closest(CONTRACT_OK[name]))) continue; // 1px = visually hidden (Enter-key submit)
      const props = { ...c.props };
      for (const [v, vsel] of Object.entries(c.variants || {})) if (el.matches(vsel) || (ALSO[name + "/" + v] && el.matches(ALSO[name + "/" + v]))) Object.assign(props, c.variantProps?.[v]);
      const cs = getComputedStyle(el);
      for (const [prop, value] of Object.entries(props)) {
        if (prop === "height" && el.matches(".hm-badge--wrap")) continue; // a wrapping badge grows with its text on narrow screens (badge.css)
        const got = cs.getPropertyValue(prop), exp = wanted(prop, value);
        if (got !== exp) out.push(["UI-D14", sel(el), `${name} ${prop} is not ${value}`, `${got} != ${exp}`]);
      }
    }
  }

  // UI-D7: exactly one VISIBLE breadcrumb trail per screen: the top bar (compact on phones),
  // or on the editor screens the editor bar. The editor bar trail is hidden on phones; its
  // back link is the trail there, so it must be visible instead. A hidden trail does not count.
  const crumbs = [...document.querySelectorAll('nav[aria-label="Breadcrumb"]')].filter(visible);
  if (crumbs.length > 1) out.push(["UI-D7", sel(crumbs[1]), "more than one visible breadcrumb trail", crumbs.length]);
  const bar = document.querySelector(".hm-topbar, .hm-editor-bar");
  if (bar && !crumbs.some((n) => bar.contains(n))) {
    const back = bar.matches(".hm-editor-bar") && [...bar.querySelectorAll(".hm-editor-bar__back")].some(visible);
    if (!back) out.push(["UI-D7", sel(bar), "no visible breadcrumb trail", 0]);
  }

  // UI-D8: the selected option of a segmented control (pill row, radio, button) is a paper pill with sumi text, no border,
  // and a hairline ring (plus an inset bevel): any shadow part that is neither a ring nor inset fails.
  const paperBg = probe("background-color", "var(--color-paper)"), sumiFg = probe("color", "var(--color-sumi)");
  for (const c of document.querySelectorAll('.hm-tabs[data-variant="segmented"] [aria-current="page"], .hm-segmented__btn[aria-pressed="true"], .hm-segmented__input:checked + .hm-segmented__btn')) {
    if (!visible(c)) continue;
    const cs = getComputedStyle(c);
    const border = px(cs.borderTopWidth) > 0 && !/rgba\([^)]*,\s*0\)|transparent/.test(cs.borderTopColor);
    if (border) out.push(["UI-D8", sel(c), "selected segment has a border", `${cs.borderTopWidth} ${cs.borderTopColor}`]);
    const parts = cs.boxShadow === "none" ? [] : cs.boxShadow.split(/,(?![^(]*\))/).map((p) => p.trim());
    if (!parts.some((p) => /(^|\s)0px 0px 0px \d+px(\s|$)/.test(p) && !/\binset\b/.test(p))) out.push(["UI-D8", sel(c), "selected segment has no ring", cs.boxShadow]);
    if (parts.some((p) => !/\binset\b/.test(p) && !/(^|\s)0px 0px 0px \d+px(\s|$)/.test(p))) out.push(["UI-D8", sel(c), "selected segment has a non-ring shadow", cs.boxShadow]);
    if (cs.backgroundColor !== paperBg) out.push(["UI-D8", sel(c), "selected segment is not the paper fill", cs.backgroundColor]);
    if (cs.color !== sumiFg) out.push(["UI-D8", sel(c), "selected segment text is not sumi", cs.color]);
  }

  // UI-D9: tabs navigate, pills rearrange in place. A segmented row stays on one
  // path and differs only in in-place params; at most two tab rows per page; a
  // sub row sits under an underline row.
  const here = location.href.startsWith("http") ? location.href : "http://qa.test/"; // the self-test page is about:blank
  const PILL_PARAMS = new Set(["group", "from", "to", "range", "view"]);
  const rows = [...document.querySelectorAll("nav.hm-tabs")].filter(visible);
  let tabRows = 0, underlineSeen = false;
  for (const r of rows) {
    const variant = r.getAttribute("data-variant") || "";
    if (variant === "segmented") {
      const urls = [...r.querySelectorAll("a[href]")].map((a) => new URL(a.getAttribute("href"), here));
      if (new Set(urls.map((u) => u.pathname)).size > 1) out.push(["UI-D9", sel(r), "pills that change the path: a row that navigates is a tab row", [...new Set(urls.map((u) => u.pathname))].join(" ")]);
      const keys = new Set(urls.flatMap((u) => [...u.searchParams.keys()]));
      const declared = new Set((r.getAttribute("data-changes") || "").split(/\s+/).filter(Boolean));
      if (!declared.size) out.push(["UI-D9", sel(r), "a segmented row that does not declare what it changes (Changes)", ""]);
      const moved = [...keys].filter((k) => new Set(urls.map((u) => u.searchParams.get(k))).size > 1 && !(PILL_PARAMS.has(k) && declared.has(k)));
      if (moved.length) out.push(["UI-D9", sel(r), "pills that change a param they did not declare, or a navigating param: use tabs", moved.join(",")]);
      continue;
    }
    tabRows++;
    if (tabRows > 2) out.push(["UI-D9", sel(r), "more than two tab rows on a page", tabRows]);
    if (variant === "sub" && !underlineSeen) out.push(["UI-D9", sel(r), "a sub row without an underline row above it", ""]);
    if (variant === "sub") {
      const prev = r.previousElementSibling;
      const gap = prev ? r.getBoundingClientRect().top - prev.getBoundingClientRect().bottom : 0;
      if (gap > 12) out.push(["UI-D9", sel(r), "a sub row is not directly under its level-1 row (gap over 12px)", Math.round(gap)]);
    }
    if (variant === "") underlineSeen = true;
  }

  // UI-D10: list first. On list screens the first content block after the header is
  // the list, there is no post form beside it outside a dialog, and the header has
  // one primary that opens a create page.
  if (list) {
    const page = document.querySelector(".hm-page");
    const head = page && page.querySelector(".hm-pageheader, .hm-section-head");
    const LISTY = ".hm-table, .hm-datatable, .hm-cards, .hm-media-grid, .hm-pluginlist, .hm-themes, .hm-empty";
    if (!page || !head) out.push(["UI-D10", "div.hm-page", "no page header on a list screen", ""]);
    else {
      const first = [...page.children].filter((c) => c.compareDocumentPosition(head) & Node.DOCUMENT_POSITION_PRECEDING)
        .find((c) => visible(c) && !c.matches("nav.hm-tabs, .hm-secretbanner, .hm-flash, .hm-callout, .hm-formerrors, dialog, form[role=search], .hm-filterbar, .hm-media-toolbar"));
      if (!first || !(first.matches(LISTY) || first.querySelector(LISTY))) out.push(["UI-D10", first ? sel(first) : "div.hm-page", "the list is not the first content block after the header", ""]);
      for (const f of page.querySelectorAll("form")) {
        if ((f.getAttribute("method") || "").toLowerCase() !== "post" || f.closest("dialog, .hm-table, .hm-datatable, .hm-pluginlist, .hm-themes, .hm-pkg") || f.matches("#unsigned") || f.querySelector("#unsigned") || f.querySelector(LISTY) || (f.getAttribute("id") || "").startsWith("bulk-")) continue; // the bulk action form of the table, wrapping it or tied to its select boxes by form=
        out.push(["UI-D10", sel(f), "post form beside the list outside a dialog", f.getAttribute("action") || ""]);
      }
      const prim = [...head.querySelectorAll(".hm-btn--primary")].filter(visible);
      if (prim.length !== 1) out.push(["UI-D10", sel(head), "list header does not have exactly one primary", prim.length]);
      else if (!/\/(new|upload|invite)$/.test(new URL(prim[0].getAttribute("href") || "", here).pathname)) out.push(["UI-D10", sel(prim[0]), "list header primary does not open a create page", prim[0].getAttribute("href") || ""]);
    }
  }

  // UI-D11: inputs are calm at rest, the soft --border-field and not --border-control.
  const soft = probe("border-top-color", "var(--border-field)"), firm = probe("border-top-color", "var(--border-control)");
  for (const el of document.querySelectorAll(".hm-field__control")) {
    if (!visible(el) || el.matches('[type="color"], [type="hidden"], :disabled, [aria-invalid="true"], :focus') || el.closest(".hm-input-affix, .hm-field--invalid")) continue;
    const cs = getComputedStyle(el);
    if (px(cs.borderTopWidth) === 0) continue;
    if (cs.borderTopColor !== soft || cs.borderTopColor === firm) out.push(["UI-D11", sel(el), "input border at rest is not --border-field", cs.borderTopColor]);
  }

  // UI-D13 (static half; the focus half is live-only, see dialogCheck): every [data-hm-open]
  // resolves to a <dialog> with an accessible name (aria-labelledby text, or aria-label).
  for (const o of document.querySelectorAll("[data-hm-open]")) {
    const id = o.getAttribute("data-hm-open");
    const dlg = document.getElementById(id);
    if (!dlg || dlg.tagName !== "DIALOG") { out.push(["UI-D13", sel(o), "data-hm-open does not resolve to a dialog", id]); continue; }
    const named = (dlg.getAttribute("aria-labelledby") || "").split(/\s+/).filter(Boolean).some((i) => (document.getElementById(i)?.textContent || "").trim());
    if (!named && !(dlg.getAttribute("aria-label") || "").trim()) out.push(["UI-D13", sel(dlg), "dialog has no title (aria-labelledby text or aria-label)", id]);
  }
  return out;
}

// UI-D13 live half, Playwright side (the in-page check() cannot click): open every visible
// [data-hm-open], expect focus inside the dialog on a real control, Escape to close it and focus
// back on the opener (or on the menu control when the opener sat in a menu that has closed).
// Returns [rule, selector, message, value] tuples. Not covered by --self-test (needs a server).
let dialogsExercised = 0;
async function dialogCheck(page) {
  const out = [];
  const n = await page.locator("[data-hm-open]").count();
  for (let i = 0; i < n; i++) {
    const op = page.locator("[data-hm-open]").nth(i);
    if (!(await op.isVisible())) continue;
    const info = await op.evaluate((o) => ({ id: o.dataset.hmOpen, sel: o.tagName.toLowerCase() + "[data-hm-open=" + o.dataset.hmOpen + "]", menu: o.closest("[popover]")?.id || "" }));
    if (!(await page.locator(`dialog#${info.id}`).count())) continue; // the static half reports it
    const start = page.url();
    await op.click();
    const dlg = page.locator(`dialog#${info.id}[open]`);
    try { await dlg.waitFor({ timeout: 3000 }); } catch { out.push(["UI-D13", info.sel, "opener did not open its dialog", page.url() === start ? "" : page.url()]); if (page.url() !== start) await page.goBack(); continue; }
    dialogsExercised++;
    await page.mouse.move(0, 0);
    const where = await page.evaluate((id) => {
      const a = document.activeElement, d = document.getElementById(id);
      return a === document.body || a === d ? "none" : d.contains(a) ? "inside" : "outside";
    }, info.id);
    if (where !== "inside") out.push(["UI-D13", `dialog#${info.id}`, "focus is not on a control inside the dialog after open", where]);
    await page.keyboard.press("Escape");
    await page.waitForFunction((id) => !document.getElementById(id)?.open, info.id, { timeout: 2000 }).catch(() => {});
    const back = await page.evaluate(({ id, menu }) => {
      if (document.getElementById(id)?.open) return "dialog still open";
      const a = document.activeElement;
      const opener = [...document.querySelectorAll(`[data-hm-open="${id}"]`)].find((o) => o === a);
      if (opener) return "opener";
      return menu && a?.getAttribute("popovertarget") === menu ? "opener" : (a?.tagName.toLowerCase() || "none") + (a?.id ? "#" + a.id : "");
    }, info);
    if (back !== "opener") out.push(["UI-D13", info.sel, "focus does not return to the opener after Escape", back]);
    await page.evaluate(() => document.querySelectorAll("[popover]:popover-open").forEach((p) => p.hidePopover?.()));
  }
  return out;
}

// Runs in the page after the pointer moved onto the input: true when the border is
// neither hover colour, --color-hai or --border-control (the resting border does not count).
function hoverCheck(id) {
  const el = document.getElementById(id);
  const colour = (v) => {
    const d = document.createElement("div");
    d.style.setProperty("border-top-color", `var(--${v})`);
    document.body.appendChild(d);
    const c = getComputedStyle(d).borderTopColor;
    d.remove();
    return c;
  };
  const now = getComputedStyle(el).borderTopColor;
  return now !== colour("color-hai") && now !== colour("border-control");
}

// One planted violation per rule, styled inline so no admin CSS or server is needed.
const FIXTURE = `<!doctype html><body style="margin:0;width:800px">
<div class="hm-card" style="border:1px solid #000"><div class="hm-card__body">
  <div class="hm-card__foot" id="d1d6" style="display:block;box-shadow:0 4px 6px #f00;border-top:1px solid #000"><button class="hm-btn" style="height:36px;border-radius:6px">Cancel</button></div>
</div></div>
<div class="hm-card__foot" id="d5" style="display:flex;justify-content:flex-end;border-top:1px solid #000;width:800px"><button class="hm-btn hm-btn--primary" style="height:36px;border-radius:6px">A</button><button class="hm-btn hm-btn--primary" style="height:36px;border-radius:6px">B</button></div>
<div id="d2" style="box-shadow:0 8px 12px #000;width:20px;height:20px"></div>
<button class="hm-btn" id="d3" style="height:50px;border-radius:0">Big</button>
<input type="checkbox" id="d4a">
<fieldset class="hm-segmented" id="d4s"><input type="radio" name="s"></fieldset>
<div class="hm-savebar" id="d12" style="display:flex;justify-content:flex-end"><button class="hm-btn hm-btn--primary">Save</button><a class="hm-btn hm-btn--danger-text" href="/x/delete">Delete</a></div>
<div class="hm-savebar" id="d12b" style="display:flex;justify-content:flex-end"><div class="hm-savebar__aside" style="margin-left:40px"><a class="hm-btn hm-btn--danger-text" id="d12bdel" href="/y/delete">Delete</a></div></div>
<button data-hm-open="d13"></button><dialog id="d13"></dialog>
<button data-hm-open="d13b" id="d13bo"></button><div id="d13b"></div>
<style>:root{--color-sumi:#111827;--color-paper:#ffffff;--border-field:#d1d5db;--border-control:#8b919a;--shadow-1:0 1px 2px rgba(0,0,0,.2);--color-hai:#4b5563}</style>
<nav aria-label="Breadcrumb" id="d7a"><ol><li>Home</li></ol></nav><nav aria-label="Breadcrumb" id="d7b"><ol><li>Home</li></ol></nav>
<div class="hm-topbar" id="d7c"><nav aria-label="Breadcrumb" style="display:none"><ol><li>Home</li></ol></nav></div>
<nav class="hm-tabs" data-variant="segmented"><ul><li><a id="d8" href="/x?view=a" aria-current="page" style="border:2px solid #000;box-shadow:0 1px 2px #000;background:#e5e7eb;color:#000">A</a></li><li><a href="/x?view=b">B</a></li></ul></nav>
<nav class="hm-tabs" data-variant="segmented" id="d9seg"><ul><li><a href="/x?view=a">A</a></li><li><a href="/y?view=a">B</a></li></ul></nav>
<nav class="hm-tabs" data-variant="segmented" data-changes="group" id="d9undecl"><ul><li><a href="/x?view=a">A</a></li><li><a href="/x?view=b">B</a></li></ul></nav>
<nav class="hm-tabs" data-variant="segmented" id="d9none"><ul><li><a href="/x">A</a></li></ul></nav>
<nav class="hm-tabs" id="d9t1"><ul><li><a href="/p">P</a></li></ul></nav><nav class="hm-tabs" data-variant="sub" id="d9t2" style="margin-top:40px"><ul><li><a href="/p/a">A</a></li></ul></nav><nav class="hm-tabs" id="d9t3"><ul><li><a href="/p/b">B</a></li></ul></nav>
<div class="hm-page"><header class="hm-pageheader"><h1>List</h1><a class="hm-btn hm-btn--primary" href="/things/new" style="height:36px;border-radius:6px;display:inline-block">New</a></header><div class="hm-table" id="d10t">rows</div><form method="post" action="/things" id="d10"></form><div class="hm-pkg"><form method="post" action="/pkg/fetch" id="d10pkg"></form></div></div>
<style>:root{--radius-sm:6px}</style><button class="hm-btn" id="d14" style="border-radius:2px">Plant</button><button class="hm-btn" id="d14ok" style="border:1px solid #8b919a;border-radius:6px;font-size:16px;padding:0">Compliant</button>
<input class="hm-field__control" id="d11" style="border:1px solid #8b919a">
<input class="hm-field__control" id="d11h" style="border:1px solid #d1d5db">
<style>#d11r:hover{border-color:red!important}#d11ok:hover{border-color:var(--color-hai)!important}</style>
<input class="hm-field__control" id="d11r" style="border:1px solid #d1d5db"><input class="hm-field__control" id="d11ok" style="border:1px solid #d1d5db"></body>`;

if (selfTest) {
  const sb = await launch();
  const page = await (await sb.newContext({ viewport: { width: 1440, height: 900 } })).newPage();
  await page.setContent(FIXTURE);
  const found = await page.evaluate(check, { sticky: false, list: true, spec: SPEC });
  // Hover must darken the border to --color-hai; the planted input has no hover rule, so it stays soft.
  await page.hover("#d11h");
  if (await page.evaluate(hoverCheck, "d11h")) found.push(["UI-D11", "input#d11h", "border does not darken on hover", ""]);
  // A hover rule that sets another colour (red) fails, the hai colour passes.
  await page.hover("#d11r");
  if (await page.evaluate(hoverCheck, "d11r")) found.push(["UI-D11", "input#d11r", "border does not darken on hover", ""]);
  await page.hover("#d11ok");
  if (await page.evaluate(hoverCheck, "d11ok")) { console.log("self-test FAILED: UI-D11 hover rejected --color-hai (#d11ok)"); process.exit(1); }
  await sb.close();
  // A button that follows the contract must not be reported by UI-D14.
  if (found.some((v) => v[0] === "UI-D14" && v[1].endsWith("#d14ok"))) { console.log("self-test FAILED: UI-D14 flagged a compliant button (#d14ok)"); process.exit(1); }
  // A registry Install row form is a row action: it must not be reported.
  if (found.some((v) => v[0] === "UI-D10" && v[1].endsWith("#d10pkg"))) { console.log("self-test FAILED: UI-D10 flagged a registry row form (#d10pkg)"); process.exit(1); }
  // Each rule must fire on its own planted element, not just somewhere in the fixture.
  const own = { "UI-D1": "#d1d6", "UI-D2": "#d2", "UI-D3": "#d3", "UI-D4": ["#d4a", "#d4s~fewer than 2"], "UI-D5": "#d5", "UI-D6": "#d1d6", "UI-D7": ["#d7b", "#d7c"], "UI-D8": ["#d8~has a border", "#d8~has no ring", "#d8~non-ring shadow", "#d8~not the paper fill", "#d8~not sumi"], "UI-D9": ["#d9seg", "#d9undecl~did not declare", "#d9none~does not declare", "#d9t3", "#d9t2~directly under"], "UI-D10": "#d10", "UI-D11": ["#d11", "#d11h", "#d11r"], "UI-D12": ["#d12~far-left aside", "#d12bdel~left edge"], "UI-D13": ["#d13~no title", "#d13bo~does not resolve"], "UI-D14": "#d14~button border-top-left-radius" };
  const fired = [...new Set(found.map((v) => v[0]))].sort();
  const missing = Object.entries(own).flatMap(([r, ids]) => [ids].flat().map((id) => [r, id])).filter(([r, id]) => { const [sid, frag] = id.split("~"); return !found.some((v) => v[0] === r && v[1].endsWith(sid) && (!frag || v[2].includes(frag))); }).map(([r, id]) => `${r} on ${id}`);
  console.log(`self-test: fired ${fired.join(" ")}`);
  if (missing.length) { console.log(`self-test FAILED: rules that did not fire on their own fixture: ${missing.join(", ")}`); process.exit(1); }
  console.log("self-test ok");
  process.exit(0);
}

const unknown = only.filter((n) => !allScreens.some((s) => s.name === n));
if (unknown.length) { console.error(`unknown screen name(s): ${unknown.join(", ")}`); process.exit(1); }

const b = await launch();
const result = [];
let violations = 0, failed = 0, skipped = 0, scans = 0;

async function anonLogin(page, email) {
  await page.goto(`${base}/admin/login`);
  await page.fill("#email", email);
  await page.fill("#password", PASSWORD);
  await Promise.all([page.waitForURL((u) => !u.pathname.endsWith("/admin/login")), page.click('form[action="/admin/login"] button[type=submit]')]);
  if (page.url().includes("/admin/login")) throw new Error(`login failed for ${email}`);
}

// QA_JOBS: how many width x theme sessions run at once (default 4 = all four of a role).
// The owner's sessions finish before the editor's start, so path tokens ({post}, ...) are
// resolved by the owner first, as in a serial run. Output lines carry role/width/theme.
const JOBS = Math.max(1, Number.parseInt(process.env.QA_JOBS || "4", 10) || 1);
async function pool(tasks, n) {
  let next = 0;
  await Promise.all(Array.from({ length: Math.min(n, tasks.length) }, async () => { while (next < tasks.length) await tasks[next++](); }));
}
for (const roleName of ["owner", "editor"]) {
  const list = allScreens.filter((s) => s.roles.includes(roleName)).filter((s) => !only.length || only.includes(s.name));
  const combos = [];
  for (const width of [1440, 390]) for (const theme of ["light", "dark"]) combos.push(async () => {
    const p = await newSession(b, { theme, width, signedIn: false });
    if (roleName === "owner") await login(p); else await anonLogin(p, EDITOR_EMAIL);
    for (const s of list) {
      const label = `${roleName}:${s.name}`;
      let page = p;
      if (s.auth === false) {
        if (roleName === "editor") continue; // signed-out screens are role independent
        page = await newSession(b, { theme, width, signedIn: false });
      } else if (s.as === "newcomer") {
        // A user without a profile: the gate sends them to the step this screen is.
        page = await newSession(b, { theme, width, signedIn: false });
        await anonLogin(page, NEWCOMER_EMAIL);
      }
      const v = await visit(page, s);
      if (v.skipped) { skipped++; console.log(`${label} ${width} ${theme} skipped (no ${s.path.match(/\{(\w+)\}/)?.[1]} data seeded)`); if (!v.ok) failed++; continue; }
      if (!v.ok) { failed++; console.log(`FAIL ${label} ${width} ${theme}: status ${v.status} for ${v.url}`); continue; }
      await page.waitForTimeout(200);
      const bad = await page.evaluate(check, { sticky: STICKY_SCREENS.has(s.name), list: !!s.list, spec: SPEC });
      if (s.hover) {
        const first = page.locator(".hm-field__control:visible:not(:disabled)").first();
        if (await first.count()) {
          await first.hover();
          const id = await first.evaluate((e) => (e.id ||= "hm-qa-hover"));
          if (await page.evaluate(hoverCheck, id)) bad.push(["UI-D11", `input#${id}`, "border does not darken on hover", ""]);
        }
      }
      if (theme === "light") bad.push(...(await dialogCheck(page)));
      scans++;
      await page.screenshot({ fullPage: true, path: path.join(OUT, `${roleName === "owner" ? "" : "editor-"}${s.name}-${width}-${theme}.png`) });
      // The create dialog open: screenshot, scanned like the page. The opener is the header action.
      if (s.states?.some((st) => st.name === "new-dialog")) {
        const opener = page.locator(".hm-pageheader [data-hm-open], .hm-section-head [data-hm-open]").first();
        if (!(await opener.count())) { failed++; console.log(`FAIL ${label} ${width} ${theme}: no header action opens the create dialog`); }
        else {
          await opener.click();
          await page.locator("dialog[open]").first().waitFor({ timeout: 3000 }).catch(() => {});
          await page.mouse.move(0, 0); // the dialog opens under the pointer on phones: do not scan a hover
          await page.waitForTimeout(150);
          if (!(await page.locator("dialog[open]").count())) { failed++; console.log(`FAIL ${label} ${width} ${theme}: the create dialog did not open`); }
          else {
            for (const v of await page.evaluate(check, { sticky: false, list: false, spec: SPEC })) bad.push(v.map((x, i) => (i === 1 ? `dialog-open ${x}` : x)));
            await page.screenshot({ fullPage: true, path: path.join(OUT, `${roleName === "owner" ? "" : "editor-"}${s.name}-dialog-${width}-${theme}.png`) });
          }
        }
      }
      for (const [rule, selector, message, value] of bad) {
        violations++;
        console.log(`${rule} ${label} ${width} ${theme} ${selector} ${message} (${value})`);
        result.push({ rule, role: roleName, screen: s.name, width, theme, selector, message, value });
      }
      if (page !== p) await page.context().close();
    }
    await p.context().close();
  });
  await pool(combos, JOBS);
}

const order = (r) => [r.role, r.screen, String(r.width).padStart(5, "0"), r.theme, r.rule, r.selector].join("\u0000");
result.sort((x, y) => (order(x) < order(y) ? -1 : order(x) > order(y) ? 1 : 0));
writeResult("consistency.json", { scans, violations: result });
console.log(`\nbase=${base}: ${scans} scans, ${violations} violations, ${dialogsExercised} data-hm-open dialogs exercised, ${failed} screen failures, ${skipped} skipped, screenshots in ${OUT}`);
const ok = violations === 0 && failed === 0 && scans > 0 && (only.length > 0 || dialogsExercised > 0);
if (dialogsExercised === 0 && !only.length) console.log("no data-hm-open dialog was exercised");
if (scans === 0) console.log("no screens were scanned");
console.log(ok ? "0 violations" : "FAILED");
await b.close();
process.exit(ok ? 0 : 1);
