// Browse plugins: a click on a plugin card opens its detail page in the side
// panel (#plugin-panel) instead of navigating. The panel shows the server
// rendered fragment of the same /plugins/<name>/about page the link points to,
// so without script, or when the fetch fails, the link works as a plain link.
// Opening, Esc, the backdrop and returning focus are done by components.js.
const SELECTOR = 'a[data-hm-open="plugin-panel"]';
let latest = 0;

// A fragment may carry a [data-hm-panel-actions] block (opt-in; the plugin
// fragment has none): it is moved into the drawer footer, after Close, and
// removed again when the next fragment loads or the drawer closes.
const ACTIONS = "[data-hm-panel-actions]";
const clearActions = (dlg) => dlg.querySelectorAll(`.hm-dialog__foot ${ACTIONS}`).forEach((n) => n.remove());
document.addEventListener("close", (evt) => {
  if (evt.target.id === "plugin-panel") clearActions(evt.target);
}, true);

// Runs in the capture phase so a modified click (new tab, new window) stays a
// plain link: stopping it here keeps components.js from opening the dialog.
document.addEventListener("click", (evt) => {
  const link = evt.target.closest?.(SELECTOR);
  if (!link) return;
  if (evt.defaultPrevented || evt.button !== 0 || evt.metaKey || evt.ctrlKey || evt.shiftKey || evt.altKey) {
    evt.stopPropagation();
    return;
  }
  const dlg = document.getElementById("plugin-panel");
  const body = dlg?.querySelector(".hm-dialog__body");
  if (!body || typeof dlg.showModal !== "function") {
    evt.stopPropagation();
    return;
  }
  load(link.href, dlg, body);
}, true);

async function load(href, dlg, body) {
  const mine = ++latest;
  clearActions(dlg);
  const loading = document.createElement("p");
  loading.className = "hm-muted";
  loading.textContent = "Loading...";
  body.replaceChildren(loading);
  try {
    const res = await fetch(href, { headers: { "HX-Request": "true", "HX-Target": "plugin-panel" }, credentials: "same-origin" });
    if (!res.ok) throw new Error(String(res.status));
    const doc = new DOMParser().parseFromString(await res.text(), "text/html");
    if (mine !== latest) return;
    body.replaceChildren(...Array.from(doc.body.childNodes, (n) => document.importNode(n, true)));
    const actions = body.querySelector(ACTIONS);
    const foot = dlg.querySelector(".hm-dialog__foot");
    if (actions && foot) foot.append(actions);
    (actions ?? body).querySelector("input, select, textarea, button, [href]")?.focus();
  } catch {
    if (mine !== latest) return;
    dlg.close();
    location.href = href;
  }
}
