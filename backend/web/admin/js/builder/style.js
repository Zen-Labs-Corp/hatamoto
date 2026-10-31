// The Style tab: controls generated from the server's StyleSpec table (GET
// <admin>/builder/stylespec), never from a client copy of the vocabulary. The pure
// helpers (groupsFor, control, check) are node-tested; the breakpoint helpers live in
// builder/bp (first paint), this module is a lazy chunk. mountStyle builds the DOM. Values are validated with the table (styleCSS) before a commit.
import { styleCSS } from "@hm/canvas/host";
import { h } from "@hm/builder/dom";
import { BP_NAME, BPS, NAMES, atLabel, ownValue, inheritedValue, hiddenAt } from "@hm/builder/bp";
import { showOnWrites } from "@hm/builder/showon";
import { contextLine, stateChoices } from "@hm/builder/inspctx";
import { rowState, groupDot, isLength, buildPalette, linkSets, probe, splitValue, joinValue, stepFor, boundsFor } from "@hm/builder/controls";
import { row, lengthControl, colourControl, stopSlider } from "@hm/builder/controlsui";
import { boxModel } from "@hm/builder/boxmodel";
import { presetOf, presetValueAt, presetStateValueAt } from "@hm/builder/presetsrc";
import { swatchRow } from "@hm/builder/swatches";
import { figmaGroups, groupHasValue, initialOpen, canScrub, SHORT } from "@hm/builder/inspgroups";

// Labels of the keys added by StyleSpec v2. They live here, in the lazy chunk, so first paint does not carry them;
// the first-paint inspector reads NAMES[key] ?? key and runs after this chunk has loaded.
const NEW_NAMES = {
  ml: "Margin left", mr: "Margin right", h: "Height", maxh: "Max height", minw: "Min width", aspect: "Aspect ratio", fit: "Image fit", objpos: "Image position", overflow: "Overflow",
  pos: "Position", top: "Offset top", right: "Offset right", bottom: "Offset bottom", left: "Offset left", z: "Stack order", scrollmt: "Scroll margin top",
  ff: "Font", fw: "Font weight", lh: "Line height", ls: "Letter spacing", tt: "Text case", td: "Text decoration", fst: "Font style",
  bw: "Border width", bwt: "Border width top", bwr: "Border width right", bwb: "Border width bottom", bwl: "Border width left", bs: "Border style", bc: "Border colour",
  rtl: "Corner top left", rtr: "Corner top right", rbr: "Corner bottom right", rbl: "Corner bottom left",
  bgimg: "Background image", bgpos: "Background position", bgsize: "Background size", bgrep: "Background repeat", bgatt: "Background attachment", grad: "Gradient",
  tx: "Move X", ty: "Move Y", sc: "Scale", rot: "Rotate", skx: "Skew X", sky: "Skew Y", torigin: "Transform origin",
  blur: "Blur", bright: "Brightness", contrast: "Contrast", sat: "Saturation", gray: "Grayscale", hue: "Hue rotate", bblur: "Backdrop blur", blend: "Blend mode",
  trans: "Transition", tdur: "Transition duration", tease: "Transition easing", tdelay: "Transition delay",
  op: "Opacity", display: "Display", order: "Order", rgap: "Row gap", cgap: "Column gap", gtc: "Column tracks", cs: "Column start", rs: "Row span", js: "Justify self", ac: "Align content", shadow: "Shadow",
};
Object.assign(NAMES, NEW_NAMES);

