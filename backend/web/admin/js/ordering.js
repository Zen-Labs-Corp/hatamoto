// Order page: drag a row by its "::" handle to a new place with SortableJS, and put focus back on
// the pressed button of the moved row after a button move. Every move is also a plain POST
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

// After a button move the server redirects to the page with #row-<id> and ?moved=<op>.
// Focus goes back to the button that was pressed; if that one is now disabled (the row
// became first or last), to the row's first enabled button. The ?moved= is then dropped
// so a reload does not move focus again.
function restoreFocus(root) {
  const row = ROW.test(location.hash.slice(1)) ? document.getElementById(location.hash.slice(1)) : null;
  if (!row || !root.contains(row)) return;
  const url = new URL(location.href);
  const moved = url.searchParams.get("moved");
  const enabled = [...row.querySelectorAll("button:not(:disabled)")];
  (enabled.find((b) => b.form?.elements.op?.value === moved) ?? enabled[0])?.focus();
  if (moved !== null) {
    url.searchParams.delete("moved");
    history.replaceState(null, "", url);
  }
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
    handle: ".hm-table__handle",
    filter: "button, a, input, select, textarea",
    preventOnFilter: false,
    animation: 120,
    onEnd: async (evt) => {
      if (evt.oldIndex === evt.newIndex) return;
      const tr = evt.item;
      const name = (tr.querySelector("th, td")?.textContent ?? "").replace(/^\s*::/, "").trim();
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
    // The browser scrolls to the #row- fragment at load and drops focus doing so: focus after that.
    if (document.readyState === "complete") restoreFocus(root);
    else window.addEventListener("load", () => setTimeout(() => restoreFocus(root), 0), { once: true });
    drag(root);
  });
}

document.addEventListener("DOMContentLoaded", init);
document.addEventListener("htmx:afterSettle", init);
export { init };
