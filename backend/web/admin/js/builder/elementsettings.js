// Element settings of the inspector (a lazy chunk): Link options and the image/gallery lightbox hints on the
// Content tab; Anchor, Accessible name and Tooltip on the Advanced tab. The controls are plain store commands
// through the inspector's `commit`, so undo covers them. The server validates again (422 on save).
import { h, actButton, warnNote, switchRow } from "@hm/builder/dom";
import { find, locate } from "@hm/builder/store";
import { label } from "@hm/builder/navigator";

const ANCHOR = /^[a-z][a-z0-9-]{0,63}$/;
const RELS = [["nofollow", "Nofollow"], ["sponsored", "Sponsored"], ["ugc", "User generated content"]];
let uid = 0;

function* walk(nodes) {
  for (const n of nodes ?? []) {
    yield n;
    yield* walk(n.c);
  }
}

/** Whether n's anchor prop is the page anchor: not when its plugin kind declares an `anchor` prop of its own (the core's universal one is then off, so `u` lacks it). kinds: the boot kinds list, null for all. */
const ownsAnchor = (n, kinds) => !kinds || (kinds.find((k) => k.kind === n.t)?.u ?? ["anchor"]).includes("anchor");

/** What is wrong with anchor v on node, or "" (the server's rules: grammar, reserved names, a node id, uniqueness). reserved: options.reserved. */
export function anchorProblem(v, node, doc, reserved = [], kinds = null) {
  if (v === "") return "";
  if (!ANCHOR.test(v)) return "Start with a letter; use lowercase letters, digits and hyphens.";
  // hm-* are the builder's own ids; *-strip-label the section strips (the server lists the rest)
  if (reserved.includes(v) || v.startsWith("hm-") || v.endsWith("-strip-label")) return `“${v}” is used by the page itself; choose another.`;
  const all = [...walk(doc?.nodes)];
  if (all.some((n) => n.id === v)) return `“${v}” is the id of an element on this page; choose another.`;
  if (all.some((n) => n.id !== node.id && ownsAnchor(n, kinds) && n.p?.anchor === v)) return `“${v}” is already used on this page.`;
  return "";
}

const INLINE_LINK = /<a[\s>/]/i;
const hasLink = (n, key = "href") => (typeof n.p?.[key] === "string" && n.p[key] !== "") || Boolean(n.b?.[key]);
/** Whether n renders (or may render) a link, button or other interactive content: the server's interactiveNode. kinds: boot kinds by name. */
function interactive(n, kinds) {
  if (kinds[n.t]?.ia) return true;
  switch (n.t) {
    case "image":
      return hasLink(n) || n.p?.lightbox === true;
    case "container":
      return hasLink(n);
    case "gallery":
      return n.p?.lightbox === true;
    case "heading":
    case "text":
      return INLINE_LINK.test(n.p?.html ?? "");
  }
  return false;
}
/** Whether image n opens in a lightbox (its own switch or its gallery's) with nothing to name the link by: no accessible name, no alt text (a decorative image has none), no caption. */
export function lightboxUnnamed(n, parent) {
  if (n?.t !== "image" || hasLink(n)) return false;
  if (!(n.p?.lightbox === true || (parent?.t === "gallery" && parent.p?.lightbox === true))) return false;
  const has = (key) => (typeof n.p?.[key] === "string" && n.p[key].trim() !== "") || Boolean(n.b?.[key]);
  return !(has("aria") || has("caption") || (n.p?.decorative !== true && has("alt")));
}

/** The editor's warning for node (an image, or a gallery with a lightbox), or "". */
export function lightboxWarning(node, parent) {
  if (node.t === "gallery") {
    const n = node.p?.lightbox === true ? (node.c ?? []).filter((c) => lightboxUnnamed(c, node)).length : 0;
    return n ? `${n} image${n === 1 ? "" : "s"} in this gallery open${n === 1 ? "s" : ""} in a lightbox without alt text or a caption. Screen readers cannot name the link: add one to each.` : "";
  }
  if (!lightboxUnnamed(node, parent)) return "";
  return node.p?.decorative === true ? "A decorative image has no name for its lightbox link. Add a caption, or untick decorative." : "This lightbox link has no name for screen readers. Add alt text or a caption.";
}

