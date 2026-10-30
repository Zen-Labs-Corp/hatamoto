// The page's SEO drawer: a slide-over on the right of the canvas holding the shared SEO panel (@hm/seopanel).
// The panel is fetched on the first open, so first paint pays nothing for it. Editing a field reports the values
// to the saver (they travel with the next save); the live checks post the draft doc along with the fields.
// The SEO button also shows the page's score once the builder is idle (mountSEOBadge), so it lives in this lazy chunk too.

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
 * mountSEOBadge({btn, root, urls, getDoc, getName, csrf, fetch}): the score on the closed SEO button.
 * refresh() asks the analyze endpoint about the draft (no seo_ fields: the server falls back to the stored SEO) and reads the
 * score from its #seo-score element; set(el) takes that element from a check the drawer already ran. A failure leaves the button plain.
 * Returns {refresh, set}.
 */
export function mountSEOBadge({ btn, root, urls, getDoc, getName, csrf = () => "", fetch: f = (...a) => globalThis.fetch(...a), parse = (h) => new DOMParser().parseFromString(h, "text/html") }) {
  let seq = 0;
  const set = (el) => {
    const score = el?.querySelector?.("strong")?.textContent?.trim();
    const tpl = root.querySelector(`[data-hm-bs-seo-badge="${/hm-seo__score--(pass|warn|fail)/.exec(el?.className)?.[1]}"]`);
    if (!score || !tpl) return;
    btn.querySelector("[data-hm-seo-score]")?.remove();
    const badge = tpl.content.firstElementChild.cloneNode(true);
    badge.dataset.hmSeoScore = "";
    badge.append(score);
    btn.append(badge);
    btn.setAttribute("aria-label", `SEO, score ${score}`);
  };
  const refresh = async () => {
    const mine = ++seq;
    try {
      const res = await f(urls.analyze, {
        method: "POST",
        headers: { "HX-Request": "true", "HX-Target": "be-panel", "X-CSRF-Token": csrf(), "Content-Type": "application/x-www-form-urlencoded" },
        body: new URLSearchParams({ doc: JSON.stringify(getDoc()), name: getName?.() ?? "" }),
      });
      if (!res.ok) return;
      const el = parse(await res.text()).querySelector("#seo-score");
      if (mine === seq) set(el);
    } catch {
      // the button stays as it is
    }
  };
  return { refresh, set };
}

/**
 * mountSEODrawer({doc, btn, root, urls, getDoc, getName, onScore, onEdit, onError, load, fetch})
 * btn opens and closes root (the dialog element); focus moves into the drawer on open and back on Escape or Close: to the opener when that was the SEO button or a More-menu proxy, else to the SEO button, or the More menu when btn is hidden.
 * onEdit(values) runs after a field changed. Returns {open, close, isOpen, showFieldError}.
 */
export function mountSEODrawer({ doc = document, btn, root, urls, getDoc, getName, onScore = () => {}, onEdit = () => {}, onError = () => {}, load = () => import("@hm/seopanel"), fetch: f = (...a) => globalThis.fetch(...a) }) {
  let loaded = null;
  let busy = false;
  let opener = null; // what had focus when the drawer opened, kept only when it was the SEO button or a More-menu control
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
    const o = doc.activeElement;
    opener = o === btn || o?.matches?.("[popovertarget=bs-more], [data-hm-bs-proxy]") ? o : null;
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
    e.detail.parameters.name = getName?.() ?? "";
  });
  // A failed check leaves a note in the results (a server-rendered template); the next good swap replaces it.
  const results = () => root.querySelector("#be-panel");
  const dropNote = () => results()?.querySelector?.("[data-hm-seodrawer-shown]")?.remove();
  const note = (e) => {
    const panel = results();
    const tpl = root.querySelector("[data-hm-seodrawer-note]");
    if (!panel || !tpl || (e.detail?.target ?? e.target) !== panel || panel.querySelector("[data-hm-seodrawer-shown]")) return;
    const n = tpl.content.cloneNode(true);
    n.firstElementChild.dataset.hmSeodrawerShown = "1";
    panel.prepend(n);
  };
  for (const t of ["htmx:responseError", "htmx:sendError", "htmx:timeout"]) root.addEventListener(t, note);
  root.addEventListener("htmx:afterSwap", (e) => {
    dropNote();
    if ((e.detail?.target ?? e.target) === results()) onScore(root.querySelector("#seo-score")); // the checks came back: the button shows the same score
  });
  // A refused value is shown under its field, on the tab that holds it (the server-side message of a rejected save).
  // focus: false (a background autosave) marks the field only when the panel is already loaded: it never opens the drawer or moves focus.
  const showFieldError = async (field, msg, { focus = true } = {}) => {
    if (focus && !isOpen()) await open();
    const input = root.querySelector(`[name="seo_${field}"]`);
    if (!input) return;
    if (focus) root.querySelector("#seo-tab-advanced")?.click();
    const id = `${input.id}-error`;
    let p = root.querySelector(`#${id}`);
    if (!p) {
      p = root.querySelector("[data-hm-seodrawer-field-error]").content.firstElementChild.cloneNode(true);
      p.id = id;
      input.closest(".hm-field").append(p);
    }
    p.textContent = msg;
    input.setAttribute("aria-invalid", "true");
    const described = (input.getAttribute("aria-describedby") ?? "").split(/\s+/).filter(Boolean); // the help text stays announced
    if (!described.includes(id)) input.setAttribute("aria-describedby", [...described, id].join(" "));
    if (focus) input.focus();
  };
  const edited = (e) => {
    const input = e.target;
    const err = input?.id && root.querySelector(`#${input.id}-error`);
    if (err) {
      err.remove();
      input.removeAttribute("aria-invalid");
      const rest = (input.getAttribute("aria-describedby") ?? "").split(/\s+/).filter((t) => t && t !== err.id);
      if (rest.length) input.setAttribute("aria-describedby", rest.join(" "));
      else input.removeAttribute("aria-describedby");
    }
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
  return { open, close, isOpen, showFieldError };
}
