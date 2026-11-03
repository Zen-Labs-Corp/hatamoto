// The Pages tab body (a lazy chunk, mounted the first time the tab shows): a filter field and the
// site's pages, the current one marked. Each row is a link to that page's builder (list + open only).
import { h } from "@hm/builder/dom";

/** The pages whose title or path contains q (case-insensitive); all of them for a blank q. */
export function filterPages(pages, q) {
  const s = q.trim().toLowerCase();
  return s ? pages.filter((p) => `${p.title} ${p.path}`.toLowerCase().includes(s)) : pages;
}

/**
 * mountPages(host, {pages, announce}): pages is boot.pageList [{id,title,path,editHref,status,current}].
 * Returns {el, draw}; draw() repaints from the filter field.
 */
export function mountPages(host, { pages = [], announce = () => {} } = {}) {
  const search = h("input", { type: "search", class: "hm-field__control", id: "hm-bs-pages-q", placeholder: "Find a page…", autocomplete: "off", "aria-label": "Find a page", "aria-controls": "hm-bs-pages-list" });
  const ico = document.querySelector?.("template[data-hm-bs-ico]")?.content?.querySelector('[data-ico="search"]')?.cloneNode(true) ?? h("span", { "data-ico": "search" }); // the server-rendered icon, as on the Layers search
  const list = h("ul", { class: "hm-bs-pages", id: "hm-bs-pages-list" });
  const note = h("p", { class: "hm-muted hm-bs-pages__empty", hidden: true });
  const el = h("div", { class: "hm-bs-pages__body" }, h("div", { class: "hm-bs-pages__q" }, h("span", { class: "hm-bs__search" }, h("span", { "data-hm-bs-searchico": "", "aria-hidden": "true" }, ico), search)), list, note);
  host.replaceChildren(el);
  const icon = () => document.querySelector?.("template[data-hm-bs-ico]")?.content?.querySelector('[data-ico="file-text"]')?.cloneNode(true) ?? h("span", { "data-ico": "file-text" }); // the page glyph, server-rendered like the search icon
  const unpublished = (status) => (status === "scheduled" ? "Scheduled" : status === "published" ? "" : "Draft"); // a live page without a path is the capped current page: no false Draft
  const row = (p) =>
    h(
      "li",
      {},
      h(
        "a",
        { class: "hm-bs-page", href: p.editHref, "aria-current": p.current ? "page" : false },
        h("span", { class: "hm-bs-page__ico", "aria-hidden": "true" }, icon()),
        h("span", { class: "hm-bs-page__title", text: p.title || "Untitled" }),
        h("span", { class: "hm-bs-page__path", text: p.path || unpublished(p.status) }),
      ),
    );
  function draw() {
    const shown = filterPages(pages, search.value);
    list.replaceChildren(...shown.map(row));
    const q = search.value.trim();
    const msg = q && !shown.length ? "No pages match." : !pages.length ? "No pages yet." : !q && pages.every((p) => p.current) ? "No other pages yet." : ""; // the list holds the current page too
    note.textContent = msg;
    if (msg) note.removeAttribute("hidden");
    else note.setAttribute("hidden", "");
    if (search.value.trim()) announce(shown.length === 1 ? "1 page" : `${shown.length} pages`);
  }
  search.addEventListener("input", draw);
  draw();
  return { el, draw };
}
