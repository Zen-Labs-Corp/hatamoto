// The builder screen: boot JSON -> immutable store -> canvas (render queue, sync.js) and saves (autosave or Update).
import { hmTime } from "@hm/dates";
import { createCanvas, styleDecls } from "@hm/canvas/host";
import { createStore, commit, undo, redo, jump, canUndo, canRedo, find, moveSubject, canEditText, ownsText, setChildKinds, locate, takesParentHover, siblings, newId, v1Shaped, duplicateOps, flat } from "@hm/builder/store";
import { makeKindNames } from "@hm/builder/kindnames";
import { mountNavigator, moveOp, label as rowLabel, useLabels } from "@hm/builder/navigator";
import { presetNote, clipboard } from "@hm/builder/design";
import { resolveKey } from "@hm/builder/keymap";
import { BP_NAME, DEVICE_LABEL, hiddenAt } from "@hm/builder/bp";
import { mountInspector as drawInspector } from "@hm/builder/inspector";
import { idle, lazy } from "@hm/builder/lazy";
import { insertPoint, makeNode, itemsOf, docPos, posValid, addLabel } from "@hm/builder/palette";
import { paletteExtra, wantsSlot } from "@hm/builder/reusecore";
import { insertOps, insertAll } from "@hm/builder/reuseops";
import { innerRef, loopAbove } from "@hm/builder/bridge";
import { createRenderer, createSaver, stateLine, shortLine } from "@hm/builder/sync";
import { mountPanes, mountLeftTabs } from "@hm/builder/panes";
import { createPreviewState, mountDock, mountToolItems, shouldOpenInspector } from "@hm/builder/phone";
import { h, $, $$ } from "@hm/builder/dom";

const CLIP = { copynode: "copy", cutnode: "cut", pastenode: "paste", wrap: "wrap", unwrap: "unwrap", selectsiblings: "selectSiblings" };
const WIDTHS = { base: 1280, md: 768, sm: 390 };
// UX-2 contract; api is set at the end of start().
let api = null;
const selectCbs = new Set();

/** Mounts extra inspector chrome into el. */
export function mountInspector(el) {
  return api ? api.mountInspector(el) : null;
}

/** cb(selectedIdOrNull, {fromCanvas}) on every selection change; returns an unsubscribe. */
export function onSelect(cb) {
  selectCbs.add(cb);
  return () => selectCbs.delete(cb);
}

/** pos = {parent, index}; what = item key, kind or {pattern}. Resolves true once the element is in the page, false when nothing was inserted (unknown item, refused position, a template or block that did not load). */
export function insertAt(pos, what) {
  return api ? api.insertAt(pos, what) : Promise.resolve(false);
}

const csrf = () => document.querySelector('meta[name="csrf-token"]')?.content ?? "";

