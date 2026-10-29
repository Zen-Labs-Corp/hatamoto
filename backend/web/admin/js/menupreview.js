// Menu editor preview: the menu as the active theme draws it, for the tree as
// it is now, saved or not. The editor form (every i.N.* field) is posted to the
// preview route with the frame as its target, so the server needs nothing but
// the form. The frame is same-origin without scripts (sandbox attribute), so
// this page can read it: it sets the light or dark scheme on the frame's page
// and turns a click on a link into a selection of the matching row. The page
// works the same when the preview is not available: the card is hidden until a
// render works.
const DELAY = 300;

function init(root) {
  root.querySelectorAll("[data-hm-preview]").forEach((box) => {
    if (box.dataset.hmReady) return;
    let frame = box.querySelector("[data-hm-preview-frame]");
    const form = box.closest("form");
    const tree = form?.querySelector("[data-hm-menutree]");
    if (!frame || !form || !tree) return;
    box.setAttribute("data-hm-ready", "1");
    const card = box.closest(".hm-card") ?? box;
    // A button the form posts with, to the preview route and into the frame. It is
    // never shown: the editor's own buttons stay the only ones to see.
    const submit = document.createElement("button");
    submit.type = "submit";
    submit.hidden = true;
    submit.formAction = box.dataset.action;
    submit.formNoValidate = true;
    form.append(submit);
    let timer = 0;
    let pending = null; // the frame the next render is loading into
    let serial = 0;
    let scheme = "light";

    const paint = () => {
      const html = frame.contentDocument?.documentElement;
      if (!html) return;
      if (scheme === "dark") html.setAttribute("data-theme", "dark");
      else html.removeAttribute("data-theme");
    };

    // A form posted into a frame that has already loaded adds a history entry,
    // and Back would then step through the previews. Each render goes into a new
    // frame, whose first navigation replaces its entry, and takes the place of
    // the shown one once it has loaded.
    const render = () => {
      pending?.remove();
      const next = frame.cloneNode(false);
      next.name = `menu-preview-${++serial}`;
      next.hidden = true;
      frame.after(next);
      // Added after the frame is in the page, so its first (blank) load is not seen.
      next.addEventListener("load", () => loaded(next));
      pending = next;
      submit.formTarget = next.name;
      form.requestSubmit(submit);
    };

    const request = () => {
      clearTimeout(timer);
      timer = setTimeout(render, DELAY);
    };

    // The preview posts the editor form, which is not a save: keep the editor's
    // own submit handling (leave warning, busy state) out of it.
    window.addEventListener("submit", (evt) => {
      if (evt.submitter === submit) evt.stopImmediatePropagation();
    }, true);

    const loaded = (next) => {
      const doc = next.contentDocument;
      if (next !== pending || doc?.location.href === "about:blank") return;
      pending = null;
      // No document to read, or an error text instead of a page: nothing to show
      // for now. The card comes back with the next render that works.
      if (!doc || doc.contentType !== "text/html" || !doc.documentElement.querySelector("body *")) {
        next.remove();
        card.hidden = true;
        return;
      }
      next.dataset.width = frame.dataset.width;
      frame.remove();
      next.hidden = false;
      frame = next;
      paint();
      doc.addEventListener("click", (evt) => {
        const hit = evt.target.closest?.('[id^="menu-pv-"]');
        if (!hit) return;
        evt.preventDefault();
        const row = [...tree.querySelectorAll(":scope > [data-hm-item]")][Number(hit.id.slice("menu-pv-".length))];
        if (row) document.dispatchEvent(new CustomEvent("hm:select", { detail: { items: [row] } }));
      });
    };

    box.querySelectorAll("[data-hm-preview-width]").forEach((b) => b.addEventListener("click", () => {
      frame.dataset.width = b.dataset.hmPreviewWidth;
      box.querySelectorAll("[data-hm-preview-width]").forEach((o) => o.setAttribute("aria-pressed", String(o === b)));
    }));
    box.querySelectorAll("[data-hm-preview-scheme]").forEach((b) => b.addEventListener("click", () => {
      scheme = b.dataset.hmPreviewScheme;
      box.querySelectorAll("[data-hm-preview-scheme]").forEach((o) => o.setAttribute("aria-pressed", String(o === b)));
      paint();
    }));

    // Anything that changes what the menu holds renders it again: typing, a pick,
    // the location, and the tree itself (a move, an indent, an undo).
    const relevant = (evt) => /^(i\.\d+\.|location$)/.test(evt.target.name ?? "");
    for (const type of ["input", "change"]) form.addEventListener(type, (evt) => { if (relevant(evt)) request(); });
    tree.addEventListener("hm:picked", request);
    new MutationObserver(request).observe(tree, { childList: true, subtree: true, attributes: true, attributeFilter: ["data-depth", "data-type"] });
    request();
  });
}

document.addEventListener("DOMContentLoaded", () => init(document));
document.addEventListener("htmx:afterSettle", (evt) => init(evt.target));
export { init };
