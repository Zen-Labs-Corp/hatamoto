// The page's Translations drawer: a slide-over beside the canvas holding the server-rendered panel (the page's copies
// with Open and Create, and what search engines see). It is fetched on the first open, so first paint pays nothing for it.

const FAILED = ["htmx:responseError", "htmx:sendError", "htmx:timeout"];

/**
 * mountTranslationsDrawer({doc, btn, root, url, onError, htmx}): btn opens and closes root; focus moves into the drawer on
 * open and back to btn on Escape or Close. Returns {open, close, toggle, isOpen}.
 */
export function mountTranslationsDrawer({ doc = document, btn, root, url, onError = () => {}, htmx: hx = () => globalThis.htmx }) {
  let loaded = null;
  let busy = false;
  const isOpen = () => !root.hidden;
  const close = (back = true) => {
    if (!isOpen()) return;
    root.hidden = true;
    btn.setAttribute("aria-expanded", "false");
    if (back) btn.focus();
  };
  // htmx asks for the panel with the body as its target (it names the body's id), so the server answers the
  // bare panel and htmx swaps and processes it.
  const fill = () =>
    new Promise((resolve, reject) => {
      const body = root.querySelector("[data-hm-trdrawer-body]");
      const api = hx();
      const failed = () => new Error("The translations did not load.");
      if (!api || !body?.id) return reject(failed());
      const fail = () => {
        off();
        reject(failed());
      };
      const off = () => FAILED.forEach((t) => body.removeEventListener(t, fail));
      FAILED.forEach((t) => body.addEventListener(t, fail));
      api.ajax("GET", url, { source: body, target: body, swap: "innerHTML" }).then(() => { off(); resolve(); }, fail);
    });
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
