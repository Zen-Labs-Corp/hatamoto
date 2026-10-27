// Site styles (a lazy chunk, owner only): the site's colours and corners in a side dialog, saved through the same two
// routes as the Theme pages (colours and corners to /theme/colours, the builder colours to /theme/design-system) in
// fetch mode, so the page is not reloaded and nothing new is written. The pure parts are tested without a DOM.
// Run: node --test scripts/admin-qa/unit/builder-sitestyles.test.mjs
import { h } from "@hm/builder/dom";
import { clone } from "@hm/builder/savedialog";
import { COLOUR_NAME } from "@hm/builder/colournames";

const HEX = /^#([0-9a-f]{3}|[0-9a-f]{6}|[0-9a-f]{8})$/i; // as the server: builder.hexRe
const RADIUS = /^\d{1,3}(\.\d{1,2})?(px|rem|em)$/;

// [form field, label, palette name]: the Theme colours, then the builder colours (Design system tokens).
export const THEME_COLOURS = [["primary", COLOUR_NAME.primary, "primary"], ["secondary", "Secondary colour (for themes that use it)", null], ["background", COLOUR_NAME.bg, "bg"], ["text", COLOUR_NAME.text, "text"]];
export const TOKEN_COLOURS = [["color_accent", COLOUR_NAME.accent, "accent"], ["color_surface", COLOUR_NAME.surface, "surface"], ["color_muted", COLOUR_NAME.muted, "muted"]];
export const CORNERS = [["radius_sm", "Small"], ["radius_md", "Medium"], ["radius_lg", "Large"]];

