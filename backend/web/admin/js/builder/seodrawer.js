// The page's SEO drawer: a slide-over on the right of the canvas holding the shared SEO panel (@hm/seopanel).
// The panel is fetched on the first open, so first paint pays nothing for it. Editing a field reports the values
// to the saver (they travel with the next save); the live checks post the draft doc along with the fields.

/** The panel's values as the save request carries them. */
export function readSEO(root) {
  const val = (name) => root.querySelector(`[name="${name}"]`)?.value ?? "";
  return {
    title: val("seo_title"),
    description: val("seo_description"),
    canonical: val("seo_canonical"),
    og_image: val("seo_og_image"),
    no_index: !!root.querySelector('[name="seo_noindex"]')?.checked,
  };
}

/**
 * mountSEODrawer({doc, btn, root, urls, getDoc, onEdit, onError, load, fetch})
 * btn opens and closes root (the dialog element); focus moves into the drawer on open and back to the opener (btn, or the More menu when btn is hidden) on Escape or Close.
 * onEdit(values) runs after a field changed. Returns {open, close, isOpen}.
 */
export function mountSEODrawer({ doc = document, btn, root, urls, getDoc, onEdit = () => {}, onError = () => {}, load = () => import("@hm/seopanel"), fetch: f = (...a) => globalThis.fetch(...a) }) {
  let loaded = null;
  let busy = false;
  let opener = null; // what had focus when the drawer opened: the SEO button, or its More-menu proxy on a phone
  const isOpen = () => !root.hidden;
  const shown = (el) => !!el && el.getClientRects().length > 0;
  // The first of them on screen: the button is hidden on a phone and the menu item once its menu closed.
  const returnFocus = () => [opener, btn, doc.querySelector("[popovertarget=bs-more]")].find(shown)?.focus();
  const close = (back = true) => {
    if (!isOpen()) return;
    root.hidden = true;
    btn.setAttribute("aria-expanded", "false");
    if (back) returnFocus();
  };
  const fill = async () => {
    // The fragment page answers a bare panel only to an htmx-style request.
    const res = await f(urls.seo, { headers: { "HX-Request": "true", "HX-Target": root.id } });
    if (!res.ok) throw new Error("The SEO panel did not load.");
    const html = await res.text(); // the server's own markup
    const mod = await load();
    root.querySelector("[data-hm-seodrawer-body]").innerHTML = html;
    globalThis.htmx?.process?.(root);
    const panel = root.querySelector("[data-hm-seo]");
    if (panel) {
      panel.dataset.hmReady = "1";
      mod.mountSEOPanel(panel);
    }
  };
  const open = async () => {
    if (isOpen() || busy) return;
    opener = doc.activeElement;
    busy = true;
    try {
      loaded ??= fill().catch((e) => {
        loaded = null;
        throw e;
      });
      await loaded;
    } catch (e) {
      onError(e);
      return;
    } finally {
      busy = false;
    }
    root.hidden = false;
    btn.setAttribute("aria-expanded", "true");
    // The panel came with the stored page; the checks must read the draft as it is now.
    const results = root.querySelector("#be-panel");
    if (results) globalThis.htmx?.trigger?.(results, "hm:analyze");
    (root.querySelector("[role=tab][aria-selected=true]") ?? root.querySelector("[data-hm-seodrawer-close]"))?.focus();
  };
  // The analysis reads the draft, not the stored page: the doc rides on every analyze request.
  root.addEventListener("htmx:configRequest", (e) => {
    e.detail.parameters.doc = JSON.stringify(getDoc());
  });
  const edited = (e) => {
    if (e.target?.closest?.("[data-hm-seodrawer-body] [name^='seo_']")) onEdit(readSEO(root));
  };
  root.addEventListener("input", edited);
  root.addEventListener("change", edited);
  root.querySelector("[data-hm-seodrawer-close]")?.addEventListener("click", () => close());
  btn.addEventListener("click", () => (isOpen() ? close() : open()));
  // Escape is the drawer's only while it has the key: a dialog, a popover or a handler deeper in the drawer owns its own.
  root.addEventListener("keydown", (e) => {
    if (e.key !== "Escape" || e.defaultPrevented || !isOpen() || doc.querySelector(":popover-open, dialog[open]")) return;
    e.preventDefault();
    e.stopPropagation(); // the builder's own Escape (deselect) must not also run
    close();
  });
  return { open, close, isOpen };
}