// What a refused value should have been, by kind; a hint is shown as the placeholder of free-text keys.
const ERRORS = {
  enum: "Pick one of the listed values.",
  media: "Use an image path from the media library, like /media/photo.jpg.",
  grad: "Use linear-180-000-accent or radial-fff-000: an angle, then colours (tokens or hex without #).",
  gtc: "Use up to six tracks, like 1fr 2fr.",
  lit: "Use a suggestion or a valid number or length.",
  color: "Use a palette colour or a hex value like #1e293b.",
};
/** The hint for a refused value of entry: the count range and the scroll-margin length are named in the message. */
function errorHint(entry) {
  if (entry.kind === "count") return `Use a whole number from ${entry.min ?? 1} to ${entry.max ?? 12}.`;
  if (entry.key === "scrollmt") return "Use a length like 80px or 5rem.";
  return ERRORS[entry.kind] ?? "Use a suggestion or a valid length.";
}
const HINTS = { grad: "linear-180-000-accent", bgimg: "/media/photo.jpg", gtc: "1fr 2fr" };
const GROUP_HINTS = { "Corners & border": "A width shows only once a border style is set." };
// Labels of the options whose value is not readable as it is (the docs name these).
const OPTION_NAMES = {
  shadow: { none: "None", sm: "Small", md: "Medium", lg: "Large" },
  trans: { none: "None", colors: "Colors", opacity: "Opacity", shadow: "Shadow", transform: "Transform", "transform-shadow": "Transform and shadow", all: "All" },
  tdur: { 75: "75 ms", 100: "100 ms", 150: "150 ms", 200: "200 ms", 300: "300 ms", 500: "500 ms", 700: "700 ms", 1000: "1000 ms" },
  tdelay: { 0: "0 ms", 75: "75 ms", 100: "100 ms", 150: "150 ms", 200: "200 ms", 300: "300 ms", 500: "500 ms" },
  tease: { linear: "Linear", ease: "Ease", "ease-in": "Ease in", "ease-out": "Ease out", "ease-in-out": "Ease in and out" },
};
// Enum keys edited as a row of toggle buttons (the others stay selects).
const BUTTON_KEYS = new Set(["dir", "ai", "jc", "wrap", "as"]);
// Count keys edited as presets 1..PRESETS plus a bounded number field.
const PRESETS = 4;
// Parents whose children are laid out by the container controls (span, align self).
const HOSTS = new Set(["container", "columns"]);

const FLEX = ["dir", "ai", "jc", "ac", "wrap", "gap", "rgap", "cgap"];
const GRID = ["cols", "gtc", "rows", "gap", "rgap", "cgap", "ai", "jc", "ac"];
const OFFSETS = ["top", "right", "bottom", "left", "z", "scrollmt"];

/** Whether any breakpoint of node positions it (offsets and stack order only matter then). */
export const positioned = (node) => Object.values(node.s ?? {}).some((s) => s?.pos && s.pos !== "static");

// Group name, keys (a list, or a function of node and parent), and whether it applies.
const GROUPS = [
  ["Layout", (n) => (n.p?.layout === "grid" ? GRID : FLEX), (n) => n.t === "container"],
  ["In its parent", (_n, parent) => (parent.t === "columns" || parent.p?.layout === "grid" ? ["span", "cs", "rs", "as", "js", "order"] : ["as", "order"]), (_n, parent) => !!parent && HOSTS.has(parent.t)],
  ["Spacing", ["pt", "pb", "pl", "pr", "mt", "mb", "ml", "mr"], () => true],
  ["Size", ["w", "minw", "maxw", "h", "minh", "maxh", "aspect", "fit", "objpos", "overflow"], () => true],
  ["Position", (n) => ["pos", ...(positioned(n) ? OFFSETS : [])], () => true],
  ["Typography", ["ff", "fs", "fw", "lh", "ls", "ta", "tt", "td", "fst", "color"], () => true],
  ["Background", ["bg", "bgimg", "grad", "bgsize", "bgpos", "bgrep", "bgatt"], () => true],
  ["Border", ["radius", "rtl", "rtr", "rbr", "rbl", "bw", "bwt", "bwr", "bwb", "bwl", "bs", "bc"], () => true],
  ["Effects", ["shadow", "op", "tx", "ty", "sc"], () => true],
  ["Transform", ["rot", "skx", "sky", "torigin"], () => true],
  ["Filter", ["blur", "bright", "contrast", "sat", "gray", "hue", "blend", "bblur"], () => true],
  ["Transition", ["trans", "tdur", "tease", "tdelay"], () => true], // states:false in the table, so only the Normal view lists them
  ["Visibility", ["display"], () => true],
];

/** [{name, entries}] of the table entries that apply to node (inside parent, or null at the top), in display order.
 * In a state (hover, focus, active, parent-hover) only the entries the table flags `states` are listed. `skip` is a set of keys left out. */
