// Tabs: a WAI-ARIA tablist from the panel labels.
let uid = 0;
for (const box of document.querySelectorAll(".hm-tabs")) {
  const panels = [...box.children].filter((el) => el.matches(".hm-tp"));
  if (!panels.length) continue;
  const list = document.createElement("div");
  list.className = "hm-tabs__l";
  list.role = "tablist";
  const tabs = panels.map((panel) => {
    const id = "hm-tab" + uid++;
    const tab = document.createElement("button");
    tab.type = "button";
    tab.id = id;
    tab.role = "tab";
    panel.id ||= id + "p";
    tab.setAttribute("aria-controls", panel.id);
    tab.textContent = panel.querySelector(".hm-tp__t").textContent;
    panel.role = "tabpanel";
    panel.setAttribute("aria-labelledby", id);
    list.append(tab);
    return tab;
  });
  const show = (n, focus) => {
    tabs.forEach((tab, i) => {
      tab.setAttribute("aria-selected", i === n);
      tab.tabIndex = i === n ? 0 : -1;
      panels[i].hidden = i !== n;
    });
    if (focus) tabs[n].focus();
  };
  list.addEventListener("click", (e) => {
    const n = tabs.indexOf(e.target.closest("[role=tab]"));
    if (n >= 0) show(n);
  });
  list.addEventListener("keydown", (e) => {
    const at = tabs.indexOf(document.activeElement);
    const to = { ArrowRight: at + 1, ArrowLeft: at - 1, Home: 0, End: tabs.length - 1 }[e.key];
    if (at < 0 || to === undefined) return;
    e.preventDefault();
    show((to + tabs.length) % tabs.length, true);
  });
  box.classList.add("hm-tabs--js");
  box.prepend(list);
  show(0);
}
