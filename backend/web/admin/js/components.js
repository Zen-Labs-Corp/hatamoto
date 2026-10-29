// Component glue for native elements: <dialog>, toasts. Everything else
// (popover, details) needs no script.
import { hmDate, hmTime, hmDateTime } from "@hm/dates";
import { carryHref, confirmPost, isDismissed, markDismissed, phraseMatches, safeStorage, unsignedGatesMet } from "@hm/shelllogic";

// <button data-hm-open="dialog-id"> opens a modal dialog. Focus starts on [autofocus], else
// the first field of the body, else its first button; on close it returns to the opener, or
// to the control that owned the menu the opener sat in when that menu has closed.
const openers = new WeakMap();
const FIELDS = "input:not([type=hidden]):not([disabled]), select:not([disabled]), textarea:not([disabled]), [contenteditable=true]";
document.addEventListener("click", (evt) => {
  const opener = evt.target.closest?.("[data-hm-open]");
  if (!opener) return;
  const dlg = document.getElementById(opener.dataset.hmOpen);
  if (dlg && typeof dlg.showModal === "function") {
    evt.preventDefault();
    openers.set(dlg, opener);
    dlg.showModal();
    const body = dlg.querySelector(".hm-dialog__body") || dlg;
    (dlg.querySelector("[autofocus]") || body.querySelector(FIELDS) || body.querySelector("button, [href]"))?.focus();
  }
});
document.addEventListener("close", (evt) => {
  const opener = openers.get(evt.target);
  if (!opener) return;
  openers.delete(evt.target);
  const visible = (el) => el?.isConnected && el.getClientRects().length > 0;
  const pop = opener.closest("[popover]");
  const target = visible(opener) ? opener : pop?.id && document.querySelector(`[popovertarget="${pop.id}"]`);
  (visible(target) ? target : document.querySelector(`[data-hm-open="${evt.target.id}"]`))?.focus();
}, true);

// Clicking the backdrop (the dialog element itself, outside its panel) closes it, but only
// when the press and the release both landed on the backdrop: a drag that selects text in a
// field and ends outside the panel must not close the dialog and lose what was typed.
// A dialog marked data-hm-static (it holds input) ignores the backdrop.
let backdropDown = null;
document.addEventListener("pointerdown", (evt) => { backdropDown = evt.target; }, true);
document.addEventListener("click", (evt) => {
  const dlg = evt.target;
  if (dlg instanceof HTMLDialogElement && dlg.open && backdropDown === dlg && !dlg.hasAttribute("data-hm-static")) dlg.close();
  backdropDown = null;
});

// Window aliases for the date helpers in dates.js (for inline scripts and plugins).
window.hmDate = hmDate;
window.hmTime = hmTime;
window.hmDateTime = hmDateTime;

// Copy buttons. <button data-hm-copy-field="input-id"> copies that field's value (secret
// banner); the field is selected first so it can still be copied by hand when the clipboard
// API is unavailable. <button data-hm-copy-url="/path"> copies the absolute address and is
// revealed only when the clipboard API exists.
const copyFallback = /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent) ? "Press \u2318C to copy." : "Press Ctrl+C to copy.";
document.addEventListener("click", async (evt) => {
  const btn = evt.target.closest?.("[data-hm-copy-field],[data-hm-copy-url]");
  if (!btn) return;
  let text;
  if (btn.dataset.hmCopyField !== undefined) {
    const field = document.getElementById(btn.dataset.hmCopyField);
    if (!field) return;
    field.select();
    text = field.value;
  } else {
    text = new URL(btn.dataset.hmCopyUrl, location.origin).href;
  }
  try {
    await navigator.clipboard.writeText(text);
    showToast({ kind: "success", message: "Copied." });
  } catch {
    showToast({ kind: "info", message: copyFallback });
  }
});
function revealCopyUrl(root) {
  if (navigator.clipboard) root.querySelectorAll("[data-hm-copy-url]").forEach((b) => { b.hidden = false; });
}
document.addEventListener("DOMContentLoaded", () => revealCopyUrl(document));
document.addEventListener("htmx:afterSettle", (evt) => revealCopyUrl(evt.target));

