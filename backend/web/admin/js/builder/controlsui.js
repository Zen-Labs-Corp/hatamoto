// The Style tab's control chrome (a lazy chunk): the row every control sits in. A leading dot says whether the
// value is set here (filled), comes from elsewhere (ring) or is unset (none); a source label says where from, in
// text ("Set here", "From class .card", "Inherited from Desktop", "Custom value"); an icon button resets the value set here.
// The decision itself (rowState, sourceOf) is the pure controls.js.
import { h, actButton, chip } from "@hm/builder/dom";
import { sourceOf, probe, splitValue, joinValue, boundsFor, stepFor, stepValue, tokenOrder, tokenLabel, hexFor, loadRecent, pushRecent } from "@hm/builder/controls";

/**
 * row({id, name, label, body, status, note, onReset}) -> {el, reset, paint(state)}.
 * name: the field's name for "Reset <name>"; label: its <label> or group label element; body: the control elements;
 * status / note: the description and error elements (the control's aria-describedby points at them).
 * paint(state) takes a controls.rowState() result (with classValue) and fills the source chip.
 */
export function row({ id, name, label, body, status, note, onReset }) {
  const dot = h("span", { class: "hm-bs-dot", "aria-hidden": "true" });
  const src = chip("", { class: "hm-bs-src", hidden: true });
  const reset = actButton("", { class: "hm-bs-reset", "aria-label": `Reset ${name}`, "aria-describedby": `${id}-s`, onclick: onReset });
  const el = h("div", { class: "hm-field hm-bs-field" }, h("div", { class: "hm-bs-head" }, dot, label, src, reset), ...body, status, note);
  return {
    el,
    reset,
    paint(state) {
      const s = sourceOf(state);
      dot.dataset.dot = state.dot;
      src.dataset.source = s.kind;
      src.textContent = s.text;
      src.hidden = !s.text;
      reset.hidden = state.dot !== "set";
    },
  };
}

let drags = 0; // slider drag sessions: one drag is one undo step (store.commit coalesce key)

/**
 * lengthControl({id, entry, name, palette, describe, apply}) -> {el, num, sync(value)}: a token select (when the key has
 * tokens), the number field (id, so the row's label names it), a unit select and a slider, each with its own accessible
 * name ("<name> token", "<name>", "<name> unit", "<name> slider"). apply(value | null, opts) validates and commits; it
 * returns whether the value was taken. Arrow keys step by 1 (Shift 10); a slider drag shares one session key.
 * compact (the box model's sides): only the number (named by aria-label, there is no row label) and one select that holds the
 * units and the tokens; no token select, no slider.
 */
