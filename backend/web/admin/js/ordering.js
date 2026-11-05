// Order page: drag a row to a new place with SortableJS, and put focus back on
// the moved row's button after a button move. Every move is also a plain POST
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

// After a button move the server redirects to the page with #row-<id>: focus
// the button that was pressed (same label), or the row's first button.
function restoreFocus(root) {
  const row = ROW.test(location.hash.slice(1)) ? document.getElementById(location.hash.slice(1)) : null;
  if (!row || !root.contains(row)) return;
  const want = sessionStorage.getItem("hm-order-button");
  sessionStorage.removeItem("hm-order-button");
  const buttons = [...row.querySelectorAll("button")];
  const target = buttons.find((b) => b.textContent.trim() === want) ?? buttons[0];
  target?.focus();
}

function rememberButton(root) {
  root.addEventListener("submit", (evt) => {
    const b = evt.submitter;
    if (b) sessionStorage.setItem("hm-order-button", b.textContent.trim());
  });
}

async function post(url, token, id, position) {
  const body = new URLSearchParams({ position: String(position) });
  const res = await fetch(`${url}${id}/move`, {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/x-www-form-urlencoded", "X-CSRF-Token": token, Accept: "text/html" },
    body,
  });
  return res.ok;
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
      const ok = await post(root.dataset.hmOrderUrl, token, tr.id.replace(ROW, ""), all.indexOf(tr) + 1);
      say(root, ok ? `${name} moved to position ${all.indexOf(tr) + 1} of ${all.length}.` : `${name} was not moved. Reload the page and try again.`);
      if (ok) location.reload();
    },
  });
}

function init() {
  document.querySelectorAll("[data-hm-orderlist]").forEach((root) => {
    if (root.dataset.hmOrderReady) return;
    root.dataset.hmOrderReady = "1";
    rememberButton(root);
    restoreFocus(root);
    drag(root);
  });
}

document.addEventListener("DOMContentLoaded", init);
document.addEventListener("htmx:afterSettle", init);
export { init };
