/*
 * hm-track.js: the core visit and engagement beacon. Served by Hatamoto at
 * /theme/assets/hm-track.js and added to every public page; themes do not ship it.
 *
 * Cookie-less and ID-less. Nothing here is a visitor or session identifier: the
 * only state is a tab-scoped sessionStorage marker that never leaves the browser.
 *
 * A visit is the first pageview of a tab or an arrival from another site.
 *   ext   = document.referrer host (www stripped) is non-empty and differs from
 *           location.hostname.
 *   entry = no marker yet, or ext and the referrer differs from the one stored in
 *           the marker (a fresh arrival from outside starts a new visit; a reload
 *           with the same referrer does not). Without sessionStorage access:
 *           entry = ext || no referrer.
 * Every event carries the visit's attributes (stored ones, or freshly read ones on
 * entry) so pageviews and engagement land on the visit's channel. Only entry:true
 * counts a visit. Only the referrer HOST is sent, never a URL, query or fragment.
 *
 * Engagement (compiled out when the owner turns it off): engage/multi on the tab's
 * second pageview, engage/scroll when 75% of the page is first scrolled, each once
 * per visit; first:true marks the visit's first engagement event.
 */
(function () {
  'use strict';
  var ENGAGE = __HM_ENGAGE__;
  var KEY = 'hm_v';
  var URL_PATH = '/api/v1/track';

  // Canvas/draft previews and automated browsers are not visits.
  if (document.documentElement.dataset.hmPreview === 'true') return;
  if (navigator.webdriver) return;

  function clean(s, max) {
    return String(s || '').toLowerCase().replace(/[^a-z0-9._~:\/-]/g, '').slice(0, max);
  }
  function bareHost(h) {
    return String(h || '').toLowerCase().replace(/\.$/, '').replace(/^www\./, '');
  }
  function hostOf(url) {
    if (!url) return '';
    try { return bareHost(new URL(url).hostname); } catch (e) { return ''; }
  }

  var referrer = document.referrer || '';
  var refHost = hostOf(referrer);
  var ext = refHost !== '' && refHost !== bareHost(location.hostname);

  var marker = null;
  var stored = true;
  try {
    var raw = sessionStorage.getItem(KEY);
    if (raw) marker = JSON.parse(raw);
    if (marker && typeof marker !== 'object') marker = null;
  } catch (e) { stored = false; marker = null; }

  var entry = stored ? (!marker || (ext && referrer !== marker.r)) : (ext || referrer === '');

  if (entry || !marker) {
    var q = new URLSearchParams(location.search);
    marker = {
      r: ext ? referrer : '',
      s: clean(q.get('utm_source'), 64),
      m: clean(q.get('utm_medium'), 64),
      c: clean(q.get('utm_campaign'), 64),
      p: clean(location.pathname, 200),
      g: !!(q.get('gclid') || q.get('msclkid')),
      x: {}
    };
  }
  var flags = marker.x || (marker.x = {});

  function save() {
    if (!stored) return;
    try { sessionStorage.setItem(KEY, JSON.stringify(marker)); } catch (e) {}
  }

  function attrs(type) {
    return {
      type: type,
      ref: hostOf(marker.r).slice(0, 100),
      us: marker.s,
      um: marker.m,
      uc: marker.c,
      paid: marker.g,
      path: marker.p
    };
  }

  function send(ev) {
    var body = JSON.stringify({ v: 2, events: [ev] });
    // A string body is a simple text/plain request (no preflight) and credentials
    // stay out of the fetch, so it works from a sandboxed (opaque origin) page too.
    if (self.origin === 'null' || !navigator.sendBeacon) {
      if (window.fetch) {
        fetch(URL_PATH, { method: 'POST', body: body, keepalive: true, credentials: 'omit' }).catch(function () {});
      }
    } else {
      navigator.sendBeacon(URL_PATH, new Blob([body], { type: 'text/plain' }));
    }
  }

  function engage(kind) {
    var ev = attrs('engage');
    ev.kind = kind;
    if (!flags.f) { ev.first = true; flags.f = 1; }
    flags[kind === 'multi' ? 'm' : 's'] = 1;
    save();
    send(ev);
  }

  var pv = attrs('page_view');
  pv.entry = entry;
  send(pv);

  if (ENGAGE) {
    if (!entry && stored && !flags.m) engage('multi');
    if (!flags.s) {
      var onScroll = function () {
        var el = document.documentElement;
        var seen = (window.pageYOffset || el.scrollTop || 0) + (window.innerHeight || el.clientHeight || 0);
        if (el.scrollHeight > 0 && seen / el.scrollHeight >= 0.75) {
          window.removeEventListener('scroll', onScroll);
          engage('scroll');
        }
      };
      window.addEventListener('scroll', onScroll, { passive: true });
    }
  }
  save();
})();
