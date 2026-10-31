// The canvas host: a double-buffered sandboxed preview iframe speaking
// postMessage protocol v1 (and v2, when asked for) to the embedded canvas_bridge.js, with the selection
// marks (outline, section toolbar, add bars, header/footer hint) on top. It
// knows nothing about the editor: builder/screen.js turns its callbacks into edits.
// The iframe never gets allow-same-origin. The bridge asks for a port with one
// data-free window message; the host answers with one end of a private
// MessageChannel, once per load. Only that port is read (no other window message
// is), and every payload is validated by parseFrameMessage. A frame that
// navigates away from the document we loaded into it is stopped and distrusted.
import { adminURL } from "@hm/adminpath";
import { CANVAS_CONTRACT, ID_RE, parseFrameMessage, toBridge, validToBridge } from "@hm/canvas/protocol";
import { contentHeight, crumbs, zoomScale } from "@hm/canvas/layout";

function h(tag, props = {}, ...kids) {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(props)) {
    if (v === false || v === null || v === undefined) continue;
    if (k === "text") e.textContent = v;
    else e.setAttribute(k, v === true ? "" : v);
  }
  e.append(...kids);
  return e;
}

const stylespecURL = () => adminURL("/builder/stylespec");

/** The CSS value of a style-table entry for value, or null when the table does not allow it (mirrors builder.styleDecl). */
export function styleCSS(entry, value) {
  switch (entry.kind) {
    case "enum":
      return entry.enum && Object.hasOwn(entry.enum, value) ? entry.enum[value] : null;
    case "count": {
      const n = Number(value);
      return Number.isInteger(n) && String(n) === value && n >= (entry.min ?? 1) && n <= (entry.max ?? 12) ? entry.tpl.replace("%d", value) : null;
    }
    default:
      if (entry.tokens && Object.hasOwn(entry.tokens, value) && entry.tokens[value]) return entry.tokens[value];
      return entry.pattern && new RegExp(entry.pattern).test(value) ? value : null;
  }
}

const hints = () => ({
  header: { text: "The site header is part of the theme.", href: adminURL("/theme/menus"), label: "Edit menus" },
  footer: { text: "The site footer is part of the theme.", href: adminURL("/settings/general#identity"), label: "Site settings" },
});

/**
 * root: the empty container. handlers: {onSelect(id, path?), onEdit, onEditText, onRichOpen, onSave, onContract(ok),
 * onLayout, sectionLabel(id)}. Protocol v2 (opts.v2) adds optional handlers:
 * onHover, onSelectNode, onContextMenu, onHandleDown, getSelection, onNodeText, onNodeTextEnd, onNodeTextLimit,
 * onSelection, onKey, onDrift (the frame disagrees with expectOrder), nodeLabel; onLayout then receives the
 * node rects ({id,k,t,p,x,y,w,h}, document space). Returns the canvas controller.
 */
/** The inline preview declarations [[prop, css]] for entry=value: null when the value is refused, [] for a settle key (only the server's css shows it). */
export function styleDecls(entry, value) {
  const css = value === null ? null : styleCSS(entry, value);
  if (value !== null && css === null) return null;
  return entry.settle ? [] : entry.props.map((p) => [p, css]);
}

/** The page point of a frame-viewport point (x, y): the frame's box on screen plus the point scaled. */
export const framePoint = (rect, scale, x, y) => ({ x: rect.left + x * scale, y: rect.top + y * scale });

