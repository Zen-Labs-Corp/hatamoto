// The page's Translations drawer: a slide-over beside the canvas holding the server-rendered panel (the page's copies
// with Open and Create, and what search engines see). It is fetched on the first open, so first paint pays nothing for it.

/**
 * mountTranslationsDrawer({doc, btn, root, url, onError, fetch}): btn opens and closes root; focus moves into the drawer on
 * open and back to btn on Escape or Close. Returns {open, close, toggle, isOpen}.
 */
export function mountTranslationsDrawer({ doc = document, btn, root, url, onError = () => {}, fetch: f = (...a) => globalThis.fetch(...a) }) {
  let loaded = null;
  let busy = false;
  const isOpen = () => !root.hidden;
  const close = (back = true) => {
    if (!isOpen()) return;
    root.hidden = true;
    btn.setAttribute("aria-expanded", "false");
    if (back) btn.focus();
  };
  const fill = async () => {
    // The fragment page answers a bare panel only to an htmx-style request.
    const res = await f(url, { headers: { "HX-Request": "true", "HX-Target": root.id } });
    if (!res.ok) throw new Error("The translations did not load.");
    root.querySelector("[data-hm-trdrawer-body]").innerHTML = await res.text(); // the server's own markup
    globalThis.htmx?.process?.(root);
  };
  const open = async () => {
    if (isOpen() || busy) return;
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
    // The SEO drawer sits at the same place: close it through its own button, so its state stays right.
    doc.querySelector("[data-hm-bs-seo-drawer]:not([hidden]) [data-hm-seodrawer-close]")?.click();
    root.hidden = false;
    btn.setAttribute("aria-expanded", "true");
    root.querySelector("a, button")?.focus();
  };
  root.querySelector("[data-hm-trdrawer-close]")?.addEventListener("click", () => close());
  root.addEventListener("keydown", (e) => {
    if (e.key !== "Escape" || !isOpen() || root.querySelector("dialog[open]")) return;
    e.stopPropagation();
    close();
  });
  return { open, close, toggle: () => (isOpen() ? close() : open()), isOpen };
}
