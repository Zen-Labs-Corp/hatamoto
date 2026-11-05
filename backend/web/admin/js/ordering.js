// Order page: drag a row to a new place with SortableJS, and put focus back on
// the moved row's menu button after a button move. Every move is also a plain POST
// form (the buttons), so nothing here is needed to reorder; the page without
// this module works the same. Dragging is offered for flat lists only: a tree
// is moved with the indent and outdent buttons.
const ROW = /^row-/;

function rows(body) {
  return [...body.children].filter((tr) => ROW.test(tr.id));
}

function say(root, text) {
  const live = root.querySelector("[data-hm-orderlive]");
  if (live) live.textContent = text;
}

// After a button move the server redirects to the page with #row-<id>. The
// buttons live in the row's closed menu, where focus() does nothing, so focus
// goes to the row's menu trigger, which is always focusable.
function restoreFocus(root) {
  const row = ROW.test(location.hash.slice(1)) ? document.getElementById(location.hash.slice(1)) : null;
  if (!row || !root.contains(row)) return;
  row.querySelector('button[aria-haspopup="menu"]')?.focus();
}

async function post(url, token, id, position) {
  const body = new URLSearchParams({ position: String(position) });
  const res = await fetch(`${url}${id}/move`, {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/x-www-form-urlencoded", "X-CSRF-Token": token, Accept: "application/json" },
    body,
    redirect: "manual",
  });
  // The server answers 204 when the move worked and an error status with the
  // reason otherwise; a redirect (opaque, status 0) is never a success.
  return res.ok ? "" : (await res.text().catch(() => "")).trim() || "Reload the page and try again.";
}

function drag(root) {
  const body = root.querySelector("tbody");
  if (!body || !window.Sortable || root.dataset.hmDrag === undefined) return;
  const token = document.querySelector('meta[name="csrf-token"]')?.content ?? "";
  window.Sortable.create(body, {
    draggable: "tr",
    filter: "button, a, input, select, textarea",
    preventOnFilter: false,
    animation: 120,
    onEnd: async (evt) => {
      if (evt.oldIndex === evt.newIndex) return;
      const tr = evt.item;
      const name = tr.querySelector("th, td")?.textContent.trim() ?? "";
      const all = rows(body);
      const err = await post(root.dataset.hmOrderUrl, token, tr.id.replace(ROW, ""), all.indexOf(tr) + 1);
      say(root, err ? `${name} was not moved. ${err} Reload the page to see the saved order.` : `${name} moved to position ${all.indexOf(tr) + 1} of ${all.length}.`);
    },
  });
}

function init() {
  document.querySelectorAll("[data-hm-orderlist]").forEach((root) => {
    if (root.dataset.hmOrderReady) return;
    root.dataset.hmOrderReady = "1";
    restoreFocus(root);
    drag(root);
  });
}

document.addEventListener("DOMContentLoaded", init);
document.addEventListener("htmx:afterSettle", init);
export { init };