// <a data-hm-dismiss="element-id" href="..."> removes the element instead of
// following the link; without JS the link reloads the list without the banner.
document.addEventListener("click", (evt) => {
  const btn = evt.target.closest?.("[data-hm-dismiss]");
  const target = btn && document.getElementById(btn.dataset.hmDismiss);
  if (!target) return;
  evt.preventDefault();
  target.remove();
});

// Callouts with a data-hm-dismiss-key can be dismissed; the choice is kept for the
// browser session (sessionStorage), so a dismissed banner stays away on later pages.
function hideDismissedCallouts(root) {
  root.querySelectorAll?.(".hm-callout[data-hm-dismiss-key]").forEach((el) => {
    if (isDismissed(safeStorage("session"), el.dataset.hmDismissKey)) el.remove();
  });
}
document.addEventListener("DOMContentLoaded", () => hideDismissedCallouts(document));
document.addEventListener("click", (evt) => {
  const callout = evt.target.closest?.("[data-hm-callout-dismiss]")?.closest(".hm-callout");
  if (!callout) return;
  markDismissed(safeStorage("session"), callout.dataset.hmDismissKey);
  callout.remove();
});

// Toasts: the server sends HX-Trigger {"hm:toast":{kind,message,timeout}}.
const TOAST_KINDS = new Set(["error", "warning", "success", "info"]);
const TOAST_MAX = 4; // per host
function showToast(detail) {
  // Errors (role=alert) stay until dismissed: an auto-dismissing error can be
  // missed (WCAG 2.2.1). Everything else goes after a few seconds.
  const persistent = detail.kind === "error";
  // Both hosts are persistent live regions in the layout (polite status, assertive alert):
  // a region that already exists announces what is inserted into it, so nodes carry no role.
  const host = document.getElementById(persistent ? "hm-toasts-alert" : "hm-toasts");
  const tpl = document.getElementById("hm-toast-tpl");
  if (!host || !tpl) return;
  const node = tpl.content.firstElementChild.cloneNode(true);
  const kind = TOAST_KINDS.has(detail.kind) ? detail.kind : "info";
  node.classList.add(`hm-toast--${kind}`);
  node.querySelectorAll("[data-hm-toast-icon]").forEach((i) => { if (i.dataset.hmToastIcon !== kind) i.remove(); });
  node.querySelector("[data-hm-toast-message]").textContent = String(detail.message ?? "");
  const remove = () => node.remove();
  node.querySelector("[data-hm-toast-close]").addEventListener("click", remove);
  host.appendChild(node);
  while (host.children.length > TOAST_MAX) host.firstElementChild.remove();
  let timer = persistent ? 0 : setTimeout(remove, detail.timeout ?? 4000);
  const pause = () => clearTimeout(timer);
  const resume = () => { if (!persistent) timer = setTimeout(remove, 2000); };
  node.addEventListener("mouseenter", pause);
  node.addEventListener("focusin", pause);
  node.addEventListener("mouseleave", resume);
  node.addEventListener("focusout", resume);
}
document.addEventListener("hm:toast", (evt) => showToast(evt.detail ?? {}));

// Esc dismisses the newest toast, unless the key belongs to an open dialog or popover.
document.addEventListener("keydown", (evt) => {
  if (evt.key !== "Escape" || evt.defaultPrevented) return;
  if (document.querySelector("dialog[open], :popover-open")) return;
  const last = document.getElementById("hm-toasts-alert")?.lastElementChild || document.getElementById("hm-toasts")?.lastElementChild;
  if (last) { evt.preventDefault(); last.remove(); }
});

// Tables: select-all and the bulk toolbar. The checkboxes work without JS (they
// post with the form); this only reveals the toolbar and keeps the count.
function syncBulk(card) {
  const rows = [...card.querySelectorAll("[data-hm-row-select]")];
  const checked = rows.filter((c) => c.checked).length;
  const bar = card.querySelector("[data-hm-bulk-toolbar]");
  if (bar) {
    bar.hidden = checked === 0;
    bar.querySelector("[data-hm-bulk-count]").textContent = `${checked} selected`;
  }
  const all = card.querySelector("[data-hm-select-all]");
  if (all) {
    all.checked = rows.length > 0 && checked === rows.length;
    all.indeterminate = checked > 0 && checked < rows.length;
  }
}
// Without JS the toolbar and the Apply button are always shown (they are plain
// submit buttons in a form). With JS the toolbar appears once rows are ticked
// and the filters apply as they change, so Apply steps aside.
function initTables(root) {
  root.querySelectorAll?.(".hm-tablecard").forEach(syncBulk);
  root.querySelectorAll?.("[data-hm-apply]").forEach((b) => b.classList.add("hm-visually-hidden-focusable"));
}
document.addEventListener("DOMContentLoaded", () => initTables(document));
document.addEventListener("htmx:afterSettle", (evt) => initTables(evt.target));
document.addEventListener("change", (evt) => {
  const t = evt.target;
  const card = t.closest?.(".hm-tablecard");
  if (!card) return;
  if (t.matches("[data-hm-select-all]")) {
    card.querySelectorAll("[data-hm-row-select]").forEach((c) => { c.checked = t.checked; });
    syncBulk(card);
  } else if (t.matches("[data-hm-row-select]")) {
    syncBulk(card);
  }
});