export function groupsFor(table, node, parent = null, state = "", skip = new Set()) {
  const by = new Map(table.filter((e) => (!state || e.states) && !skip.has(e.key)).map((e) => [e.key, e]));
  return GROUPS.filter(([, , when]) => when(node, parent))
    .map(([name, keys]) => ({ name, entries: (typeof keys === "function" ? keys(node, parent) : keys).map((k) => by.get(k)).filter(Boolean) }))
    .filter((g) => g.entries.length);
}

/**
 * How an entry is edited: number + unit + slider (lengths), toggle buttons (small enums, count presets with a number
 * field), a select of fixed values, or free text with token suggestions.
 */
export function control(entry) {
  const names = entry.enum ? Object.keys(entry.enum) : [];
  if (names.length && names.every((v) => v !== "" && !Number.isNaN(Number(v)))) names.sort((a, b) => a - b); // object keys put whole numbers first: opacity would list 0, 1, 0.05…
  if (entry.kind === "enum" && BUTTON_KEYS.has(entry.key)) return { kind: "buttons", options: names.map((v) => [v, v]) };
  if (entry.kind === "enum") return { kind: "select", options: names.map((v) => [v, OPTION_NAMES[entry.key]?.[v] ?? v]) };
  if (entry.kind === "count" || entry.kind === "cols") {
    const min = entry.min ?? 1;
    const max = entry.max ?? 12;
    const presets = [];
    if (min >= 1) for (let i = min; i <= Math.min(max, min + PRESETS - 1); i++) presets.push([String(i), String(i)]); // z and order: the number field only
    return { kind: "buttons", options: presets, min, max, number: true };
  }
  if (isLength(entry)) return { kind: "length" };
  if (entry.kind === "color") return { kind: "colour" };
  return { kind: "text", suggestions: Object.keys(entry.tokens ?? {}) };
}

/** Whether the table allows value for entry. */
export const check = (entry, value) => styleCSS(entry, value) !== null;

export { stateChoices as stateOptions };

/** The nearest value inherited at a style-map key (a breakpoint, or a breakpoint plus a state): wider breakpoints of the same state first, then Normal at this width and wider. */
export function inheritedAt(node, at, key) {
  const [b, st] = at.split(":");
  if (!st) return inheritedValue(node, b, key);
  const i = BPS.indexOf(b);
  for (const k of [...BPS.slice(0, i).reverse().map((x) => `${x}:${st}`), ...BPS.slice(0, i + 1).reverse()]) {
    const value = ownValue(node, k, key);
    if (value !== null) return { value, bp: k };
  }
  return null;
}

// Which groups the user opened or closed, kept for the page session (not stored): a group otherwise starts open only when it has a value set here.
const openGroups = new Map();

let uid = 0;
let refocus = false; // a State choice remounts the panel: the new State select takes focus back

/**
 * mountStyle({node, parent, table, bp, user, display, presets, palette, commit, commitMany, announce}); display is the kind's natural display (boot kinds table) -> element. presets is the boot presets: a row whose value only the node's class gives says "From class .<name>"; a value that differs from the class's says "Custom value".
 * commitMany([{key, value, bp?}], bp) -> bool is the same as one undo step (Show on writes several widths at once). commit(key, value|null, bp?) -> bool is
 * the screen's setStyle command (bp defaults to the one being edited); a refused value is
 * flagged and never committed. Controls update in place after a commit: nothing redraws.
 */
