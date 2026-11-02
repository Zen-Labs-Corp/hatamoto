// Admin entry: the shell only. Pages load their own screen modules. No eval, no inline handlers (CSP).
import "@hm/components";
import "@hm/daterange";
import "@hm/multiselect";
import "@hm/select";
import "@hm/recovery";
import { adminBase } from "@hm/adminpath";
import { installCsrfFetch, photoFailed, photoFallback, rememberSidebar, rememberedSidebar, safeStorage } from "@hm/shelllogic";

// <main> gets tabindex only for the skip-link jump: a static one made Chrome scroll to its top on any mousedown.
document.addEventListener("click", (evt) => {
  const link = evt.target.closest?.("a.hm-skip");
  const main = link && document.getElementById(link.hash.slice(1));
  if (!main) return;
  main.tabIndex = -1;
  const drop = () => main.removeAttribute("tabindex");
  main.addEventListener("blur", drop, { once: true });
  main.addEventListener("mousedown", drop, { once: true, capture: true });
  main.focus();
});

const csrf = document.querySelector('meta[name="csrf-token"]')?.content ?? "";

installCsrfFetch(globalThis, adminBase(), csrf);

document.addEventListener("htmx:configRequest", (evt) => {
  evt.detail.headers["X-CSRF-Token"] = document.querySelector('meta[name="csrf-token"]')?.content ?? csrf;
});

// Tell the server which scheme is showing (so the toggle flips it) and the page to return to.
document.addEventListener("submit", (evt) => {
  const form = evt.target;
  if (!(form instanceof HTMLFormElement) || form.getAttribute("action") !== adminBase() + "/theme") return;
  const field = (name, value) => {
    const input = form.querySelector(`input[name="${name}"]`) ?? Object.assign(document.createElement("input"), { type: "hidden", name });
    input.value = value;
    form.append(input);
  };
  field("scheme", window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light");
  field("back", location.pathname + location.search);
});

// Sidebar toggle: collapses it to a rail. Hidden in the markup: no dead button without script.
const sidebarToggle = document.querySelector("[data-hm-sidebar-toggle]");
if (sidebarToggle) {
  sidebarToggle.hidden = false;
  const shell = document.querySelector(".hm-shell");
  // A native tooltip repeats the label unless the rail hides it, so it is set only while collapsed.
  const railTitles = (on) => document.querySelectorAll(".hm-nav-link").forEach((a) => on ? (a.title = a.querySelector("span").textContent) : a.removeAttribute("title"));
  if (rememberedSidebar(safeStorage("local"), window.matchMedia("(min-width: 769px)").matches)) {
    shell.dataset.sidebar = "collapsed";
    sidebarToggle.setAttribute("aria-expanded", "false");
    railTitles(true);
  }
  sidebarToggle.addEventListener("click", () => {
    const collapsed = shell.dataset.sidebar !== "collapsed";
    shell.dataset.sidebar = collapsed ? "collapsed" : "open";
    sidebarToggle.setAttribute("aria-expanded", String(!collapsed));
    railTitles(collapsed);
    rememberSidebar(safeStorage("local"), collapsed);
  });
}

// Centre the current subnav link inside its own scroller only, so the window never moves.
const subnavLink = document.querySelector('.hm-subnav [aria-current="page"]');
const subnav = subnavLink?.closest(".hm-subnav");
if (subnav && subnav.scrollWidth > subnav.clientWidth) subnav.scrollLeft += subnavLink.getBoundingClientRect().left - subnav.getBoundingClientRect().left - (subnav.clientWidth - subnavLink.offsetWidth) / 2;

// Photo fallback (error does not bubble: capture).
document.addEventListener("error", (e) => { if (e.target.matches?.("img[data-hm-photo]")) photoFallback(e.target); }, true);
document.querySelectorAll("img[data-hm-photo]").forEach((img) => { if (photoFailed(img)) photoFallback(img); });
