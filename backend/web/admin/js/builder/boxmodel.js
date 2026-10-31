// The spacing box model (a lazy chunk): padding nested in margin, one number per side. Each number is a text field
// (top, right, bottom, left is the DOM and tab order; CSS grid areas only place them): drag it to scrub (vertical for
// top and bottom, horizontal for left and right), click to type ("2rem" works), ArrowUp/ArrowDown step by 1 and 10
// with Shift. One link toggle makes the four sides of a ring move together; the 0/S/M/L/XL presets write the theme's
// spacing steps. A linked or preset write is one batch (one undo step) and a whole scrub is one undo step.
// A side keeps its own row state: set here is solid, inherited is muted and italic and the accessible name says so
// ("Padding top, 96px, inherited"); a "Reset <ring>" button clears the ring's sides set here.
// Hovering or focusing a padding side dispatches a bubbling "hm-pad-hover" event ({side} or {side: null}).
// The pure parts (ring order, link sets, typed values, scrub maths) are controls.js and scrub.js.
import { h, actButton } from "@hm/builder/dom";
import { linkSets, writesFor, stepFor, stepValue, boundsFor } from "@hm/builder/controls";
import { parseTyped, displayOf, presetSteps, attachScrub, axisOf } from "@hm/builder/scrub";

let ids = 0;
const title = (s) => s[0].toUpperCase() + s.slice(1);
const SIDES = ["top", "right", "bottom", "left"];
const NUM = /^(-?\d+(?:\.\d+)?)([a-z%]*)$/i;

/**
 * boxModel({entries, name, palette, stateOf, commit, commitBatch, valid, hint}) -> {el, keys, paint()}.
 * entries: the table entries of the group; name(key) its label ("Padding top"); stateOf(key) -> controls.rowState;
 * commit(key, value, opts) and commitBatch([{key, value}], opts) -> bool write at the style-map key being edited;
 * valid(entry, value) -> bool the table's verdict; hint the text of a refused value. keys: the keys it draws (the rest of the group stays single rows).
 * With both rings the padding ring is drawn inside the margin ring.
 */