export function mountStyle({ node, parent = null, table, bp: device, state = "", onState = () => {}, display, presets = [], palette: bootPalette = null, user = "", onEditSiteColours = null, commit: commitStyle, commitMany, announce = () => {} }) {
  if (!table) return h("p", { class: "hm-muted", text: "Loading style options…" });
  const bp = state ? `${device}:${state}` : device; // the style-map key every control reads and writes
  const lab = (k) => (state && !k.includes(":") ? `Normal, ${atLabel(k)}` : atLabel(k));
  const pick = (id) => {
    refocus = true;
    const name = stateChoices(parent).find(([s]) => s === id)?.[1] ?? id;
    announce(id ? `State: ${name}. Canvas shows ${name.toLowerCase()}.` : "State: Normal.");
    onState(id);
  };
  const root = h("div", { class: "hm-bs-style" }, contextLine({ device, state, choices: stateChoices(parent), onState: pick }));
  const preset = presetOf(node, presets);
  const palette = buildPalette(bootPalette, table); // site colours, spacing and font sizes: token labels and swatches
  const cur = { ...node, s: Object.fromEntries(Object.entries(node.s ?? {}).map(([b, m]) => [b, { ...m }])) }; // follows the commits made here
  const remember = (key, value, at) => {
    const m = (cur.s[at] ??= {});
    if (value === null) delete m[key];
    else m[key] = value;
  };
  /** After a hide or display write the two Visibility controls must agree: the Show on switches repaint in place, the "Show as" field is rebuilt (unless it made the write). */
  function syncVisibility(keys, from) {
    if (!keys.some((k) => k === "hide" || k === "display")) return;
    visibility.paint?.();
    if (from !== "display" && visibility.show) {
      const next = field(visibility.show.entry);
      visibility.show.box.replaceWith(next);
      visibility.show.box = next;
    }
  }
  /** "Change Font size (Tablet)": the history label of a write at a width and state; several keys read "Change style". */
  const styleLabel = (keys, at) => (keys.length === 1 ? `Change ${NAMES[keys[0]] ?? keys[0]}${at === "base" ? "" : ` (${atLabel(at)})`}` : "Change style");
  /** The screen's commit, remembering the value so the Position group can show its offsets once a pos is set. */
  function commit(key, value, at = bp, opts) {
    if (!commitStyle(key, value, at, { label: styleLabel([key], at), ...opts })) return false;
    remember(key, value, at);
    if (key === "pos") showOffsets();
    syncVisibility([key], key);
    paintGroups();
    return true;
  }
  /** Several writes as one commit (one undo step); a write may carry its own bp, and a null that has nothing to clear is dropped. */
  function commitBatch(writes, at, opts) {
    const todo = writes.filter(({ key, value, bp: b = at }) => !(value === null && cur.s[b]?.[key] === undefined));
    if (!todo.length) return true;
    const labelled = { label: styleLabel([...new Set(todo.map((w) => w.key))], new Set(todo.map((w) => w.bp ?? at)).size === 1 ? (todo[0].bp ?? at) : "base"), ...opts };
    if (!(commitMany ? commitMany(todo, at, labelled) : todo.every(({ key, value, bp: b = at }) => commitStyle(key, value, b, labelled)))) return false;
    for (const { key, value, bp: b = at } of todo) remember(key, value, b);
    syncVisibility(todo.map((w) => w.key), "hide");
    paintGroups();
    return true;
  }
  /** The group headers' "set here" dot: any key of the group has a value at the style-map key being edited. */
  function paintGroups() {
    for (const { entries, dot, vh } of groups) dot.hidden = vh.hidden = !groupDot(entries, (k) => ownValue(cur, bp, k));
    for (const b of boxModels) b.paint();
  }
  const visibility = { paint: null, show: null }; // the Show on paint function and the "Show as" field
  const groups = []; // [{entries, dot}] of the group headers
  const boxModels = []; // the Spacing group's box model
  let position = null; // the Position block: its pos field stays, the offsets after it follow pos
  for (const g of figmaGroups(groupsFor(table, cur, parent, state))) {
    const dot = h("span", { class: "hm-bs-dot", "data-dot": "set", hidden: true, "aria-hidden": "true" });
    const vh = h("span", { class: "hm-visually-hidden", hidden: true, text: "A value is set here" });
    const gid = `hm-bs-g${++uid}`;
    const open = openGroups.get(g.name) ?? initialOpen(g.name, groupHasValue(g.entries, (k) => ownValue(cur, bp, k)), cur);
    const head = h("button", { type: "button", class: "hm-bs-grp__t", id: `${gid}-t`, "aria-expanded": String(open), "aria-controls": `${gid}-r` }, h("span", { text: g.name }), dot, vh);
    const body = h("div", { class: "hm-bs-grp__b", id: `${gid}-r`, hidden: !open }); // collapsed, not removed: the controls keep painting, so a closed header's dot stays right
    head.addEventListener("click", () => {
      const now = head.getAttribute("aria-expanded") !== "true";
      head.setAttribute("aria-expanded", String(now));
      body.hidden = !now;
      openGroups.set(g.name, now);
    });
    root.append(h("section", { class: "hm-bs-grp", "aria-labelledby": `${gid}-t` }, h("header", {}, head), body));
    groups.push({ entries: g.entries, dot, vh });
    if (GROUP_HINTS[g.name]) body.append(h("p", { class: "hm-muted", text: GROUP_HINTS[g.name] }));
    if (g.name === "Layout" && !state && ["hide", "display"].every((k) => table.some((e) => e.key === k))) body.append(showOnRow());
    for (const sub of g.sub) {
      const box = linkSets(sub.entries.map((e) => e.key)).length ? spacingBox(sub.entries) : null;
      const boxes = [...(box ? [box.el] : []), ...sub.entries.filter((e) => !box?.keys.includes(e.key)).map((e) => field(e))];
      sub.entries.forEach((e, i) => {
        if (e.key === "display") visibility.show = { entry: e, box: boxes[i] }; // the display block has no box model, so the indices still line up
      });
      const block = h("div", { class: "hm-bs-grp__sub" }, ...(g.sub.length > 1 && sub.name !== g.name ? [h("h4", { class: "hm-bs-subhead", text: sub.name })] : []), ...boxes);
      body.append(block);
      if (sub.name === "Position") position = { fs: block, boxes };
    }
  }
  paintGroups();
  if (refocus) {
    refocus = false;
    queueMicrotask(() => root.querySelector('select[aria-label="State"]')?.focus()); // after the caller has attached the panel
  }
  return root;

  function showOffsets() {
    if (!position) return;
    const want = groupsFor(table, cur, parent).find((g) => g.name === "Position").entries.slice(1);
    if ((want.length > 0) === (position.boxes.length > 1)) return;
    for (const b of position.boxes.splice(1)) b.remove();
    const added = want.map((e) => field(e));
    position.fs.append(...added);
    position.boxes.push(...added);
    if (added.length) announce("Offsets added to Position");
  }

  /** The where-from state of a key, as the row's dot and source label read it. */
  function stateFor(key, own = ownValue(cur, bp, key)) {
    const inh = inheritedAt(cur, bp, key);
    const presetState = state ? presetStateValueAt(preset, device, state, key) : null;
    const presetVal = presetValueAt(preset, device, "", key);
    return {
      ...rowState({
        own,
        inh: inh && { value: inh.value, from: lab(inh.bp), normal: state !== "" && !inh.bp.includes(":") },
        presetState,
        presetVal,
        presetName: preset?.name,
      }),
      classValue: state ? presetState : presetVal, // what the class gives at the style-map key being edited
    };
  }

  /** The Spacing group as margin and padding rings; every write goes through commit / commitBatch like a row's. */
  function spacingBox(entries) {
    const box = boxModel({
      entries,
      name: (k) => NAMES[k] ?? k,
      palette,
      stateOf: stateFor,
      commit: (key, value, opts) => commit(key, value, bp, opts),
      commitBatch: (writes, opts) => commitBatch(writes, bp, opts),
      valid: check,
      hint: errorHint(entries[0]),
    });
    boxModels.push(box);
    return box;
  }

  /** Show on: one switch per width (on = shown there), independent of each other; the width being edited is ringed. */
  function showOnRow() {
    const id = `hm-bs-s${++uid}`;
    const box = h("div", { class: "hm-field hm-bs-field hm-bs-showon", role: "group", "aria-labelledby": `${id}-l` }, h("span", { class: "hm-field__label", id: `${id}-l`, text: "Show on" }));
    const rows = BPS.map((b) => {
      const cb = h("input", { id: `${id}-${b}`, type: "checkbox", class: "hm-field__box", role: "switch", "aria-current": b === device ? "true" : false });
      cb.addEventListener("change", () => {
        const on = cb.checked;
        const writes = showOnWrites(cur, b, on, display);
        if (!commitBatch(writes, device, { label: `${on ? "Show" : "Hide"} on ${BP_NAME[b]}` })) cb.checked = !on;
        else announce(`${on ? "Shown" : "Hidden"} on ${BP_NAME[b]}`);
        paint();
      });
      box.append(h("label", { class: "hm-field__check hm-switch", for: cb.id }, cb, h("span", { text: BP_NAME[b] })));
      return { b, cb };
    });
    function paint() {
      for (const { b, cb } of rows) cb.checked = !hiddenAt(cur, b);
    }
    paint();
    visibility.paint = paint;
    return box;
  }

  function field(entry, title) {
    const id = `hm-bs-s${++uid}`;
    const c = control(entry);
    const name = title ?? NAMES[entry.key] ?? entry.key;
    let own = ownValue(cur, bp, entry.key);
    const unsetSrc = stateFor(entry.key, null); // where the value comes from when nothing is set here (the server's cascade)
    const inh = unsetSrc.source === "inherited" ? unsetSrc : null;
    const grouped = c.kind === "buttons";
    const status = h("span", { class: "hm-visually-hidden hm-bs-status", id: `${id}-s` }); // the source chip says it on screen; this is the full sentence for a screen reader
    const note = h("span", { class: "hm-bs-err", id: `${id}-e`, hidden: true, text: `That value is not allowed here. ${errorHint(entry)}` });
    const describe = `${id}-s ${id}-e`;
    const fromPreset = unsetSrc.source === "preset" ? unsetSrc.value : null;
    const unset = inh ? `Inherited: ${inh.value}` : "Not set";
    let el; // the select or text input, or the row of a button group
    let num = null; // the number field of a count group
    let box = null; // the field's row, set once built
    let sync = () => {}; // repaints the control from `own`
    let len = null; // the number + unit + slider control of a length key
    let col = null; // the swatch and popover of a colour key
    let stops = null; // the slider of an enum with ordered stops (opacity)
    let pills = null; // the Fill group's theme colour pills (the background only)

    if (c.kind === "select") {
      el = h("select", { id, class: "hm-field__control", "aria-describedby": describe }, h("option", { value: "", text: unset }));
      for (const [v, label] of c.options) el.append(h("option", { value: v, text: label }));
      sync = () => {
        if (own !== null && !c.options.some(([v]) => v === own) && !el.querySelector(`option[value="${CSS.escape(own)}"]`)) el.append(h("option", { value: own, text: `${own} (custom)` }));
        el.value = own ?? "";
        stops?.sync(own, inh?.value ?? fromPreset);
      };
      if (entry.key === "op") stops = stopSlider({ name, options: c.options.map(([v]) => v), apply });
    } else if (grouped) {
      const buttons = c.options.map(([v, label]) => h("button", { type: "button", class: "hm-bs-opt", "data-v": v, "aria-pressed": "false", text: label }));
      num = c.number ? h("input", { id, class: "hm-field__control hm-bs-num", type: "number", min: String(c.min), max: String(c.max), step: "1", "aria-label": c.options.length ? `${name}, other number` : name, "aria-describedby": describe }) : null;
      sync = () => {
        for (const b of buttons) {
          b.setAttribute("aria-pressed", String(own === b.dataset.v));
          b.classList.toggle("hm-bs-opt--inherited", own === null && inh?.value === b.dataset.v);
        }
        if (num) num.value = own !== null && !c.options.some(([v]) => v === own) ? own : "";
      };
      for (const b of buttons) {
        b.addEventListener("click", () => {
          const v = own === b.dataset.v ? null : b.dataset.v; // pressing the active one unsets it
          if (commit(entry.key, v)) {
            own = v;
            paint();
          }
        });
      }
      el = h("div", { class: "hm-bs-opts", role: "group", "aria-labelledby": `${id}-l`, "aria-describedby": describe }, ...buttons);
      if (num) el.append(num);
    } else if (c.kind === "length") {
      len = lengthControl({ id, entry, name, palette, describe, apply });
      el = len.el;
      sync = () => {
        len.sync(own, inh ? String(inh.value) : fromPreset !== null ? String(fromPreset) : "");
      };
    } else if (c.kind === "colour") {
      col = colourControl({ id, entry, name, palette, describe, apply, user, onEditSiteColours });
      el = col.el;
      pills = entry.key === "bg" ? swatchRow({ name, palette, current: own, apply: (token) => apply(token) }) : null;
      sync = () => {
        col.sync(own, inh?.value ?? fromPreset);
        pills?.sync(own);
      };
    } else {
      el = h("input", { id, class: "hm-field__control", type: "text", autocomplete: "off", spellcheck: "false", list: `${id}-l`, placeholder: inh ? String(inh.value) : fromPreset !== null ? String(fromPreset) : (HINTS[entry.key] ?? "Not set"), "aria-describedby": describe });
      sync = () => {
        el.value = own ?? "";
      };
    }
    const input = grouped ? num : len ? len.num : col ? col.hex : el;
    const datalist = c.kind === "text" ? h("datalist", { id: `${id}-l` }, ...c.suggestions.map((s) => h("option", { value: s }))) : null;

    function paint() {
      sync();
      const inherits = own === null && inh;
      el.classList.toggle("hm-bs-inherited", !!inherits);
      view.paint(stateFor(entry.key, own));
      status.textContent = own !== null ? `Set on ${atLabel(bp)}.` : inh ? `Inherited from ${inh.from}: ${inh.value}.` : fromPreset !== null ? `From class ${preset.label || preset.name}: ${fromPreset}.` : "";
    }
    /** Validates v (null clears) against the table, flags a refused one, commits and repaints; opts is the commit option (a drag session). */
    function apply(v, opts) {
      const bad = v !== null && !check(entry, v);
      if (bad) input.setAttribute("aria-invalid", "true");
      else input.removeAttribute("aria-invalid");
      note.hidden = !bad;
      if (bad || !commit(entry.key, v, bp, opts)) return false;
      own = v;
      paint();
      return true;
    }
    if (input && !len && !col) input.addEventListener("change", () => apply(input.value.trim() || null));
    const label = grouped ? h("span", { class: "hm-field__label", id: `${id}-l`, text: name }) : h("label", { class: "hm-field__label", for: id, text: name });
    const view = row({
      id,
      name,
      label,
      scrub: len && canScrub(entry) ? scrubOf(entry, () => own ?? inh?.value ?? fromPreset, apply, palette) : undefined,
      body: [el, pills?.el, stops?.el, datalist],
      status,
      note,
      onReset: () => {
        if (commit(entry.key, null)) {
          own = null;
          note.hidden = true;
          input?.removeAttribute("aria-invalid");
          paint();
        }
      },
    });
    box = view.el;
    paint();
    return box;
  }
}