/** The first node below n that is or may be interactive (depth first), or null. */
export function interactiveIn(n, kinds) {
  for (const c of n.c ?? []) {
    if (interactive(c, kinds)) return c;
    const x = interactiveIn(c, kinds);
    if (x) return x;
  }
  return null;
}

/** Why a container cannot be a link, or "": a tag other than div, or interactive content inside (what the save would refuse). */
export function linkProblem(node, kinds) {
  if (node.t !== "container") return "";
  if (node.p?.tag && node.p.tag !== "div") return "A container with a link must use the div tag: change its tag on the Advanced tab first.";
  const x = interactiveIn(node, kinds);
  return x ? `A container with a link cannot hold interactive content: “${label(x)}” is inside it. Move it out first.` : "";
}

/** An inline error line for input: set(msg) shows it (aria-invalid, described by it), set("") removes every trace. */
function fieldError(input, errId, hintId = "") {
  const err = h("span", { class: "hm-bs-err", id: errId, hidden: true });
  input.after(err);
  const hint = hintId;
  return {
    el: err,
    set(msg) {
      err.textContent = msg;
      err.hidden = msg === "";
      if (msg) input.setAttribute("aria-invalid", "true");
      else input.removeAttribute("aria-invalid");
      input.setAttribute("aria-describedby", [hint, msg ? errId : ""].filter(Boolean).join(" "));
      if (!msg && !hint) input.removeAttribute("aria-describedby");
    },
  };
}

/** Replaces the node ids in a server message with the labels the navigator shows ("n0b73: a container…" -> "“Section”: a container…"). */
export function nameNodes(message, doc) {
  const named = {};
  const add = (ns) => ns?.forEach((n) => { named[n.id] = label(n); add(n.c); });
  add(doc?.nodes);
  return String(message ?? "").replace(/\b[A-Za-z][\w-]+\b/g, (w) => (named[w] ? `“${named[w]}”` : w));
}

/** The anchors of the page (own doc only: anchors inside blocks and parts are not visible here). */
export const anchorsOf = (doc, kinds = null) => [...walk(doc?.nodes)].filter((n) => ownsAnchor(n, kinds)).map((n) => n.p?.anchor).filter((a) => typeof a === "string" && a !== "");

/** The link field's choices: published pages, then "#anchor" of this page. */
export const linkChoices = (doc, pages = [], kinds = null) => [...pages, ...[...new Set(anchorsOf(doc, kinds))].sort().map((a) => `#${a}`)];

/** Joins rel tokens in the fixed order the server renders them; "" when none. */
export const joinRel = (tokens) => RELS.map(([t]) => t).filter((t) => tokens.includes(t)).join(" ");

const field = (id, name, el, ...rest) => h("div", { class: "hm-field hm-bs-field" }, h("label", { class: "hm-field__label", for: id, text: name }), el, ...rest);

function checkbox(name, checked, onchange) {
  const id = `hm-bs-es${++uid}`;
  const sw = switchRow(id, name); // every on/off setting of the inspector is the same switch (UI-03)
  const el = sw.input;
  el.checked = checked;
  el.addEventListener("change", () => onchange(el.checked));
  return { el, row: h("div", { class: "hm-field hm-bs-field hm-bs-field--check" }, sw.label) };
}

/**
 * Appends the Link options and the lightbox coupling to the Content panel of the drawn node.
 * ctx: {api: the inspector api, commit(ops, opts) -> bool}
 */
