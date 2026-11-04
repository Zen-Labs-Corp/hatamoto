/*
 * hm-tags.js: the core tag manager. Served by Hatamoto at
 * /theme/assets/hm-tags.js and linked from public pages when the owner turns
 * on at least one tag in Settings > Tracking; themes do not ship it. The
 * enabled vendors are substituted into CFG when the file is served.
 *
 * A vendor loads when hm.consent.allows(category) is true, or when hm.consent
 * is absent and the banner is off (CFG.consent unset). It loads once, and
 * again for newly allowed vendors on every "hm:consent".
 * Consent Mode defaults and updates are sent by hm-consent.js, never here.
 * A tag already loaded cannot be unloaded when consent is later withdrawn.
 *
 * Conversion contract: a theme dispatches, on document,
 *   new CustomEvent('hm:conversion', {detail: {id: submissionId, form: slug, value: null}})
 * once per successful form submit. Each loaded vendor maps it to its own event.
 */
(function () {
  'use strict';
  var CFG = __HM_TAGS__;
  var w = window;
  var d = document;

  function isMember() {
    try {
      return d.cookie.split('; ').some(function (c) { return c.indexOf(CFG.member + '=') === 0; });
    } catch (e) { return false; } // sandboxed page: no cookies, so no marker
  }
  if (CFG.excludeSignedIn && isMember()) return;

  function allowed(cat) {
    var c = w.hm && w.hm.consent;
    if (!c) return !CFG.consent; // banner on but its script missing: fail closed
    return c.allows(cat);
  }

  function gtag() {
    w.dataLayer = w.dataLayer || [];
    if (typeof w.gtag !== 'function') w.gtag = function () { w.dataLayer.push(arguments); };
    w.gtag.apply(w, arguments);
  }
  var gtagStarted = false;
  function gtagStart() {
    if (gtagStarted) return;
    gtagStarted = true;
    gtag('js', new Date());
  }

  function load(src, opts) {
    var s = d.createElement('script');
    s.async = true;
    s.src = src;
    if (opts && opts.crossorigin) s.setAttribute('crossorigin', opts.crossorigin);
    if (opts && opts.onload) s.onload = opts.onload;
    (d.head || d.documentElement).appendChild(s);
  }

  function stubQueue(name, extra) {
    if (w[name]) return w[name];
    var f = w[name] = function () { f.q.push(arguments); };
    f.q = [];
    if (extra) extra(f);
    return f;
  }

  // One bootstrap per vendor, keyed by Vendor.Boot. v is {id, boot, category, src, aux, value}.
  var BOOT = {
    ga4: function (v) { gtagStart(); gtag('config', v.value); load(v.src); },
    gads: function (v) { gtagStart(); gtag('config', v.value); load(v.src); },
    gtm: function (v) {
      w.dataLayer = w.dataLayer || [];
      w.dataLayer.push({ 'gtm.start': Date.now(), event: 'gtm.js' });
      load(v.src);
    },
    meta: function (v) {
      var f = stubQueue('fbq', function (n) {
        n.callMethod = null; n.push = function () { n.q.push(arguments); }; n.loaded = true; n.version = '2.0';
        n.queue = n.q;
        if (!w._fbq) w._fbq = n;
      });
      f('init', v.value);
      f('track', 'PageView');
      load(v.src);
    },
    clarity: function (v) { stubQueue('clarity'); load(v.src); },
    uet: function (v) {
      w.uetq = w.uetq || [];
      load(v.src, {
        onload: function () {
          try {
            var o = { ti: v.value, enableAutoSpaTracking: true, q: w.uetq };
            w.uetq = new w.UET(o);
            w.uetq.push('pageLoad');
          } catch (e) { /* the tag stays queued */ }
        }
      });
    },
    linkedin: function (v) {
      w._linkedin_data_partner_ids = (w._linkedin_data_partner_ids || []).concat([v.value]);
      if (!w.lintrk) {
        w.lintrk = function (a, b) { w.lintrk.q.push([a, b]); };
        w.lintrk.q = [];
      }
      load(v.src);
    },
    tiktok: function (v) {
      var q = w.ttq = w.ttq || [];
      ['page', 'track', 'identify', 'instances', 'debug', 'on', 'off', 'once', 'ready', 'alias', 'group',
        'enableCookie', 'disableCookie'].forEach(function (m) {
        q[m] = function () { q.push([m].concat(Array.prototype.slice.call(arguments, 0))); };
      });
      q._i = q._i || {};
      q._i[v.value] = [];
      q._i[v.value]._u = 'https://analytics.tiktok.com/i18n/pixel/events.js';
      q._t = q._t || {};
      q._t[v.value] = +new Date();
      q._o = q._o || {};
      q._o[v.value] = {};
      load(v.src);
      q.page();
    },
    pinterest: function (v) {
      if (!w.pintrk) {
        w.pintrk = function () { w.pintrk.queue.push(Array.prototype.slice.call(arguments)); };
        w.pintrk.queue = [];
        w.pintrk.version = '3.0';
      }
      load(v.src);
      w.pintrk('load', v.value);
      w.pintrk('page');
    },
    tawk: function (v) {
      w.Tawk_API = w.Tawk_API || {};
      w.Tawk_LoadStart = new Date();
      load(v.src, { crossorigin: '*' });
    },
    crisp: function (v) {
      w.$crisp = w.$crisp || [];
      w.CRISP_WEBSITE_ID = v.value;
      load(v.src);
    },
    hubspot: function (v) { w._hsq = w._hsq || []; load(v.src); },
    adsense: function (v) { load(v.src, { crossorigin: 'anonymous' }); }
  };

  // What a form conversion means to each vendor. Vendors without an entry
  // (HubSpot, Tawk.to, Crisp, AdSense) record nothing.
  var MAP = {
    ga4: function (v, e) { gtag('event', 'generate_lead', { form_id: e.form }); },
    gads: function (v, e) { if (v.aux) gtag('event', 'conversion', { send_to: v.value + '/' + v.aux }); },
    gtm: function (v, e) {
      w.dataLayer = w.dataLayer || [];
      w.dataLayer.push({ event: 'hm_conversion', hm_form: e.form, hm_submission_id: e.id });
    },
    meta: function (v, e) { w.fbq('track', 'Lead', {}, { eventID: e.id }); },
    clarity: function (v, e) { w.clarity('event', 'hm_conversion'); },
    uet: function (v, e) { w.uetq.push('event', 'lead', {}); },
    linkedin: function (v, e) { if (v.aux) w.lintrk('track', { conversion_id: Number(v.aux) }); },
    tiktok: function (v, e) { w.ttq.track('SubmitForm'); },
    pinterest: function (v, e) { w.pintrk('track', 'lead'); }
  };

  var loaded = {};

  function loadAllowed() {
    CFG.vendors.forEach(function (v) {
      if (loaded[v.id] || !allowed(v.category) || !BOOT[v.boot]) return;
      loaded[v.id] = true;
      try { BOOT[v.boot](v); } catch (e) { /* one failing tag must not stop the others */ }
    });
  }

  d.addEventListener('hm:consent', loadAllowed);
  d.addEventListener('hm:conversion', function (ev) {
    var e = (ev && ev.detail) || {};
    CFG.vendors.forEach(function (v) {
      if (!loaded[v.id] || !MAP[v.boot]) return;
      try { MAP[v.boot](v, e); } catch (err) { /* ignore */ }
    });
  });
  loadAllowed();
})();