/**
 * What dragging a length label does: starts from the value shown (set, inherited or from the class) with a token resolved
 * through the palette (space steps, font sizes) or the entry's own token map, and writes in that value's unit (px when none).
 * A value that is not a number (auto, custom) does not scrub. The unit, step and bounds are read when a drag starts.
 */
export function scrubOf(entry, shown, apply, palette = null) {
  const p = probe(entry);
  const fallback = p.units.includes("px") ? "px" : (p.units[0] ?? "");
  const resolve = (value) => {
    const s = splitValue(entry, value);
    if (s.kind !== "token") return s;
    const t = /^space-\d+$/.test(s.token) ? palette?.space?.[s.token.slice(6)] : entry.kind === "fs" ? (palette?.fs?.[s.token] ?? entry.tokens[s.token]) : entry.tokens[s.token];
    return t === undefined ? { kind: "custom" } : splitValue({ ...entry, tokens: {} }, t);
  };
  let at = { n: 0, unit: fallback };
  const get = () => {
    const s = resolve(shown());
    if (s.kind !== "num") return null;
    at = s.num === "" ? { n: 0, unit: fallback } : { n: Number(s.num), unit: s.unit };
    return at.n;
  };
  return { short: SHORT[entry.key], get, apply: (n, opts) => apply(joinValue({ kind: "num", num: String(n), unit: at.unit }), opts), step: () => stepFor(entry, at.unit), bounds: () => boundsFor(entry.key, entry, at.unit) };
}