// Error summaries take focus when they are rendered (also after an htmx swap).
function focusErrors(root) {
  const el = root.querySelector?.("[data-hm-focus]");
  if (el) el.focus();
}
document.addEventListener("htmx:afterSwap", (evt) => focusErrors(evt.target));
document.addEventListener("DOMContentLoaded", () => focusErrors(document));

// Flash messages arrive as inline banners (they must work without JS, and they
// survive a reload of the page). With JS they become toasts: announced through
// the live region, auto-dismissed unless they are errors.
function flashToToasts(root) {
  root.querySelectorAll("[data-hm-flash]").forEach((el) => {
    showToast({ kind: el.dataset.hmFlash, message: el.textContent.trim() });
    el.remove();
  });
}
document.addEventListener("DOMContentLoaded", () => flashToToasts(document));
document.addEventListener("htmx:afterSettle", () => flashToToasts(document));

// Menus (row menus, the page-header overflow) and popover panels (Filters):
// placed under the opener, focus moves to the first item or field, arrow keys
// move between menu items, Esc and light dismiss are native, and focus goes
// back to the opener on close.
// Items a keyboard can land on: disabled (Undo with nothing to undo) and hidden ones are skipped.
const menuItems = (m) => [...m.querySelectorAll('[role="menuitem"]')].filter((i) => !i.disabled && i.getAttribute("aria-disabled") !== "true" && !i.hidden && !i.closest("[hidden]"));
const openerOf = (m) => document.querySelector(`[popovertarget="${m.id}"]:not([popovertargetaction="hide"])`);
document.addEventListener("toggle", (evt) => {
  const m = evt.target;
  if (!m.classList?.contains("hm-menu") && !m.classList?.contains("hm-popover-panel")) return;
  const opener = openerOf(m);
  opener?.setAttribute("aria-expanded", String(evt.newState === "open"));
  if (evt.newState !== "open") {
    if (m.contains(document.activeElement) || document.activeElement === document.body) opener?.focus();
    return;
  }
  if (opener) {
    // Clamp to the visible width (scrollbar excluded) with an 8px margin. Menus and the
    // date range sit right-aligned under their button; other panels left-aligned.
    const r = opener.getBoundingClientRect();
    const vw = document.documentElement.clientWidth;
    m.style.top = `${Math.max(8, Math.min(r.bottom + 4, innerHeight - m.offsetHeight - 8))}px`;
    const end = m.classList.contains("hm-menu") || m.classList.contains("hm-daterange__pop");
    const left = end ? r.right - m.offsetWidth : r.left;
    m.style.left = `${Math.max(8, Math.min(left, vw - m.offsetWidth - 8))}px`;
  }
  const filter = m.querySelector("[data-hm-menu-filter]");
  if (filter) {
    filter.value = "";
    filterMenu(m, "");
    filter.focus();
    return;
  }
  (menuItems(m)[0] || m.querySelector("a[href], input:not([type=hidden]), select, textarea, button"))?.focus();
}, true);
// A filterable menu (data-hm-menu-filter): typing hides the items that do not
// match; ArrowDown moves to the first match, Enter follows it.
function filterMenu(m, q) {
  const needle = q.trim().toLowerCase();
  let shown = 0;
  for (const it of m.querySelectorAll('[role="menuitem"]')) {
    const hit = !needle || it.textContent.toLowerCase().includes(needle);
    it.hidden = !hit;
    if (hit) shown++;
  }
  const empty = m.querySelector("[data-hm-menu-empty]");
  if (empty) empty.hidden = shown > 0;
}
document.addEventListener("input", (evt) => {
  const f = evt.target.closest?.("[data-hm-menu-filter]");
  if (f) filterMenu(f.closest(".hm-menu"), f.value);
});
document.addEventListener("keydown", (evt) => {
  const f = evt.target.closest?.("[data-hm-menu-filter]");
  if (!f) return;
  const first = menuItems(f.closest(".hm-menu")).filter((i) => !i.hidden)[0];
  if (evt.key === "ArrowDown" && first) { evt.preventDefault(); first.focus(); }
  if (evt.key === "Enter" && first) { evt.preventDefault(); first.click(); }
});
document.addEventListener("keydown", (evt) => {
  const m = evt.target.closest?.(".hm-menu");
  if (!m || evt.target.matches?.("[data-hm-menu-filter]")) return;
  const items = menuItems(m);
  const i = items.indexOf(evt.target);
  const n = { ArrowDown: i + 1, ArrowUp: i - 1, Home: 0, End: items.length - 1 }[evt.key];
  if (n === undefined || items.length === 0) return;
  evt.preventDefault();
  items[(n + items.length) % items.length].focus();
});