export function lengthControl({ id, entry, name, palette, describe, apply, compact = false }) {
  const p = probe(entry);
  const options = [...p.units, ...(p.auto ? ["auto"] : [])];
  let unit = p.units.includes("px") ? "px" : (p.units[0] ?? ""); // the last real unit: what a typed number gets
  let custom = null;
  let session = null;
  let shown = ""; // what an unset field shows as its placeholder (inherited or from the preset): arrow keys start from it
  const tokens = tokenOrder(entry, palette);
  const tok = tokens.length && !compact
    ? h("select", { class: "hm-field__control hm-bs-tok", "aria-label": `${name} site size`, "aria-describedby": describe }, h("option", { value: "", text: "Custom" }), ...tokens.map((t) => h("option", { value: t, text: tokenLabel(entry, t, palette) })))
    : null;
  const num = h("input", { id, class: "hm-field__control hm-bs-num", type: "number", step: String(stepFor(entry, unit)), autocomplete: "off", "aria-label": compact ? name : null, "aria-describedby": describe });
  const choices = compact ? [...options, ...tokens] : options; // the compact select lists the tokens after the units
  const sel = choices.length
    ? h("select", { class: "hm-field__control hm-bs-unit", "aria-label": `${name} unit`, disabled: choices.length === 1 }, ...choices.map((u) => h("option", { value: u, text: options.includes(u) ? u : tokenLabel(entry, u, palette) })))
    : null;
  const slider = compact ? null : h("input", { type: "range", class: "hm-bs-slider", "aria-label": `${name} slider` });
  const el = h("div", { class: compact ? "hm-bs-len hm-bs-len--c" : "hm-bs-len" }, tok, h("div", { class: "hm-bs-len__n" }, num, sel), slider);

  const join = (n, u = unit) => joinValue({ kind: "num", num: n, unit: u });
  const fit = () => {
    num.step = String(stepFor(entry, unit));
    if (!slider) return;
    const b = boundsFor(entry.key, entry, unit);
    Object.assign(slider, { min: String(b.min), max: String(b.max), step: String(stepFor(entry, unit)) });
  };
  function sync(value, inherited = "") {
    const s = splitValue(entry, value);
    custom?.remove();
    custom = null;
    if (s.kind === "num" && s.num !== "" && (s.unit !== "" || p.bare)) unit = s.unit; // a bare "0" says no unit: keep the key's own
    if (tok) tok.value = s.kind === "token" ? s.token : "";
    const home = compact ? sel : tok; // the select that can show a token or a custom value
    if (s.kind === "custom" && home) {
      custom = h("option", { value: s.raw, text: `${s.raw} (custom)` });
      home.append(custom);
      home.value = s.raw;
    }
    shown = inherited === null ? "" : String(inherited);
    num.placeholder = s.kind === "custom" && !home ? `${s.raw} (custom)` : shown;
    if (sel && !(compact && (s.kind === "custom" || s.kind === "token"))) sel.value = s.kind === "auto" ? "auto" : options.includes(unit) ? unit : (options[0] ?? "");
    if (compact && s.kind === "token") sel.value = s.token;
    num.value = s.kind === "num" ? s.num : "";
    num.disabled = s.kind === "auto";
    fit();
    if (slider) slider.value = s.kind === "num" && s.num !== "" ? s.num : slider.min;
  }

  /** The inherited / preset value as a number: a token is resolved through the palette ("space-4" -> "1rem"). */
  const shownNum = () => {
    let v = splitValue(entry, shown);
    if (v.kind === "token") v = splitValue({ ...entry, tokens: {} }, (v.token.startsWith("space-") ? palette?.space?.[v.token.slice(6)] : palette?.fs?.[v.token]) ?? "");
    return v.kind === "num" && v.num !== "" ? v : null;
  };
  /** Where an arrow key starts: the typed number, else the inherited / preset value when it is a number (its unit is taken when this key allows it), else the key's default (0, held to the slider's range). */
  const startAt = () => {
    if (num.value.trim() !== "") return Number(num.value);
    const from = shownNum();
    if (from) {
      if (from.unit !== "" || p.bare) unit = from.unit; // a bare "0" names no unit: keep this key's own
      return Number(from.num);
    }
    const b = boundsFor(entry.key, entry, unit);
    return Math.min(b.max, Math.max(b.min, 0));
  };
  tok?.addEventListener("change", () => apply(tok.value || null));
  num.addEventListener("change", () => apply(join(num.value.trim())));
  num.addEventListener("keydown", (e) => {
    if ((e.key !== "ArrowUp" && e.key !== "ArrowDown") || e.altKey || e.ctrlKey || e.metaKey) return;
    e.preventDefault();
    const from = startAt();
    if (sel) sel.value = unit;
    fit();
    const next = String(stepValue(from, e.key === "ArrowUp" ? 1 : -1, stepFor(entry, unit), e.shiftKey, boundsFor(entry.key, entry, unit)));
    num.value = next;
    apply(join(next)); // no session: each press is its own undo step
  });
  sel?.addEventListener("change", () => {
    if (sel.value === "auto" || tokens.includes(sel.value)) return void apply(sel.value);
    unit = sel.value;
    fit();
    if (num.value.trim() !== "") apply(join(num.value.trim()));
  });
  const drag = (final) => () => {
    session ??= `s:${id}:${++drags}`;
    apply(join(slider.value), { coalesce: session });
    if (final) session = null;
  };
  slider?.addEventListener("input", drag(false));
  slider?.addEventListener("change", drag(true));
  for (const ev of ["pointercancel", "blur"]) slider?.addEventListener(ev, () => (session = null)); // a drag that ends without a change event
  return { el, num, sync };
}

