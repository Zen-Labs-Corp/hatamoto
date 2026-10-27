// Admin entry: the shell only (htmx configuration, CSRF header, theme and
// sign-out hooks, the component glue). Screen and editor modules are declared
// by the pages that need them (their "modules" block) and load only there.
// No eval, no inline handlers (the CSP forbids both).
import "@hm/components";
import "@hm/daterange";
import "@hm/multiselect";
import "@hm/select";
import "@hm/recovery";
import { adminBase } from "@hm/adminpath";
import { installCsrfFetch, photoFailed, photoFallback, rememberSidebar, rememberedSidebar, safeStorage } from "@hm/shelllogic";

// <main> is not focusable by default: a static tabindex=-1 made a mousedown on any plain text focus
// it, and Chrome then scrolled to its top between mousedown and mouseup. The skip link makes it
// focusable just for the jump and drops that again on blur or the next mousedown.
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

// With no theme chosen yet the page follows the browser's colour scheme; tell
// the server which one is showing so the toggle flips it, and which page to
// come back to (the referrer is never sent).
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

// Top bar toggle: collapses the sidebar. Hidden in the markup, so without
// script there is no dead button.
const sidebarToggle = document.querySelector("[data-hm-sidebar-toggle]");
if (sidebarToggle) {
  sidebarToggle.hidden = false;
  const shell = document.querySelector(".hm-shell");
  // Remembered on desktop only: under 768px the sidebar is a top bar and has no toggle.
  if (rememberedSidebar(safeStorage("local"), window.matchMedia("(min-width: 769px)").matches)) {
    shell.dataset.sidebar = "collapsed";
    sidebarToggle.setAttribute("aria-expanded", "false");
  }
  sidebarToggle.addEventListener("click", () => {
    const collapsed = shell.dataset.sidebar !== "collapsed";
    shell.dataset.sidebar = collapsed ? "collapsed" : "open";
    sidebarToggle.setAttribute("aria-expanded", String(!collapsed));
    rememberSidebar(safeStorage("local"), collapsed);
  });
}

// Photo fallback (error does not bubble: capture).
document.addEventListener("error", (e) => { if (e.target.matches?.("img[data-hm-photo]")) photoFallback(e.target); }, true);
document.querySelectorAll("img[data-hm-photo]").forEach((img) => { if (photoFailed(img)) photoFallback(img); });