// Tab leaves a menu and closes it: focus goes to the opener first, so the Tab that is
// already in flight continues from there to the next control on the page.
document.addEventListener("keydown", (evt) => {
  if (evt.key !== "Tab") return;
  const m = evt.target.closest?.(".hm-menu");
  if (!m) return;
  openerOf(m)?.focus();
  m.hidePopover();
});

// Tooltips (WCAG 1.4.13): Esc hides every visible tooltip until the pointer
// moves or focus changes. The label is also aria-label; this is only the visual.
document.addEventListener("keydown", (evt) => { if (evt.key === "Escape") document.documentElement.dataset.tooltipsHidden = ""; });
const showTips = () => { delete document.documentElement.dataset.tooltipsHidden; };
document.addEventListener("pointermove", showTips);
document.addEventListener("focusin", showTips);

// Confirmation dialogs. A link with data-hm-confirm goes to a confirmation page
// without JS; with JS it opens the native <dialog> and posts from there. openConfirm
// is the one place that fills #hm-confirm: the title names the action (title, else
// "<okLabel>?"), the message is announced as its description, and the confirm button
// is red only while danger is set. fields are extra hidden inputs as [name, value]
// pairs. Focus returns to opener when the dialog closes.
export function openConfirm({ action, message, okLabel = "Confirm", title, danger = false, fields = [], opener }) {
  const dlg = document.getElementById("hm-confirm");
  if (!dlg || typeof dlg.showModal !== "function") return false;
  const form = dlg.querySelector("form");
  form.action = action;
  form.elements._csrf.value = document.querySelector('meta[name="csrf-token"]')?.content ?? "";
  form.querySelectorAll("[data-hm-confirm-field]").forEach((n) => n.remove());
  for (const [name, value] of fields) {
    const input = document.createElement("input");
    input.type = "hidden"; input.name = name; input.value = value; input.dataset.hmConfirmField = "";
    form.appendChild(input);
  }
  dlg.querySelector("#hm-confirm-title").textContent = title || (okLabel === "Confirm" ? "Are you sure?" : `${okLabel}?`);
  dlg.querySelector("[data-hm-confirm-message]").textContent = message;
  const ok = dlg.querySelector("[data-hm-confirm-ok]");
  ok.querySelector("span").textContent = okLabel;
  ok.classList.toggle("hm-btn--danger", danger);
  ok.classList.toggle("hm-btn--primary", !danger);
  if (opener) dlg.addEventListener("close", () => opener.focus(), { once: true });
  dlg.showModal();
  return true;
}
document.addEventListener("click", (evt) => {
  const link = evt.target.closest?.("[data-hm-confirm]");
  if (!link || !document.getElementById("hm-confirm") || evt.defaultPrevented) return;
  if (evt.button !== 0 || evt.metaKey || evt.ctrlKey || evt.shiftKey || evt.altKey) return;
  evt.preventDefault();
  // A menu item disappears with its menu: focus returns to the menu's button.
  const menu = link.closest(".hm-menu");
  const opener = menu ? openerOf(menu) : link;
  menu?.hidePopover();
  // A submit button inside a form posts that form's own fields (minus the token) from the dialog.
  const form = link.matches("button[type=submit], input[type=submit]") ? link.closest("form") : null;
  const post = form ? confirmPost(form, link) : { action: link.dataset.hmPost, fields: [] };
  openConfirm({
    action: post.action, fields: post.fields,
    message: link.dataset.hmConfirm, okLabel: link.dataset.hmConfirmLabel || "Confirm",
    title: link.dataset.hmConfirmTitle, danger: "hmDanger" in link.dataset, opener,
  });
});
// A link with data-hm-carry="input-id" takes that field's value along as ?email= (the invite
// form's "Add with a password instead"). Without script it is a plain link.
document.addEventListener("click", (evt) => {
  const a = evt.target.closest?.("[data-hm-carry]");
  const field = a && document.getElementById(a.dataset.hmCarry);
  if (!field) return;
  a.dataset.hmCarryHref ??= a.getAttribute("href");
  a.href = carryHref(a.dataset.hmCarryHref, field.value, location.href);
});
document.addEventListener("click", (evt) => {
  if (evt.target.closest?.("[data-hm-confirm-cancel]")) document.getElementById("hm-confirm")?.close();
});

