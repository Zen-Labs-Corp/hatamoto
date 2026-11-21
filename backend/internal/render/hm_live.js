/* Static mode: loads the comment form (with a fresh per-visitor token) from the live origin into the
   placeholder the static page carries. Without JS or on failure the placeholder's link stays. */
(function () {
  'use strict';
  var meta = document.querySelector('meta[name="hm-live-origin"]');
  if (!meta || !window.fetch || !window.URL) return;
  var live = meta.getAttribute('content');
  var q = new URLSearchParams(location.search);
  Array.prototype.forEach.call(document.querySelectorAll('[data-hm-live-form]'), function (el) {
    var u;
    try { u = new URL(el.getAttribute('data-hm-live-form')); } catch (e) { return; }
    if (u.origin !== live) return; /* only the configured live origin */
    ['creply', 'cnotice', 'cerror'].forEach(function (k) { var v = q.get(k); if (v) u.searchParams.set(k, v); });
    fetch(u.href, { credentials: 'omit', cache: 'no-store' })
      .then(function (r) { return r.ok ? r.text() : null; })
      .then(function (html) {
        if (!html) return;
        var t = document.createElement('template');
        t.innerHTML = html; /* scripts in a template never run */
        /* The theme wraps the form in #hm-comment-form and the fragment brings its own copy, so swap the
           wrapper: replacing only the placeholder would nest a second element with that id. */
        (el.closest('#hm-comment-form') || el).replaceWith(t.content);
      })
      .catch(function () {});
  });
})();
