// Submissions tab: a click on a row's link opens that submission in the side drawer without leaving the list.
// The drawer body is the server-rendered fragment of the same ?s= URL the link points to, so without script,
// or when the fetch fails, the link works as a plain link. Opening, Esc and the backdrop are the dialog's own
// (components.js); this adds row selection, j/k between rows and focus back to the row on close.
import { nextIndex, shouldHandleKey, drawerURL, listURL } from "@hm/forms/submissionslogic";

const ROW = "tr[data-hm-ss-row]";
const OPEN = "a[data-hm-ss-open]";
const BODY = "[data-hm-ss-drawer-body]";
let latest = 0;

const body = () => document.querySelector(BODY);
const drawer = () => body()?.closest("dialog") ?? null;
const rows = () => Array.from(document.querySelectorAll(ROW));
const selectedIndex = () => rows().findIndex((r) => r.getAttribute("aria-selected") === "true");

function select(row) {
  for (const r of rows()) r.removeAttribute("aria-selected");
  row.setAttribute("aria-selected", "true");
  row.classList.remove("hm-ss__row--unread"); // opening marks it read on the server
}

async function open(row) {
  const link = row.querySelector(OPEN);
  const dlg = drawer();
  const target = body();
  if (!link || !dlg || !target || typeof dlg.showModal !== "function") return false;
  const mine = ++latest;
  const url = drawerURL(link.href, row.dataset.hmSsId ?? "");
  try {
    const res = await fetch(url, { headers: { "HX-Request": "true", "HX-Target": "hm-ss-drawer" }, credentials: "same-origin" });
    if (!res.ok) throw new Error(String(res.status));
    const doc = new DOMParser().parseFromString(await res.text(), "text/html");
    if (mine !== latest) return true;
    target.replaceChildren(...Array.from(doc.body.childNodes, (n) => document.importNode(n, true)));
    select(row);
    if (!dlg.open) dlg.showModal();
    history.pushState(null, "", url);
    target.querySelector("input, select, textarea, button, [href]")?.focus();
    return true;
  } catch {
    if (mine === latest) location.href = link.href;
    return true;
  }
}

// Capture phase, like the plugin panel: a modified click (new tab, new window) stays a plain link.
document.addEventListener("click", (evt) => {
  const link = evt.target.closest?.(`${ROW} ${OPEN}`);
  if (!link) return;
  if (evt.defaultPrevented || evt.button !== 0 || evt.metaKey || evt.ctrlKey || evt.shiftKey || evt.altKey) return;
  if (!drawer()) return;
  evt.preventDefault();
  evt.stopPropagation();
  open(link.closest(ROW));
}, true);

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
