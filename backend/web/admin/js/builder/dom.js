// A tiny element builder for the builder screen: h("button", {class: "bs-x", text: "Save", onclick}, ...children).
// Attributes go through setAttribute (never the style attribute: the admin CSP is nonce-only).
function put(e, props) {
  for (const [k, v] of Object.entries(props)) {
    if (v === false || v === null || v === undefined) continue;
    if (k === "text") e.textContent = v;
    else if (k.startsWith("on") && typeof v === "function") e.addEventListener(k.slice(2), v);
    else if (k === "class") e.setAttribute(k, [e.getAttribute?.(k), v].filter(Boolean).join(" ")); // keeps the classes a cloned template carries
    else e.setAttribute(k, v === true ? "" : v);
  }
  return e;
}

export function h(tag, props = {}, ...kids) {
  const e = put(document.createElement(tag), props);
  e.append(...kids.filter((k) => k !== null && k !== undefined && k !== false)); // a conditional child that is off adds nothing
  return e;
}

/** Clone of the server-rendered <template data-hm-bs-NAME> with `text` in SLOT. */
function stamp(name, slot, fallback, text, props) {
  const e = document.querySelector?.(`template[data-hm-bs-${name}]`)?.content.firstElementChild?.cloneNode(true);
  if (!e) return h(fallback, { text, ...props });
  e.querySelector(slot).textContent = text;
  return put(e, props);
}

export const actButton = (text, props = {}) => stamp("act", "span", "button", text, props);
/** Clone of the server-rendered plain badge <template data-hm-bs-chip> with `text`: a read-only label (device chip, source chips). */
export function chip(text, props = {}) {
  const e = document.querySelector?.("template[data-hm-bs-chip]")?.content.firstElementChild?.cloneNode(true);
  if (!e) return h("span", { class: "hm-bs-tag", text, ...props });
  e.textContent = text;
  return put(e, props);
}
/** A lone on/off setting as the admin's switch: {input, label} with a role=switch checkbox (a set of related options stays checkboxes). */
export function switchRow(id, text, props = {}) {
  const input = h("input", { id, type: "checkbox", class: "hm-field__box", role: "switch", ...props });
  return { input, label: h("label", { class: "hm-field__check hm-switch", for: id }, input, h("span", { text })) };
}
export const warnNote = (text, props = {}) => stamp("warn", ".hm-callout__body", "p", text, props);

export const $ = (root, sel) => root.querySelector(sel);
export const $$ = (root, sel) => Array.from(root.querySelectorAll(sel));

/**
 * For a popover with no invoker: a function that says, once it has closed, whether focus should go back to where the
 * person was. True unless focus had moved elsewhere before it closed (a click into another field), as in components.js.
 * It reads focus at beforetoggle, since the browser has already put focus back by the time toggle fires.
 */
export function focusKept(m) {
  let keep = true;
  m.addEventListener("beforetoggle", (e) => {
    if (e.newState === "closed") keep = m.contains(document.activeElement) || document.activeElement === document.body;
  });
  return () => keep;
}
