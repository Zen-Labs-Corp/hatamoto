// Submissions tab: a click on a row's link opens that submission in the side drawer without leaving the list.
// The link carries hx-get/hx-target/hx-sync/hx-push-url (forms_submissions_tab.go): htmx asks for the
// server-rendered fragment of the same ?s= URL, swaps it into the drawer body and pushes that URL. Without
// script, or when the request fails, the link works as a plain link. Opening, Esc and the backdrop are the
// dialog's own (components.js); this adds row selection, j/k between rows and focus back to the row on close.
import { nextIndex, shouldHandleKey, listURL } from "@hm/forms/submissionslogic";

const ROW = "tr[data-hm-ss-row]";
const OPEN = "a[data-hm-ss-open]";
const BODY_ID = "hm-ss-drawer"; // the swap target; the server answers this target with the bare drawer
const FOCUS = "input, select, textarea, button, [href]";
const FAILED = ["htmx:responseError", "htmx:sendError", "htmx:timeout"]; // never htmx:sendAbort: a newer request replaced it
let latest = 0;

const body = () => document.getElementById(BODY_ID);
const drawer = () => body()?.closest("dialog") ?? null;
const rows = () => Array.from(document.querySelectorAll(ROW));
const selectedIndex = () => rows().findIndex((r) => r.getAttribute("aria-selected") === "true");
const link = (evt) => {
  const el = evt.detail?.requestConfig?.elt;
  return el?.matches?.(OPEN) ? el : null;
};

function select(row) {
  for (const r of rows()) r.removeAttribute("aria-selected");
  row.setAttribute("aria-selected", "true");
  row.classList.remove("hm-ss__row--unread"); // opening marks it read on the server
}

// Capture phase, like the plugin panel: a modified click (new tab, new window), or a page without htmx /
// showModal, stays a plain link. Stopping it here keeps htmx, which takes every click on an hx-get link, away.
document.addEventListener("click", (evt) => {
  const a = evt.target.closest?.(`${ROW} ${OPEN}`);
  if (!a) return;
  const dlg = drawer();
  if (evt.defaultPrevented || evt.button !== 0 || evt.metaKey || evt.ctrlKey || evt.shiftKey || evt.altKey
    || !window.htmx || typeof dlg?.showModal !== "function") evt.stopPropagation();
}, true);

// With the drawer open, j/k load the next submission through the same attributes the link carries.
function open(row) {
  const a = row.querySelector(OPEN);
  if (!a) return;
  if (!window.htmx) {
    location.href = a.href;
    return;
  }
  window.htmx.ajax("GET", a.getAttribute("hx-get") ?? a.href, { source: a, target: `#${BODY_ID}`, swap: "innerHTML" }).catch(() => {}); // a newer j/k aborts this one
}

document.addEventListener("htmx:beforeRequest", (evt) => {
  if (link(evt)) evt.detail.requestConfig.hmGen = ++latest;
});
// A request overtaken by a close (or a newer one) must not swap or push its address.
document.addEventListener("htmx:beforeSwap", (evt) => {
  if (link(evt) && evt.detail.requestConfig.hmGen !== latest) evt.detail.shouldSwap = false;
});
document.addEventListener("htmx:afterSwap", (evt) => {
  const a = link(evt);
  if (!a || evt.target !== body()) return;
  select(a.closest(ROW));
  const dlg = drawer();
  if (!dlg.open) dlg.showModal();
  body().querySelector(FOCUS)?.focus();
});
for (const type of FAILED) {
  document.addEventListener(type, (evt) => {
    const a = link(evt);
    if (a && evt.detail.requestConfig.hmGen === latest) location.href = a.href;
  });
}

// j/k move between rows from anywhere on the page, also from focus inside the open drawer (key events
// bubble out of a modal dialog). With the drawer open the next submission loads into it; otherwise the row
// is just selected. Esc is left to the dialog.
document.addEventListener("keydown", (evt) => {
  if (evt.key !== "j" && evt.key !== "k") return;
  if (!shouldHandleKey(evt)) return;
  const all = rows();
  const i = nextIndex(selectedIndex(), all.length, evt.key);
  if (i < 0) return;
  evt.preventDefault();
  const row = all[i];
  if (drawer()?.open) open(row);
  else {
    select(row);
    row.querySelector(OPEN)?.focus();
  }
});

// Closing the drawer drops ?s= from the address and returns focus to the selected row.
document.addEventListener("close", (evt) => {
  if (evt.target !== drawer()) return;
  latest++;
  history.replaceState(null, "", listURL(location.href));
  rows()[selectedIndex()]?.querySelector(OPEN)?.focus();
}, true);

// Back and forward step between addresses that were pushed without a reload: reload to show the right state.
window.addEventListener("popstate", () => location.reload());

// A page rendered with ?s= carries the open submission: show the drawer and mark its row.
function autoOpen() {
  const dlg = drawer();
  if (!dlg || !document.querySelector("[data-hm-ss-autoopen]")) return;
  const id = body()?.querySelector("[data-hm-ss-id]")?.dataset.hmSsId;
  const row = rows().find((r) => r.dataset.hmSsId === id);
  if (row) select(row);
  if (!dlg.open && typeof dlg.showModal === "function") dlg.showModal();
}
autoOpen();