/** #abc -> #aabbcc, lower case: what a colour input takes. */
export const longHex = (v) => (/^#[0-9a-f]{3}$/i.test(v) ? `#${[...v.slice(1)].map((c) => c + c).join("")}` : v).toLowerCase();

/** {field: message} for every colour or corner that is not valid; the server checks again. */
export function validate(values) {
  const errs = {};
  for (const [name] of [...THEME_COLOURS, ...TOKEN_COLOURS]) if (name in values && !HEX.test(values[name])) errs[name] = "Choose a colour, for example #1a2b3c.";
  for (const [name] of CORNERS) if (name in values && !RADIUS.test(values[name])) errs[name] = "Use a size like 8px or 0.5rem.";
  return errs;
}

/** The fields to post: only the edited ones; the server keeps the stored value of every field that is absent. */
export function body(values, csrf) {
  const b = new URLSearchParams({ _csrf: csrf });
  for (const [k, v] of Object.entries(values)) b.set(k, v);
  return b;
}

/** The palette colours (by palette name) after a save, from the values now stored. */
export function paletteColours(siteStyles, values) {
  const all = { ...siteStyles.theme, ...siteStyles.tokens, ...values };
  return Object.fromEntries([...THEME_COLOURS, ...TOKEN_COLOURS].filter((c) => c[2]).map(([name, , key]) => [key, all[name]]));
}

/** The two forms' values that differ from what is stored: {theme: {...}, tokens: {...}}. */
export function changes(siteStyles, values) {
  const diff = (stored, names) => Object.fromEntries(names.filter((n) => n in values && values[n].toLowerCase() !== String(stored[n] ?? "").toLowerCase()).map((n) => [n, values[n]]));
  return { theme: diff(siteStyles.theme, [...THEME_COLOURS, ...CORNERS].map((c) => c[0])), tokens: diff(siteStyles.tokens, TOKEN_COLOURS.map((c) => c[0])) };
}

/** POSTs one form in fetch mode: {} when saved, {errors} for field problems (422), else throws with the server's message ({code,error}, or the first of {errors}). */
export async function post(url, body) {
  const res = await fetch(url, { method: "POST", credentials: "same-origin", headers: { "X-Hm-Fetch": "1" }, body });
  if (res.status === 204) return {};
  const json = await res.json().catch(() => ({}));
  if (res.status === 422 && json.errors && Object.keys(json.errors).length) return { errors: json.errors };
  throw new Error(json.error || Object.values(json.errors ?? {})[0] || "The site styles could not be saved. Reload the page and try again.");
}

/**
 * open({boot, csrf, onSaved(colours)}): shows the dialog; resolves when it closes. boot.siteStyles holds the stored values
 * and is updated after a save. The builder colours (Accent, Surface, Muted) show only when the theme uses them.
 */
export function open({ boot, csrf, onSaved = () => {} }) {
  const ss = boot.siteStyles;
  const tokensUsed = !!boot.design?.tokens;
  const fields = new Map();
  const field = (name, label, value, kind) => {
    const text = h("input", { type: "text", class: "hm-field__control", id: `hm-bs-ss-${name}`, name, value, autocomplete: "off", spellcheck: "false" });
    const err = h("p", { class: "hm-field__error", id: `hm-bs-ss-${name}-e`, hidden: true });
    const row = [h("label", { class: "hm-field__label", for: `hm-bs-ss-${name}`, text: label })];
    if (kind === "colour") {
      const pick = h("input", { type: "color", class: "hm-bs-ss__pick", "aria-label": `${label} picker`, value: HEX.test(value) ? longHex(value).slice(0, 7) : "#000000" });
      pick.addEventListener("input", () => (text.value = pick.value));
      text.addEventListener("input", () => HEX.test(text.value) && (pick.value = longHex(text.value).slice(0, 7)));
      row.push(h("div", { class: "hm-bs-ss__row" }, pick, text));
    } else row.push(text);
    fields.set(name, { text, err });
    return h("div", { class: "hm-field" }, ...row, err);
  };
  const stored = (n) => ss.theme[n] ?? ss.tokens[n] ?? "";
  const group = (title, ...kids) => h("fieldset", { class: "hm-bs-ss__group" }, h("legend", { class: "hm-bs-ss__legend", text: title }), ...kids);
  const problem = h("p", { class: "hm-field__error", role: "alert", hidden: true });
  const cancel = clone("act", "Cancel", { type: "button" });
  const save = clone("actp", "Save site styles", { type: "submit" });
  const form = h(
    "form",
    { method: "dialog", class: "hm-dialog__panel", novalidate: true },
    h("h2", { class: "hm-dialog__title", id: "hm-bs-ss-t", text: "Site styles" }),
    h(
      "div",
      { class: "hm-dialog__body" },
      group("Colours", ...THEME_COLOURS.map(([n, l]) => field(n, l, stored(n), "colour")), ...(tokensUsed ? TOKEN_COLOURS.map(([n, l]) => field(n, l, stored(n), "colour")) : [])),
      group("Corners", h("p", { class: "hm-field__help", text: "Small, Medium and Large corners are used by buttons, inputs and cards." }), ...CORNERS.map(([n, l]) => field(n, `${l} corners`, stored(n)))),
      problem,
      h("a", { href: boot.urls.theme, target: "_blank", rel: "noopener", text: "Open full theme settings" }),
    ),
    h("div", { class: "hm-dialog__foot" }, cancel, save),
  );
  const dlg = h("dialog", { class: "hm-dialog", "data-side": "right", "aria-labelledby": "hm-bs-ss-t" }, form);

  const show = (errs) => {
    let first = null;
    for (const [name, { text, err }] of fields) {
      const m = errs[name];
      err.textContent = m ?? "";
      err.hidden = !m;
      if (m) (text.setAttribute("aria-invalid", "true"), text.setAttribute("aria-describedby", err.id), (first ??= text));
      else (text.removeAttribute("aria-invalid"), text.removeAttribute("aria-describedby"));
    }
    first?.focus();
    return !first;
  };
  const fail = (msg) => ((problem.textContent = msg), (problem.hidden = false));
  const busy = (b) => {
    for (const el of [cancel, save]) el.disabled = b;
    save.setAttribute("aria-busy", String(b));
  };
  const COLOURS = new Set([...THEME_COLOURS, ...TOKEN_COLOURS].map((c) => c[0]));
  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    problem.hidden = true;
    const values = Object.fromEntries([...fields].map(([n, f]) => [n, f.text.value.trim()]));
    if (!show(validate(values))) return;
    for (const n of COLOURS) if (n in values) values[n] = longHex(values[n]);
    const { theme, tokens } = changes(ss, values);
    let saved = false;
    const send = async (key, url, body, diff) => {
      if (!Object.keys(diff).length) return {};
      const r = await post(url, body);
      if (!r.errors) (Object.assign(ss[key], diff), (saved = true));
      return r.errors ?? {};
    };
    busy(true);
    try {
      let errs = await send("theme", boot.urls.theme, body(theme, csrf), theme);
      if (!Object.keys(errs).length) errs = await send("tokens", boot.urls.theme.replace(/colours$/, "design-system"), body(tokens, csrf), tokens);
      if (saved) onSaved(paletteColours(ss, {}));
      if (!Object.keys(errs).length) return dlg.close();
      busy(false);
      if (show(errs)) fail("Some values were not accepted. Check them and try again.");
    } catch (err) {
      if (saved) onSaved(paletteColours(ss, {}));
      busy(false);
      fail(err.message);
    }
  });
  cancel.addEventListener("click", () => dlg.close());
  return new Promise((resolve) => {
    dlg.addEventListener("close", () => (dlg.remove(), resolve()));
    document.body.append(dlg);
    dlg.showModal();
    fields.get("primary").text.focus();
  });
}