/**
 * stopSlider({name, options, apply}) -> {el, sync(value)}: a slider over an enum's ordered stops (opacity), one drag one undo step.
 * options: the stop values in order; apply(value, opts) commits one. The thumb reads as the opacity value (aria-valuetext);
 * unset, it sits at the inherited value, else at "1" (fully opaque).
 */
export function stopSlider({ name, options, apply }) {
  let session = null;
  const el = h("input", { type: "range", class: "hm-bs-slider", min: "0", max: String(options.length - 1), step: "1", "aria-label": `${name} slider` });
  const text = () => el.setAttribute("aria-valuetext", options[Number(el.value)] ?? "");
  const drag = (final) => () => {
    text();
    session ??= `s:${name}:${++drags}`;
    apply(options[Number(el.value)], { coalesce: session });
    if (final) session = null;
  };
  const end = () => (session = null); // a drag that ends without a change event (cancel, blur) still closes its session
  el.addEventListener("input", drag(false));
  el.addEventListener("change", drag(true));
  el.addEventListener("pointercancel", end);
  el.addEventListener("blur", end);
  return {
    el,
    sync(value, inherited = null) {
      const at = options.indexOf(value ?? inherited);
      el.value = String(at >= 0 ? at : Math.max(0, options.indexOf("1")));
      text();
    },
  };
}

let openColour = null; // the key whose popover was open when its panel was rebuilt: the new swatch takes the focus back
const FOCUSABLE = "button:not([disabled]), input:not([disabled]):not([type=hidden]), select:not([disabled])";

/**
 * colourControl({id, entry, name, palette, describe, apply}) -> {el, hex, sync(own, shown), close()}: a swatch button that opens
 * a popover (the admin's .hm-popover-panel and Popover API: Esc and an outside click close it, focus returns to the
 * swatch) with the site's colours, a native colour input, a hex field and Clear. Choosing a palette colour commits its
 * token name, never its hex. Tab wraps inside the panel: the popover is non-modal, so without this Tab would walk
 * out of it and leave it open behind the field. Colours reach the DOM only through the --hm-bs-sw custom property (CSSOM).
 * Palette colours show their plain names (Brand, Text...); an unset swatch says "None" on a checker; the last six custom colours
 * of this user are offered again; onEditSiteColours (owners only) adds an "Edit site colours" button.
 * sync(own, shown): own is the value set here, shown what to paint when it is unset (inherited or preset).
 */