// Character counters ("12/80" under a limited text field).
document.addEventListener("input", (evt) => {
  const t = evt.target;
  if (!t.id || !(t.maxLength > 0)) return;
  const counter = document.querySelector(`[data-hm-counter="${CSS.escape(t.id)}"]`);
  if (counter) counter.textContent = `${[...t.value].length}/${t.maxLength}`;
});
// hx-indicator shows the spinner (CSS, via .htmx-request); this adds what a
// screen reader needs: aria-busy on the region being updated and the indicator's
// status text while the request runs. An indicator with empty text stays silent.
function busyIndicators(elt) {
  const sel = elt.closest?.("[hx-indicator]")?.getAttribute("hx-indicator") ?? "";
  return sel.startsWith("#") ? [...document.querySelectorAll(sel)] : [];
}
function setBusy(evt, on) {
  const { elt, target } = evt.detail;
  if (!elt) return;
  if (target?.setAttribute) on ? target.setAttribute("aria-busy", "true") : target.removeAttribute("aria-busy");
  for (const ind of busyIndicators(elt)) {
    const label = ind.querySelector("[data-hm-busy-label]");
    if (label) label.textContent = on ? ind.dataset.hmBusyText ?? "" : "";
  }
}
document.addEventListener("htmx:beforeRequest", (evt) => setBusy(evt, true));
document.addEventListener("htmx:afterRequest", (evt) => setBusy(evt, false));
// The submitter gets aria-busy and aria-disabled, never `disabled`: a disabled submitter is
// dropped from the form data and the editors' _op/op/action buttons carry their intent in
// name/value. A second submit while busy is cancelled. htmx forms, bulk toolbars, dialog
// forms, the confirm dialog and the dropzone upload manage their own pending state.
// The lock is time-boxed: Stop, Esc or a beforeunload "Stay" leave the page alive with no
// pageshow, and a form that stays locked cannot be saved again. File uploads (multipart) are
// exempt from the time-box: a slow transfer outlasts it, and a second click would abort the
// upload in flight and restart it. They stay locked until the page navigates.
const BUSY_MAX_MS = 10000;
const busyButtons = new Set();
let busyTimer = 0;
function clearBusy() {
  clearTimeout(busyTimer);
  for (const b of busyButtons) {
    b.removeAttribute("aria-busy");
    b.removeAttribute("aria-disabled");
    const label = b.querySelector("[data-hm-busy-restore]");
    if (label) { label.textContent = label.dataset.hmBusyRestore; label.removeAttribute("data-hm-busy-restore"); }
  }
  busyButtons.clear();
}
document.addEventListener("submit", (evt) => {
  const form = evt.target;
  if (evt.defaultPrevented || !form.matches?.("form[method=post i]")) return;
  if (form.matches("[hx-post],[hx-get],[hx-put],[hx-patch],[hx-delete],[data-hm-bulk-toolbar],[data-hm-dropzone],[target]") || form.closest("#hm-confirm")) return;
  const external = form.id ? `button[type=submit][form="${CSS.escape(form.id)}"]` : null;
  const btn = evt.submitter ?? form.querySelector("button[type=submit]:not([form])") ?? (external && document.querySelector(external));
  if (btn?.getAttribute("aria-busy") === "true" || [...busyButtons].some((b) => b.form === form)) return evt.preventDefault();
  if (!btn || btn.hasAttribute("formtarget") || btn.formMethod === "get") return; // stays on this page
  btn.setAttribute("aria-busy", "true");
  btn.setAttribute("aria-disabled", "true");
  const label = btn.dataset.hmBusyText && btn.querySelector("span");
  if (label) { label.dataset.hmBusyRestore = label.textContent; label.textContent = btn.dataset.hmBusyText; }
  busyButtons.add(btn);
  if ((btn.formEnctype || form.enctype) === "multipart/form-data") return;
  clearTimeout(busyTimer);
  busyTimer = setTimeout(clearBusy, BUSY_MAX_MS);
});
// A page restored from the back/forward cache still shows the pending state.
window.addEventListener("pageshow", (evt) => { if (evt.persisted) clearBusy(); });

