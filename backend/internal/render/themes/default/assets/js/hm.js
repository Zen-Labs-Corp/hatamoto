/* hm.js - progressive enhancement for the default theme. Every feature works
 * without JavaScript; this only upgrades it. No dependencies. */
(function () {
  'use strict';

  // Keys left out of the JSON body: the honeypot, the redirect target and the
  // providers' own response fields (hm_captcha carries the token instead).
  // hm_ts, the timing token, is sent.
  var SKIP = {
    _redirect: 1, hm_website: 1,
    'cf-turnstile-response': 1, 'g-recaptcha-response': 1, 'h-captcha-response': 1,
  };

  var SCRIPTS = {
    turnstile: 'https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit',
    hcaptcha: 'https://js.hcaptcha.com/1/api.js?render=explicit',
    recaptcha: 'https://www.google.com/recaptcha/api.js?render=',
  };
  var scriptLoads = {};
  var CAPTCHA_TIMEOUT = 10000; // ms to wait for a reCAPTCHA token before giving up

  function loadScript(src) {
    if (!scriptLoads[src]) {
      scriptLoads[src] = new Promise(function (resolve, reject) {
        var el = document.createElement('script');
        el.src = src;
        el.async = true;
        el.onload = resolve;
        // Forget a failed load so the next submit tries the script again.
        el.onerror = function (e) { delete scriptLoads[src]; el.remove(); reject(e); };
        document.head.appendChild(el);
      });
    }
    return scriptLoads[src];
  }

  function setHidden(form, name, value) {
    var input = form.elements[name];
    if (!input) {
      input = document.createElement('input');
      input.type = 'hidden';
      input.name = name;
      form.appendChild(input);
    }
    input.value = value;
  }

  // Timing token: fetched at load, never part of the (cached) page, so the
  // server can tell a form filled in seconds from one a person took time on.
  function addTimingToken(form) {
    var m = /^\/forms\/([^/]+)\/submit$/.exec(form.getAttribute('action') || '');
    if (!m || !window.fetch) return;
    fetch('/api/v1/public/forms/' + m[1] + '/token', { credentials: 'omit' })
      .then(function (res) { return res.ok ? res.json() : null; })
      .then(function (body) {
        // A token restored from a draft is older, so it shows the real time on the page.
        if (body && body.token && !(form.elements.hm_ts && form.elements.hm_ts.value)) setHidden(form, 'hm_ts', body.token);
      })
      .catch(function () { /* no token: the server skips the timing check */ });
  }

  // Captcha: renders the provider's widget into the form's container and keeps
  // its token in hm_captcha. Returns null for a form without one; otherwise
  // token() resolves once a token is in hm_captcha and reset() asks for a new one.
  function setupCaptcha(form) {
    var box = form.querySelector('[data-hm-captcha]');
    if (!box) return null;
    var provider = box.getAttribute('data-hm-captcha');
    var key = box.getAttribute('data-sitekey');
    var widget;
    function api() { return window[provider === 'recaptcha' ? 'grecaptcha' : provider]; }
    function onToken(token) { setHidden(form, 'hm_captcha', token || ''); }

    if (provider === 'recaptcha') {
      // v3 is invisible: a token is requested at submit time. token() resolves
      // false when none could be had (blocked script, bad key, no answer in
      // CAPTCHA_TIMEOUT ms) so the submit never hangs.
      return {
        token: function () {
          var attempt = loadScript(SCRIPTS.recaptcha + encodeURIComponent(key)).then(function () {
            return new Promise(function (resolve) {
              try {
                api().ready(function () {
                  try {
                    api().execute(key, { action: 'hm_form' }).then(function (t) { onToken(t); resolve(true); }, function () { resolve(false); });
                  } catch (e) { resolve(false); }
                });
              } catch (e) { resolve(false); }
            });
          }).catch(function () { return false; });
          var timeout = new Promise(function (resolve) { setTimeout(function () { resolve(false); }, CAPTCHA_TIMEOUT); });
          return Promise.race([attempt, timeout]);
        },
        reset: function () { onToken(''); },
      };
    }
    if (!SCRIPTS[provider]) return null;
    function mount() {
      return loadScript(SCRIPTS[provider]).then(function () {
        if (widget !== undefined) return;
        widget = api().render(box, {
          sitekey: key,
          callback: onToken,
          'expired-callback': function () { onToken(''); },
          'error-callback': function () { onToken(''); },
        });
      }).catch(function () { /* blocked script: the server refuses the submit */ });
    }
    mount();
    return {
      // A script that failed to load is retried on the next submit.
      // The script load gets the same CAPTCHA_TIMEOUT as reCAPTCHA, so a stalled
      // request shows the retry message instead of leaving Send dead.
      token: function () {
        if (widget !== undefined) return Promise.resolve();
        var timeout = new Promise(function (resolve) { setTimeout(function () { resolve(false); }, CAPTCHA_TIMEOUT); });
        return Promise.race([mount(), timeout]);
      },
      reset: function () {
        onToken('');
        if (widget !== undefined && api()) api().reset(widget);
      },
    };
  }

  // Forms: submit as JSON to the public API and swap in a success message;
  // on a server or network failure fall back to the native urlencoded POST
  // (303 flow). A 422 (failed spam check, refused message) shows the server's
  // message inline instead: a native re-post would only fail again.
  function collect(form) {
    var data = {};
    new FormData(form).forEach(function (value, key) {
      if (SKIP[key] || typeof value !== 'string') return;
      if (Object.prototype.hasOwnProperty.call(data, key)) {
        data[key] = [].concat(data[key], value);
      } else {
        data[key] = value;
      }
    });
    return data;
  }

  var CAPTCHA_RETRY = 'The spam check could not be completed. Please try again.';

  function showError(form, message) {
    var box = form.parentNode.querySelector('.hm-form__error');
    if (!box) {
      box = document.createElement('p');
      box.className = 'hm-form__error';
      box.setAttribute('role', 'alert');
      form.parentNode.insertBefore(box, form);
    }
    box.textContent = message;
  }

  // A cached page from before the captcha was switched on has no widget, so
  // the server answers 422 with code "captcha_required". Reload once to get the
  // current page, keeping what the visitor typed in sessionStorage; the guard
  // key keeps it from looping (a second 422 shows the message). Any other 422
  // (a refused message) is shown inline. Without sessionStorage nothing can be
  // kept or guarded, so the message is shown instead.
  var RELOAD_KEY = 'hm-form-reload-';
  var DRAFT_KEY = 'hm-form-draft-';
  function staleCachedPage(form, slug, body) {
    if (body.code !== 'captcha_required' || form.querySelector('[data-hm-captcha]')) return false;
    try {
      if (sessionStorage.getItem(RELOAD_KEY + slug)) return false;
      sessionStorage.setItem(DRAFT_KEY + slug, JSON.stringify(collect(form)));
      sessionStorage.setItem(RELOAD_KEY + slug, '1');
    } catch (e) { return false; }
    location.reload();
    return true;
  }

  // Puts back the values saved before a reload, then forgets them.
  function restoreDraft(form, slug) {
    var draft;
    try {
      draft = JSON.parse(sessionStorage.getItem(DRAFT_KEY + slug) || 'null');
      sessionStorage.removeItem(DRAFT_KEY + slug);
    } catch (e) { return; }
    if (!draft) return;
    // The saved hm_ts is still a valid signed token and keeps the time the visitor
    // spent on the stale page, so a quick resubmit is not taken for a bot.
    if (typeof draft.hm_ts === 'string' && draft.hm_ts) setHidden(form, 'hm_ts', draft.hm_ts);
    Object.keys(draft).forEach(function (name) {
      try {
        var field = form.elements.namedItem(name);
        if (!field || field.type === 'hidden' || typeof draft[name] !== 'string') return;
        if (field.length !== undefined && !field.options) { // radio group
          if (field.value !== undefined && !field.type) field.value = draft[name];
          return; // checkbox groups: left as is
        }
        if (field.type === 'checkbox' || field.type === 'radio') field.checked = field.value === draft[name];
        else if (field.type !== 'file') field.value = draft[name];
      } catch (e) { /* one bad field must not stop the form */ }
    });
  }

  function enhanceForm(form, captcha) {
    restoreDraft(form, form.getAttribute('data-hm-form'));
    form.addEventListener('submit', function (event) {
      if (!window.fetch) return;
      var honeypot = form.elements.hm_website;
      if (honeypot && honeypot.value) return; // let the server swallow bots
      event.preventDefault();
      var slug = form.getAttribute('data-hm-form');
      (captcha ? captcha.token() : Promise.resolve(true)).then(function (got) {
        if (got === false) {
          showError(form, CAPTCHA_RETRY);
          return null;
        }
        return fetch('/api/v1/public/forms/' + encodeURIComponent(slug) + '/submit', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json', 'X-Page-URL': location.href },
          body: JSON.stringify(collect(form)),
        });
      })
        .then(function (res) {
          if (!res) return;
          if (res.ok) {
            try { sessionStorage.removeItem(RELOAD_KEY + slug); } catch (e) { /* no storage */ }
            var ok = document.createElement('p');
            ok.className = 'hm-form__success';
            ok.setAttribute('role', 'status');
            ok.textContent = 'Thank you, your message has been sent.';
            form.replaceWith(ok);
            return;
          }
          if (res.status === 422) {
            return res.json().catch(function () { return {}; }).then(function (body) {
              if (staleCachedPage(form, slug, body)) return;
              showError(form, body.error || 'Your message could not be accepted.');
              if (captcha) captcha.reset();
            });
          }
          throw new Error('submit failed: ' + res.status);
        })
        .catch(function () {
          HTMLFormElement.prototype.submit.call(form);
        });
    });
  }

  // A form that is not enhanced (the form widget) posts natively; reCAPTCHA v3
  // still needs its token first.
  function holdNativeSubmit(form, captcha) {
    var ready = false;
    form.addEventListener('submit', function (event) {
      if (ready) return;
      var honeypot = form.elements.hm_website;
      if (honeypot && honeypot.value) return;
      event.preventDefault();
      captcha.token().then(function (got) {
        if (got === false) {
          showError(form, CAPTCHA_RETRY);
          return;
        }
        ready = true;
        HTMLFormElement.prototype.submit.call(form);
      });
    });
  }

  function initForms() {
    document.querySelectorAll('form.hm-form').forEach(function (form) {
      addTimingToken(form);
      var captcha = setupCaptcha(form);
      if (form.hasAttribute('data-hm-form')) enhanceForm(form, captcha);
      else if (captcha) holdNativeSubmit(form, captcha);
    });
  }

  // Menu: on wide screens the list is always visible (the details element is
  // kept open); below that, Escape or a click outside closes an open navigation.
  function enhanceMenu() {
    var wide = window.matchMedia('(min-width: 48rem)');
    function sync() {
      document.querySelectorAll('details.hm-nav').forEach(function (d) {
        d.open = wide.matches;
      });
    }
    sync();
    wide.addEventListener('change', sync);
    // A click on the scrim (the details element itself, not its summary or list) closes it.
    document.addEventListener('click', function (event) {
      var d = event.target;
      if (!wide.matches && d && d.matches && d.matches('details.hm-nav[open]')) d.removeAttribute('open');
    });
    document.addEventListener('keydown', function (event) {
      if (event.key !== 'Escape' || wide.matches) return;
      document.querySelectorAll('details.hm-nav[open]').forEach(function (d) {
        d.removeAttribute('open');
      });
    });
  }

  // Pageview: one cookie-less beacon per page load; the server only counts it.
  function trackPageView() {
    // Canvas/draft previews are not visits (and would only fire a preflight).
    if (document.documentElement.dataset.hmPreview === 'true') return;
    var body = JSON.stringify({ events: [{ type: 'page_view' }] });
    // sendBeacon always sends credentials, which a sandboxed (opaque origin)
    // page cannot do cross-origin. A string body is a simple text/plain request
    // (no preflight) and credentials stay out, so it works from both.
    if (self.origin === 'null' || !navigator.sendBeacon) {
      if (window.fetch) {
        fetch('/api/v1/track', { method: 'POST', body: body, keepalive: true, credentials: 'omit' }).catch(function () {});
      }
    } else {
      navigator.sendBeacon('/api/v1/track', new Blob([body], { type: 'application/json' }));
    }
  }

  // Light/dark: light is the default; the header toggle opts into dark and the
  // choice is remembered in localStorage. Without JS the toggle stays hidden.
  var THEME_KEY = 'hm-theme';
  var root = document.documentElement;
  try {
    if (localStorage.getItem(THEME_KEY) === 'dark') root.setAttribute('data-theme', 'dark');
  } catch (e) { /* storage blocked: stay light */ }

  function enhanceThemeToggle() {
    var button = document.querySelector('[data-hm-theme-toggle]');
    if (!button) return;
    function sync() {
      var dark = root.getAttribute('data-theme') === 'dark';
      button.setAttribute('aria-pressed', dark ? 'true' : 'false');
      button.setAttribute('aria-label', dark ? 'Switch to light mode' : 'Switch to dark mode');
    }
    sync();
    button.hidden = false;
    button.addEventListener('click', function () {
      var dark = root.getAttribute('data-theme') !== 'dark';
      if (dark) root.setAttribute('data-theme', 'dark');
      else root.removeAttribute('data-theme');
      try { localStorage.setItem(THEME_KEY, dark ? 'dark' : 'light'); } catch (e) { /* not remembered */ }
      sync();
    });
  }

  // Plugin directory: the server renders every card; this reveals the search
  // box and filters the cards by their data-search text.
  function enhancePluginDirectory() {
    var inputs = document.querySelectorAll('[data-hm-plugin-search]');
    Array.prototype.forEach.call(inputs, function (input) {
      var box = input.closest('.hm-plugin-directory');
      var noun = input.getAttribute('data-noun') || 'plugin';
      var count = box && box.querySelector('[data-hm-plugin-count]');
      var cards = box ? box.querySelectorAll('[data-search]') : [];
      function filter() {
        var q = input.value.trim().toLowerCase();
        var shown = 0;
        Array.prototype.forEach.call(cards, function (card) {
          var match = q === '' || (card.getAttribute('data-search') || '').indexOf(q) !== -1;
          card.hidden = !match;
          if (match) shown++;
        });
        if (count) count.textContent = shown + ' ' + noun + (shown === 1 ? '' : 's');
      }
      input.hidden = false;
      if (count) count.hidden = false;
      input.addEventListener('input', filter);
      filter();
    });
  }

  function init() {
    trackPageView();
    enhanceThemeToggle();
    initForms();
    enhanceMenu();
    enhancePluginDirectory();
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
