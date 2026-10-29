// Pure helpers of the shell glue; storage and forms come in as arguments, so node can test them
// (scripts/admin-qa/unit/shelllogic.test.mjs).

export const SIDEBAR_KEY = "hm-sidebar";

// window.localStorage/sessionStorage ("local"/"session"); null when blocked (reading it throws).
export function safeStorage(kind, win = globalThis) {
  try { return win[`${kind}Storage`] ?? null; } catch { return null; }
}

// confirmPost: where the confirm dialog posts and what it carries for a submit button in a form.
// The button's formaction beats the form's action; no action attribute means the page URL.
// FormData(form, submitter) adds the button's name and value; _csrf is dropped (the dialog adds
// its own). Attribute, never .action: a control named "action" shadows that property.
export function confirmPost(form, submitter, FormDataCtor = FormData) {
  return {
    action: submitter.getAttribute("formaction") || (form.getAttribute("action") ?? location.href),
    fields: [...new FormDataCtor(form, submitter)].filter(([k]) => k !== "_csrf"),
  };
}

// carryHref returns href with ?email= set to value; an empty value gives the href back unchanged,
// so a field the user cleared does not leave an old address in the link.
export function carryHref(href, value, base) {
  if (!value) return href;
  const url = new URL(href, base);
  url.searchParams.set("email", value);
  return url.toString();
}

// rememberedSidebar reports whether the stored state is "collapsed" on a screen wide enough to
// have the toggle. Blocked storage means "not collapsed".
export function rememberedSidebar(storage, desktop) {
  try { return desktop && storage.getItem(SIDEBAR_KEY) === "collapsed"; } catch { return false; }
}

export function rememberSidebar(storage, collapsed) {
  try { storage.setItem(SIDEBAR_KEY, collapsed ? "collapsed" : "open"); } catch { /* storage blocked */ }
}

const dismissedKey = (key) => `hm-dismissed:${key}`;

export function isDismissed(storage, key) {
  try { return Boolean(storage.getItem(dismissedKey(key))); } catch { return false; }
}

export function markDismissed(storage, key) {
  try { storage.setItem(dismissedKey(key), "1"); } catch { /* storage blocked */ }
}

// A photo that fails to load becomes the initials avatar, or (form preview) is hidden.
export function photoFallback(img, doc = document) {
  const initials = img.dataset?.initials;
  if (initials === undefined) { img.hidden = true; return; }
  const avatar = doc.createElement("span");
  avatar.className = "hm-avatar";
  avatar.dataset.initials = initials;
  avatar.ariaHidden = "true";
  img.replaceWith(avatar);
}

// Failed before this script ran.
export const photoFailed = (img) => img.complete && !img.naturalWidth;

// DangerConfirm phrase check. Exact match, like the server: no trim, no case folding.
export const phraseMatches = (typed, phrase) => phrase !== "" && typed === phrase;

// Allow-unsigned form (data-hm-gate): true when every gate has an answer. It only
// enables the submit button; the server checks all four gates again. The address
// is compared the way the server does, trimmed and ignoring case; an empty site
// address never matches, and a password only has to be present here.
export const UNSIGNED_DURATIONS = ["1h", "24h", "forever"];
export const unsignedGatesMet = ({ ack, host, siteHost, password, duration }) =>
  ack === true && siteHost !== "" && String(host).trim().toLowerCase() === siteHost.toLowerCase() && UNSIGNED_DURATIONS.includes(duration) && String(password) !== "";

// Admin fetches carry the token.
export function installCsrfFetch(g, base, token) {
  const f = g.fetch;
  if (!token || f.c) return;
  (g.fetch = (i, o) => {
    const u = new URL(i.url ?? i, g.location.href), p = u.pathname + "/";
    if (u.origin === g.location.origin && p.startsWith(base + "/") && !p.startsWith(base + "/_/")) {
      const h = new Headers(o?.headers ?? i.headers);
      h.has("X-CSRF-Token") || h.set("X-CSRF-Token", token);
      o = { ...o, headers: h };
    }
    return f.call(g, i, o);
  }).c = 1;
}