// A focused control must never sit under the sticky save bar. Browsers treat a
// partly visible element as in view and do not scroll, so scroll by the overlap.
document.addEventListener("focusin", (evt) => {
  const el = evt.target;
  const bar = document.querySelector(".hm-savebar");
  if (!bar || !(el instanceof Element) || bar.contains(el)) return;
  const r = el.getBoundingClientRect();
  const b = bar.getBoundingClientRect();
  if (r.bottom <= b.top || r.top >= b.bottom) return;
  const free = b.top;
  const gap = 8;
  const dy = r.height + gap * 2 > free ? r.top - gap : r.bottom - b.top + gap;
  window.scrollBy({ top: dy, behavior: "instant" });
});

// Date range picker: a preset is a plain link and applies at once; Apply is only
// for the custom From/To. To can not precede From.
document.addEventListener("change", (evt) => {
  const pop = evt.target.closest?.("[data-hm-daterange]");
  if (!pop || evt.target.name !== "from") return;
  const to = pop.querySelector('[name="to"]');
  to.min = evt.target.value;
  if (to.value < evt.target.value) to.value = evt.target.value;
});

// DangerConfirm: the server always checks confirm_phrase; this only keeps the danger submit
// disabled until the typed text equals the phrase. Server HTML never disables it, so
// without JS the form still works.
function syncDanger(form) {
  const input = form.querySelector("[data-hm-danger-input]");
  const btn = form.querySelector("[data-hm-danger-submit]");
  if (input && btn) btn.disabled = !phraseMatches(input.value, form.dataset.hmDangerPhrase ?? "");
}
function syncDangerAll(root) {
  root.querySelectorAll?.("[data-hm-danger-confirm]").forEach(syncDanger);
}
document.addEventListener("input", (evt) => {
  const form = evt.target.closest?.("[data-hm-danger-confirm]");
  if (form) syncDanger(form);
});
document.addEventListener("DOMContentLoaded", () => syncDangerAll(document));
window.addEventListener("pageshow", () => syncDangerAll(document));
document.addEventListener("htmx:afterSettle", (evt) => syncDangerAll(evt.target));

// Allow unsigned packages: the submit stays disabled until the acknowledgement, the typed
// site address, a duration and a password are all given. The server checks every gate; server
// HTML never disables the button, so without JS the form still works.
function syncGate(form) {
  const btn = form.querySelector("[data-hm-gate-submit]");
  if (!btn) return;
  btn.disabled = !unsignedGatesMet({
    ack: form.elements.understand?.checked === true,
    host: form.elements.host?.value ?? "",
    siteHost: form.dataset.hmGateHost ?? "",
    password: form.elements.password?.value ?? "",
    duration: form.elements.duration?.value ?? "",
  });
}
function syncGateAll(root) {
  root.querySelectorAll?.("[data-hm-gate]").forEach(syncGate);
}
document.addEventListener("input", (evt) => {
  const form = evt.target.closest?.("[data-hm-gate]");
  if (form) syncGate(form);
});
document.addEventListener("change", (evt) => {
  const form = evt.target.closest?.("[data-hm-gate]");
  if (form) syncGate(form);
});
document.addEventListener("DOMContentLoaded", () => syncGateAll(document));
window.addEventListener("pageshow", () => syncGateAll(document));
document.addEventListener("htmx:afterSettle", (evt) => syncGateAll(evt.target));
