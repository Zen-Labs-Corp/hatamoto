/*
 * hm-consent.js: the core cookie consent banner and Consent Mode v2 bridge.
 * Served by Hatamoto at /theme/assets/hm-consent.js and placed first in <head>
 * when the owner turns the banner on; themes do not ship it. The settings are
 * substituted for __HM_CONSENT__ when the file is served.
 *
 * Public API (documented in the site docs):
 *   hm.consent.get()          -> {necessary:true, analytics, marketing, functional, decided}
 *   hm.consent.allows(cat)    -> boolean (analytics|marketing|functional|necessary)
 *   hm.consent.open()         -> open the preferences dialog
 *   hm.consent.version        -> category-set version of this configuration
 *   document event "hm:consent", detail = get(), on every change and once at
 *   load when a valid stored choice exists.
 * Without this script hm.consent is undefined and every category is granted.
 *
 * The choice lives in the hm_consent cookie (mirrored in localStorage). The
 * only thing sent to the server is a counter beacon: {choice: all|none|custom}.
 * Dialog and banner are built with createElement/textContent only.
 */
(function () {
  'use strict';
  var CFG = __HM_CONSENT__;
  var KEY = 'hm_consent';
  var YEAR_S = 31536000;
  var CATS = ['analytics', 'marketing', 'functional'];
  var FLAG = { analytics: 'a', marketing: 'm', functional: 'f' };
  var S = CFG.strings || {};
  var enabled = {};
  (CFG.categories || []).forEach(function (c) { enabled[c] = true; });

  var w = window;
  var d = document;
  var hm = w.hm = w.hm || {};

  // ---- Consent Mode v2: defaults first, before anything else on the page.
  w.dataLayer = w.dataLayer || [];
  if (typeof w.gtag !== 'function') {
    w.gtag = function () { w.dataLayer.push(arguments); };
  }
  var DENIED = {
    ad_storage: 'denied', ad_user_data: 'denied', ad_personalization: 'denied',
    analytics_storage: 'denied', functionality_storage: 'denied',
    personalization_storage: 'denied', security_storage: 'granted'
  };
  if (CFG.region === 'eu_uk') DENIED.wait_for_update = 500;
  w.gtag('consent', 'default', DENIED);

  var state = { analytics: false, marketing: false, functional: false, decided: false };

  function snapshot() {
    return {
      necessary: true, analytics: state.analytics, marketing: state.marketing,
      functional: state.functional, decided: state.decided
    };
  }

  function g(v) { return v ? 'granted' : 'denied'; }

  function announce() {
    w.gtag('consent', 'update', {
      analytics_storage: g(state.analytics),
      ad_storage: g(state.marketing), ad_user_data: g(state.marketing),
      ad_personalization: g(state.marketing),
      functionality_storage: g(state.functional),
      personalization_storage: g(state.functional)
    });
    d.dispatchEvent(new CustomEvent('hm:consent', { detail: snapshot() }));
  }

  // ---- Storage: cookie wins, localStorage mirrors it.
  // A sandboxed page (opaque origin) throws on every cookie access. It cannot
  // keep a choice, so the choice lives in memory for that page view only.
  function readCookie() {
    var parts;
    try { parts = d.cookie ? d.cookie.split('; ') : []; } catch (e) { return null; }
    for (var i = 0; i < parts.length; i++) {
      if (parts[i].indexOf(KEY + '=') === 0) return parts[i].slice(KEY.length + 1);
    }
    return null;
  }

  function readStored() {
    var raw = readCookie();
    if (raw === null) {
      try { raw = w.localStorage.getItem(KEY); } catch (e) { raw = null; }
    }
    if (!raw) return null;
    try {
      var o = JSON.parse(decodeURIComponent(raw));
      var age = Date.now() / 1000 - o.t;
      if (o.v !== CFG.version || !(age >= 0 && age < YEAR_S)) return null;
      return o;
    } catch (e) { return null; }
  }

  function writeStored() {
    var o = {
      v: CFG.version, t: Math.floor(Date.now() / 1000),
      a: state.analytics ? 1 : 0, m: state.marketing ? 1 : 0, f: state.functional ? 1 : 0
    };
    var val = encodeURIComponent(JSON.stringify(o));
    try {
      d.cookie = KEY + '=' + val + '; Max-Age=' + YEAR_S + '; Path=/; SameSite=Lax' +
        (w.location.protocol === 'https:' ? '; Secure' : '');
    } catch (e) { /* sandboxed page: the choice stays in memory */ }
    try { w.localStorage.setItem(KEY, val); } catch (e) { /* storage blocked */ }
  }

  function applyStored(o) {
    CATS.forEach(function (c) { state[c] = !!enabled[c] && o[FLAG[c]] === 1; });
    state.decided = true;
  }

  function choiceName() {
    var on = 0, total = 0;
    CATS.forEach(function (c) {
      if (!enabled[c]) return;
      total++;
      if (state[c]) on++;
    });
    if (on === 0) return 'none';
    return on === total ? 'all' : 'custom';
  }

  function count() {
    var body = JSON.stringify({ choice: choiceName() });
    var url = '/api/v1/public/consent';
    try {
      if (w.self.origin !== 'null' && navigator.sendBeacon && navigator.sendBeacon(url, body)) return;
    } catch (e) { /* fall through */ }
    try {
      w.fetch(url, { method: 'POST', body: body, keepalive: true, credentials: 'omit' }).catch(function () {});
    } catch (e) { /* counting is best effort */ }
  }

  function decide(values) {
    CATS.forEach(function (c) { state[c] = !!enabled[c] && !!values[c]; });
    state.decided = true;
    writeStored();
    announce();
    count();
    removeBanner();
    onReady(ensureReopen);
  }

  // ---- UI
  var CSS =
    '.hm-consent-banner,.hm-consent-backdrop{font:16px/1.5 system-ui,-apple-system,"Segoe UI",sans-serif;color:#111;box-sizing:border-box}' +
    '.hm-consent-banner *,.hm-consent-backdrop *{box-sizing:border-box}' +
    '.hm-consent-banner{position:fixed;left:16px;right:16px;bottom:16px;max-width:640px;z-index:2147483000;background:#fff;border:1px solid #767676;border-radius:8px;padding:16px 20px;box-shadow:0 4px 24px rgba(0,0,0,.2)}' +
    '.hm-consent-title{margin:0 0 4px;font-size:1.125rem;font-weight:700}' +
    '.hm-consent-text{margin:0 0 12px}' +
    '.hm-consent-actions{display:flex;flex-wrap:wrap;gap:8px}' +
    '.hm-consent-btn{font:inherit;font-weight:600;padding:8px 16px;border-radius:6px;border:2px solid #111;background:#fff;color:#111;cursor:pointer}' +
    '.hm-consent-btn[data-primary]{background:#111;color:#fff}' +
    '.hm-consent-backdrop{position:fixed;inset:0;z-index:2147483001;background:rgba(0,0,0,.5);display:flex;align-items:center;justify-content:center;padding:16px}' +
    '.hm-consent-dialog{background:#fff;border-radius:8px;max-width:520px;width:100%;max-height:100%;overflow:auto;padding:20px 24px}' +
    '.hm-consent-cat{margin:0 0 12px}' +
    '.hm-consent-cat label{display:flex;gap:8px;align-items:flex-start;font-weight:600}' +
    '.hm-consent-cat input{margin:5px 0 0;width:18px;height:18px;flex:none}' +
    '.hm-consent-desc{display:block;margin:0 0 0 26px;font-weight:400;color:#333}' +
    '.hm-consent-reopen{position:fixed;left:16px;bottom:16px;z-index:2147482999;font:inherit;font-size:.875rem;padding:6px 12px;border-radius:6px;border:1px solid #767676;background:#fff;color:#111;cursor:pointer}' +
    '.hm-consent-banner button:focus-visible,.hm-consent-backdrop button:focus-visible,.hm-consent-backdrop input:focus-visible,.hm-consent-reopen:focus-visible{outline:3px solid #1a56db;outline-offset:2px}' +
    '@media (prefers-reduced-motion:reduce){.hm-consent-banner,.hm-consent-backdrop{transition:none;animation:none}}';

  var banner = null;
  var dialog = null; // {backdrop, opener, boxes}
  var reopenBtn = null;


  function el(tag, cls, text) {
    var n = d.createElement(tag);
    if (cls) n.className = cls;
    if (text !== undefined) n.textContent = text;
    return n;
  }

  function button(label, primary, onClick) {
    var b = el('button', 'hm-consent-btn', label);
    b.type = 'button';
    if (primary) b.setAttribute('data-primary', '');
    b.addEventListener('click', onClick);
    return b;
  }

  function allOf(v) {
    var o = {};
    CATS.forEach(function (c) { o[c] = v; });
    return o;
  }

  function injectStyle() {
    if (d.getElementById('hm-consent-style')) return;
    var s = el('style');
    s.id = 'hm-consent-style';
    s.textContent = CSS;
    (d.head || d.documentElement).appendChild(s);
  }

  function showBanner() {
    if (banner || state.decided) return;
    injectStyle();
    banner = el('div', 'hm-consent-banner');
    banner.setAttribute('role', 'region');
    banner.setAttribute('aria-label', S.banner_label || 'Cookie notice');
    banner.appendChild(el('p', 'hm-consent-title', CFG.title || S.title));
    banner.appendChild(el('p', 'hm-consent-text', CFG.text || S.text));
    var row = el('div', 'hm-consent-actions');
    row.appendChild(button(S.reject, false, function () { decide(allOf(false)); }));
    row.appendChild(button(S.preferences, false, function (e) { openDialog(e.currentTarget); }));
    row.appendChild(button(S.accept, true, function () { decide(allOf(true)); }));
    banner.appendChild(row);
    d.body.appendChild(banner);
  }

  function removeBanner() {
    if (banner && banner.parentNode) banner.parentNode.removeChild(banner);
    banner = null;
  }

  function focusables(root) {
    return Array.prototype.filter.call(
      root.querySelectorAll('button,input,[href],select,textarea,[tabindex]'),
      function (n) { return !n.disabled && n.tabIndex >= 0; });
  }

  function closeDialog() {
    if (!dialog) return;
    var opener = dialog.opener;
    d.removeEventListener('keydown', dialog.onKey, true);
    dialog.backdrop.parentNode.removeChild(dialog.backdrop);
    dialog = null;
    if (opener && opener.isConnected && typeof opener.focus === 'function') opener.focus();
    else if (reopenBtn) reopenBtn.focus();
  }

  function openDialog(opener) {
    if (dialog) return;
    if (!d.body) return;
    injectStyle();
    var backdrop = el('div', 'hm-consent-backdrop');
    var box = el('div', 'hm-consent-dialog');
    box.setAttribute('role', 'dialog');
    box.setAttribute('aria-modal', 'true');
    box.setAttribute('aria-labelledby', 'hm-consent-dialog-title');
    var h = el('h2', 'hm-consent-title', S.dialog_title);
    h.id = 'hm-consent-dialog-title';
    box.appendChild(h);

    var boxes = {};
    function category(id, checked, locked) {
      var wrap = el('div', 'hm-consent-cat');
      var label = el('label');
      var input = el('input');
      input.type = 'checkbox';
      input.checked = checked;
      input.disabled = locked;
      label.appendChild(input);
      label.appendChild(d.createTextNode(S[id] || id));
      wrap.appendChild(label);
      wrap.appendChild(el('span', 'hm-consent-desc', S[id + '_desc'] || ''));
      box.appendChild(wrap);
      return input;
    }
    category('necessary', true, true);
    CATS.forEach(function (c) {
      if (enabled[c]) boxes[c] = category(c, state[c], false);
    });

    function values() {
      var o = {};
      CATS.forEach(function (c) { o[c] = !!(boxes[c] && boxes[c].checked); });
      return o;
    }
    var row = el('div', 'hm-consent-actions');
    row.appendChild(button(S.reject, false, function () { var v = allOf(false); closeDialog(); decide(v); }));
    row.appendChild(button(S.accept, false, function () { var v = allOf(true); closeDialog(); decide(v); }));
    row.appendChild(button(S.save, true, function () { var v = values(); closeDialog(); decide(v); }));
    row.appendChild(button(S.close, false, closeDialog));
    box.appendChild(row);
    backdrop.appendChild(box);
    backdrop.addEventListener('mousedown', function (e) { if (e.target === backdrop) closeDialog(); });

    function onKey(e) {
      if (e.key === 'Escape') {
        e.preventDefault();
        e.stopPropagation();
        closeDialog();
        return;
      }
      if (e.key !== 'Tab') return;
      var f = focusables(box);
      if (!f.length) return;
      var first = f[0], last = f[f.length - 1];
      if (!box.contains(d.activeElement)) {
        e.preventDefault();
        first.focus();
      } else if (e.shiftKey && d.activeElement === first) {
        e.preventDefault();
        last.focus();
      } else if (!e.shiftKey && d.activeElement === last) {
        e.preventDefault();
        first.focus();
      }
    }
    d.addEventListener('keydown', onKey, true);
    dialog = { backdrop: backdrop, opener: opener, onKey: onKey };
    d.body.appendChild(backdrop);
    var f = focusables(box);
    if (f.length) f[0].focus();
  }

  function onReady(fn) {
    if (d.readyState === 'loading') d.addEventListener('DOMContentLoaded', fn);
    else fn();
  }

  function ensureReopen() {
    if (d.querySelector('[data-hm-consent-open]') || reopenBtn) return;
    injectStyle();
    reopenBtn = el('button', 'hm-consent-reopen', S.reopen);
    reopenBtn.type = 'button';
    reopenBtn.setAttribute('data-hm-consent-open', '');
    d.body.appendChild(reopenBtn);
  }

  d.addEventListener('click', function (e) {
    var t = e.target && e.target.closest ? e.target.closest('[data-hm-consent-open]') : null;
    if (!t) return;
    e.preventDefault();
    openDialog(t);
  });

  hm.consent = {
    version: CFG.version,
    get: snapshot,
    allows: function (cat) {
      if (cat === 'necessary') return true;
      return CATS.indexOf(cat) >= 0 && !!state[cat];
    },
    open: function () { onReady(function () { openDialog(d.activeElement); }); }
  };

  // ---- Start
  function startUI() {
    onReady(function () {
      if (state.decided) ensureReopen();
      else showBanner();
    });
  }

  var stored = readStored();
  if (stored) {
    applyStored(stored);
    announce();
    startUI();
  } else if (CFG.region === 'eu_uk') {
    // Outside the region nobody is asked and everything is allowed.
    var url = '/api/v1/public/consent/region';
    w.fetch(url, { credentials: 'omit', cache: 'no-store' })
      .then(function (r) { return r.json(); })
      .then(function (j) { return !(j && j.show === false); }, function () { return true; })
      .then(function (show) {
        if (show) { startUI(); return; }
        CATS.forEach(function (c) { state[c] = true; });
        state.decided = true;
        announce();
      });
  } else {
    startUI();
  }
})();
