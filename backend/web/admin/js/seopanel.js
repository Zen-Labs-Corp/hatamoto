// The shared SEO panel's tabs (Search, Social, Schema, Advanced). Progressive enhancement: without
// JavaScript the tab row stays hidden and the four panels show stacked under their headings.
// Hidden panels stay in the form, so every field still posts and the checks still refresh.

/** The tab an arrow, Home or End key moves to; null for any other key. Tabs wrap round. */
export function tabTarget(index, key, count) {
  const to = { ArrowRight: index + 1, ArrowLeft: index - 1, Home: 0, End: count - 1 }[key];
  return to === undefined ? null : (to + count) % count;
}

/** Wires one panel. */
export function mountSEOPanel(root) {
  const list = root.querySelector("[data-hm-seo-tabs]");
  if (!list) return;
  const tabs = [...list.querySelectorAll('[role="tab"]')];
  const select = (tab, focus = false) => {
    for (const t of tabs) {
      const on = t === tab;
      t.setAttribute("aria-selected", String(on));
      t.tabIndex = on ? 0 : -1;
      if (on && focus) t.focus();
      document.getElementById(t.getAttribute("aria-controls")).hidden = !on;
    }
  };
  list.hidden = false;
  for (const h of root.querySelectorAll("[data-hm-seo-panel-h]")) h.hidden = true;
  list.addEventListener("click", (e) => {
    const tab = e.target.closest('[role="tab"]');
    if (tab) select(tab);
  });
  list.addEventListener("keydown", (e) => {
    const to = tabTarget(tabs.indexOf(document.activeElement), e.key, tabs.length);
    if (to === null) return;
    e.preventDefault();
    select(tabs[to], true);
  });
  // A field the server rejected sits in one of the panels: open it, so the error is seen.
  const bad = tabs.find((t) => document.getElementById(t.getAttribute("aria-controls")).querySelector(".hm-field--invalid"));
  select(bad ?? tabs[0]);
}

function boot() {
  for (const root of document.querySelectorAll("[data-hm-seo]")) {
    if (root.dataset.hmReady) continue;
    root.dataset.hmReady = "1";
    mountSEOPanel(root);
  }
}
if (typeof document !== "undefined") {
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", boot);
  else boot();
}