export function boxModel({ entries, name, palette, stateOf, commit, commitBatch, valid, hint }) {
  const by = new Map(entries.map((e) => [e.key, e]));
  const rings = linkSets(entries.map((e) => e.key));
  const own = (k) => {
    const s = stateOf(k);
    return s.dot === "set" ? s.value : null;
  };
  /** The number and unit a side shows (own, else inherited), a space token resolved through the palette; auto and unknown values count as 0px. */
  const numberOf = (k) => {
    let v = stateOf(k).value;
    v = v === null || v === undefined ? "" : String(v);
    if (/^space-\d+$/.test(v)) v = palette?.space?.[v.slice(6)] ?? v;
    const m = NUM.exec(v);
    return m ? { n: Number(m[1]), unit: m[2].toLowerCase() || "px" } : { n: 0, unit: "px" };
  };
  const painters = [];
  // Starts linked when something is set and every ring with a value has the same one on all four sides.
  const alike = ({ keys }) => keys.every((k) => own(k) === own(keys[0]));
  const linked = { on: rings.some(({ keys }) => keys.some((k) => own(k) !== null)) && rings.every((r) => r.keys.every((k) => own(k) === null) || alike(r)) };
  const writer = new Map(); // ring name -> writeKeys(list, value, opts)
  const built = rings.map(({ name: ring, keys }) => {
    const rid = `hm-bs-b${++ids}`;
    const label = title(ring);
    const err = h("p", { class: "hm-bs-err", id: `${rid}-e`, hidden: true, text: `That value is not allowed here. ${hint}` });
    const inputs = [];
    /** Writes value (null clears) to every key of list: one commit, or one batch (one undo step) for several. A refused value marks the sides and writes nothing. */
    const writeKeys = (list, v, opts) => {
      const bad = v !== null && !list.every((k) => valid(by.get(k), v));
      for (const [i, k] of keys.entries()) {
        if (bad && list.includes(k)) inputs[i].setAttribute("aria-invalid", "true");
        else inputs[i].removeAttribute("aria-invalid");
      }
      err.hidden = !bad;
      if (bad) return false;
      return list.length > 1 ? commitBatch(writesFor(list, v), opts) : commit(list[0], v, opts);
    };
    writer.set(ring, writeKeys);
    const reset = actButton("", { class: "hm-bs-reset", "aria-label": `Reset ${ring}`, hidden: true });
    reset.addEventListener("click", () => commitBatch(keys.filter((k) => own(k) !== null).map((key) => ({ key, value: null }))));

    keys.forEach((key, i) => {
      const entry = by.get(key);
      const input = h("input", { class: `hm-bs-bm__n hm-bs-bm__n--${SIDES[i]}`, type: "text", inputmode: "decimal", autocomplete: "off", spellcheck: "false", "data-side": SIDES[i], "aria-describedby": `${rid}-e` });
      const write = (v, opts) => writeKeys(linked.on && v !== null ? keys : [key], v, opts);
      input.addEventListener("change", () => {
        const typed = parseTyped(entry, input.value);
        if (!typed.ok) {
          input.setAttribute("aria-invalid", "true");
          err.hidden = false;
          return;
        }
        write(typed.value);
      });
      input.addEventListener("keydown", (e) => {
        if ((e.key !== "ArrowUp" && e.key !== "ArrowDown") || e.altKey || e.ctrlKey || e.metaKey) return;
        e.preventDefault();
        const { n, unit } = numberOf(key);
        const next = stepValue(n, e.key === "ArrowUp" ? 1 : -1, stepFor(entry, unit), e.shiftKey, boundsFor(key, entry, unit));
        write(next === 0 ? "0" : `${next}${unit}`); // each press is its own undo step
      });
      // A drag works in px (1 unit per pixel): a rem or em start is converted, any other unit is kept.
      let unit = "px";
      attachScrub(input, {
        axis: axisOf(SIDES[i]),
        session: "bm",
        bounds: boundsFor(key, entry, "px"),
        get: () => {
          const s = numberOf(key);
          const rel = s.unit === "rem" || s.unit === "em";
          unit = rel ? "px" : s.unit;
          return rel ? s.n * 16 : s.n;
        },
        apply: (n, opts) => write(n === 0 ? "0" : `${n}${unit}`, opts),
      });
      if (ring === "padding" && typeof CustomEvent === "function") {
        // The canvas hatches the padding of the selected section while its field is hovered or focused (contract: keep this event).
        const hover = (side) => () => input.dispatchEvent(new CustomEvent("hm-pad-hover", { bubbles: true, detail: { side } }));
        for (const t of ["pointerenter", "focusin"]) input.addEventListener(t, hover(SIDES[i]));
        for (const t of ["pointerleave", "focusout"]) input.addEventListener(t, hover(null));
      }
      painters.push(() => {
        const s = stateOf(key);
        const shown = displayOf(s.dot === "none" ? null : s.value, palette);
        input.value = shown.text + shown.unit;
        input.dataset.dot = s.dot;
        input.removeAttribute("aria-invalid");
        const spoken = /\d$/.test(shown.text) ? `${shown.text}${shown.unit || "px"}` : shown.text;
        input.setAttribute("aria-label", `${name(key)}, ${spoken}${s.dot === "inherited" ? ", inherited" : s.dot === "none" ? ", not set" : ""}`);
      });
      inputs.push(input);
    });
    painters.push(() => {
      reset.hidden = !keys.some((k) => own(k) !== null);
      err.hidden = true;
    });
    const core = h("div", { class: "hm-bs-bm__c" }); // the element, or the padding ring inside the margin ring
    const el = h("div", { class: `hm-bs-bm__${ring === "margin" ? "m" : "p"}`, role: "group", "aria-label": label }, h("span", { class: "hm-bs-bm__t", text: label }), ...inputs, reset, core, err);
    return { el, core };
  });

  const [outer, inner] = built;
  if (inner) {
    outer.core.append(inner.el);
    inner.core.append(h("div", { class: "hm-bs-bm__el", "aria-hidden": "true", text: "Element" }));
  } else if (outer) outer.core.append(h("div", { class: "hm-bs-bm__el", "aria-hidden": "true", text: "Element" }));

  // Presets set the padding: all four sides when linked, else top and bottom.
  const pad = rings.find((r) => r.name === "padding");
  const tools = [];
  if (pad) {
    const steps = presetSteps(palette);
    const presets = steps.map(({ label, value }) => {
      const b = h("button", { type: "button", class: "hm-bs-opt hm-bs-quick", "aria-label": `Padding: ${label}`, "aria-pressed": "false", text: label });
      b.addEventListener("click", () => writer.get("padding")(linked.on ? pad.keys : [pad.keys[0], pad.keys[2]], value));
      return { b, value };
    });
    painters.push(() => {
      const sides = linked.on ? pad.keys : [pad.keys[0], pad.keys[2]];
      for (const { b, value } of presets) b.setAttribute("aria-pressed", String(sides.every((k) => own(k) === value)));
    });
    tools.push(h("div", { class: "hm-bs-bm__presets", role: "group", "aria-label": "Padding presets" }, ...presets.map((p) => p.b)));
  }
  if (rings.length) {
    const link = actButton("", { class: "hm-bs-linkbtn", "aria-label": "Link all sides", "aria-pressed": String(linked.on), title: "Link all sides" });
    link.addEventListener("click", () => {
      linked.on = !linked.on;
      link.setAttribute("aria-pressed", String(linked.on));
      painters.forEach((p) => p());
    });
    tools.push(link);
  }
  const el = h("div", { class: `hm-bs-bm${inner ? " hm-bs-bm--nested" : ""}` }, ...(tools.length ? [h("div", { class: "hm-bs-bm__tools" }, ...tools)] : []), ...(outer ? [outer.el] : []));
  return { el, keys: rings.flatMap((r) => r.keys), paint: () => painters.forEach((p) => p()) };
}