export function colourControl({ id, entry, name, palette, describe, apply, user = "", onEditSiteColours = null }) {
  const popId = `hm-bs-pop${id}`;
  let session = null;
  const paintSw = (node, css) => (css ? node.style.setProperty("--hm-bs-sw", css) : node.style.removeProperty("--hm-bs-sw"));
  const swatch = h("button", { type: "button", id, class: "hm-bs-swatch", popovertarget: popId, "aria-expanded": "false", "aria-describedby": describe }, h("span", { class: "hm-bs-none", "aria-hidden": "true", text: "None" }));
  const err = h("p", { class: "hm-bs-err", id: `${id}-pe`, hidden: true, text: "Use a palette colour or a hex value like #1e293b." });
  const hex = h("input", { type: "text", class: "hm-field__control hm-bs-hex", autocomplete: "off", spellcheck: "false", placeholder: "#rrggbb", "aria-label": "Hex colour", "aria-describedby": `${id}-pe` });
  const native = h("input", { type: "color", class: "hm-bs-native", "aria-label": "Custom colour" });
  const clear = actButton("Clear");
  const recentList = h("div", { class: "hm-bs-recent__l" });
  const recent = h("div", { class: "hm-bs-recent", hidden: true }, h("p", { class: "hm-bs-pal__t", text: "Recent" }), recentList);
  const paintRecent = () => {
    const list = loadRecent(user);
    recent.hidden = !list.length;
    recentList.replaceChildren(
      ...list.map((c) => {
        const b = h("button", { type: "button", class: "hm-bs-copt hm-bs-copt--sw", "aria-label": `Recent colour ${c}`, title: c });
        paintSw(b, c);
        b.addEventListener("click", () => take(c) && close());
        return b;
      }),
    );
  };
  const remember = (v) => /^#[0-9a-f]{3,8}$/i.test(v ?? "") && (pushRecent(user, v), paintRecent());
  const site = onEditSiteColours ? actButton("Edit site colours", { class: "hm-bs-site", onclick: () => (close(), onEditSiteColours()) }) : null;
  const noneLabel = h("span", { class: "hm-bs-none-l hm-muted", text: "None" });
  const opts = palette.colors.map((c) => {
    const dot = h("span", { class: "hm-bs-sw", "aria-hidden": "true" });
    paintSw(dot, c.hex || c.css);
    return { c, b: h("button", { type: "button", class: "hm-bs-copt", "aria-pressed": "false", title: `${c.label ?? c.name} ${c.hex}`.trim() }, dot, h("span", { text: c.label ?? c.name }), h("span", { class: "hm-muted", text: c.hex })) };
  });
  const panel = h("div", { class: "hm-popover-panel", id: popId, popover: "auto", role: "dialog", "aria-label": `${name} colour` },
    h("div", { class: "hm-popover-panel__body" },
      h("p", { class: "hm-bs-pal__t", text: "Site colours" }),
      h("div", { class: "hm-bs-copts" }, ...opts.map((o) => o.b)),
      recent,
      h("label", { class: "hm-bs-custom" }, h("span", { text: "Custom" }), noneLabel, native),
      hex, err, clear, site));
  const el = h("div", { class: "hm-bs-colour" }, swatch, panel);

  let current = null; // the value set here, for Clear
  const close = () => panel.matches(":popover-open") && panel.hidePopover();
  const take = (v, opt) => {
    const ok = apply(v, opt);
    err.hidden = ok || v === null;
    return ok;
  };
  function sync(own, shown) {
    current = own;
    const v = own ?? shown ?? null;
    const token = v !== null && Object.hasOwn(entry.tokens ?? {}, v) ? v : null;
    const resolved = token ? (palette.colors.find((c) => c.name === token)?.hex ?? "") : (v ?? "");
    paintSw(swatch, resolved || null);
    swatch.toggleAttribute("data-empty", !resolved);
    swatch.toggleAttribute("data-none", v === null); // the word None is shown only when the name says so too
    swatch.setAttribute("aria-label", v === null ? `${name}, None, not set` : `${name}, ${token ? (palette.colors.find((c) => c.name === token)?.label ?? token) : "custom"}, ${resolved || v}${own === null ? ", inherited" : ""}`);
    for (const { c, b } of opts) b.setAttribute("aria-pressed", String(own === c.name));
    native.value = hexFor(v, palette) || "#000000";
    native.toggleAttribute("data-empty", own === null || !!token);
    noneLabel.hidden = !(own === null || !!token);
    paintRecent();
    if (document.activeElement !== hex) hex.value = own !== null && !token ? own : resolved;
  }

  for (const { c, b } of opts)
    b.addEventListener("click", () => {
      if (take(c.name)) close();
    });
  const drag = (final) => () => {
    session ??= `s:${id}:${++drags}`;
    take(native.value, { coalesce: session });
    if (final) (session = null), remember(native.value);
  };
  native.addEventListener("input", drag(false));
  native.addEventListener("change", drag(true));
  for (const ev of ["pointercancel", "blur"]) native.addEventListener(ev, () => (session = null));
  hex.addEventListener("change", () => take(hex.value.trim().toLowerCase() || null) && remember(hex.value.trim().toLowerCase()));
  clear.addEventListener("click", () => {
    if (current === null || take(null)) close(); // nothing set here: nothing to commit (no empty undo step)
  });
  panel.addEventListener("keydown", (e) => {
    if (e.key !== "Tab") return;
    const f = [...panel.querySelectorAll(FOCUSABLE)].filter((x) => !x.closest("[hidden]"));
    const [first, last] = [f[0], f.at(-1)];
    if (e.shiftKey && document.activeElement === first) {
      e.preventDefault();
      last.focus();
    } else if (!e.shiftKey && document.activeElement === last) {
      e.preventDefault();
      first.focus();
    }
  });
  panel.addEventListener("toggle", (e) => (openColour = e.newState === "open" ? entry.key : null));
  if (openColour === entry.key)
    queueMicrotask(() => {
      if (swatch.isConnected && (!document.activeElement || document.activeElement === document.body)) swatch.focus();
      openColour = null;
    });
  return { el, hex, sync, close };
}