export function contentExtras(node, panel, { api, commit }) {
  if (!panel.isConnected) return;
  const props = api.kinds.find((k) => k.kind === node.t)?.props ?? [];
  const has = (key) => props.some((p) => p.key === key);
  const cur = () => find(api.getDoc(), node.id)?.p ?? {};
  const hrefInput = panel.querySelector('[data-prop="href"] input');
  if (hrefInput) {
    const id = `hm-bs-es${++uid}`;
    hrefInput.setAttribute("list", id);
    hrefInput.setAttribute("autocomplete", "off");
    const list = h("datalist", { id });
    const fill = () => list.replaceChildren(...linkChoices(api.getDoc(), api.options?.pages, api.kinds).map((o) => h("option", { value: o })));
    fill();
    hrefInput.addEventListener("focus", fill);
    hrefInput.after(list);
  }
  let group = null;
  if (has("target") && has("rel")) {
    group = h("div", { class: "hm-bs-linkopts", role: "group", "aria-label": "Link options" });
    const tab = checkbox("Open in new tab", cur().target === "_blank", (on) => commit([{ op: "setProp", id: node.id, key: "target", value: on ? "_blank" : null }], { label: "Change Open in new tab" }));
    tab.el.setAttribute("data-target", "");
    group.append(tab.row);
    const have = String(cur().rel ?? "").split(" ");
    for (const [token, name] of RELS) {
      const c = checkbox(name, have.includes(token), () => {
        const tokens = RELS.map(([t]) => t).filter((t) => group.querySelector(`[data-rel="${t}"]`).checked);
        commit([{ op: "setProp", id: node.id, key: "rel", value: joinRel(tokens) || null }], { label: "Change Link relationship" });
      });
      c.el.setAttribute("data-rel", token);
      group.append(c.row);
    }
    const href = panel.querySelector('[data-prop="href"]');
    if (href) href.after(group); // Link options sit right under the Link field
    else panel.append(group);
  }
  const lightbox = panel.querySelector('[data-prop="lightbox"] input');
  const kinds = Object.fromEntries(api.kinds.map((k) => [k.kind, k]));
  const linkErr = hrefInput ? fieldError(hrefInput, `hm-bs-es${++uid}-e`, hrefInput.getAttribute("aria-describedby") ?? "") : null;
  // The reason the disabled one of Link and Lightbox is off, as text (a title is out of reach for keyboard and screen readers).
  const noteId = `hm-bs-es${++uid}-n`;
  const note = lightbox ? h("p", { class: "hm-muted", id: noteId, hidden: true }) : null;
  if (note) panel.querySelector('[data-prop="lightbox"]').after(note);
  // A bound href is a link too: its value lives in the node's bindings, not in p.
  const isLinked = () => {
    const n = find(api.getDoc(), node.id);
    return hasLink(n ?? {});
  };
  const paint = () => {
    const p = cur();
    const linked = isLinked();
    if (group) group.hidden = !linked;
    if (node.t === "image" && lightbox && hrefInput) {
      const bound = Boolean(find(api.getDoc(), node.id)?.b?.href);
      lightbox.disabled = linked;
      hrefInput.disabled = p.lightbox === true || bound;
      const why = linked ? "This image has a link, so it cannot also open a lightbox. Clear the link to use one." : p.lightbox === true ? "Lightbox is on, so the image cannot also be a link. Turn it off to add a link." : "";
      note.textContent = why;
      note.hidden = why === "";
      for (const el of [lightbox, hrefInput]) {
        const rest = (el.getAttribute("aria-describedby") ?? "").split(" ").filter((t) => t && t !== noteId);
        if (el.disabled && why) rest.push(noteId);
        if (rest.length) el.setAttribute("aria-describedby", rest.join(" "));
        else el.removeAttribute("aria-describedby");
      }
    }
  };
  // The Link field: a container link the save would refuse is explained here and not stored; emptying the field
  // clears its options in the same commit (one undo step restores all three). Capture phase: this runs before the
  // field's own handler, which would commit the href alone.
  hrefInput?.addEventListener(
    "input",
    (e) => {
      if (hrefInput.value !== "") {
        const msg = linkProblem(find(api.getDoc(), node.id) ?? node, kinds);
        linkErr.set(msg);
        if (msg) e.stopImmediatePropagation();
        return;
      }
      linkErr.set("");
      const p = cur();
      // a plain div container takes no accessible name without a link: drop it in the same step
      const plainDiv = node.t === "container" && (!p.tag || p.tag === "div") && p.aria !== undefined;
      if (p.target === undefined && p.rel === undefined && !plainDiv) return;
      e.stopImmediatePropagation();
      const ops = ["href", "target", "rel", ...(plainDiv ? ["aria"] : [])].filter((key) => p[key] !== undefined).map((key) => ({ op: "setProp", id: node.id, key, value: null }));
      commit(ops, { label: "Clear link" });
      group?.querySelectorAll("input").forEach((c) => (c.checked = false));
      paint();
    },
    true,
  );
  if (node.t === "image" || node.t === "gallery") {
    const warn = warnNote("", { role: "status", hidden: true });
    const warnBody = warn.querySelector(".hm-callout__body") ?? warn; // the bare <p> when the page has no template
    const show = () => {
      const now = find(api.getDoc(), node.id);
      const msg = now ? lightboxWarning(now, locate(api.getDoc(), node.id)?.parent) : "";
      warnBody.textContent = msg;
      warn.hidden = msg === "";
    };
    show();
    panel.append(warn);
    panel.addEventListener("input", show);
    panel.addEventListener("change", show);
  }
  if (linkErr && isLinked()) linkErr.set(linkProblem(find(api.getDoc(), node.id) ?? node, kinds)); // an existing link that cannot be saved
  paint(); // never commits: selecting a node does not change it
  panel.addEventListener("input", paint);
  panel.addEventListener("change", paint);
}

