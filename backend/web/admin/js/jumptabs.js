// hm/tabs with in-page links: aria-current="page" follows the hash and the section in view.
// The markup works without this script (the first tab is current on load).
function init(root) {
  root.querySelectorAll("nav.hm-tabs").forEach((nav) => {
    if (nav.dataset.hmReady) return;
    const links = [...nav.querySelectorAll('a.hm-tabs__tab[href^="#"]')];
    const targets = links.map((a) => document.getElementById(a.getAttribute("href").slice(1)));
    if (!links.length || targets.some((t) => !t)) return;
    nav.dataset.hmReady = "1";
    const mark = (link) => {
      links.forEach((a) => (a === link ? a.setAttribute("aria-current", "page") : a.removeAttribute("aria-current")));
    };
    const fromHash = () => {
      const i = links.findIndex((a) => a.getAttribute("href") === location.hash);
      if (i >= 0) mark(links[i]);
    };
    window.addEventListener("hashchange", fromHash);
    const seen = new Set();
    const io = new IntersectionObserver((entries) => {
      entries.forEach((e) => (e.isIntersecting ? seen.add(e.target) : seen.delete(e.target)));
      const i = targets.findIndex((t) => seen.has(t));
      if (i >= 0) mark(links[i]);
    }, { rootMargin: "0px 0px -60% 0px" });
    targets.forEach((t) => io.observe(t));
    fromHash();
  });
}

document.addEventListener("DOMContentLoaded", () => init(document));
export { init };
