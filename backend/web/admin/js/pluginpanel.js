// Browse plugins and themes: a click on a card opens its detail page in the side panel (#plugin-panel).
// The card carries hx-get/hx-target/hx-sync (pages/plugins/browse.html, pages/theme/browse.html): htmx asks
// for the fragment of the same /about page the link points to and swaps it into #plugin-panel-body. Without
// script, or when the request fails, the link works as a plain link. Opening, Esc, the backdrop and returning
// focus are components.js. Each page's fragment is kept until the page reloads.
const SELECTOR = 'a[data-hm-open="plugin-panel"]';
const BODY = "plugin-panel-body";
const FOCUS = "input, select, textarea, button, [href]";
const FAILED = ["htmx:responseError", "htmx:sendError", "htmx:timeout"]; // never htmx:sendAbort: a newer click replaced it
const cache = new Map(); // request path -> fragment HTML
const card = (evt) => {
  const el = evt.detail?.requestConfig?.elt;
  return el?.matches?.(SELECTOR) ? el : null;
};
const dialog = () => document.getElementById("plugin-panel");

// A fragment may carry a [data-hm-panel-actions] block (opt-in; the plugin
// fragment has none): it is moved into the drawer footer, after Close, and
// removed again when the next fragment loads or the drawer closes.
const ACTIONS = "[data-hm-panel-actions]";
const clearActions = (dlg) => dlg.querySelectorAll(`.hm-dialog__foot ${ACTIONS}`).forEach((n) => n.remove());
document.addEventListener("close", (evt) => {
  if (evt.target.id === "plugin-panel") clearActions(evt.target);
}, true);

// Capture phase: a modified click (new tab, new window), or a page without htmx / showModal, stays a plain
// link. Stopping it here keeps htmx and components.js from taking it.
document.addEventListener("click", (evt) => {
  const link = evt.target.closest?.(SELECTOR);
  if (!link) return;
  const dlg = dialog();
  if (evt.defaultPrevented || evt.button !== 0 || evt.metaKey || evt.ctrlKey || evt.shiftKey || evt.altKey
    || !window.htmx || !document.getElementById(BODY) || typeof dlg?.showModal !== "function") evt.stopPropagation();
}, true);

function settle(dlg, body) {
  const actions = body.querySelector(ACTIONS);
  const foot = dlg.querySelector(".hm-dialog__foot");
  if (actions && foot) foot.append(actions);
  setTimeout(() => (actions ?? body).querySelector(FOCUS)?.focus(), 0); // after components.js's own focus
}

document.addEventListener("htmx:beforeRequest", (evt) => {
  if (!card(evt)) return;
  const dlg = dialog();
  const body = document.getElementById(BODY);
  clearActions(dlg);
  const html = cache.get(evt.detail.pathInfo.requestPath);
  if (html !== undefined) {
    evt.preventDefault(); // no request: the fragment is already here
    body.removeAttribute("aria-busy"); // htmx set it before this event and sends no request to clear it
    body.innerHTML = html; // the server's own markup, kept from the first open
    window.htmx.process(body);
    settle(dlg, body);
    return;
  }
  const loading = document.createElement("p");
  loading.className = "hm-muted";
  loading.textContent = "Loading...";
  body.replaceChildren(loading);
});

document.addEventListener("htmx:beforeSwap", (evt) => {
  // Before the actions block moves to the footer, so the cached copy is the server's markup.
  if (card(evt) && evt.detail.xhr.status === 200) cache.set(evt.detail.pathInfo.requestPath, evt.detail.serverResponse);
});

document.addEventListener("htmx:afterSwap", (evt) => {
  if (card(evt) && evt.target.id === BODY) settle(dialog(), evt.target);
});

for (const type of FAILED) {
  document.addEventListener(type, (evt) => {
    const link = card(evt);
    if (!link) return;
    dialog()?.close();
    location.href = link.href;
  });
}