/**
 * Appends Anchor, Accessible name and Tooltip to the Advanced panel of the drawn node.
 * ctx: {api, commit}
 */
export function advancedExtras(node, panel, { api, commit }) {
  if (!panel.isConnected) return;
  const keys = api.kinds.find((k) => k.kind === node.t)?.u ?? [];
  const doc = () => api.getDoc();
  const set = (key, value, label, opts) => commit([{ op: "setProp", id: node.id, key, value }], { label, ...opts });
  const text = (key, name, max, hint) => {
    const id = `hm-bs-es${++uid}`;
    const el = h("input", { id, type: "text", class: "hm-field__control", maxlength: String(max), autocomplete: "off", "aria-describedby": `${id}-h` });
    el.value = node.p?.[key] ?? "";
    const help = h("span", { class: "hm-muted", id: `${id}-h`, text: hint });
    return { id, el, help, row: field(id, name, el, help) };
  };
  const out = [];
  if (keys.includes("anchor")) {
    const a = text("anchor", "Anchor", 64, "Lowercase letters, digits and hyphens. Link to it with #anchor.");
    const err = fieldError(a.el, `${a.id}-e`, `${a.id}-h`);
    const copy = actButton("Copy #anchor", { hidden: !node.p?.anchor });
    a.el.addEventListener("input", () => {
      const v = (a.el.value = a.el.value.toLowerCase());
      const msg = anchorProblem(v, node, doc(), api.options?.reserved, api.kinds);
      err.set(msg);
      if (msg) return; // not stored: the save would refuse it
      set("anchor", v || null, "Change Anchor", { coalesce: true });
      copy.hidden = v === "";
    });
    copy.addEventListener("click", async () => {
      try {
        await navigator.clipboard.writeText(`#${find(doc(), node.id)?.p?.anchor ?? ""}`);
        api.announce("Anchor link copied");
      } catch {
        api.announce("Could not copy; select the anchor and copy it");
      }
    });
    out.push(h("div", { class: "hm-bs-anchor" }, a.row, copy));
    if (node.p?.v1) out.push(h("p", { class: "hm-muted", text: "Setting an anchor makes this page use the free-form layout." }));
  }
  const parent = locate(doc(), node.id)?.parent ?? null;
  const nameable = {
    image: node.p?.href || node.p?.lightbox || (parent?.t === "gallery" && parent.p?.lightbox),
    // a plain div has no role that takes a name: it needs a link or a landmark tag (as the server checks)
    container: node.p?.href || node.b?.href || (node.p?.tag && node.p.tag !== "div"),
  };
  const linked = node.t in nameable ? nameable[node.t] : true;
  if (keys.includes("aria") && linked) {
    const a = text("aria", "Accessible name", 200, "Read by screen readers instead of the visible text; start it with that text. Leave empty to use the text.");
    a.el.addEventListener("input", () => set("aria", a.el.value.trim() === "" ? null : a.el.value, "Change Accessible name", { coalesce: true }));
    out.push(a.row);
  }
  if (keys.includes("tip")) {
    const t = text("tip", "Tooltip", 200, "Shown when the pointer rests on the element.");
    t.el.addEventListener("input", () => set("tip", t.el.value.trim() === "" ? null : t.el.value, "Change Tooltip", { coalesce: true }));
    out.push(t.row);
  }
  panel.append(...out);
}