export function createCanvas(root, handlers, opts = {}) {
  const v2 = opts.v2 === true;
  const stage = h("div", { class: "hm-pe__stage" });
  const marks = h("div", { class: "hm-pe__marks", "data-hm-pe-marks": "" });
  const frames = [0, 1].map((i) => h("iframe", { class: "hm-pe__frame hm-pe__frame--idle", sandbox: "allow-scripts", title: i === 0 ? "Page preview" : "Page preview (loading)", "aria-hidden": "true", tabindex: "-1" }));
  stage.append(...frames, marks);
  root.append(stage);

  let active = 0; // index of the visible frame
  let loading = null; // buffer that holds a document not yet swapped in
  const revs = [undefined, undefined];
  const expecting = [false, false]; // a load we asked for is pending
  const trusted = [false, false]; // the frame holds a document we loaded (until it navigates away)
  let latestRev = 0;
  let viewport = 1280;
  let scale = 1;
  let zoom = "fit"; // "fit" follows the pane; 100, 75 and 50 are explicit and survive fit()
  let pane = 0; // the last pane width the caller gave
  let device = "base";
  let inset = 0; // height of the sticky strip over the pane: the visible part of the stage starts below it
  let chrome = null; // the lazy strip (canvas/chrome.js), mounted after the first layout
  let selectedId = null;
  let layout = { sections: [] };
  let docHeight = 0;
  let hint = null;
  let scrollY = 0;
  let editable = true;
  let hiddenIds = new Set();
  let openDetails = [];
  // v2 state: node rects, hovered/selected node, per-frame handshake, expected section order.
  let nodes = [];
  let hoverId = null;
  let selectedNode = null;
  let editingId = null; // node edited inline (from `selection`)
  const v2Ready = [false, false];
  let topSections = []; // top-level section ids of the latest v2 layout
  let expected = null; // {ids, seq}: compared when the frame paints that seq
  let seqCounter = 0;
  let forced = null; // {sel, key, state}: the state the frame shows, re-posted to each fresh document
  let styleSpec = null; // promise of the style table, fetched on first setStyle
  const sectionsOrder = () => layout.sections;
  // The strip (breadcrumbs, zoom, device band) is a lazy chunk: built once, then told to repaint.
  let chromeLoad = null;
  const ensureChrome = () => {
    chromeLoad ??= import("@hm/canvas/chrome")
      .then((m) => {
        const parts = [m.mountStrip(api, handlers), ...(v2 ? [m.mountToolbar(api, handlers), m.mountHandles(api, handlers), m.mountDirectLayer(api, handlers)] : [])];
        chrome = { update: () => parts.forEach((x) => x.update()), destroy: () => parts.forEach((x) => x.destroy()) };
      })
      .catch((err) => console.error("hatamoto canvas: strip", err));
  };

  const ports = [null, null]; // one MessageChannel per document; the port is the only trusted channel
  const ready = [false, false]; // the bridge's hm:ready arrived over the port
  function closePort(idx) {
    ports[idx]?.close();
    ports[idx] = null;
  }
  function openPort(idx) {
    closePort(idx);
    const ch = new MessageChannel();
    ports[idx] = ch.port1;
    ch.port1.onmessage = (e) => onFrame(idx, e.data);
    frames[idx].contentWindow?.postMessage({ hm: 1, type: "hm:port" }, "*", [ch.port2]);
  }
  // The only window message read: the bridge's port request, from the frame being loaded.
  const onRequest = (e) => {
    const i = e.source ? frames.findIndex((f) => f.contentWindow === e.source) : -1;
    const d = e.data;
    if (e.origin === "null" && i >= 0 && expecting[i] && !ready[i] && d?.hm === 1 && d.type === "hm:ready" && d.contract === "port") openPort(i);
  };
  window.addEventListener("message", onRequest);
  const post = (idx, msg) => ports[idx]?.postMessage(msg);
  const postV2 = (idx, msg) => validToBridge(msg) && post(idx, msg);

  function applySize() {
    const height = docHeight || 800;
    stage.style.width = `${viewport * scale}px`;
    stage.style.height = `${height * scale}px`;
    frames.forEach((f) => {
      f.style.width = `${viewport}px`;
      f.style.height = `${height}px`;
      f.style.transform = `scale(${scale})`;
    });
    drawMarks();
  }

  function paintFrames() {
    frames.forEach((f, i) => {
      const on = i === active;
      f.classList.toggle("hm-pe__frame--idle", !on);
      f.setAttribute("aria-hidden", String(!on));
      f.title = on ? "Page preview" : "Page preview (loading)";
    });
  }

  /** The frame left the document we gave it: stop it, ignore it, ask for a fresh render. */
  function reset(idx) {
    closePort(idx);
    trusted[idx] = false;
    expecting[idx] = false;
    v2Ready[idx] = false;
    frames[idx].setAttribute("src", "about:blank");
    if (idx === loading) loading = null;
    handlers.onFrameLost?.(idx === active);
  }

  function onLoad(idx) {
    const src = frames[idx].getAttribute("src");
    if (!src || src === "about:blank") return;
    if (!expecting[idx]) return reset(idx);
    expecting[idx] = false;
    if (idx !== active && revs[idx] !== undefined && revs[idx] < latestRev) return; // a newer render is on its way; the active frame keeps its edit
    if (editingId !== null) {
      editingId = null; // the edit lived in the frame just replaced; its textend is dropped from now on
      drawMarks();
    }
    if (!ports[idx]) openPort(idx); // the bridge normally asked before load
    const port = ports[idx];
    setTimeout(() => {
      if (ports[idx] === port && !ready[idx] && idx === active) {
        editable = false; // no hm:ready: an older bridge
        handlers.onContract?.(false);
      }
    }, 3000);
    if (idx !== active) {
      active = idx;
      loading = null;
      paintFrames();
      post(idx, { hm: 1, type: "hm:scroll-to", y: scrollY });
    }
    post(idx, { hm: 1, type: "hm:init", selectedId, editable, openDetails });
    if (v2) postV2(idx, toBridge.init({ editable, selectedKey: ID_RE.test(selectedId ?? "") ? selectedId : null }));
    if (v2 && forced) postV2(idx, toBridge.force(forced));
    handlers.onSwap?.(revs[idx]);
  }
  frames.forEach((f, i) => f.addEventListener("load", () => onLoad(i)));

  function onFrame(idx, data) {
    if (!trusted[idx]) return;
    const m = parseFrameMessage(data);
    if (!m) return;
    const isActive = idx === active;
    if (m.hm === 2) return onMessageV2(m, idx, isActive);
    if (m.type === "hm:ready") {
      ready[idx] = true;
      const ok = m.contract === CANVAS_CONTRACT;
      editable = ok;
      handlers.onContract?.(ok);
      if (!ok) post(idx, { hm: 1, type: "hm:init", selectedId: null, editable: false });
      return;
    }
    if (m.type === "hm:layout") {
      if (!isActive && idx !== loading) return; // not from the outgoing document
      layout = { sections: m.sections, header: m.header, footer: m.footer };
      docHeight = contentHeight(m);
      applySize();
      ensureChrome();
      handlers.onLayout?.();
      return;
    }
    if (!isActive) return;
    switch (m.type) {
      case "hm:select":
        // With v2 the select message already named the deepest element; a later v1 select only carries a field click
        // (focus that field), and never replaces a deeper selection with its section.
        if (v2 && (!m.path || (selectedNode && selectedNode.id !== m.sectionId))) break;
        hint = null;
        handlers.onSelect(m.sectionId, m.path);
        drawMarks();
        break;
      case "hm:edit":
        handlers.onEdit?.(m.sectionId, m.path, m.value);
        break;
      case "hm:edit-text":
        handlers.onEditText?.(m.sectionId, m.path, m.index, m.value, m.expect);
        break;
      case "hm:rich-open":
        handlers.onRichOpen?.(m.sectionId, m.path, { top: m.rect.top * scale, left: m.rect.left * scale, width: m.rect.width * scale, height: m.rect.height * scale });
        break;
      case "hm:chrome-click":
        hint = m.area;
        drawMarks();
        break;
      case "hm:save":
        handlers.onSave?.();
        break;
      case "hm:details-open":
        openDetails = m.fields;
        break;
      case "hm:scroll":
        scrollY = m.y;
        break;
    }
  }

  function onMessageV2(m, idx, isActive) {
    if (!v2) return;
    if (m.type === "ready") {
      v2Ready[idx] = m.contract === "2";
      return;
    }
    if (!v2Ready[idx]) return;
    if (m.type === "layout") {
      if (!isActive && idx !== loading) return; // not from the outgoing document
      nodes = m.nodes;
      topSections = nodes.filter((n) => n.k === "s" && n.p === "").map((n) => n.id);
      const bottom = m.main.y + m.main.h;
      layout = {
        sections: nodes.filter((n) => n.k === "s").map((n) => ({ id: n.id, type: n.t, top: n.y, height: n.h })),
        header: { top: 0, height: m.main.y },
        footer: { top: bottom, height: Math.max(0, m.docH - bottom) },
      };
      docHeight = m.docH;
      applySize();
      ensureChrome();
      handlers.onLayout?.(nodes);
      return;
    }
    if (!isActive) return;
    switch (m.type) {
      case "hover":
        hoverId = m.id;
        handlers.onHover?.(m.id, m.k);
        drawMarks();
        break;
      case "select":
        hint = null;
        selectedNode = { id: m.id, k: m.k };
        editingId = null;
        if (m.k === "s") handlers.onSelect(m.id, undefined, { shift: m.shift, ctrl: m.ctrl });
        else handlers.onSelectNode?.(m.id, { shift: m.shift, ctrl: m.ctrl });
        drawMarks();
        break;
      case "contextmenu": {
        const at = framePoint(frames[active].getBoundingClientRect(), scale, m.x, m.y);
        handlers.onContextMenu?.(m.id, m.k, at.x, at.y);
        break;
      }
      case "text":
        handlers.onNodeText?.(m.id, m.prop, m.prop === "html" ? m.html : m.value);
        break;
      case "textend":
        editingId = null;
        drawMarks();
        handlers.onNodeTextEnd?.(m.id, m.prop);
        break;
      case "textlimit":
        handlers.onNodeTextLimit?.(m.id, m.prop);
        break;
      case "selection":
        if (editingId !== m.id) {
          editingId = m.id;
          drawMarks();
        }
        handlers.onSelection?.(m);
        break;
      case "key":
        if (/^s$/i.test(m.key)) break; // a frame never saves
        handlers.onKey?.(m.key, m.shift, { editing: editingId !== null });
        break;
      case "painted":
        if (!m.ok) {
          expected = null;
          handlers.onDrift?.();
        } else if (expected && m.seq >= expected.seq) {
          const want = expected.ids;
          expected = null;
          if (want.length !== topSections.length || want.some((id, i) => id !== topSections[i])) handlers.onDrift?.();
        }
        break;
    }
  }

  // The move handle lives in the toolbar (chrome.js), which is built once: the press is delegated on the stage.
  stage.addEventListener("pointerdown", (e) => {
    const grip = e.target.closest?.(".hm-pe__handle");
    if (grip) handlers.onHandleDown?.(e, grip.dataset.id);
  });

  function drawMarks() {
    marks.replaceChildren();
    const secs = sectionsOrder();
    const i = secs.findIndex((s) => s.id === selectedId);
    const sel = i >= 0 ? secs[i] : null;
    // A selected non-section node (v2) is outlined by its own rect, sections as before.
    const node = v2 && selectedNode?.k === "n" ? nodes.find((n) => n.id === selectedNode.id) : null;
    if (sel || node) {
      const outline = h("div", { class: "hm-pe__outline", "data-hm-pe-outline": "" });
      const box = node ? { top: node.y, height: node.h } : sel;
      outline.style.top = `${box.top * scale}px`;
      outline.style.height = `${box.height * scale}px`;
      if (node) {
        outline.style.left = `${node.x * scale}px`;
        outline.style.width = `${node.w * scale}px`;
        outline.style.right = "auto";
      }
      marks.append(outline);
    }
    // The rest of a multi-selection: outlined, no tag.
    const primary = node ? node.id : sel?.id;
    for (const id of v2 ? (handlers.getSelection?.() ?? []) : []) {
      const rect = id === primary ? null : nodes.find((n) => n.id === id);
      if (!rect) continue;
      const member = h("div", { class: "hm-pe__outline hm-pe__outline--member", "data-hm-pe-outline": "" });
      member.style.cssText = `top:${rect.y * scale}px;height:${rect.h * scale}px;left:${rect.x * scale}px;width:${rect.w * scale}px;right:auto`;
      marks.append(member);
    }
    const over = v2 && hoverId && hoverId !== selectedNode?.id ? nodes.find((n) => n.id === hoverId) : null;
    if (over) {
      const ring = h("div", { class: "hm-pe__hover", "data-hm-pe-hover": "" });
      ring.style.cssText = `top:${over.y * scale}px;left:${over.x * scale}px;width:${over.w * scale}px;height:${over.h * scale}px`;
      const tag = (over.k === "s" ? handlers.sectionLabel?.(over.id) : handlers.nodeLabel?.(over.id, over.k, over.t)) ?? "";
      if (tag && hoverId !== selectedId) ring.append(h("span", { class: "hm-pe__tag", text: tag }));
      marks.append(ring);
    }
    if (hint) {
      const box = layout[hint];
      const hh = hints()[hint];
      const chip = h("div", { class: "hm-pe__hint", role: "status" }, h("span", { text: hh.text }), h("a", { href: hh.href, text: hh.label }));
      const close = h("button", { type: "button", class: "hm-btn hm-btn--sm hm-btn--ghost", "aria-label": "Dismiss", text: "×" });
      close.addEventListener("click", () => {
        hint = null;
        drawMarks();
      });
      chip.append(close);
      chip.style.top = `${((box?.top ?? 0) + (box?.height ?? 0) / 2) * scale}px`;
      marks.append(chip);
    }
    chrome?.update();
  }

  /** The style-table entry for key; the table is fetched once, on first use. */
  async function styleEntry(key) {
    styleSpec ??= fetch(stylespecURL(), { credentials: "same-origin" }).then((r) => {
      if (!r.ok) throw new Error(`style table: ${r.status}`);
      return r.json();
    });
    return (await styleSpec).find((e) => e.key === key);
  }

  paintFrames();
  applySize();

  const api = {
    /** Loads a rendered document (a URL) into the hidden buffer; it swaps in when loaded and not outdated. */
    load(url, rev) {
      const target = 1 - active;
      latestRev = Math.max(latestRev, rev ?? 0);
      revs[target] = rev;
      loading = target;
      v2Ready[target] = ready[target] = false;
      closePort(target);
      trusted[target] = true; // the loading document speaks (over its port) before its load event
      expecting[target] = true;
      frames[target].src = url;
    },
    setSelected(id, { scroll = false } = {}) {
      selectedId = id;
      if (v2) selectedNode = id ? { id, k: "s" } : null;
      if (id) post(active, { hm: 1, type: "hm:select", sectionId: id, scrollIntoView: false });
      if (id && v2) postV2(active, toBridge.select({ sel: "section", key: id, scrollIntoView: false }));
      drawMarks();
      if (id && scroll) this.scrollToSection(id);
    },
    /** Selects any node (v2): sel "section" (id is the section key) or "node"; outlines it, tells the frame, scroll brings it into view. */
    setSelectedNode(id, { sel = "node", scroll = false } = {}) {
      if (!v2) return;
      const section = sel === "section";
      selectedId = id && section ? id : null;
      selectedNode = id ? { id, k: section ? "s" : "n" } : null;
      if (id) postV2(active, toBridge.select({ sel, key: id, scrollIntoView: false }));
      drawMarks();
      const box = id && scroll ? nodes.find((n) => n.id === id) : null;
      if (box) root.scrollTo?.({ top: box.y * scale, behavior: "smooth" });
    },
    /** The reported box of a node, in document space ({x, y, w, h}; multiply by `scale` for the pane), or null. */
    nodeRect(id) {
      const n = nodes.find((x) => x.id === id);
      return n ? { x: n.x, y: n.y, w: n.w, h: n.h } : null;
    },
    setHidden(ids) {
      hiddenIds = new Set(ids);
      drawMarks();
    },
    setViewport(w, paneWidth) {
      viewport = w;
      pane = paneWidth;
      scale = zoomScale(zoom, pane, viewport, opts.minScale?.());
      applySize();
    },
    /** The pane resized: a Fit zoom follows it, an explicit zoom keeps its scale. */
    fit(paneWidth) {
      pane = paneWidth;
      scale = zoomScale(zoom, pane, viewport, opts.minScale?.());
      applySize();
    },
    /** Sets the zoom ("fit", 100, 75 or 50); the scale never goes below the floor in layout.js. */
    setZoom(mode) {
      zoom = mode;
      scale = zoomScale(zoom, pane, viewport, opts.minScale?.());
      applySize();
    },
    /** The device the strip's band shows ("base", "md" or "sm"). */
    setDevice(d) {
      device = d;
      chrome?.update();
    },
    /** Height of the sticky strip over the pane (set by the strip); the formatting bar keeps clear of it. */
    setInset(px) {
      inset = px;
    },
    /** The ancestor chain of a node from the latest layout: [{id, k, t}], root first. */
    nodeChain(id) {
      return crumbs(nodes, id);
    },
    get zoom() {
      return zoom;
    },
    get device() {
      return device;
    },
    get inset() {
      return inset;
    },
    /** The selected element's box in document space: {id, k, t, x, y, w, h}, or null (the toolbar sits by it). */
    get selectionBox() {
      const i = selectedNode?.id;
      if (!i) return null;
      const n = nodes.find((x) => x.id === i);
      if (n) return { id: n.id, k: n.k, t: n.t, x: n.x, y: n.y, w: n.w, h: n.h };
      const s = layout.sections.find((x) => x.id === i);
      return s ? { id: s.id, k: "s", t: s.type, x: 0, y: s.top, w: viewport, h: s.height } : null;
    },
    get hoverId() {
      return hoverId;
    },
    get editingId() {
      return editingId;
    },
    get editable() {
      return editable && v2;
    },
    get selectedId() {
      return selectedNode?.id ?? null;
    },
    /** Scrolls the pane (the iframe is full document height) to a section's reported box. */
    scrollToSection(id) {
      const box = layout.sections.find((b) => b.id === id);
      if (!box) return;
      root.scrollTo?.({ top: box.top * scale, behavior: "smooth" });
    },
    /** Posts v2 messages to the visible frame; false (nothing posted) when it cannot be patched now, so the caller reloads. The last carries a seq for `painted`. */
    apply(msgs) {
      if (!v2 || loading !== null || !trusted[active] || !v2Ready[active] || !msgs.every(validToBridge)) return false;
      msgs.forEach((m, i) => post(active, i === msgs.length - 1 ? { ...m, seq: ++seqCounter } : m));
      return true;
    },
    /** Shows a state (hover, focus, active, parent-hover) on a node in the frame, or clears it with null. Re-posted after each reload. */
    force(sel, id, state) {
      if (!state && !forced) return;
      const msg = state ? { sel, key: id, state } : { sel: forced.sel, key: forced.key, state: null }; // clearing names the node last forced
      forced = state ? msg : null;
      if (v2 && trusted[active] && v2Ready[active]) postV2(active, toBridge.force(msg));
    },
    /** Starts editing the text of a node (sel "node" or "section"): the frame takes the focus, then the bridge opens its editor. */
    edit(id, sel = "node") {
      if (!v2 || !trusted[active] || !v2Ready[active]) return;
      frames[active].focus?.();
      postV2(active, toBridge.edit({ sel, key: id }));
    },
    /** Formats the text being edited on node `id` (cmd as in protocol.js; opts {href, newTab}). */
    format(id, cmd, opts = {}) {
      if (v2 && trusted[active] && v2Ready[active]) postV2(active, toBridge.format({ sel: "node", key: id, cmd, ...opts }));
    },
    /** The top-level section ids the visible frame holds now, per its latest layout. */
    frameOrder() {
      return topSections.slice();
    },
    /** The visible section ids the frame should hold after the pending patches; a mismatch at the next `painted` calls handlers.onDrift. */
    expectOrder(ids) {
      expected = { ids: ids.slice(), seq: seqCounter };
    },
    /** Previews a style change inline (declarations from the style table; value null clears; a refused value rejects), then confirm() resolves to the compiled css map that replaces it. */
    async setStyle(sel, id, bp, styleKey, value, confirm) {
      const entry = v2 ? await styleEntry(styleKey) : null;
      if (!entry) throw new Error(`unknown style key ${styleKey}`);
      const css = value === null ? null : styleCSS(entry, value);
      if (value !== null && css === null) throw new Error(`value ${value} is not allowed for ${styleKey}`);
      if (!entry.settle && !this.apply([toBridge.setVar({ sel, key: id, decls: entry.props.map((p) => [p, css]) })])) return false; // settle keys: the css message alone shows them
      const compiled = await confirm();
      return this.apply([toBridge.css({ sel, key: id, css: compiled })]);
    },
    get scale() {
      return scale;
    },
    get layout() {
      return layout;
    },
    /** The node rects of the latest v2 layout. */
    get nodes() {
      return nodes;
    },
    get stage() {
      return stage;
    },
    destroy() {
      closePort(0);
      closePort(1);
      window.removeEventListener("message", onRequest);
      stage.remove();
      chrome?.destroy?.();
    },
  };
  return api;
}