function start(root, boot) {
  let st = createStore(boot.doc);
  let selected = null; // the primary selection: the element the inspector shows (the last one picked)
  let selection = new Set(); // every selected id, the primary included
  let addTarget = null; // {parent, index}: where a "+" asked the Add tab to insert; the next insert uses it
  let anchor = null; // where a Shift-click range starts
  let device = "base";
  let styleTable = null;

  const bar = document;
  const stateEl = $(bar, "[data-hm-bs-state]");
  const say = $(stateEl, ".hm-savestate__text");
  // One chip says it all ("Draft · Saved 14:02"): the server badge steps aside. A block or part has no badge, so just "Saved 14:02".
  const statusEl = $(bar, "[data-hm-bs-status]");
  const status = statusEl?.textContent.trim() ?? "";
  if (statusEl) (statusEl.hidden = true), (say.textContent = stateLine({ state: "saved" }, { status }));
  const savestate = $(stateEl, ".hm-savestate");
  savestate.dataset.short = shortLine({ state: "saved" }, status);
  // The shared hm/savestate partial: only data-state and the text change.
  const SAVE_STATE = { pending: "unsaved", saving: "saving", saved: "saved", error: "error", conflict: "error", stopped: "error" };
  const banner = $(bar, "[data-hm-bs-banner]");
  const panel = $(root, "[data-hm-bs-panel]");
  const nameInput = $(bar, "[data-hm-bs-title]");
  const undoBtn = $(bar, "[data-hm-bs-undo]");
  const redoBtn = $(bar, "[data-hm-bs-redo]");
  const historyBtn = $(bar, "[data-hm-bs-history]");
  const canvasEl = $(root, "[data-hm-bs-canvas]");
  const live = h("div", { class: "hm-visually-hidden", role: "status", "aria-live": "polite" });
  root.append(live);
  const announce = (msg) => {
    live.textContent = "";
    live.textContent = msg;
  };
  /** A refusal or failure: heard through the live region and seen on the status line (as a store refusal is). */
  const warn = (msg) => {
    announce(msg);
    say.textContent = msg;
    savestate.dataset.state = "error";
    savestate.dataset.short = "Problem";
  };
  let sheet = null;
  const designOps = lazy(() => import("@hm/builder/designops"));
  const showOnChunk = lazy(() => import("@hm/builder/showon")); // Show on: one width at a time
  const sheetChunk = lazy(() => import("@hm/builder/keys"));
  const historyChunk = lazy(() => import("@hm/builder/history"));
  const pickChunk = lazy(() => import("@hm/builder/picker"));
  const menuChunk = lazy(() => import("@hm/builder/ctxmenu"));
  const clipChunk = lazy(() => import("@hm/builder/clipops")); // multi-selection ops, node clipboard
  const commandChunk = lazy(() => import("@hm/builder/commandk")); // the Ctrl/Cmd+K palette
  const startChunk = lazy(() => import("@hm/builder/startpanel")); // the empty-page start panel
  const hintChunk = lazy(() => import("@hm/builder/coachmarks")); // the two one-time hints
  const siteChunk = lazy(() => import("@hm/builder/sitestyles")); // Site styles, owners only
  $$(bar, "[data-hm-bs-js]").forEach((el) => {
    el.hidden = false;
  });

  const kindName = makeKindNames(boot.kinds);
  useLabels({ kindName, sections: boot.sections });
  let relabel = null; // historylabel.js once loaded (idle); until then a commit keeps its caller's label
  const labelChunk = lazy(() => import("@hm/builder/historylabel").then((m) => (relabel = m)));
  const label = (id) => {
    const n = find(st.doc, id);
    return n ? rowLabel(n, boot.reuse?.blocks, kindName, boot.sections) : "";
  };

  setChildKinds(boot.kinds);
  const design = boot.design ?? { tokens: false, presets: [], canCreate: false };

  const tree = mountNavigator($(root, "[data-hm-bs-tree]"), {
    getDoc: () => st.doc,
    getSelected: () => selected,
    getSelection: () => [...selection],
    onSelect: (id, mods) => select(id, false, mods), // mods.pick: a plain click or tap (not keys, not Ctrl/Shift)
    getDevice: () => device,
    onToggleHide: (id) => toggleHide(id),
    onMenu: (id, x, y) => showMenu(id, x, y),
    onAdd: (btn) => addAt({ parent: "", index: 0 }, btn),
    onRename: (id, value) => run([{ op: "setProp", id, key: "navlabel", value: value || null }], { label: "Rename" }) && announce(value ? `Renamed to ${value}` : "Name removed"),
    getNote: (n) => presetNote(n, design.presets),
    getBlocks: () => boot.reuse?.blocks ?? [],
    kindName,
    getSections: () => boot.sections ?? [],
    bar: $(root, "[data-hm-bs-navbar]"),
    announce,
  });
  $(root, "[data-hm-bs-rename]")?.addEventListener("click", () => dispatch("rename"));

  const inspector = drawInspector(panel, {
    getDoc: () => st.doc,
    getSelected: () => selected,
    getSelectionCount: () => selection.size,
    kinds: boot.kinds,
    sections: boot.sections,
    options: boot.options ?? {},
    user: boot.user ?? "",
    canStyle: !!(boot.canStyle && boot.siteStyles),
    editSiteStyles: () => siteBtn?.click(),
    bindSources: boot.bindSources ?? [],
    urls: boot.urls,
    csrf,
    device: () => device,
    styleTable: () => styleTable,
    commit: (ops, opts) => run(ops, opts),
    design,
    reload: () => renderer.stage(),
    copyStyle: () => copySelected(),
    pasteStyle: () => pasteSelected(),
    restore: boot.urls.nodeVersions ? (id) => restoreNode(id) : null,
    force: (id, state) => {
      const { sel, key } = id && find(st.doc, id) ? innerRef(st.doc, id) : {};
      canvas.force(sel, key, sel ? state : null);
    },
    reuse: boot.reuse,
    select: (id) => select(id),
    announce,
  });

  const paneWidth = () => Math.max(0, canvasEl.clientWidth - 24);
  const loopTyped = new Map(); // node id -> prop, typed in on the canvas under a loop and pending the loop re-render
  // Typing under a loop sent nothing: re-render the loop when the edit ends so items 2..n match.
  const flushLoopText = (only) => {
    for (const [id, prop] of [...loopTyped]) {
      if (only && only !== id) continue;
      loopTyped.delete(id);
      const n = find(st.doc, id);
      if (n && loopAbove(st.doc, id)) renderer.commit(st.doc, st.doc, [{ op: "setProp", id, key: prop, value: n.p?.[prop] ?? "" }]);
    }
  };
  let ui = {}; // the inline session and the formatting bar, once loaded
  const inline = lazy(() => import("@hm/builder/inlinehtml").then((m) => (ui.i = m.mountInline({ run, doc: () => st.doc, renderer }))));
  const repatch = (id) => inline().then((i) => i.repatch(id)).catch(() => renderer.stage());
  const fieldJump = lazy(() => import("@hm/builder/fieldjump"));
  const toField = (id, path, fix) => {
    select(id);
    dispatch("inspect");
    if (fix) repatch(id);
    fieldJump().then((m) => m.focusField(panel, id, path, announce));
  };
  const edited = (id, path) => toField(id, path, 1);
  // The bar needs the page list only for its link row: that chunk failing leaves the bar working with no list.
  let linkPages = null;
  const fmtBar = lazy(() => {
    import("@hm/builder/elementsettings").then((es) => (linkPages = es.linkChoices)).catch(() => {});
    return import("@hm/canvas/fmtbar").then((m) => (ui.b = m.mountBar(canvas, { announce, pages: () => (linkPages ? linkPages(st.doc, boot.options?.pages, boot.kinds) : []) })));
  });
  setTimeout(() => fmtBar().catch(() => {}));
  const withBar = (f) => fmtBar().then(f).catch(() => warn("The formatting bar did not load. Reload the page."));
  // The text chunk did not load: say so, keep what was typed as plain text.
  const textLost = (id, prop, v) => {
    run([{ op: "setProp", id, key: "text", value: prop === "html" ? new DOMParser().parseFromString(v.replace(/<br>/g, "\n"), "text/html").body.textContent : v }, { op: "setProp", id, key: "html", value: null }], { coalesce: "lost" + id }, { inline: true });
    warn("Text editing did not load. Typing is kept as plain text; reload to format it.");
  };
  // focus or a click in the host UI also ends the edit (a browser may not blur the frame's editor)
  for (const type of ["pointerdown", "focusin"]) document.addEventListener(type, () => flushLoopText(), true);
  const canvas = createCanvas(
    canvasEl,
    {
      onSelect: (id, _path, mods) => select(id, true, mods),
      onSelectNode: (id, mods) => select(id, true, mods),
      getSelection: () => [...selection].map((id) => frameRef(find(st.doc, id), locate(st.doc, id)).key), // the frame's keys
      onHandleDown: (e, id) => {
        if (selection.size > 1) return warn("Drag one element at a time");
        const spec = { id: moveSubject(st.doc, id) };
        if (drag) drag.start(e, spec);
        else handleDown = { e, spec }; // pressed before the drag chunk arrived: it starts when it does, if still held
      },
      onNodeText: (id, prop, value) => {
        if (!canEditText(boot.kinds, find(st.doc, id), prop)) return;
        const loop = loopAbove(st.doc, id);
        if (loop) loopTyped.set(id, prop);
        inline().then((i) => i.text(id, prop, value, !loop)).catch(() => textLost(id, prop, value));
      },
      onEdit: edited,
      onEditText: edited,
      onRichOpen: (id, path) => toField(id, path),
      onSelection: (m) => withBar((b) => b.show(m)),
      onNodeTextLimit: () => warn("This text is at the 20,000-character limit. Nothing more can be added."),
      onNodeTextEnd: (id) => {
        inline().then((i) => (flushLoopText(id), i.end(id))).catch(() => (flushLoopText(id), warn("Text editing did not load. Reload the page.")));
        withBar((b) => b.hide());
      },
      onContextMenu: (id, _k, x, y) => showMenu(id, x, y, true),
      // "k" from a text edit is "insert link"; from the page it is the command palette (keyAction)
      onKey: (key, shift, ctx) => (key === "k" && ctx?.editing ? withBar((b) => b.link()) : keyAction(key, shift)),
      onDrift: () => renderer.stage(),
      onFrameLost: () => renderer.stage(),
      onSwap: () => (ui.b?.hide(), ui.i?.reset(), preview.ready()), // a new frame has no edit: no bar, and the next one is a new undo step
      nodeLabel: (id, _k, type) => label(id) || type,
      sectionLabel: (id) => label(id),
      onAction: (name) => dispatch(name, true), // the element toolbar: duplicate, wrap, togglehide, remove
      onMore: (r) => showMenu(selected, r.left, r.bottom, true),
      isHidden: (id) => {
        const n = find(st.doc, moveSubject(st.doc, id));
        return !!n && hiddenAt(n, device);
      },
      // the keyboard path to the "+" layout picker: the position right after the selection
      onAddAfter: (btn) => {
        const l = selected && locate(st.doc, moveSubject(st.doc, selected));
        if (l) addAt({ parent: l.parentId, index: l.index + 1 }, btn);
      },
      onCrumb: (id) => select(id, false, { pick: true }), // null is the Page crumb: nothing selected; not "from the canvas", so the frame follows
      onZoom: (mode) => announce(mode === "fit" ? "Zoom: fit to the pane." : `Zoom: ${mode}%.`),
      onAdd: (pt, btn) => {
        const pos = docPos(st.doc, pt);
        if (pos) addAt(pos, btn);
        else announce("That spot has moved. Select the element and use Add after in the toolbar.");
      }, // the "+" between elements and inside empty containers
    },
    { v2: true, minScale: () => (dock.stacked() ? 0.1 : undefined) }, // the phone shell has no room to scroll a 0.5 stage: Fit may go lower
  );

  // The first render: a loading state until a frame is swapped in, then "Preview unavailable" with
  // Try again if it errors or takes longer than PREVIEW_TIMEOUT. Later re-renders keep the old frame.
  const preview = createPreviewState(canvasEl, () => renderer.stage());

  // Splitters and collapse buttons; the ResizeObserver below refits the preview scale.
  const panes = mountPanes(root, { user: boot.user ?? "", announce });
  // Templates and blocks: only when the site can use them (boot.reuse.available).
  const reuse = boot.reuse?.available ? boot.reuse : null;
  const insertFrom = (item, o) => {
    const pos = posValid(st.doc, addTarget) ? addTarget : null; // a position the doc has outgrown falls back to the usual insert point
    addTarget = null;
    return item.kind === "template" || item.kind === "block" ? insertReuse(item, pos, o) : insertItem(item, pos, o);
  };
  // The left pane's tabs; the Add tab's body is the lazy addtab.js, mounted the first time the tab shows.
  let palette = null;
  const paletteChunk = lazy(() =>
    import("@hm/builder/addtab").then((m) => {
      palette = m.mountPalette($(root, "[data-hm-bs-palette]"), {
        kinds: boot.kinds,
        sections: boot.sections,
        onInsert: insertFrom,
        announce,
        slot: !!reuse && wantsSlot(reuse),
        user: boot.user ?? "",
        urls: reuse?.urls,
        extra: () => (reuse ? paletteExtra(reuse, boot.entity) : []),
      });
      bindPaletteDrag();
      return palette;
    }),
  );
  const openPalette = (patterns = false) => paletteChunk().then((p) => (patterns ? p.showPatterns() : p.open())).catch(() => warn("The Add tab did not load. Reload the page."));
  // Phone: the dock opens the left pane (on its Navigator or Add tab) or the Inspector as a sheet over the page.
  const dock = mountDock(root, (name) => name !== "insp" && leftTabs.pick(name));
  const phonePane = (name) => dock.stacked() && dock.pick(name);
  const leftTabs = mountLeftTabs(root, (name) => {
    if (name === "add") paletteChunk().catch(() => warn("The Add tab did not load. Reload the page."));
    if (dock.stacked() && ["nav", "add"].includes(root.dataset.hmBsTab)) dock.show(name); // the tab inside the sheet follows the dock
  });
  $(root, "[data-hm-bs-addbtn]")?.addEventListener("click", () => dispatch("palette"));
  const cmdkBtn = $(root, "[data-hm-bs-cmdk]");
  cmdkBtn?.addEventListener("click", () => dispatch("commandk"));
  if (cmdkBtn && /Mac|iPhone|iPad/.test(navigator.platform)) ($(cmdkBtn, "span").textContent = "⌘K"), cmdkBtn.setAttribute("aria-label", "⌘K: open command palette");
  // The reuse UI is a lazy chunk, mounted on arrival; the palette's groups come from reusecore.js.
  let reuseUI = null;
  const RELOAD_REUSE = "Templates and blocks did not load. Reload the page.";
  const reuseReady = reuse
    ? import("@hm/builder/reuse")
        .then((m) => {
          reuseUI = m.mountReuse($(root, "[data-hm-bs-reuse]"), { reuse, kinds: boot.kinds, csrf, getDoc: () => st.doc, getSelected: () => selected, run: (ops, o) => run(ops, o), select: (id) => select(id), announce, repaint: () => palette?.repaint() });
          reuseUI.refresh();
          return reuseUI;
        })
        .catch((err) => {
          console.error("hatamoto builder: reuse", err);
          $(root, "[data-hm-bs-reuse]").append(h("p", { class: "hm-muted hm-bs-reuse__s", role: "alert", text: RELOAD_REUSE }));
          return null;
        })
    : null;
  // Dragging is a lazy chunk (drag.js): bound as soon as it arrives, a moment after first paint.
  let drag = null;
  let dragSrc = null;
  let paletteBound = false;
  const bindPaletteDrag = () => {
    if (!drag || !palette || paletteBound) return;
    paletteBound = true;
    dragSrc(palette.el, { pick: palette.pick, start: drag.start });
  };
  let handleDown = null; // {e, spec}: a move-handle press waiting for the chunk
  for (const type of ["pointerup", "pointercancel"]) document.addEventListener(type, () => (handleDown = null), true);
  import("@hm/builder/drag")
    .then(({ createDrag, dragSource }) => {
      drag = createDrag({ canvas, getDoc: () => st.doc, scroller: canvasEl, onDrop: (target, spec) => drop(target, spec) });
      if (handleDown) {
        const { e, spec } = handleDown;
        handleDown = null;
        drag.start(e, spec);
      }
      dragSrc = dragSource;
      bindPaletteDrag();
      dragSource($(root, "[data-hm-bs-tree]"), {
        pick: (e) => {
          const row = e.target.closest?.("input") ? null : e.target.closest?.("[data-id]");
          if (row && selection.size > 1) return warn("Drag one element at a time"), null;
          return row ? () => ({ id: row.dataset.id }) : null;
        },
        start: drag.start,
      });
    })
    .catch((err) => {
      console.error("hatamoto builder: drag", err);
      warn("Dragging did not load. Reload the page.");
    });

  // The inline style preview needs the style table; until it loads (or for a value it
  // does not allow) the plan asks for a full reload instead.
  fetch(boot.urls.stylespec, { credentials: "same-origin" })
    .then((r) => (r.ok ? r.json() : null))
    .then((t) => {
      styleTable = t;
      inspector.draw();
    })
    .catch(() => {});
  // The library picker lives in content.js (a lazy chunk). Until it has loaded, a click on a picker
  // opener (Image field, rich-text toolbar) is held back, the chunk fetched, the click replayed.
  const loadContent = lazy(() => import("@hm/content"));
  let contentLoaded = false;
  document.addEventListener(
    "click",
    (evt) => {
      const opener = evt.target.closest?.("[data-hm-media-picker]");
      if (!opener || contentLoaded) return;
      evt.preventDefault();
      evt.stopImmediatePropagation();
      loadContent()
        .then(() => {
          contentLoaded = true;
          opener.click();
        })
        .catch((err) => console.error("hatamoto builder: media picker", err));
    },
    true,
  );
  idle(inspector.prefetch); // the Style chunk, after first paint (import(), never modulepreload)
  idle(() => designOps().catch(() => {}));
  idle(() => labelChunk().catch(() => {}));
  idle(() => clipChunk().catch(() => {}));
  idle(() => sheetChunk().catch(() => {})); // the shortcut sheet, so "?" opens it at once
  idle(() => commandChunk().then(paletteChunk).catch(() => {})); // + the Add tab
  idle(() => import("@hm/builder/fields").then((m) => m.prefetch()).catch(() => {})); // the section-form chunks (editor kit, repeater, content), likewise
  const decls = (_node, bp, key, value) => {
    const entry = styleTable?.find((e) => e.key === key);
    if (!entry) return null;
    if (bp.includes(":")) return []; // a state: inline values would paint it on Normal; the canvas-only force selectors in the server's css show it
    if (bp !== "base" && bp !== device) return []; // no visible effect at this width: the server's css alone settles it
    return styleDecls(entry, value); // settle keys give []: only the server's css message shows them
  };

  const renderer = createRenderer({
    urls: boot.urls,
    csrf,
    canvas,
    getDoc: () => st.doc,
    decls,
    onError: (msg, err) => {
      console.error("hatamoto builder:", msg, err);
      preview.fail();
    },
  });

  const saver = createSaver({
    urls: boot.urls,
    csrf,
    boot,
    getDoc: () => st.doc,
    getName: () => nameInput?.value ?? boot.name,
    onAdopt: (doc) => {
      st = createStore(doc); // the server's sanitiser changed the doc: it is the truth, history restarts
      clipboard.clear();
      select(null);
      renderer.stage();
      paintTools();
    },
    onState: (q) => {
      const at = hmTime(new Date());
      say.textContent = stateLine(q, { draft: saver.draft, at, status });
      savestate.dataset.short = shortLine(q, status); // the phone bar shows this, not the full line
      savestate.dataset.state = SAVE_STATE[q.state] || "saved";
      if (q.state === "conflict") showConflict(q.conflict?.message);
      else if (q.state === "error" || q.state === "stopped") showSaveProblem(stateLine(q, { draft: saver.draft }));
      else if (q.state === "saved") clearBanner("problem", "conflict", "recovery", "note"); // a saved page has no use for an old browser copy: a late Restore would wipe this session
    },
  });

  // Banners float over the canvas; each kind (problem, conflict, recovery, note) is shown once.
  function putBanner(kind, node) {
    clearBanner(kind);
    node.dataset.banner = kind;
    banner.append(node);
  }
  const clearBanner = (...kinds) => kinds.forEach((k) => banner.querySelector(`[data-banner="${k}"]`)?.remove());
  // The bar's short state can't hold a reason on a phone; show it in full where it is always visible.
  function showSaveProblem(text) {
    putBanner("problem", h("div", { class: "hm-flash hm-flash--error", role: "alert", text }));
  }

  // The conflict and recovery banners are a lazy chunk; if it cannot load, the plain problem line shows.
  const banners = lazy(() => import("@hm/builder/banners"));
  idle(() => banners().catch(() => {})); // so the conflict banner appears in the same tick as the status chip
  function showConflict(message) {
    clearBanner("problem");
    banners()
      .then((m) => m.conflict(banner, saver, { versionsUrl: boot.urls.versions }))
      .catch(() => showSaveProblem(message || "This page was changed somewhere else. Reload the page."));
  }

  // A note (not published, slug taken) stays until it is dismissed or the next save; plain text if the chunk cannot load.
  const showNote = (text) => banners().then((m) => m.note(banner, text)).catch(() => putBanner("note", h("div", { class: "hm-flash hm-flash--info", role: "status", text })));

  function offerRecovery() {
    const found = saver.restorable();
    if (!found) return;
    const restore = () => {
      try {
        st = createStore(JSON.parse(found.copy.content));
        clipboard.clear();
      } catch {
        return false;
      }
      if (nameInput && found.copy.name) nameInput.value = found.copy.name;
      select(null);
      renderer.stage();
      paintTools();
      saver.changed();
      return true;
    };
    banners().then((m) => m.recovery(banner, saver, found, restore)).catch(() => {});
  }

  function paintTools() {
    if (undoBtn) undoBtn.disabled = !canUndo(st);
    if (redoBtn) redoBtn.disabled = !canRedo(st);
    for (const id of selection) if (!find(st.doc, id)) selection.delete(id);
    if (selected && !find(st.doc, selected)) select(null);
    if (startUI && st.doc.nodes.length) (startUI.destroy(), (startUI = null)); // content arrived by any route (insert, restore, undo, paste): the panel must not cover it
    tree.draw();
    inspector.refresh();
    reuseUI?.refresh();
    historyUI?.refresh();
  }

  function afterChange(before, opts) {
    renderer.commit(before, st.doc, st.applied, opts);
    saver.changed();
    paintTools();
  }

  /** Applies ops as one history entry; an invalid op is refused with its reason. */
  function run(ops, commitOpts = {}, syncOpts = {}) {
    const before = st.doc;
    try {
      const { label } = commitOpts;
      st = commit(st, ops, relabel?.isGeneric(label) ? { ...commitOpts, label: relabel.derive(ops, before, { kindName, label }) } : commitOpts);
    } catch (err) {
      say.textContent = err.message;
      savestate.dataset.state = "error";
      savestate.dataset.short = "Problem";
      return false;
    }
    afterChange(before, syncOpts);
    // Only a person's own commit is a first insert; undoing a delete (step) re-inserts but is not one.
    if (!inserted && ops.some((o) => o.op === "insert")) (inserted = true), hints?.done();
    return true;
  }

  function step(fn) {
    const before = st.doc;
    const next = fn(st);
    if (next.applied.length === 0) return;
    st = next;
    afterChange(before, {});
  }

  let historyUI = null; // the open history panel
  let startUI = null; // the start panel, while the page is empty
  let hints = null; // the one-time hints
  let inserted = false; // a first insert has been made: the start panel and hints are done
  const doUndo = () => step(undo);
  const doRedo = () => step(redo);

  /** Adds a layout at pos ({parent, index}); with an anchor a picker opens first (layouts, "Choose an element...", "Insert a ready-made section..."). */
  function addAt(pos, anchor) {
    const add = (node, name) => {
      if (!run([{ op: "insert", parent: pos.parent, index: pos.index, node }], { label: addLabel(pos, name) })) return;
      select(node.id);
      announce(`Added ${name} at position ${pos.index + 1}`);
    };
    pickChunk()
      .then((P) =>
        P.openPicker({
          anchor,
          restore: () => tree.focus(),
          onPick: (l) => add(l.make(), l.name),
          onChoose: (r) => {
            addTarget = pos; // the next insert from the Add tab goes here
            announce(`Choose ${r.mode === "pattern" ? "a ready-made section" : "an element"} to add at position ${pos.index + 1}`);
            showAdd(r.mode === "pattern");
          },
        }),
      )
      .catch(() => warn("The layout picker did not load. Reload the page."));
  }

  /** keepOpen (Shift+Enter in the Add tab): the search keeps the focus and the filter, ready for the next one. */
  function insertItem(item, pos, { keepOpen = false } = {}) {
    const node = makeNode(item, boot.kinds);
    const at = pos ?? insertPoint(st.doc, selected, boot.kinds);
    if (!run([{ op: "insert", parent: at.parent, index: at.index, node }], { label: "Add" })) return false;
    select(node.id);
    settle(keepOpen);
    announce(`Added ${label(node.id)} to ${position(node.id)}`);
    return true;
  }

  function settle(keepOpen) {
    if (keepOpen) return palette?.open();
    palette?.reset();
    tree.draw(true);
  }

  async function insertReuse(item, pos, { keepOpen = false } = {}) {
    let nodes;
    try {
      const ui = reuseUI ?? (await reuseReady);
      if (!ui) return announce(RELOAD_REUSE), false;
      nodes = await ui.nodes(item);
    } catch (err) {
      reuseUI?.say(err.message);
      return announce(err.message), false;
    }
    if (!run(pos ? insertAll(pos.parent, pos.index, nodes) : insertOps(st.doc, selected, nodes, boot.kinds), { label: `Add ${item.name}` })) return false;
    select(nodes[0].id);
    settle(keepOpen);
    announce(`Inserted ${item.name}`);
    return true;
  }

  function drop(target, spec) {
    if (spec.id) {
      const at = locate(st.doc, spec.id);
      if (!at || (at.parentId === target.parent && at.index === target.index)) return;
      if (run([target.op], { label: "Move" })) {
        select(spec.id);
        announce(`Moved ${label(spec.id)} to ${position(spec.id)}`);
      }
    } else if (run([target.op], { label: "Add" })) {
      palette?.used(spec.key); // a dragged item counts for Recent too
      select(spec.node.id);
      announce(`Added ${label(spec.node.id)} to ${position(spec.node.id)}`);
    }
  }

  // The frame names a section or component by its section key, anything else by node id.
  function frameRef(n, l) {
    const sel = n && ((!l.parent && v1Shaped(n)) || n.t === "component") ? "section" : "node";
    return { sel, key: n && sel === "section" && n.t !== "component" ? n.c[0].id : n?.id };
  }

  /** Selects id; Ctrl/Cmd toggles, Shift extends the sibling range (clipops chunk). */
  function select(id, fromCanvas = false, mods = {}, scroll = !fromCanvas) {
    addTarget = null;
    const known = id && find(st.doc, id);
    const user = fromCanvas || !!mods.pick; // a person's own pick, not an insert or an undo selecting something
    if (known && (mods.shift || mods.ctrl)) return loadClip().then((C) => C && choose(C.pickSelection(st.doc, selection, anchor, id, mods), fromCanvas, scroll, user));
    choose(known ? { selection: new Set([id]), primary: id, anchor: id } : { selection: new Set(), primary: null, anchor: null }, fromCanvas, scroll, user);
  }

  // scroll: bring the node into view in the frame; off when the pointer is already on it.
  function choose(r, fromCanvas = false, scroll = !fromCanvas, userPick = false) {
    const was = selection.size;
    selection = r.selection;
    selected = r.primary;
    anchor = r.anchor;
    if (was > 1 || selection.size > 1) inspector.draw();
    if (selected) tree.reveal(selected);
    tree.draw();
    if (!fromCanvas || was > 1 || selection.size > 1) {
      const n = selected && find(st.doc, selected);
      const l = n && locate(st.doc, selected);
      const { sel, key } = frameRef(n, l);
      canvas.setSelectedNode(selected && key, { sel, scroll });
    }
    inspector.show();
    if (shouldOpenInspector({ stacked: dock.stacked(), selected, userPick })) dock.pick("insp");
    reuseUI?.refresh();
    for (const cb of selectCbs) cb(selected, { fromCanvas });
  }

  const position = (id) => {
    const at = locate(st.doc, id);
    return at ? `position ${at.index + 1} of ${siblings(st.doc, at.parentId).length}` : "";
  };

  /** The clipops chunk, or null after announcing that it did not load. */
  const loadClip = () =>
    clipChunk().catch((err) => {
      console.error("hatamoto builder: clipops", err);
      warn("Copy, paste and multi-select actions did not load. Reload the page.");
      return null;
    });

  /** Runs a clipops bulk function on the selection; the chunk loads on first use. */
  const bulk = (fn, opts) =>
    loadClip().then((C) => C?.[fn]({ doc: st.doc, run, select: (id) => select(id), selectAll: (ids) => choose({ selection: new Set(ids), primary: ids[ids.length - 1], anchor: ids[0] }), announce, refuse: warn, selected, kinds: boot.kinds, csrf, url: boot.urls.paste }, [...selection], opts));

  function removeSelected() {
    if (selection.size > 1) return bulk("removeMany");
    const target = moveSubject(st.doc, selected); // a v1 section selected as its component goes whole, never leaving an empty shell
    const at = locate(st.doc, target);
    if (!at) return;
    const sib = siblings(st.doc, at.parentId);
    const next = sib[at.index + 1] ?? sib[at.index - 1] ?? at.parent;
    const name = label(target);
    if (run([{ op: "remove", id: target }], { label: "Delete" })) {
      select(next ? next.id : null);
      announce(`Deleted ${name}.`);
    }
  }

  function duplicateSelected() {
    if (selection.size > 1) return bulk("duplicateMany");
    const target = moveSubject(st.doc, selected);
    const ops = duplicateOps(st.doc, target);
    if (run(ops, { label: "Duplicate" })) {
      select(ops[0].node.id);
      announce(`Duplicated ${label(target)}`);
    }
  }

  /** Opens the actions menu for id at a page point (an element outside the selection becomes the selection). */
  function showMenu(id, x, y, fromCanvas = false) {
    if (!selection.has(id)) select(id, false, {}, !fromCanvas); // the pointer is on it: do not scroll the pane away
    menuChunk()
      .then((M) => M.openMenu({ items: M.menuItems({ doc: st.doc, selection: [...selection], selected, kinds: boot.kinds, reuse: !!reuse, device }), x, y, onPick: (action) => dispatch(action, fromCanvas), restore: () => tree.focus() }))
      .catch(() => warn("The menu did not load. Reload the page."));
  }

  /** The designops chunk, or null after announcing that it did not load. */
  const loadDesignOps = () =>
    designOps().catch((err) => {
      console.error("hatamoto builder: designops", err);
      warn("Copy and paste style did not load. Reload the page.");
      return null;
    });

  async function copySelected() {
    const id = selected; // the element the key was pressed on, whatever is selected when the chunk arrives
    const D = await loadDesignOps(); // the copy / paste builders are a lazy chunk (designops.js), prefetched when idle
    const n = D && id && find(st.doc, id);
    if (!n) return;
    D.copyStyle(n);
    announce(`Copied the style of ${label(id)}`);
  }

  async function pasteSelected() {
    const id = selected;
    const D = await loadDesignOps();
    const n = D && id && find(st.doc, id);
    if (!n) return;
    if (!clipboard.has()) return announce("Copy a style first");
    const parent = locate(st.doc, id)?.parent;
    const parentHover = takesParentHover(parent);
    const ops = D.pasteOps(n, st.doc, { parentHover });
    if (!ops.length) return announce("Nothing to paste here");
    if (!run(ops, { label: "Paste style" })) return;
    announce(`Pasted the style onto ${label(id)}${!parentHover && clipboard.hasParentHover() ? ", minus Parent hover" : ""}`);
  }

  /** The restore dialog (noderestore.js, lazy): the versions that held id, put back as one commit. */
  function restoreNode(id) {
    if (!id || !boot.urls.nodeVersions) return;
    import("@hm/builder/noderestore")
      .then((M) => M.openRestore({ id, doc: () => st.doc, url: `${boot.urls.nodeVersions}?node=${encodeURIComponent(id)}`, kindName, label, commit: (ops, o) => run(ops, o), announce, returnFocus: (nid) => requestAnimationFrame(() => root.querySelector(`[data-hm-bs-tree] [role="treeitem"][data-id="${nid}"]`)?.focus()) }))
      .catch(() => warn("The restore dialog did not load. Reload the page."));
  }

  /** Hides or shows id on the device (the whole selection on a multi-selection). */
  async function toggleHide(id) {
    const M = await showOnChunk().catch(() => {
      warn("Show and hide did not load. Reload the page.");
      return null;
    });
    const n = M && find(st.doc, id);
    if (!n) return;
    const on = !hiddenAt(n, device);
    const ids = selection.size > 1 && selection.has(id) ? flat(st.doc).filter((x) => selection.has(x.id)) : [n];
    const ops = ids.flatMap((x) => M.showOnWrites(x, device, !on, boot.kinds.find((k) => k.kind === x.t)?.display).map(({ bp, key, value }) => ({ op: "setStyle", id: x.id, bp, key, value })));
    const what = ids.length > 1 ? ids.length + " elements" : kindName(n.t);
    if (run(ops, { label: `${on ? "Hide" : "Show"} ${what} on ${BP_NAME[device]}` })) announce(`${ids.length > 1 ? ids.length + " elements" : label(id)} ${on ? "hidden" : "shown"} on ${BP_NAME[device]}`);
  }

  function moveSelected(action) {
    const op = moveOp(st.doc, selected, action, boot.kinds);
    if (!op) return announce("It cannot move further that way");
    if (run([op], { label: "Move" })) {
      const into = op.parent && (action === "indent" || action === "outdent") ? ` into ${label(op.parent)}` : "";
      announce(`Moved ${label(selected)}${into} to ${position(selected)}`);
    }
  }

  let cmdk = null;
  function showAdd(patterns = false) {
    panes.toggle("nav", true);
    leftTabs.pick("add");
    phonePane("add");
    return openPalette(patterns);
  }

  /** A command picked in the palette: the same actions as the buttons and keys. */
  function runCommand(id) {
    if (id.startsWith("device-")) return $(bar, `[data-hm-bs-device="${id.slice(7)}"]`)?.click();
    if (id === "sitestyles") return $(bar, "[data-hm-bs-sitestyles]")?.click();
    if (id === "preview") return window.open(boot.urls.view, "_blank", "noopener");
    dispatch(id);
  }

  function dispatch(action, fromCanvas = false) {
    switch (action) {
      case "save":
        return saveNow("save");
      case "undo":
        doUndo();
        return announce("Undone");
      case "redo":
        doRedo();
        return announce("Redone");
      case "duplicate":
        return duplicateSelected();
      case "history":
        if (historyUI) return historyUI.close();
        return historyChunk()
          .then((M) => {
            historyBtn?.setAttribute("aria-expanded", "true");
            historyUI = M.openHistory({ anchor: historyBtn, getStore: () => st, onJump: (n) => step((s) => jump(s, n)), onClose: () => (historyUI = null, historyBtn?.setAttribute("aria-expanded", "false")) });
          })
          .catch(() => warn("The history did not load. Reload the page."));
      case "menu": {
        const r = tree.rect(selected) ?? canvasEl.getBoundingClientRect();
        return showMenu(selected, r.left + 24, r.bottom);
      }
      case "pasteinside":
        return bulk("paste", { inside: true });
      case "savetpl":
      case "mkblock":
        panes.toggle("nav", true);
        return reuseUI?.open(action === "savetpl" ? "tpl-sel" : "blk");
      case "copystyle":
        return copySelected();
      case "pastestyle":
        return pasteSelected();
      case "restore":
        return restoreNode(selected);
      case "remove":
        return removeSelected();
      case "togglehide":
        return toggleHide(moveSubject(st.doc, selected)); // a v1 section hides whole, not its inner component
      case "togglenav":
        return dock.stacked() ? dock.toggle("nav") : panes.toggle("nav");
      case "toggleinspector":
        return dock.stacked() ? dock.toggle("insp") : panes.toggle("insp");
      case "inspect":
        panes.toggle("insp", true);
        phonePane("insp");
        return inspector.focus();
      case "commandk":
        return commandChunk()
          .then((m) => (cmdk ??= m.createCommandK({ getItems: () => [...itemsOf(boot.kinds, boot.sections, { slot: !!reuse && wantsSlot(reuse) }), ...(reuse ? paletteExtra(reuse, boot.entity) : [])], canStyle: !!boot.canStyle, preview: !!boot.urls.view, user: boot.user ?? "", onInsert: insertFrom, onRun: runCommand, announce })).open())
          .catch(() => warn("The command palette did not load. Reload the page."));
      case "palette":
        return showAdd();
      case "edittext": {
        const n = selected && find(st.doc, selected);
        if (!ownsText(n)) return; // only the element's own text, as Enter in the frame (never a descendant's)
        const { sel, key } = frameRef(n, locate(st.doc, selected));
        return canvas.edit(key, sel);
      }
      case "sheet":
        return sheetChunk()
          .then((m) => (sheet ??= m.createSheet()).open())
          .catch((err) => announce(`The shortcut list did not load. Reload the page. ${err.message}`));
      case "moveup":
      case "movedown":
      case "outdent":
      case "indent":
        return moveSelected(action);
      case "rename":
        panes.toggle("nav", true); // the name is typed into the structure row
        leftTabs.pick("nav");
        phonePane("nav"); // on a phone the dock opens the pane
        // a canvas pick names the whole v1 section; in the tree the focused row is renamed
        return tree.rename(fromCanvas ? moveSubject(st.doc, selected) : selected);
      default:
        if (CLIP[action]) bulk(CLIP[action]);
        else tree.act(action);
    }
  }

  // Keys from the canvas frame arrive as the bridge's {key, shift}.
  function keyAction(key, shift) {
    const e = key === "Delete" ? { key } : { key, ctrlKey: true, shiftKey: shift };
    const action = resolveKey(e, { free: true, selected: !!selected });
    if (action) dispatch(action);
  }

  document.addEventListener("keydown", (e) => {
    const t = e.target;
    const tr = !!t.closest?.("[data-hm-bs-tree]");
    const ctx = {
      text: !!t.closest?.("input, textarea, select, [contenteditable]"),
      tree: tr,
      free: tr || t === document.body || !!t.closest?.("[data-hm-bs-canvas]"),
      control: !!t.closest?.("button, a, summary, [role=button], [role=tab], [role=menuitem]"),
      selected: !!selected,
      range: !!getSelection()?.toString(),
    };
    const action = resolveKey(e, ctx);
    if (!action) return;
    e.preventDefault();
    dispatch(action);
  });

  const noteKey = `hm-builder-note:${boot.entity}`;
  // The publish note crosses the reload in session storage; read it as short plain text or not at all.
  const rememberNote = (key, note) => {
    try {
      sessionStorage.setItem(key, note);
    } catch {
      /* storage blocked: the note is not shown after the reload */
    }
  };
  const takeNote = (key) => {
    try {
      const v = sessionStorage.getItem(key);
      sessionStorage.removeItem(key);
      return typeof v === "string" && v.length <= 300 ? v : "";
    } catch {
      return "";
    }
  };

  async function saveNow(op) {
    const res = await saver.save(op);
    if (!res.ok) return;
    if (op === "publish" && res.status === "published") {
      // the page reloads as a published page: carry the note (slug taken) across
      if (res.note) rememberNote(noteKey, res.note);
      return location.reload();
    }
    // saved, but the server says something the person should know (not published, slug taken)
    if (res.note) showNote(res.note);
  }

  $(bar, "[data-hm-bs-keys]")?.addEventListener("click", () => dispatch("sheet"));
  const siteBtn = $(bar, "[data-hm-bs-sitestyles]");
  if (boot.canStyle && boot.siteStyles && siteBtn) {
    siteBtn.hidden = false;
    // a save re-stages the page and refreshes the colour swatches the Inspector shows
    const saved = (colours) => (design.palette && Object.assign(design.palette.colors, colours), renderer.stage(), inspector.draw(), announce("Site styles saved."));
    siteBtn.addEventListener("click", () => {
      siteBtn.closest("[popover]")?.hidePopover?.();
      siteChunk().then((m) => m.open({ boot, csrf: csrf(), onSaved: saved })).catch(() => warn("Site styles did not load. Reload the page."));
    });
  }
  undoBtn?.addEventListener("click", doUndo);
  redoBtn?.addEventListener("click", doRedo);
  // The press light-dismisses the open panel before the click: that click closes, it must not reopen.
  let historyWasOpen = false;
  historyBtn?.addEventListener("pointerdown", () => (historyWasOpen = !!historyUI));
  historyBtn?.addEventListener("click", () => {
    const was = historyWasOpen;
    historyWasOpen = false;
    if (!was) dispatch("history");
  });
  $$(bar, "[data-hm-bs-save]").forEach((b) => b.addEventListener("click", () => saveNow(b.dataset.hmBsSave)));
  nameInput?.addEventListener("input", () => saver.changed());
  mountToolItems(bar, matchMedia.bind(window));
  const chip = $(bar, "[data-hm-bs-device-chip]");
  $$(bar, "[data-hm-bs-device]").forEach((b) => {
    const d = b.dataset.hmBsDevice; // real widths in the label; the short name on a phone (CSS)
    b.replaceChildren(h("span", { class: "hm-bs__dlong", text: DEVICE_LABEL[d] }), h("span", { class: "hm-bs__dshort", text: BP_NAME[d] }));
    b.addEventListener("click", () => {
      device = d;
      root.dataset.hmDevice = d;
      chip.hidden = d === "base";
      chip.dataset.device = d;
      chip.textContent = `Editing ${BP_NAME[d]}`;
      announce(`Editing ${BP_NAME[d]} styles.`);
      $$(bar, "[data-hm-bs-device]").forEach((x) => x.setAttribute("aria-pressed", String(x === b)));
      canvas.setViewport(WIDTHS[device], paneWidth());
      canvas.setDevice(d);
      inspector.draw();
      tree.draw();
    });
  });
  new ResizeObserver(() => canvas.fit(paneWidth())).observe(canvasEl);
  window.addEventListener("beforeunload", (e) => {
    saver.flushLocal();
    if (saver.dirty) e.preventDefault();
  });

  if (new URLSearchParams(location.search).get("qa") === "1") {
    root.dataset.hmQa = "";
    window.__hmBuilder = { doc: () => st.doc, selected: () => selected, selection: () => [...selection], insertAt: (pos, what) => api.insertAt(pos, what) };
  }

  const kept = takeNote(noteKey);
  if (kept) {
    showNote(kept);
  }

  const itemFor = (what) => {
    const w = what?.pattern ?? what;
    if (w && typeof w === "object") return w;
    return [...itemsOf(boot.kinds, boot.sections), ...(reuse ? paletteExtra(reuse, boot.entity) : [])].find((i) => i.key === w || i.kind === w) ?? null;
  };
  api = {
    mountInspector: (el) => inspector.attach?.(el) ?? null,
    insertAt: (pos, what) => {
      const item = itemFor(what);
      if (!item || !pos) return Promise.resolve(false);
      pos = { parent: pos.parent ?? "", index: pos.index }; // the document root is parent ""
      return Promise.resolve(item.kind === "template" || item.kind === "block" ? insertReuse(item, pos) : insertItem(item, pos));
    },
  };

  // An empty page opens the Add tab and a start panel of patterns; two hints show once per user.
  if (!st.doc.nodes.length) {
    leftTabs.pick("add");
    startChunk()
      .then((m) => {
        if (st.doc.nodes.length) return;
        const patterns = [...itemsOf(boot.kinds, boot.sections), ...(reuse ? paletteExtra(reuse, boot.entity) : [])].filter((i) => i.group === "Patterns");
        startUI = m.mountStart(canvasEl, { patterns, urls: reuse?.urls, onPattern: (i) => api.insertAt({ parent: "", index: 0 }, i), onBlank: () => dispatch("palette") });
      })
      .catch(() => {});
  }
  idle(() => hintChunk().then((m) => (inserted ? m.dismissAll(localStorage, boot.user ?? "") : (hints = m.mountCoachmarks({ canvasEl, navFoot: $(root, ".hm-bs__navfoot"), user: boot.user ?? "", empty: !st.doc.nodes.length })))).catch(() => {}));

  canvas.setViewport(WIDTHS.base, paneWidth());
  select(null);
  paintTools();
  renderer.stage();
  offerRecovery();
}

const root = document.querySelector("[data-hm-builder]");
const bootEl = document.getElementById("hm-builder-boot");
if (root && bootEl) start(root, JSON.parse(bootEl.textContent));
