/* Canvas bridge (protocol v1, plus v2 once the parent sends hm:2 init; see
 * docs/CANVAS.md). Injected into admin previews only; it turns the
 * rendered page into an in-place editing surface and talks to the admin shell
 * via postMessage. No network access, no dynamic code evaluation. */
(function () {
  'use strict';
  // First script in <head>: keep the native references, ask the host for the one
  // MessagePort, and swallow the handover in the capture phase so no later
  // listener sees it. Everything after travels over that port.
  // hm:port:start
  var parentWin = window.parent;
  var pm = MessagePort.prototype.postMessage;
  var listen = window.addEventListener;
  var port = null;
  var latched = false;
  var queue = [];
  function onPort(e) {
    var d = e.data;
    if (!d || d.hm !== 1 || d.type !== 'hm:port') return;
    e.stopImmediatePropagation();
    if (latched) return;
    if (e.source !== parentWin || !e.ports || e.ports.length !== 1) return;
    latched = true;
    port = e.ports[0];
    port.onmessage = onHostMessage;
    while (queue.length) pm.call(port, queue.shift());
  }
  listen.call(window, 'message', onPort, { capture: true });
  // (an older host reads this as a contract mismatch)
  parentWin.postMessage({ hm: 1, type: 'hm:ready', contract: 'port' }, '*');
  function send(msg) {
    if (port) pm.call(port, msg); else queue.push(msg);
  }
  // hm:port:end
  var doc = document;
  var root = doc.documentElement;
  var editable = false;
  var selectedId = '';
  var original = new WeakMap();
  var lastText = new WeakMap(); // text last posted (or stamped) per [data-hm-text] node
  var BLANK = /^[\t\n\f\r \u00a0]*$/;
  var v2 = false; // set by the parent's {hm:2,type:'init'}; until then v1 only
  var selectedKey = ''; // v2 selection (any node), re-applied after patches
  var editingProp = null; // v2 inline-edited [data-hm-prop] element

  function post(msg) {
    msg.hm = 1;
    send(msg);
  }

  var style = doc.createElement('style');
  style.id = 'hm-canvas-style';
  style.textContent =
    '[data-hm-section]{cursor:default}' +
    // With a v2 parent the parent draws hover and selection in one crisp layer (a frame outline shrinks with the zoom); the frame keeps them for v1.
    'html[data-hm-editing]:not([data-hm-v2]) [data-hm-section]:hover{outline:2px solid rgba(52,80,138,.45);outline-offset:-2px}' +
    'html[data-hm-editing]:not([data-hm-v2]) [data-hm-section].hm-canvas-selected{outline:2px solid #34508A;outline-offset:-2px}' +
    'html[data-hm-editing]:not([data-hm-v2]) [data-hm-node].hm-canvas-hover{outline:1px dashed rgba(52,80,138,.6);outline-offset:-1px}' +
    'html[data-hm-editing]:not([data-hm-v2]) [data-hm-node].hm-canvas-selected{outline:2px solid #34508A;outline-offset:-2px}' +
    'html[data-hm-editing] [data-hm-node][data-hm-holds]:empty{min-height:64px;outline:1px dashed rgba(52,80,138,.45);outline-offset:-1px}' +
    'html[data-hm-editing] [data-hm-prop][contenteditable]{cursor:text;outline:none}' +
    'html[data-hm-editing] [data-hm-field][contenteditable]{text-decoration:none!important;text-transform:none!important}' +
    'html[data-hm-editing] [contenteditable]{cursor:text;outline:none}' +
    'html[data-hm-editing] [contenteditable]:hover{box-shadow:0 0 0 1px rgba(52,80,138,.4)}' +
    'html[data-hm-editing] [contenteditable]:focus{box-shadow:0 0 0 2px #34508A}' +
    'html[data-hm-editing] [data-hm-kind="richtext"],html[data-hm-editing] [data-hm-kind="html"]{cursor:pointer}';

  function sectionOf(el) {
    return el && el.closest ? el.closest('[data-hm-section]') : null;
  }

  // Text inside an html-kind field: stamped by the server in canvas renders as
  // data-hm-text="<field path>#<n>"; the bridge only reads the address.
  function isTextNode(el) {
    return !!(el && el.hasAttribute && el.hasAttribute('data-hm-text'));
  }

  function isTextField(el) {
    var kind = el.getAttribute('data-hm-kind');
    return !kind || kind === 'code';
  }

  function setEditable(on) {
    editable = on;
    if (on) root.setAttribute('data-hm-editing', ''); else root.removeAttribute('data-hm-editing');
    wireEditable(doc);
  }

  // contenteditable on the text fields and text nodes under scope (the document, or a patched node).
  function wireEditable(scope) {
    var els = scope.querySelectorAll('[data-hm-field], [data-hm-text]');
    for (var i = 0; i < els.length; i++) {
      if (els[i].hasAttribute('data-hm-field') && !isTextField(els[i])) continue;
      if (editable) els[i].setAttribute('contenteditable', 'plaintext-only');
      else els[i].removeAttribute('contenteditable');
    }
  }

  // <details> keep the public page's open state. While editing, only the ones
  // holding the field being edited or clicked are opened so it stays reachable.
  // A details is identified by its first field (section id + data-hm-field), so
  // the parent can replay the opened set into the next preview frame.
  function detailsKey(d) {
    var f = d.querySelector('[data-hm-field]');
    var key = f ? f.getAttribute('data-hm-field') : '';
    if (!f) {
      // details inside an html body: the field is an ancestor, so use the
      // first stamped text node as the identity.
      f = d.querySelector('[data-hm-text]');
      key = f ? f.getAttribute('data-hm-text') : '';
    }
    var s = sectionOf(d);
    if (!f || !s) return null;
    return { sectionId: s.getAttribute('data-hm-section'), path: key };
  }

  function openDetailsOf(el) {
    if (!editable || !el || !el.closest) return;
    var d = el.closest('details');
    var changed = false;
    while (d) {
      if (!d.open) changed = true;
      d.open = true;
      d = d.parentElement ? d.parentElement.closest('details') : null;
    }
    if (!changed) return;
    var all = doc.querySelectorAll('details[open]');
    var fields = [];
    for (var i = 0; i < all.length; i++) {
      var k = detailsKey(all[i]);
      if (k) fields.push(k);
    }
    post({ type: 'hm:details-open', fields: fields });
  }

  // Re-open details carried over from the previous frame (no report back).
  function restoreDetails(fields) {
    if (!editable || !Array.isArray(fields)) return;
    var all = doc.getElementsByTagName('details');
    for (var i = 0; i < all.length; i++) {
      var k = detailsKey(all[i]);
      if (!k) continue;
      for (var j = 0; j < fields.length; j++) {
        if (fields[j] && fields[j].sectionId === k.sectionId && fields[j].path === k.path) all[i].open = true;
      }
    }
  }

  function box(el) {
    if (!el) return undefined;
    var r = el.getBoundingClientRect();
    return { top: r.top + window.scrollY, height: r.height };
  }

  var layoutQueued = false;
  function sendLayout() {
    layoutQueued = false;
    if (v2) { sendLayout2(); return; }
    var list = doc.querySelectorAll('[data-hm-section]');
    var sections = [];
    for (var i = 0; i < list.length; i++) {
      var b = box(list[i]);
      sections.push({
        id: list[i].getAttribute('data-hm-section'),
        type: list[i].getAttribute('data-hm-type') || '',
        top: b.top,
        height: b.height
      });
    }
    post({
      type: 'hm:layout',
      docHeight: Math.max(root.scrollHeight, doc.body ? doc.body.scrollHeight : 0),
      sections: sections,
      header: box(doc.querySelector('body > header')),
      footer: box(doc.querySelector('body > footer'))
    });
  }
  function queueLayout() {
    if (layoutQueued) return;
    layoutQueued = true;
    window.requestAnimationFrame(sendLayout);
  }

  function select(id, scroll) {
    selectedId = id || '';
    var stale = doc.querySelectorAll('.hm-canvas-selected');
    for (var s = 0; s < stale.length; s++) stale[s].classList.remove('hm-canvas-selected');
    var list = doc.querySelectorAll('[data-hm-section]');
    for (var i = 0; i < list.length; i++) {
      var on = list[i].getAttribute('data-hm-section') === selectedId;
      list[i].classList.toggle('hm-canvas-selected', on);
      if (on && scroll) list[i].scrollIntoView({ block: 'center' });
    }
  }

  // Never navigate or submit from inside the canvas.
  doc.addEventListener('click', function (e) {
    e.preventDefault();
    var target = e.target;
    openDetailsOf(target.closest && target.closest('summary, [data-hm-field]'));
    var field = target.closest ? target.closest('[data-hm-field]') : null;
    var section = sectionOf(target);
    if (v2) clickNode2(target, e.shiftKey, e.ctrlKey || e.metaKey);
    if (section) {
      var id = section.getAttribute('data-hm-section');
      var selectMsg = { type: 'hm:select', sectionId: id };
      if (field) selectMsg.path = field.getAttribute('data-hm-field');
      // v2 reports the selection itself; a click on a field still sends the
      // v1 select so the parent can focus that field in the settings panel.
      if (!v2) {
        select(id, false);
        post(selectMsg);
      } else if (field && !(e.ctrlKey || e.metaKey || e.shiftKey)) {
        // (with a modifier the v2 select above is the whole story: a plain v1 select would reset the multi-selection)
        post(selectMsg);
      }
      if (editable && field && field.getAttribute('data-hm-kind') === 'richtext') {
        var r = field.getBoundingClientRect();
        post({
          type: 'hm:rich-open',
          sectionId: id,
          path: field.getAttribute('data-hm-field'),
          rect: { top: r.top + window.scrollY, left: r.left + window.scrollX, width: r.width, height: r.height }
        });
      }
      return;
    }
    var chrome = target.closest ? target.closest('header, footer') : null;
    if (chrome && chrome.parentNode === doc.body) {
      post({ type: 'hm:chrome-click', area: chrome.tagName === 'HEADER' ? 'header' : 'footer' });
    }
  }, true);
  doc.addEventListener('auxclick', function (e) { e.preventDefault(); }, true);
  doc.addEventListener('submit', function (e) { e.preventDefault(); }, true);

  doc.addEventListener('focusin', function (e) {
    var el = e.target;
    openDetailsOf(el);
    if (el.hasAttribute && el.hasAttribute('contenteditable') && (el.hasAttribute('data-hm-field') || isTextNode(el))) {
      original.set(el, el.textContent);
      if (isTextNode(el) && !lastText.has(el)) lastText.set(el, el.textContent);
    }
  });

  // A blank text node would vanish on re-parse and shift every later ordinal,
  // so blank input is never posted; leaving the node blank restores the text.
  doc.addEventListener('focusout', function (e) {
    var el = e.target;
    if (!isTextNode(el) || !lastText.has(el) || !BLANK.test(el.textContent)) return;
    el.textContent = lastText.get(el);
  });

  doc.addEventListener('input', function (e) {
    var el = e.target;
    if (v2 && el.hasAttribute && el.hasAttribute('data-hm-prop')) {
      textInput2(el);
      return;
    }
    if (editable && isTextNode(el)) {
      // The HTML parser reads a lone CR or CRLF as LF, so store it that way.
      var value = el.textContent.replace(/\r\n?/g, '\n');
      var addr = el.getAttribute('data-hm-text');
      var hash = addr.lastIndexOf('#');
      var host = sectionOf(el);
      if (BLANK.test(value) || hash < 0 || !host) return;
      if (!lastText.has(el)) lastText.set(el, original.has(el) ? original.get(el) : value);
      if (value === lastText.get(el)) return; // nothing changed (e.g. Esc after a withheld blank)
      post({
        type: 'hm:edit-text',
        sectionId: host.getAttribute('data-hm-section'),
        path: addr.slice(0, hash),
        index: Number(addr.slice(hash + 1)),
        value: value,
        expect: lastText.get(el)
      });
      lastText.set(el, value);
      queueLayout();
      return;
    }
    if (!editable || !el.hasAttribute || !el.hasAttribute('data-hm-field') || !isTextField(el)) return;
    var section = sectionOf(el);
    if (!section) return;
    post({
      type: 'hm:edit',
      sectionId: section.getAttribute('data-hm-section'),
      path: el.getAttribute('data-hm-field'),
      value: el.textContent,
      kind: 'plaintext'
    });
    queueLayout();
  });

  doc.addEventListener('keydown', function (e) {
    var el = e.target;
    if (!el.hasAttribute || !el.hasAttribute('contenteditable')) return;
    // Typing inside <summary> must not toggle the disclosure.
    if (el.closest('summary') && (e.key === ' ' || e.key === 'Enter')) e.stopPropagation();
    var code = el.getAttribute('data-hm-kind') === 'code';
    var rk = el === editingProp ? richKind(el) : '';
    if (rk && (e.ctrlKey || e.metaKey) && !e.altKey) {
      var lk = String(e.key).toLowerCase();
      var fc = e.shiftKey ? '' : lk === 'b' ? 'bold' : lk === 'i' ? 'italic' : lk === 'u' ? 'underline' : '';
      if (fc) { e.preventDefault(); fmt(fc); return; }
      if (lk === 'z' && !e.shiftKey && undoFormat(el, rk)) { e.preventDefault(); return; }
      if (lk === 'k') { e.preventDefault(); post2({ type: 'key', key: 'k', shift: !!e.shiftKey }); return; }
    }
    if (e.key === 'Enter' && !code) {
      e.preventDefault();
      if (rk && e.shiftKey) { if (rk === 'text') insertBreak(el); return; } // a line break in a text; a heading is one line
      el.blur();
    } else if (e.key === 'Escape') {
      e.preventDefault();
      if (rk) {
        if (original.has(el) && ser(clean(el, rk)) !== original.get(el)) {
          fill(el, clean(fromHTML(original.get(el)), rk));
          typed(el);
        }
      } else if (original.has(el) && el.textContent !== original.get(el)) {
        el.textContent = original.get(el);
        typed(el);
      }
      el.blur();
    }
  }, true);

  // Cmd/Ctrl+S while focus is inside the frame never reaches the admin
  // document, so forward it as a save request (once per key press).
  doc.addEventListener('keydown', function (e) {
    if (!editable || !(e.metaKey || e.ctrlKey) || String(e.key).toLowerCase() !== 's') return;
    e.preventDefault();
    if (e.repeat) return; // holding the keys down must not queue a save per repeat
    post({ type: 'hm:save' });
  }, true);

  doc.addEventListener('keyup', function (e) {
    if (e.target.closest && e.target.closest('summary') && e.target.hasAttribute('contenteditable')) e.stopPropagation();
  }, true);

  // Typing or pasting past the 20,000-character bound is refused, and the host says so (the canvas must not show text the doc never receives).
  var LIMIT = 20000;
  function overLimit(el, data) {
    var rk = richKind(el);
    var sel = doc.getSelection && doc.getSelection();
    var have = rk ? ser(clean(el, rk)).length : el.textContent.length;
    if (have + data.length - (sel ? String(sel).length : 0) <= LIMIT) return false;
    var nd = propRef(el);
    if (nd) post2({ type: 'textlimit', id: keyOf(nd), k: kindOf(nd), prop: el.getAttribute('data-hm-prop') });
    return true;
  }
  doc.addEventListener('paste', function (e) {
    // with the caret inside a mark or link the target is that element, not the editing host
    var el = editingProp && editingProp.contains(e.target) ? editingProp : e.target;
    if (!el.hasAttribute || !el.hasAttribute('contenteditable')) return;
    e.preventDefault();
    var rk = el === editingProp ? richKind(el) : '';
    if (rk && e.clipboardData) {
      var pasted = e.clipboardData.getData('text/html');
      var tree = pasted ? clean(fromHTML(pasted), rk) : [];
      if (tree.length && overLimit(el, ser(tree))) return;
      if (tree.length && insertTree(el, tree)) return;
    }
    var text = e.clipboardData ? e.clipboardData.getData('text/plain') : '';
    text = text.replace(/\r\n?/g, '\n');
    if (!(el.getAttribute('data-hm-kind') === 'code')) text = text.replace(/\s*\n\s*/g, ' ');
    if (el === editingProp && overLimit(el, text)) return;
    doc.execCommand('insertText', false, text);
  }, true);

  doc.addEventListener('beforeinput', function (e) {
    if (editingProp && editingProp.contains(e.target) && typeof e.data === 'string' && /^insert(Text|FromPaste|Replacement|FromDrop)/.test(e.inputType) && overLimit(editingProp, e.data)) { e.preventDefault(); return; }
    if (!editingProp || !richKind(editingProp) || !editingProp.contains(e.target)) return;
    var t = e.inputType;
    if (t === 'historyUndo' && undoFormat(editingProp, richKind(editingProp))) { e.preventDefault(); return; }
    if (/^(insertParagraph|insertFromDrop|insertFromYank|insertFromPasteAsQuotation|insertLineBreak|formatBold|formatItalic|formatUnderline|formatStrikeThrough)$/.test(t)) e.preventDefault();
    if (t === 'insertLineBreak' && richKind(editingProp) === 'text') insertBreak(editingProp);
    var f = { formatBold: 'bold', formatItalic: 'italic', formatUnderline: 'underline', formatStrikeThrough: 'strike' }[t];
    if (f) fmt(f);
  }, true);
  function noDrag(e) { if (editingProp && editingProp.contains(e.target)) e.preventDefault(); }
  doc.addEventListener('dragstart', noDrag, true);
  doc.addEventListener('drop', noDrag, true);

  function onHostMessage(e) {
    var m = e.data;
    if (m && m.hm === 2) { handleV2(m); return; }
    if (!m || m.hm !== 1 || typeof m.type !== 'string') return;
    if (m.type === 'hm:init') {
      setEditable(m.editable !== false);
      restoreDetails(m.openDetails);
      select(m.selectedId, false);
      queueLayout();
    } else if (m.type === 'hm:select') {
      select(m.sectionId, !!m.scrollIntoView);
    } else if (m.type === 'hm:scroll-to') {
      window.scrollTo(0, Number(m.y) || 0);
    }
  }

  var scrollQueued = false;
  window.addEventListener('scroll', function () {
    if (scrollQueued) return;
    scrollQueued = true;
    window.requestAnimationFrame(function () {
      scrollQueued = false;
      post({ type: 'hm:scroll', y: window.scrollY });
    });
  }, { passive: true });
  window.addEventListener('resize', queueLayout);
  window.addEventListener('load', queueLayout);

  // ---- protocol v2 (negotiated; docs/CANVAS.md) ----------------------------------
  // Everything below runs only after the parent's {hm:2,type:'init'}. Inbound
  // messages are validated with the same rules as protocol.js validToBridge.

  var ID_RE = /^[A-Za-z0-9_-]{1,64}$/;
  var PROP_RE = /^[a-z-]{1,40}$/;
  var NODE_SEL = '[data-hm-node],[data-hm-section]';

  function post2(msg) {
    msg.hm = 2;
    send(msg);
  }
  function isStr(x) { return typeof x === 'string'; }
  function isId(x) { return isStr(x) && ID_RE.test(x); }
  function isNodeRef(sel, key) { return (sel === 'section' || sel === 'node') && isId(key); }
  function isPlace(p) { return !!p && typeof p === 'object' && (p.sel === 'main' || isNodeRef(p.sel, p.key)); }
  function okPlace(m) {
    var n = 0, which;
    ['before', 'after', 'parent'].forEach(function (k) { if (m[k] !== undefined) { n++; which = m[k]; } });
    return n === 1 && isPlace(which);
  }
  function okHtml(m) { return isStr(m.html) && m.html.length <= 1000000; }
  function okCSS(c) {
    if (!c || typeof c !== 'object' || Array.isArray(c)) return false;
    var keys = Object.keys(c);
    if (keys.length > 200) return false;
    for (var i = 0; i < keys.length; i++) {
      if (!isId(keys[i]) || !isStr(c[keys[i]]) || c[keys[i]].length > 100000) return false;
    }
    return true;
  }
  function okDecls(d) {
    if (!Array.isArray(d) || d.length > 50) return false;
    for (var i = 0; i < d.length; i++) {
      var x = d[i];
      if (!Array.isArray(x) || x.length !== 2 || !isStr(x[0]) || !PROP_RE.test(x[0])) return false;
      if (x[1] !== null && (!isStr(x[1]) || x[1].length > 200)) return false;
    }
    return true;
  }
  function valid2(m) {
    if (!isStr(m.type)) return false;
    if (m.seq !== undefined && !(typeof m.seq === 'number' && m.seq >= 0 && m.seq % 1 === 0)) return false;
    var subject = isNodeRef(m.sel, m.key);
    switch (m.type) {
      case 'init': return typeof m.editable === 'boolean' && (m.selectedKey === null || isId(m.selectedKey));
      case 'patch': return subject && okHtml(m) && (m.css === undefined || okCSS(m.css));
      case 'insert': return okHtml(m) && (m.css === undefined || okCSS(m.css)) && okPlace(m);
      case 'move': return subject && okPlace(m);
      case 'remove': case 'edit': return subject;
      case 'setVar': return subject && okDecls(m.decls);
      case 'css': return subject && okCSS(m.css);
      case 'select': return subject && typeof m.scrollIntoView === 'boolean';
      case 'scrollTo': return typeof m.y === 'number' && isFinite(m.y);
      case 'format': return subject && isStr(m.cmd) && /^(bold|italic|underline|strike|code|link|unlink|clear|done)$/.test(m.cmd) &&
        (m.href === undefined ? m.cmd !== 'link' : (isStr(m.href) && m.href.length >= 1 && m.href.length <= 2000)) &&
        (m.newTab === undefined || typeof m.newTab === 'boolean');
      case 'force': return subject && (m.state === null || (typeof m.state === 'string' && /^(hover|focus|active|parent-hover)$/.test(m.state)));
    }
    return false;
  }

  function keyOf(el) { return el.getAttribute('data-hm-section') || el.getAttribute('data-hm-node'); }
  function kindOf(el) { return el.hasAttribute('data-hm-section') ? 's' : 'n'; }
  function nodeOf(el) { return el && el.closest ? el.closest(NODE_SEL) : null; }
  // A component inside a native tree is a display:contents wrapper carrying both
  // markers: it has no box of its own, so its rect is the union of its children.
  function boxless(el) { return el.hasAttribute('data-hm-node') && el.hasAttribute('data-hm-section') && el.style.display === 'contents'; }
  function rectOf(el) {
    var r = el.getBoundingClientRect();
    if (!boxless(el)) return r;
    var l = Infinity, t = Infinity, rt = -Infinity, b = -Infinity;
    for (var c = el.firstElementChild; c; c = c.nextElementSibling) {
      var cr = c.getBoundingClientRect();
      l = Math.min(l, cr.left); t = Math.min(t, cr.top); rt = Math.max(rt, cr.right); b = Math.max(b, cr.bottom);
    }
    return l === Infinity ? r : { left: l, top: t, width: rt - l, height: b - t };
  }

  function find(sel, key) {
    if (sel === 'main') return doc.querySelector('main') || doc.body;
    var esc = CSS.escape(String(key));
    return doc.querySelector(sel === 'section' ? '[data-hm-section="' + esc + '"]' : '[data-hm-node="' + esc + '"]');
  }

  function pad(v) { var n = Math.round(parseFloat(v)); return n > 0 ? Math.min(n, 10000) : 0; }

  function sendLayout2() {
    var els = doc.querySelectorAll(NODE_SEL);
    var sx = window.scrollX, sy = window.scrollY;
    var nodes = [];
    for (var i = 0; i < els.length && nodes.length < 5000; i++) {
      var el = els[i];
      var key = keyOf(el);
      if (!isId(key)) continue;
      var r = rectOf(el);
      var up = el.parentElement ? nodeOf(el.parentElement) : null;
      var pk = up ? keyOf(up) : '';
      var item = {
        id: key, k: kindOf(el), t: (el.getAttribute('data-hm-t') || el.getAttribute('data-hm-type') || '').slice(0, 100),
        p: isId(pk) ? pk : '', x: r.left + sx, y: r.top + sy, w: r.width, h: r.height
      };
      if (el.classList.contains('hm-c')) {
        var cs = window.getComputedStyle(el);
        item.c = 1;
        item.dir = cs.display === 'grid' ? 'grid' : (cs.flexDirection.indexOf('row') === 0 ? 'row' : 'column');
      }
      if (item.k === 's' && item.p === '') {
        // Rendered padding (top, right, bottom, left): the parent cannot read it across the sandbox.
        var ps = window.getComputedStyle(el);
        item.pd = [pad(ps.paddingTop), pad(ps.paddingRight), pad(ps.paddingBottom), pad(ps.paddingLeft)];
      }
      nodes.push(item);
    }
    var main = doc.querySelector('main') || doc.body;
    var mr = main.getBoundingClientRect();
    post2({
      type: 'layout', nodes: nodes,
      main: { x: mr.left + sx, y: mr.top + sy, w: mr.width, h: mr.height },
      docH: contentBottom()
    });
  }
  // scrollHeight never drops below the frame's own height, so a canvas sized to
  // it could not shrink after a delete: use the bottom of the last content box.
  function contentBottom() {
    var bottom = 0;
    for (var c = doc.body ? doc.body.firstElementChild : null; c; c = c.nextElementSibling) {
      if (/^(SCRIPT|STYLE|TEMPLATE|NOSCRIPT|LINK)$/.test(c.tagName)) continue;
      bottom = Math.max(bottom, c.getBoundingClientRect().bottom + window.scrollY);
    }
    return bottom || Math.max(root.scrollHeight, doc.body ? doc.body.scrollHeight : 0);
  }

  function selectNode(el, scroll) {
    var stale = doc.querySelectorAll('.hm-canvas-selected');
    for (var i = 0; i < stale.length; i++) stale[i].classList.remove('hm-canvas-selected');
    selectedKey = el ? keyOf(el) : '';
    if (!el) return;
    el.classList.add('hm-canvas-selected');
    if (scroll) el.scrollIntoView({ block: 'center' });
  }
  function reselect() {
    applyForce();
    if (!selectedKey) return;
    var el = find('section', selectedKey) || find('node', selectedKey);
    if (el) el.classList.add('hm-canvas-selected');
  }

  // Canvas-only: the compiled CSS carries [data-hm-force] twins of the state rules,
  // so a state can be looked at without a pointer. Replaced nodes lose the attribute,
  // hence reselect() re-applies it. Never serialised: the host reads no DOM back.
  var forced = null;
  function applyForce() {
    var stale = doc.querySelectorAll('[data-hm-force]');
    for (var i = 0; i < stale.length; i++) stale[i].removeAttribute('data-hm-force');
    if (!forced) return;
    var el = find(forced.sel, forced.key);
    if (el && forced.state === 'parent-hover') el = nodeOf(el.parentElement);
    if (el) el.setAttribute('data-hm-force', forced.state);
  }

  function clickNode2(target, shift, ctrl) {
    var nd = nodeOf(target);
    if (!nd) return;
    if (editingProp && nd.contains(editingProp)) return; // clicking into the text being edited
    selectNode(nd, false);
    var key = keyOf(nd);
    if (isId(key)) post2({ type: 'select', id: key, k: kindOf(nd), shift: !!shift, ctrl: !!ctrl });
  }

  // Right-click on a node: the parent shows the element menu at the pointer.
  doc.addEventListener('contextmenu', function (e) {
    if (!v2 || !editable) return;
    if (editingProp || (e.target.closest && e.target.closest('[contenteditable]'))) return; // spelling suggestions and Paste stay native
    var nd = nodeOf(e.target);
    if (!nd || !isId(keyOf(nd))) return;
    e.preventDefault();
    post2({ type: 'contextmenu', id: keyOf(nd), k: kindOf(nd), x: e.clientX, y: e.clientY });
  });

  var hoverKey = null;
  var hoverEl = null;
  function hover2(el) {
    var key = el ? keyOf(el) : null;
    if (key === hoverKey) return;
    if (hoverEl) hoverEl.classList.remove('hm-canvas-hover');
    hoverKey = key;
    hoverEl = key ? el : null;
    if (hoverEl) hoverEl.classList.add('hm-canvas-hover');
    if (key === null) post2({ type: 'hover', id: null });
    else if (isId(key)) post2({ type: 'hover', id: key, k: kindOf(el) });
  }
  doc.addEventListener('mousemove', function (e) { if (v2) hover2(nodeOf(e.target)); }, { passive: true });
  root.addEventListener('mouseleave', function () { if (v2) hover2(null); });

  /* hm:inline:start */
  // Inline rich text of a heading or text. Pure: clean() reads any DOM (nodeType, nodeName,
  var INLINE_MARK = { strong: 'strong', b: 'strong', em: 'em', i: 'em', u: 'u', s: 's', strike: 's', del: 's', code: 'code' };
  var INLINE_DROP = /^(script|style|template|iframe|object|embed|svg|math|img|picture|source|track|video|audio|canvas|noscript|textarea|select|option|input|button|form|head|title|meta|link|base|frame|frameset|applet|dialog)$/;
  var INLINE_BLOCK = /^(div|p|li|ul|ol|h[1-6]|blockquote|pre|table|tr|section|article|header|footer|figure|dl|dt|dd)$/;
  // The render safeURL rule (internal/entity AllowedInlineHref); inline_href_cases.json pins both.
  function inlineHrefOk(raw) {
    var h = String(raw).replace(/^\s+|\s+$/g, '');
    if (!h || h.indexOf('//') === 0 || h.indexOf('/\\') === 0 || /[\u0000-\u001f\u007f]/.test(h) || /%(?![0-9A-Fa-f]{2})/.test(h)) return false;
    var m = /^([A-Za-z][A-Za-z0-9+.\-]*):/.exec(h);
    if (m) {
      if (!/^(https?|mailto|tel|sms)$/i.test(m[1])) return false;
      return !(/^https?:\/\//i.test(h) && /[ \\]/.test(h.slice(h.indexOf('//') + 2).split(/[\/?#]/)[0]));
    }
    return h.split(/[\/?#]/)[0].indexOf(':') < 0;
  }
  function inlineWalk(list, kind, out, st) {
    var i, c, name, item, kids;
    var sep = kind === 'heading' ? ' ' : { t: 'br' };
    var put = function (x) {
      if (st.need && out.length && !(typeof x === 'object' && x.t === 'br')) out.push(sep);
      st.need = false;
      out.push(x);
    };
    for (i = 0; i < list.length; i++) {
      c = list[i];
      if (c.nodeType === 3) {
        item = String(c.nodeValue).replace(/[\r\n]+/g, ' ');
        if (item) put(item);
      } else if (c.nodeType === 1) {
        name = String(c.nodeName).toLowerCase();
        if (INLINE_DROP.test(name)) continue;
        if (name === 'br') { put(sep); continue; }
        if (INLINE_BLOCK.test(name)) {
          if (out.length) st.need = true;
          inlineWalk(c.childNodes, kind, out, st);
          if (out.length) st.need = true;
          continue;
        }
        kids = [];
        inlineWalk(c.childNodes, kind, kids, { need: false });
        if (INLINE_MARK[name]) item = { t: INLINE_MARK[name], c: kids };
        else if (name === 'a' && inlineHrefOk(c.getAttribute('href') || '')) {
          item = { t: 'a', a: { href: String(c.getAttribute('href')).replace(/^\s+|\s+$/g, ''), blank: c.getAttribute('target') === '_blank' }, c: kids };
        } else {
          for (var k = 0; k < kids.length; k++) put(kids[k]); // any other element (or an unsafe link) is unwrapped
          continue;
        }
        if (kids.length) put(item);
      }
    }
  }
  function clean(node, kind) {
    var out = [];
    inlineWalk(node.childNodes, kind, out, { need: false });
    while (out.length && (out[out.length - 1].t === 'br' || out[out.length - 1] === ' ')) out.pop();
    return out;
  }
  function inlineEsc(s, q) {
    s = s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
    return q ? s.replace(/"/g, '&quot;') : s;
  }
  // The href as Go's SanitizeInlineHTML writes it (lower-case scheme; space, non-ASCII and "<>`\\^{|} percent-encoded), so a save returns it unchanged.
  function inlineHref(h) {
    h = h.replace(/^[A-Za-z][A-Za-z0-9+.\-]*:/, function (m) { return m.toLowerCase(); });
    h = h.replace(/^(https?:\/\/)([^\/?#]*)/i, function (m, p, a) { var k = a.lastIndexOf('@'); return k < 0 ? m : p + a.slice(0, k).replace(/@/g, '%40') + a.slice(k); }); // only the last @ ends the userinfo
    var f = h.indexOf('#');
    if (f >= 0) h = h.slice(0, f + 1) + h.slice(f + 1).replace(/#/g, '%23'); // a second # is escaped
    try { return h.replace(/[^\x21-\x7e]+|["<>`\\^{|}]+/g, encodeURIComponent); } catch (e) { return h; }
  }
  function ser(tree) {
    var out = '';
    for (var i = 0; i < tree.length; i++) {
      var x = tree[i];
      if (typeof x === 'string') out += inlineEsc(x, false);
      else if (x.t === 'br') out += '<br>';
      else if (x.t === 'a') out += '<a href="' + inlineEsc(inlineHref(x.a.href), true) + '"' + (x.a.blank ? ' target="_blank" rel="noopener noreferrer"' : '') + '>' + ser(x.c) + '</a>';
      else out += '<' + x.t + '>' + ser(x.c) + '</' + x.t + '>';
    }
    return out;
  }
  function textOfTree(tree) {
    var out = '';
    for (var i = 0; i < tree.length; i++) out += typeof tree[i] === 'string' ? tree[i] : (tree[i].c ? textOfTree(tree[i].c) : '');
    return out;
  }
  function build(tree, d) {
    var frag = d.createDocumentFragment();
    for (var i = 0; i < tree.length; i++) {
      var x = tree[i];
      if (typeof x === 'string') { frag.appendChild(d.createTextNode(x)); continue; }
      var el = d.createElement(x.t);
      if (x.t === 'a') {
        el.setAttribute('href', x.a.href);
        if (x.a.blank) { el.setAttribute('target', '_blank'); el.setAttribute('rel', 'noopener noreferrer'); }
      }
      if (x.c) el.appendChild(build(x.c, d));
      frag.appendChild(el);
    }
    return frag;
  }
  // ---- marks on the tree: flattened to runs, a range of text offsets changes their marks, the nesting is rebuilt
  var INLINE_ORDER = { a: 0, strong: 1, em: 2, u: 3, s: 4, code: 5 };
  var INLINE_TAG = { bold: 'strong', italic: 'em', underline: 'u', strike: 's', code: 'code' };
  var INLINE_NAMES = ['bold', 'italic', 'underline', 'strike', 'code', 'link'];
  function inlineRuns(tree, path, out) {
    for (var i = 0; i < tree.length; i++) {
      var x = tree[i];
      if (typeof x === 'string') out.push({ s: x, m: path });
      else if (x.t === 'br') out.push({ br: 1, m: path });
      else inlineRuns(x.c, path.concat([{ t: x.t, a: x.a }]), out);
    }
    return out;
  }
  function inlineSame(x, y) { return x.t === y.t && (x.t !== 'a' || (x.a.href === y.a.href && x.a.blank === y.a.blank)); }
  function inlineHas(m, t) { for (var i = 0; i < m.length; i++) if (m[i].t === t) return m[i]; return null; }
  function inlineDrop(m, t) { return m.filter(function (x) { return x.t !== t; }); }
  function inlineGroup(runs, depth) {
    var out = [], i = 0, j, r, mk;
    while (i < runs.length) {
      r = runs[i];
      if (r.m.length <= depth) {
        if (r.br) out.push({ t: 'br' });
        else if (typeof out[out.length - 1] === 'string') out[out.length - 1] += r.s;
        else out.push(r.s);
        i++;
        continue;
      }
      mk = r.m[depth];
      for (j = i + 1; j < runs.length && runs[j].m.length > depth && inlineSame(runs[j].m[depth], mk); j++);
      out.push({ t: mk.t, a: mk.a, c: inlineGroup(runs.slice(i, j), depth + 1) });
      i = j;
    }
    return out;
  }
  function inlineSplit(runs, start, end) {
    var out = [], pos = 0, i, r, len, a, b;
    for (i = 0; i < runs.length; i++) {
      r = runs[i];
      if (r.br) { out.push({ br: 1, m: r.m, at: pos, on: false }); continue; }
      len = r.s.length;
      a = Math.min(Math.max(start - pos, 0), len);
      b = Math.min(Math.max(end - pos, 0), len);
      if (a > 0) out.push({ s: r.s.slice(0, a), m: r.m, at: pos, on: false });
      if (b > a) out.push({ s: r.s.slice(a, b), m: r.m, at: pos + a, on: true });
      if (b < len) out.push({ s: r.s.slice(b), m: r.m, at: pos + b, on: false });
      pos += len;
    }
    return out;
  }
  function inlineLinkSpan(runs, at) {
    var lens = runs.map(function (r) { return r.br ? 0 : r.s.length; });
    var pos = 0, hit = -1, i;
    for (i = 0; i < runs.length && hit < 0; i++) {
      if (lens[i] && ((at > pos && at <= pos + lens[i]) || at === pos) && inlineHas(runs[i].m, 'a')) hit = i;
      pos += lens[i];
    }
    if (hit < 0) return null;
    var link = inlineHas(runs[hit].m, 'a'), from = hit, to = hit, first = 0, size = 0;
    var inLink = function (r) { var m = inlineHas(r.m, 'a'); return m && inlineSame(m, link); };
    while (from > 0 && inLink(runs[from - 1])) from--;
    while (to < runs.length - 1 && inLink(runs[to + 1])) to++;
    for (i = 0; i < from; i++) first += lens[i];
    for (i = from; i <= to; i++) size += lens[i];
    return [first, first + size];
  }
  function seenRuns(tree, start, end) {
    var runs = inlineRuns(tree, [], []), pos = 0, seen = [], i, len;
    for (i = 0; i < runs.length; i++) {
      if (runs[i].br) continue;
      len = runs[i].s.length;
      if (start === end ? (!seen.length && len && start >= pos && start <= pos + len) : Math.min(end, pos + len) > Math.max(start, pos)) seen.push(runs[i]);
      pos += len;
    }
    return seen;
  }
  function marksOfTree(tree, start, end) {
    var seen = seenRuns(tree, start, end), tags = ['strong', 'em', 'u', 's', 'code', 'a'];
    return seen.length ? INLINE_NAMES.filter(function (n, k) { return seen.every(function (r) { return inlineHas(r.m, tags[k]); }); }) : [];
  }
  // the link every run of the selection is in (the first run's, when they share one), else null
  function linkOfTree(tree, start, end) {
    var seen = seenRuns(tree, start, end), a = null, i, m;
    for (i = 0; i < seen.length; i++) {
      m = seen[i].m.filter(function (x) { return x.t === 'a'; })[0];
      if (!m || (a && !inlineSame(a, m))) return null;
      a = a || m;
    }
    return a && { href: a.a.href, blank: !!a.a.blank };
  }
  function applyFormat(tree, start, end, cmd, href, blank) {
    var runs = inlineRuns(tree, [], []), tag = INLINE_TAG[cmd], i, p, all = true;
    if (start === end && (cmd === 'link' || cmd === 'unlink')) {
      var span = inlineLinkSpan(runs, start);
      if (!span) return tree;
      start = span[0];
      end = span[1];
    }
    if (start >= end) return tree;
    var parts = inlineSplit(runs, start, end);
    for (i = 0; i < parts.length; i++) if (parts[i].on && tag && !inlineHas(parts[i].m, tag)) all = false;
    for (i = 0; i < parts.length; i++) {
      p = parts[i];
      if (!p.on) continue;
      if (tag) p.m = all ? inlineDrop(p.m, tag) : (inlineHas(p.m, tag) ? p.m : p.m.concat([{ t: tag }]));
      else if (cmd === 'link') p.m = inlineDrop(p.m, 'a').concat([{ t: 'a', a: { href: href, blank: !!blank } }]);
      else if (cmd === 'unlink') p.m = inlineDrop(p.m, 'a');
      else if (cmd === 'clear') p.m = [];
    }
    for (i = 0; i < parts.length; i++) parts[i].m = parts[i].m.slice().sort(function (x, y) { return INLINE_ORDER[x.t] - INLINE_ORDER[y.t]; });
    return inlineGroup(parts, 0);
  }
  /* hm:inline:end */

  doc.addEventListener('dblclick', function (e) {
    if (!v2 || !editable) return;
    var el = e.target.closest && e.target.closest('[data-hm-prop]');
    if (el) startProp(el);
  });
  function richKind(el) {
    var nd = nodeOf(el);
    var t = nd ? nd.getAttribute('data-hm-t') : '';
    var prop = el.getAttribute('data-hm-prop');
    return (t === 'heading' || t === 'text') && (prop === 'text' || prop === 'html') ? t : '';
  }
  function startProp(el) {
    if (editingProp === el) return;
    stopProp();
    editingProp = el;
    var rk = richKind(el);
    original.set(el, rk ? ser(clean(el, rk)) : el.textContent);
    el.setAttribute('contenteditable', rk ? 'true' : 'plaintext-only');
    savedOffs = null;
    fmtStack = [];
    lastSel = '';
    el.focus();
    if (!rangeIn(el)) { // focus alone leaves no caret in a link (a button label): put it at the end so typing goes in
      var r = doc.createRange(), g = doc.getSelection();
      r.selectNodeContents(el);
      r.collapse(false);
      g.removeAllRanges();
      g.addRange(r);
    }
    scheduleSelection();
  }
  function fill(el, tree) {
    while (el.firstChild) el.removeChild(el.firstChild);
    el.appendChild(build(tree, doc));
  }
  function typed(el) { el.dispatchEvent(new Event('input', { bubbles: true })); }
  // The node of a v2 prop element that can be reported to the parent, else null.
  function propRef(el) {
    var nd = nodeOf(el);
    return nd && isId(keyOf(nd)) && /^[A-Za-z0-9_-]{1,40}$/.test(el.getAttribute('data-hm-prop')) ? nd : null;
  }
  function rangeIn(el) {
    var sel = doc.getSelection();
    return sel && sel.rangeCount && el.contains(sel.getRangeAt(0).commonAncestorContainer) ? sel.getRangeAt(0) : null;
  }
  function caretAfter(range, node) {
    var sel = doc.getSelection();
    range.setStartAfter(node);
    range.collapse(true);
    sel.removeAllRanges();
    sel.addRange(range);
  }
  function insertTree(el, tree) {
    var range = rangeIn(el);
    if (!range) return false;
    range.deleteContents();
    var frag = build(tree, doc);
    var last = frag.lastChild;
    range.insertNode(frag);
    if (last) caretAfter(range, last);
    typed(el);
    return true;
  }
  function insertBreak(el) {
    var range = rangeIn(el);
    if (!range) return;
    range.deleteContents();
    var br = doc.createElement('br');
    range.insertNode(br);
    var tail = doc.createRange();
    tail.setStartAfter(br);
    tail.setEnd(el, el.childNodes.length);
    if (!tail.toString() && !tail.cloneContents().querySelector('br')) br.parentNode.insertBefore(doc.createElement('br'), br.nextSibling);
    caretAfter(range, br);
    typed(el);
  }
  // ---- selection and formatting of the rich editor: the selection is kept as plain-text offsets
  var savedOffs = null; // {s, e} of the last selection seen inside the editor
  var selFrame = 0;
  var lastSel = '';
  function offsetIn(el, node, off) {
    var r = doc.createRange();
    r.setStart(el, 0);
    r.setEnd(node, off);
    return r.toString().replace(/[\r\n]+/g, ' ').length;
  }
  function curOffsets(el) {
    var sel = doc.getSelection();
    if (sel && sel.rangeCount) {
      var r = sel.getRangeAt(0);
      if (el.contains(r.startContainer) && el.contains(r.endContainer)) {
        savedOffs = { s: offsetIn(el, r.startContainer, r.startOffset), e: offsetIn(el, r.endContainer, r.endOffset) };
      }
    }
    return savedOffs;
  }
  function pointAt(el, off, atEnd) {
    var w = doc.createTreeWalker(el, 4, null, false), n, cum = 0, last = null, len;
    while ((n = w.nextNode())) {
      len = n.nodeValue.length;
      if (atEnd ? off <= cum + len : off < cum + len) return [n, Math.max(0, off - cum)];
      cum += len;
      last = n;
    }
    return last ? [last, last.nodeValue.length] : [el, 0];
  }
  function selectOffsets(el, o) {
    var a = pointAt(el, o.s, o.s === o.e), b = pointAt(el, o.e, true);
    var r = doc.createRange();
    r.setStart(a[0], a[1]);
    r.setEnd(b[0], b[1]);
    var sel = doc.getSelection();
    sel.removeAllRanges();
    sel.addRange(r);
  }
  function fmt(cmd, href, newTab) {
    var el = editingProp, rk = el && richKind(el);
    if (!rk) return;
    if (rk === 'heading' && /^(underline|strike|code)$/.test(cmd)) return; // the heading bar has no U, S or code
    if (cmd === 'done') { stopProp(); return; }
    if (cmd === 'link' && !inlineHrefOk(href)) return;
    var o = curOffsets(el);
    if (!o) return;
    var before = clean(el, rk);
    var tree = applyFormat(before, o.s, o.e, cmd, href, newTab === true);
    el.focus();
    if (ser(tree) !== ser(before)) {
      if (fmtStack.length < 50) fmtStack.push({ html: ser(before), after: ser(tree), offs: o });
      fill(el, tree);
    }
    selectOffsets(el, o);
    typed(el);
    lastSel = '';
    scheduleSelection();
  }
  // Undo of a format: fill() rewrites the DOM, which breaks the browser's own undo, so each format keeps a snapshot
  // (the markup before it and the selection). Ctrl/Cmd+Z, or the Edit menu's undo, restores the last one while the text still is
  // what that format produced; after typing it is the browser's undo (it takes the typing back, then the format is next).
  // There is no redo for a format. The stack is per edit session.
  var fmtStack = [];
  function undoFormat(el, rk) {
    var top = fmtStack[fmtStack.length - 1];
    if (!top || !rk || el !== editingProp || ser(clean(el, rk)) !== top.after) return false;
    fmtStack.pop();
    fill(el, clean(fromHTML(top.html), rk));
    selectOffsets(el, top.offs);
    typed(el);
    lastSel = '';
    scheduleSelection();
    return true;
  }
  function scheduleSelection() {
    if (selFrame || !editingProp || !richKind(editingProp)) return;
    selFrame = window.requestAnimationFrame(function () { selFrame = 0; postSelection(); });
  }
  function postSelection() {
    var el = editingProp, rk = el && richKind(el);
    var nd = rk && propRef(el);
    if (!nd) return;
    var o = curOffsets(el);
    if (!o) return;
    var rect = null, r = rangeIn(el);
    if (r) {
      rect = r.getBoundingClientRect();
      if (!rect.width && !rect.height) rect = r.getClientRects()[0] || null;
    }
    rect = rect && (rect.width || rect.height) ? rect : el.getBoundingClientRect();
    var box = { x: rect.left + window.pageXOffset, y: rect.top + window.pageYOffset, w: rect.width, h: rect.height };
    if (!isFinite(box.x + box.y + box.w + box.h)) return;
    var er = el.getBoundingClientRect(), eb = { x: er.left + window.pageXOffset, y: er.top + window.pageYOffset, w: er.width, h: er.height };
    var tree = clean(el, rk), marks = marksOfTree(tree, o.s, o.e), link = linkOfTree(tree, o.s, o.e);
    var sig = JSON.stringify([keyOf(nd), box, eb, marks, link]);
    if (sig === lastSel) return;
    lastSel = sig;
    var msg = { type: 'selection', id: keyOf(nd), k: kindOf(nd), prop: el.getAttribute('data-hm-prop'), rect: box, marks: marks };
    msg.kind = rk === 'heading' ? 'heading' : 'text';
    if (isFinite(eb.x + eb.y + eb.w + eb.h)) msg.el = eb;
    if (link && link.href.length <= 2000) msg.link = link;
    post2(msg);
  }
  doc.addEventListener('selectionchange', scheduleSelection);
  window.addEventListener('focus', function () {
    if (editingProp && richKind(editingProp) && savedOffs) { editingProp.focus(); selectOffsets(editingProp, savedOffs); }
  });
  function stopProp() {
    if (!editingProp) return;
    var el = editingProp;
    el.removeAttribute('contenteditable');
    editingProp = null;
    savedOffs = null;
    fmtStack = [];
    textEnd(el);
  }
  // The edit is over: the host re-renders a loop whose item text was typed in.
  function textEnd(el) {
    var nd = propRef(el);
    if (nd) post2({ type: 'textend', id: keyOf(nd), k: kindOf(nd), prop: el.getAttribute('data-hm-prop') });
  }
  // Firefox keeps the editor focused when the host takes focus (no focusout, only a window blur); a rich edit survives that
  // and ends on Enter, Esc, `done` or a click elsewhere in the frame.
  window.addEventListener('blur', function () {
    if (!editingProp) return;
    if (richKind(editingProp)) curOffsets(editingProp);
    else textEnd(editingProp);
  });
  doc.addEventListener('focusout', function (e) {
    var el = editingProp;
    if (e.target !== el) return;
    if (richKind(el) && !e.relatedTarget) {
      // Chromium blurs the editor while the frame still has focus when the host takes it (the link field): decide a tick later
      curOffsets(el);
      setTimeout(function () { if (editingProp === el && doc.hasFocus()) stopProp(); }, 0);
      return;
    }
    stopProp();
  });
  function textInput2(el) {
    var nd = propRef(el);
    if (!nd) return;
    var prop = el.getAttribute('data-hm-prop');
    var rk = richKind(el);
    if (rk) {
      var tree = clean(el, rk);
      var html = ser(tree);
      if (html.length > 20000) return;
      var marked = false;
      for (var i = 0; i < tree.length; i++) marked = marked || typeof tree[i] !== 'string';
      if (marked) post2({ type: 'text', id: keyOf(nd), k: kindOf(nd), prop: 'html', html: html });
      else post2({ type: 'text', id: keyOf(nd), k: kindOf(nd), prop: 'text', value: textOfTree(tree) });
      queueLayout();
      return;
    }
    var value = el.textContent;
    if (value.length > 20000) return;
    post2({ type: 'text', id: keyOf(nd), k: kindOf(nd), prop: prop, value: value });
    queueLayout();
  }

  // Starts editing the text prop of el: its own element, or inside it (own: only a prop that belongs to el itself, not to a descendant node).
  function editIn(el, own) {
    var t = el && (el.matches('[data-hm-prop]') ? el : el.querySelector('[data-hm-prop]'));
    if (!t || !editable || (own && nodeOf(t) !== el)) return false;
    startProp(t);
    return true;
  }

  doc.addEventListener('keydown', function (e) {
    if (!v2 || !editable || editingProp || e.defaultPrevented || (e.target.hasAttribute && e.target.hasAttribute('contenteditable'))) return;
    var k = e.key;
    var mod = e.ctrlKey || e.metaKey;
    // Enter on the selected element starts editing its own text (the keyboard path in); Ctrl/Cmd+K goes to the host, which opens the command palette.
    if (k === 'Enter' && !mod && !e.shiftKey && !e.altKey && selectedKey && !(e.target.closest && e.target.closest('input,textarea,select,button'))) {
      var own = editIn(find('section', selectedKey) || find('node', selectedKey), true);
      if (own) { e.preventDefault(); return; }
    }
    if ((k === 'k' || k === 'K') && mod && !e.altKey && !e.shiftKey) {
      e.preventDefault();
      post2({ type: 'key', key: 'k', shift: false });
      return;
    }
    var ctrl = mod && (k === 'z' || k === 'Z' || k === 'y' || k === 'd');
    // Node clipboard and wrap: never with Alt (Ctrl+Alt+C/V is the style clipboard)
    // and never while the page text is selected (a plain copy then).
    if (mod && !e.altKey && /^[cCxXvVgGhH]$/.test(k)) {
      var sel = doc.getSelection && doc.getSelection();
      ctrl = !((k === 'c' || k === 'x') && sel && !sel.isCollapsed);
    }
    if (ctrl && (k === 'h' || k === 'H')) e.preventDefault(); // the browser's history must not open over the builder's
    if (ctrl || k === 'Delete') post2({ type: 'key', key: k, shift: !!e.shiftKey });
  });

  // ---- patching ----
  function setCSS(map) {
    Object.keys(map).forEach(function (id) {
      var tag = doc.querySelector('style[data-hm-css="' + CSS.escape(id) + '"]');
      if (map[id] === '') { // no rules left: a fresh render has no element either
        if (tag) tag.remove();
        return;
      }
      if (!tag) {
        tag = doc.createElement('style');
        tag.setAttribute('data-hm-css', id);
        doc.head.appendChild(tag);
      }
      tag.textContent = map[id];
    });
  }
  function dropCSSUnder(el) {
    var ids = [];
    if (el.hasAttribute('data-hm-node')) ids.push(el.getAttribute('data-hm-node'));
    var inner = el.querySelectorAll('[data-hm-node]');
    for (var i = 0; i < inner.length; i++) ids.push(inner[i].getAttribute('data-hm-node'));
    // The nodes of a synced block carry no marker (the ref is one unit) but their rules are keyed by their own ids.
    var all = [el].concat([].slice.call(el.querySelectorAll('[class*="hm-n-"]')));
    all.forEach(function (n) {
      var cl = n.classList;
      for (var j = 0; cl && j < cl.length; j++) if (cl[j].indexOf('hm-n-') === 0) ids.push(cl[j].slice(5));
    });
    ids.forEach(function (id) {
      var t = doc.querySelector('style[data-hm-css="' + CSS.escape(id) + '"]');
      if (t) t.remove();
    });
  }
  // Markup is parsed inert in a <template>; the server sanitised it.
  function fromHTML(html) {
    var t = doc.createElement('template');
    t.innerHTML = html;
    return t.content;
  }
  function place(frag, m) {
    var ref;
    if (m.before && (ref = find(m.before.sel, m.before.key)) && ref.parentNode) ref.parentNode.insertBefore(frag, ref);
    else if (m.after && (ref = find(m.after.sel, m.after.key)) && ref.parentNode) ref.parentNode.insertBefore(frag, ref.nextSibling);
    else if (m.parent && (ref = find(m.parent.sel, m.parent.key))) ref.appendChild(frag);
    else return false;
    return true;
  }
  // A fresh layout always precedes `painted`, so the parent can compare the
  // frame against what it expects at the moment the patch is on screen.
  function painted(seq, ok) {
    window.requestAnimationFrame(function () {
      sendLayout2();
      window.requestAnimationFrame(function () { post2({ type: 'painted', seq: seq, ok: ok }); });
    });
  }

  function handleV2(m) {
    if (!valid2(m)) return;
    if (m.type === 'init') {
      v2 = true;
      doc.documentElement.setAttribute('data-hm-v2', '');
      setEditable(m.editable);
      if (m.selectedKey) selectNode(find('section', m.selectedKey) || find('node', m.selectedKey), false);
      post2({ type: 'ready', contract: '2' });
      queueLayout();
      return;
    }
    if (!v2) return;
    if (m.type === 'format') { // a bar click is not a layout change: no reselect, no layout, no painted
      var edited = editingProp && nodeOf(editingProp);
      if (edited && keyOf(edited) === m.key) fmt(m.cmd, m.href, m.newTab);
      return;
    }
    var el = null, ok = true;
    // Every target is looked up before anything is detached or changed, so a failure leaves the frame as it was.
    if (m.type === 'insert') ok = canPlace(m);
    else if (m.type === 'move') ok = !!(el = find(m.sel, m.key)) && canPlace(m);
    else if (m.type === 'patch' || m.type === 'remove') ok = !!(el = find(m.sel, m.key));
    if (ok) ok = applyV2(m, el);
    if (ok) {
      reselect();
      queueLayout();
    }
    if (typeof m.seq === 'number') painted(m.seq, ok);
  }

  function canPlace(m) {
    var p = m.before || m.after || m.parent;
    return !!(p && find(p.sel, p.key));
  }

  // Applies one validated message; false when it is unknown or had nothing to act on.
  function applyV2(m, el) {
    switch (m.type) {
      case 'patch':
        dropCSSUnder(el);
        if (m.css) setCSS(m.css);
        el.replaceWith(fromHTML(m.html));
        wireEditable(doc);
        return true;
      case 'insert':
        if (m.css) setCSS(m.css);
        place(fromHTML(m.html), m);
        wireEditable(doc);
        return true;
      case 'remove':
        dropCSSUnder(el);
        el.remove();
        return true;
      case 'move':
        var f = doc.createDocumentFragment();
        f.appendChild(el);
        place(f, m);
        return true;
      case 'setVar': // live style values until the compiled CSS arrives
        el = find(m.sel, m.key);
        if (!el) return false;
        m.decls.forEach(function (d) {
          if (d[1] === null) el.style.removeProperty(d[0]); else el.style.setProperty(d[0], d[1]);
          (el.__hmVars = el.__hmVars || {})[d[0]] = true;
        });
        return true;
      case 'css': // compiled CSS confirmed: drop only the preview values setVar set
        setCSS(m.css);
        el = find(m.sel, m.key);
        if (el && el.__hmVars) {
          Object.keys(el.__hmVars).forEach(function (prop) { el.style.removeProperty(prop); });
          el.__hmVars = null;
          if (!el.getAttribute('style')) el.removeAttribute('style');
        }
        return true;
      case 'edit':
        editIn(find(m.sel, m.key), false);
        return true;
      case 'force':
        forced = m.state === null ? null : { sel: m.sel, key: m.key, state: m.state };
        return true;
      case 'select':
        if (editingProp && nodeOf(editingProp) !== find(m.sel, m.key)) stopProp(); // the host moved on to another node
        if (forced && forced.key !== m.key) forced = null;
        selectNode(find(m.sel, m.key), m.scrollIntoView);
        return true;
      case 'scrollTo':
        window.scrollTo(0, m.y);
        return true;
    }
    return false;
  }

  // The script runs in <head>: anything that needs <body> waits for the parser.
  function init() {
    doc.head.appendChild(style); // last in <head>, after the theme's stylesheets
    if (window.ResizeObserver) new ResizeObserver(queueLayout).observe(doc.body);
    post({ type: 'hm:ready', contract: root.getAttribute('data-hm-contract') || '' });
    queueLayout();
  }
  if (doc.readyState === 'loading') doc.addEventListener('DOMContentLoaded', init); else init();
})();
