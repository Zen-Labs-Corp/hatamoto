// The inspector: Content / Style / Advanced tabs for the selected node. Native props
// come from the boot kinds table, style controls from the StyleSpec table (style.js),
// a component's content from the server-rendered form (fields.js). Every edit is a
// store command through api.commit, so undo and redo cover it.
import { h } from "@hm/builder/dom";
import { find, locate, takesParentHover } from "@hm/builder/store";
import { label } from "@hm/builder/navigator";
import { lazy } from "@hm/builder/lazy";

const TABS = [["content", "Content"], ["style", "Style"], ["advanced", "Advanced"]];
/** Theme schema names can carry a trailing "(variant)" ("Intro (essay)"); the panel shows the plain name. */
export const plainLabel = (s) => s.replace(/\s*\([a-z]+\)$/i, "");

let uid = 0;

/**
 * mountInspector(root, api) -> {draw, show, refresh, focus, prefetch, attach}
 * api: {getDoc, getSelected, kinds, sections, urls, csrf, device, styleTable, commit(ops, opts) -> bool, copyStyle(), pasteStyle(), design: {tokens, presets, canCreate}, reload(), reuse (boot.reuse), select(id), announce(msg), user (the boot user id: scopes remembered panel state)}
 */
export function mountInspector(root, api) {
  let tab = "content";
  let state = ""; // the Style tab's State switch ("" = Normal); lives here so in-place commits keep it
  let drawnId = null;
  let drawnNode = null;
  let busy = false;
  let fields = null;
  let rich = []; // rich text editors of the drawn panel
  let mounted = Promise.resolve(); // settles once the latest Style panel has mounted (or failed): focus waits on it
  let gen = 0; // bumped by every draw: a late editor load for an older panel is dropped
  let tabs = [];
  let info = null; // the Class row's drift area, repainted after a style edit
  const slot = h("div", { "data-hm-inspector-slot": "" }); // extra chrome mounted with attach(); it outlives draw()

  const kindInfo = (t) => api.kinds.find((k) => k.kind === t);

  /** Runs a commit that came from a control here: the controls keep their state, nothing redraws. */
  function commit(ops, opts) {
    busy = true;
    let ok = false;
    try {
      ok = api.commit(ops, opts);
    } finally {
      busy = false;
    }
    const n = find(api.getDoc(), api.getSelected());
    if (n) drawnNode = n;
    if (!ok) draw();
    return ok;
  }

  // The Content and Advanced bodies and the header tools are lazy chunks (first paint carries the tab shell only).
  const contentChunk = lazy(() => import("@hm/builder/inspcontent"));
  const headerChunk = lazy(() => import("@hm/builder/inspheader"));

  /** A panel slot filled by a lazy chunk; the busy text shows only when the chunk is slow. Settles `mounted`. */
  function lazyPanel(cls, build) {
    const slot = h("div", { class: cls });
    const mine = gen;
    const busy = setTimeout(() => {
      if (!slot.firstChild) slot.append(h("p", { class: "hm-muted", role: "status", text: "Loading…" }));
    }, 150);
    mounted = contentChunk()
      .then((m) => {
        clearTimeout(busy);
        if (mine !== gen || !slot.isConnected) return;
        const body = build(m.createContent({ api, root, commit, draw, kindInfo, gen: () => gen, pushRich: (c) => rich.push(c), setFields: (f) => (fields = f) }));
        slot.replaceWith(body);
      })
      .catch((err) => {
        clearTimeout(busy);
        console.error("hatamoto builder: inspector panel", err);
        if (mine === gen) slot.replaceChildren(h("p", { class: "hm-muted", role: "alert", text: "This panel could not be loaded. Reload the page to try again." }));
      });
    return slot;
  }

  const contentPanel = (node) => lazyPanel("hm-bs-content", (c) => c.contentPanel(node));
  const advancedPanel = (node) => lazyPanel("hm-bs-advanced", (c) => c.advancedPanel(node));

  // The Style tab body is a lazy chunk (literal import() so the budget walks see it).
  const styleChunk = lazy(() => import("@hm/builder/style"));
  // The Class row (class select, drift list, save form) loads with the header; it pulls in designops.js.
  const presetChunk = lazy(() => import("@hm/builder/presetpanel"));

  function stylePanel(node) {
    const design = api.design;
    const current = () => find(api.getDoc(), node.id);
    const slot = h("div", { class: "hm-bs-styleslot" });
    const mine = gen;
    // Busy text only when the chunk is slow (usually it was prefetched when idle).
    const busy = setTimeout(() => {
      if (!slot.firstChild) slot.append(h("p", { class: "hm-muted", role: "status", text: "Loading style options…" }));
    }, 150);
    mounted = styleChunk()
      .then(({ mountStyle }) => {
        clearTimeout(busy);
        if (mine !== gen || !slot.isConnected) return;
        slot.replaceChildren(
          mountStyle({
            node,
            parent: locate(api.getDoc(), node.id)?.parent ?? null,
            table: api.styleTable(),
            bp: api.device(),
            state,
            onState: (next) => {
              setState(next);
              draw();
            },
            display: kindInfo(node.t)?.display,
            presets: design.presets,
            palette: design.palette,
            user: api.user ?? "",
            onEditSiteColours: api.canStyle ? api.editSiteStyles : null,
            announce: api.announce,
            commit: (key, value, bp = api.device(), opts) => {
              const ok = commit([{ op: "setStyle", id: node.id, bp, key, value }], { label: "Style", ...opts });
              if (ok) info?.();
              return ok;
            },
            commitMany: (writes, bp = api.device(), opts) => {
              const ok = commit(writes.map(({ key, value, bp: at = bp }) => ({ op: "setStyle", id: node.id, bp: at, key, value })), { label: "Style", ...opts });
              if (ok) info?.();
              return ok;
            },
          }),
        );
      })
      .catch((err) => {
        clearTimeout(busy);
        console.error("hatamoto builder: style panel", err);
        if (mine === gen) slot.replaceChildren(h("p", { class: "hm-muted", text: "Style options could not be loaded. Reload the page to try again." }));
      });
    return slot;
  }

  function setState(next) {
    if (state === next) return;
    state = next;
    api.force(drawnId, next || null);
  }

  function draw() {
    fields?.destroy();
    fields = null;
    for (const r of rich) r.destroy();
    rich = [];
    gen++;
    mounted = Promise.resolve();
    const sel = api.getSelected();
    const node = sel && find(api.getDoc(), sel);
    if ((node ? sel : null) !== drawnId) setState("");
    if (state === "parent-hover" && !takesParentHover(locate(api.getDoc(), sel)?.parent)) setState("");
    drawnId = node ? sel : null;
    drawnNode = node || null;
    info = null;
    if (!node) {
      root.replaceChildren(h("p", { class: "hm-muted", text: "Select an element on the page or in the structure to edit it." }), slot);
      tabs = [];
      return;
    }
    const base = `hm-bs-i${++uid}`;
    const list = h("div", { role: "tablist", "aria-label": "Inspector sections", class: "hm-tablist hm-bs-sections" });
    tabs = TABS.map(([key, name]) => h("button", { type: "button", role: "tab", class: "hm-tablist__tab", id: `${base}-${key}-t`, "aria-controls": `${base}-${key}`, "aria-selected": String(key === tab), tabindex: key === tab ? "0" : "-1", "data-tab": key, text: name }));
    list.append(...tabs);
    const build = { content: contentPanel, style: stylePanel, advanced: advancedPanel }[tab];
    const panel = h("div", { role: "tabpanel", class: "hm-bs-panel", id: `${base}-${tab}`, "aria-labelledby": `${base}-${tab}-t`, tabindex: "0" }, build(node));
    const picked = api.getSelectionCount?.() ?? 1;
    const note = picked > 1 ? [h("p", { class: "hm-muted hm-bs-multi", role: "status", text: `${picked} elements selected. Editing the last one picked.` })] : [];
    const name = plainLabel(label(node, api.reuse?.blocks));
    const head = h("div", { class: "hm-bs-header" }, h("h3", { class: "hm-bs-name", text: name })); // the lazy header replaces it, name first
    const classHost = h("div", { class: "hm-bs-classhost" });
    const drawn = gen;
    headerChunk()
      .then((m) => drawn === gen && head.isConnected && head.replaceWith(m.header({ node, api, name })))
      .catch(console.error);
    if (node.t !== "component")
      presetChunk()
        .then((m) => {
          if (drawn !== gen || !classHost.isConnected) return;
          const row = m.presetPanel(node, () => find(api.getDoc(), node.id), { api, commit, draw, root, id: `hm-bs-pr${++uid}`, ready: () => mounted });
          info = row.paint;
          classHost.append(row.el);
        })
        .catch(console.error);
    root.replaceChildren(head, slot, ...note, classHost, list, panel);
    list.addEventListener("click", (e) => {
      const t = e.target.closest?.("[data-tab]");
      if (t) activate(t.dataset.tab, true);
    });
    list.addEventListener("keydown", (e) => {
      const i = tabs.findIndex((t) => t === document.activeElement);
      const to = { ArrowRight: i + 1, ArrowLeft: i - 1, Home: 0, End: tabs.length - 1 }[e.key];
      if (to === undefined || i < 0) return;
      e.preventDefault();
      activate(tabs[(to + tabs.length) % tabs.length].dataset.tab, true);
    });
  }

  function activate(key, focus) {
    if (key === tab) return;
    tab = key;
    setState("");
    draw();
    if (focus) root.querySelector('[role="tab"][tabindex="0"]')?.focus();
  }

  return {
    draw,
    /** The Style tab's State ("" = Normal): the canvas padding drag writes at it. */
    state: () => state,
    /** Loads the Style chunk ahead of the first Style-tab open (never rejects). */
    prefetch: () => Promise.all([styleChunk(), presetChunk(), contentChunk(), headerChunk()]).catch(() => {}),
    /** UX-1 contract (screen.mountInspector): extra chrome kept across draw(); returns the remover. */
    attach(el) {
      slot.append(el);
      return () => el.remove();
    },
    /** Selection changed: redraw only when it is another element. */
    show() {
      if (api.getSelected() !== drawnId) draw();
    },
    /** After any commit: redraw when the selected element changed from outside. */
    refresh() {
      if (busy) return;
      const n = api.getSelected() && find(api.getDoc(), api.getSelected());
      if (n === drawnNode) return;
      if (rich.some((c) => c.sync?.(n, drawnNode))) drawnNode = n; // typed on the canvas: no redraw
      else draw();
    },
    /** Moves focus into the visible panel (first control), or its tab. */
    focus() {
      const panel = root.querySelector('[role="tabpanel"]');
      const first = panel?.querySelector('[contenteditable="true"]') ?? panel?.querySelector("input:not([readonly]), select, textarea, button"); // the Text editor first: its toolbar buttons come before it in the page
      (first ?? root.querySelector('[role="tab"][tabindex="0"]'))?.focus();
      // a lazy panel may be pending: once mounted, focus its first control unless focus went elsewhere
      if (!first) {
        const gen0 = gen;
        mounted.then(() => {
          const at = document.activeElement;
          if (gen0 !== gen || (at && at !== document.body && at.getAttribute("role") !== "tab")) return;
          root.querySelector('[role="tabpanel"]')?.querySelector("input:not([readonly]), select, textarea, button")?.focus();
        });
      }
    },
  };
}
