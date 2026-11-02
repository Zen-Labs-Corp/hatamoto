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
    // A tapped/hash target stays current until the user scrolls by hand (the page may be too short to bring it to the top).
    let pinned = false;
    const fromHash = () => {
      const i = links.findIndex((a) => a.getAttribute("href") === location.hash);
      if (i < 0) return;
      pinned = true;
      mark(links[i]);
    };
    // The current section is the last one whose top has reached the scroll-padding line.
    const spy = () => {
      if (pinned) return;
      const line = parseFloat(getComputedStyle(document.documentElement).scrollPaddingTop) || 0;
      let i = 0;
      targets.forEach((t, n) => { if (t.getBoundingClientRect().top <= line + 8) i = n; });
      mark(links[i]);
    };
    window.addEventListener("hashchange", fromHash);
    window.addEventListener("scroll", spy, { passive: true });
    ["wheel", "touchmove", "keydown"].forEach((ev) => window.addEventListener(ev, () => { pinned = false; }, { passive: true }));
    fromHash();
  });
}

document.addEventListener("DOMContentLoaded", () => init(document));
export { init };
