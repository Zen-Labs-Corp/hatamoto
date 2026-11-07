// The form designer screen: boots from the page's JSON, mounts the palette, canvas and inspector,
// and wires the bar (name, status, undo, redo, save), the keyboard, Try it and the shortcut sheet.
// The preview, the inspector sections and the shortcut sheet are lazy chunks (literal import()).
// Edits are ops on the shared command log; one redraw per commit.
import { h, $, $$ } from "@hm/builder/dom";
import { lazy } from "@hm/builder/lazy";
import { createStore, commit, undo, redo, canUndo, canRedo, newField, duplicateOps, labelOps } from "@hm/forms/ops";
import { mountCanvas } from "@hm/forms/canvas";
import { mountPalette } from "@hm/forms/palette";
import { mountInspector } from "@hm/forms/inspector";
import { createFormDrag } from "@hm/forms/drag";
import { dragSource } from "@hm/builder/dragsource";
import { createFormSaver } from "@hm/forms/save";
import { resolveKey } from "@hm/forms/keymap";
import { leaveMode, afterLeave } from "@hm/forms/leave";
import { GROUPS } from "@hm/forms/shortcuts";
import { TYPES, isLayout } from "@hm/forms/types";

const previewChunk = lazy(() => import("@hm/forms/preview"));
const sheetChunk = lazy(() => import("@hm/builder/keys"));
const settingsChunk = lazy(() => import("@hm/forms/settingspanel"));
// What the Build-only parts are in Settings mode: nothing to draw, nothing under the pointer.
const INERT = { draw() {}, paint() {}, contains: () => false, rowRects: () => [], focusField() {} };

const csrf = () => document.querySelector('meta[name="csrf-token"]')?.content ?? "";
const SAVE_STATE = { pending: "unsaved", saving: "saving", saved: "saved", error: "error", conflict: "error", stopped: "error", idle: "saved" };
const STATE_TEXT = { pending: "Unsaved changes", saving: "Saving…" };

/** "just now", "4 min ago", "2 h ago", "3 days ago". */
export function ago(ms) {
  const m = Math.floor(Math.max(0, ms) / 60000);
  if (m < 1) return "just now";
  if (m < 60) return `${m} min ago`;
  if (m < 1440) return `${Math.floor(m / 60)} h ago`;
  const d = Math.floor(m / 1440);
  return `${d} ${d === 1 ? "day" : "days"} ago`;
}

function start(root, boot) {
  const settings = boot.mode === "settings"; // the Settings tab: same store, saver and bar, no canvas
  let st = createStore({ name: boot.name, description: boot.description, status: boot.status, fields: boot.fields ?? [], settings: boot.settings ?? {} });
  let selected = null;
  let errors = {};
  let leaving = false;
  let sheet = null;
  let preview = null;
  let panel = null; // the Settings panel, once its chunk has loaded

  const part = (name) => $(document, `[data-hm-fd-${name}]`);
  const sheetEl = $(root, "[data-hm-fd-sheet]");
  const canvasEl = $(root, "[data-hm-fd-canvas]");
  const cardEl = $(root, "[data-hm-fd-card]");
  const headEl = $(root, "[data-hm-fd-formhead]");
  const summaryEl = $(root, "[data-hm-fd-summary]");
  const titleEl = part("title");
  const statusEl = part("status");
  const undoBtn = part("undo");
  const redoBtn = part("redo");
  const saveBtn = part("save");
  const stateEl = part("state");
  const savestate = $(stateEl, ".hm-savestate");
  const stateText = $(stateEl, ".hm-savestate__text");

  const live = h("div", { class: "hm-visually-hidden", role: "status", "aria-live": "polite" });
  const previewEl = h("div", { hidden: true });
  canvasEl?.append(previewEl);
  root.append(live);
  const announce = (msg) => {
    live.textContent = "";
    live.textContent = msg;
  };
  $$(document, "[data-hm-bs-js]").forEach((el) => (el.hidden = false));

  const doc = () => st.doc;
  const idx = (id) => st.doc.fields.findIndex((f) => f.id === id);

  // ----- state line -----
  let reload = null;
  let savedAt = Date.parse(boot.updated) || Date.now(); // when the stored form was last written
  let clean = true;
  const savedText = () => `Saved ${ago(Date.now() - savedAt)}`;
  function showState(state, message = "") {
    savestate.dataset.state = SAVE_STATE[state] ?? "saved";
    clean = (state === "saved" || state === "idle") && !message;
    if (clean && state === "saved") savedAt = Date.now();
    const text = message || (clean ? savedText() : (STATE_TEXT[state] ?? "Not saved"));
    savestate.setAttribute("aria-live", "polite"); // a state change is announced
    stateText.textContent = text;
    savestate.dataset.short = text;
    reload?.remove();
    reload = null;
    const stopped = state === "conflict" || state === "stopped";
    saveBtn.disabled = stopped || clean; // nothing to save when the form is as stored
    if (stopped) saveBtn.title = `Saving is off: ${text}`;
    else saveBtn.removeAttribute("title");
    if (stopped) {
      reload = h("a", { href: location.href, text: "Reload", onclick: () => (leaving = true) });
      stateEl.after(reload);
    }
  }

  // The relative time ticking over is not news: silence the live region for that write; the next state change turns it back on.
  setInterval(() => {
    if (!clean) return;
    savestate.setAttribute("aria-live", "off");
    stateText.textContent = savedText();
  }, 30000);

  const saver = createFormSaver({
    url: boot.saveUrl,
    version: boot.version,
    getDoc: doc,
    csrf,
    onState: showState,
    onErrors: (map, summary) => {
      errors = map;
      redraw();
      const other = Object.entries(map).filter(([k]) => !doc().fields.some((f) => f.id === k) && !(settings && k.startsWith("settings."))).map(([, m]) => m);
      if (other.length) showState("error", `${summary} ${other.join(" ")}`.trim());
    },
  });

  // ----- parts -----
  const canvas = settings ? INERT : mountCanvas(sheetEl, {
    getDoc: doc,
    getSelected: () => selected,
    getErrors: () => errors,
    onSelect: (id) => select(id),
    onEdit: (id, key, value) => run(key === "label" ? labelOps(doc(), id, value) : [{ op: "setField", id, key, value: value === "" ? null : value }], { label: "Edit label" }),
    onAction: (id, a) => act(a, id),
    announce,
  });
  const inspector = settings ? INERT : mountInspector(part("inspector"), { getDoc: doc, getSelected: () => selected, commit: (ops, o) => run(ops, o), getErrors: () => errors, announce });
  // Below the three-column width the palette folds behind "Add a field"; the canvas stays the first thing on the screen.
  const addToggle = part("addtoggle");
  const setAdd = (open) => {
    root.dataset.add = open ? "open" : "closed";
    addToggle.setAttribute("aria-expanded", String(open));
  };
  if (addToggle) {
    addToggle.hidden = false;
    addToggle.addEventListener("click", () => setAdd(addToggle.getAttribute("aria-expanded") !== "true"));
  }
  const palette = settings ? INERT : mountPalette(part("palette"), { types: boot.types ?? [], onAdd: (type) => {
      add(type);
      if (!matchMedia("(min-width: 960px)").matches) setAdd(false);
    }, onDragStart: (type, e) => drag.start(e, { type }) });
  const drag = settings ? null : createFormDrag({
    sheet: sheetEl,
    rowRects: canvas.rowRects,
    onDrop: (index, spec) => {
      if (spec.type) {
        add(spec.type, index);
      } else {
        const from = idx(spec.id);
        const to = index > from ? index - 1 : index;
        if (from >= 0 && to !== from && run([{ op: "moveField", id: spec.id, to }], { label: "Move field" })) announce(`Moved to position ${to + 1} of ${doc().fields.length}`);
      }
    },
  });
  if (!settings) {
    dragSource(sheetEl, {
      pick: (e) => {
        const el = e.target.closest("[data-id]");
        return el && !e.target.closest("input, select, textarea, button") ? () => ({ id: el.dataset.id }) : null;
      },
      start: (e, spec) => drag.start(e, spec),
    });
  }

  // ----- drawing -----
  // Undo and Redo change the hidden form: they are off in Try it.
  function syncHistory() {
    const trying = root.dataset.mode === "try";
    undoBtn.disabled = trying || !canUndo(st);
    redoBtn.disabled = trying || !canRedo(st);
  }
  /** "10 fields · 6 required · 1 conditional" above the form, and the form's own title and description on the sheet. */
  function drawSummary() {
    const fields = doc().fields.filter((f) => !isLayout(f.type));
    const n = fields.length;
    const required = fields.filter((f) => f.required).length;
    const conditional = doc().fields.filter((f) => f.logic).length;
    summaryEl.textContent = `${n} ${n === 1 ? "field" : "fields"} · ${required} required · ${conditional} conditional`;
    const title = doc().name ?? "";
    const about = doc().description ?? "";
    headEl.hidden = !title && !about;
    headEl.firstElementChild.textContent = title;
    headEl.lastElementChild.textContent = about;
    headEl.lastElementChild.hidden = !about;
  }
  function redraw() {
    if (selected && idx(selected) < 0) selected = null;
    canvas.draw();
    inspector.draw();
    syncHistory();
    if (settings) {
      panel?.sync();
      panel?.showErrors(errors);
    } else {
      drawSummary();
    }
    if (document.activeElement !== titleEl) titleEl.value = doc().name ?? "";
    if (statusEl && document.activeElement !== statusEl) statusEl.value = doc().status;
    if (statusEl) statusEl.parentElement.dataset.status = doc().status;
  }

  /** Runs ops as one history entry; false (and a spoken reason) when the store refuses them. */
  function run(ops, o = {}) {
    try {
      st = commit(st, ops, o);
    } catch (e) {
      announce(e.message);
      if (saver.state !== "conflict" && saver.state !== "stopped") showState("error", e.message); // a stopped save keeps its bar
      return false;
    }
    for (const op of ops) {
      if (op.id) delete errors[op.id];
      if (op.op === "setSettings") delete errors[`settings.${op.key}`];
    }
    saver.markDirty();
    redraw();
    return true;
  }

  function select(id, { focus = false } = {}) {
    if (id === selected && !focus) return;
    selected = id;
    canvas.draw();
    inspector.draw();
    if (focus && id) canvas.focusField(id);
  }

  function add(type, index) {
    const at = index ?? (selected ? idx(selected) + 1 : doc().fields.length);
    const f = newField(type, doc().fields);
    if (run([{ op: "addField", index: at, field: f }], { label: `Add ${TYPES[type].label}` })) {
      select(f.id, { focus: true });
      announce(`Added ${TYPES[type].label}`);
    }
  }

  function step(delta) {
    const n = doc().fields.length;
    if (!n) return;
    const i = selected ? idx(selected) + delta : delta > 0 ? 0 : n - 1;
    select(doc().fields[Math.min(Math.max(i, 0), n - 1)].id, { focus: true });
  }

  function act(a, id = selected) {
    const i = idx(id);
    if (i < 0) return;
    const n = doc().fields.length;
    switch (a) {
      case "move-up":
      case "move-down": {
        const to = i + (a === "move-up" ? -1 : 1);
        if (to < 0 || to >= n) return announce(a === "move-up" ? "Already first" : "Already last");
        if (run([{ op: "moveField", id, to }], { label: "Move field" })) {
          announce(`Moved to position ${to + 1} of ${n}`);
          canvas.focusField(id);
        }
        return;
      }
      case "duplicate": {
        const ops = duplicateOps(doc(), id);
        if (run(ops, { label: "Duplicate field" })) select(ops[0].field.id, { focus: true });
        return;
      }
      case "delete": {
        const next = doc().fields[i + 1]?.id ?? doc().fields[i - 1]?.id ?? null;
        if (!run([{ op: "removeField", id }], { label: "Delete field" })) return;
        select(next, { focus: !!next });
        document.dispatchEvent(new CustomEvent("hm:toast", { detail: { kind: "info", message: "Field deleted. Press Ctrl/Cmd+Z to undo.", timeout: 6000 } }));
      }
    }
  }

  function history(fn) {
    st = fn(st);
    if (!st.applied.length) return;
    saver.markDirty();
    redraw();
  }

  // ----- the bar -----
  undoBtn.addEventListener("click", () => history(undo));
  redoBtn.addEventListener("click", () => history(redo));
  saveBtn.addEventListener("click", () => saver.save());
  // A stopped save does nothing; Ctrl/Cmd+S says why instead of staying silent.
  const saveKey = () => (saver.state === "conflict" || saver.state === "stopped" ? announce(`Not saved. ${stateText.textContent}`) : saver.save());
  titleEl.addEventListener("input", () => run([{ op: "setMeta", key: "name", value: titleEl.value }], { label: "Rename form", coalesce: "meta:name" }));
  statusEl?.addEventListener("change", () => run([{ op: "setMeta", key: "status", value: statusEl.value }], { label: "Change status" }));

  // Leaving by a tab or the back link saves first. When saving cannot work (a conflict, a lost session, a
  // refused or failed save) the page can still be left: by the browser's own prompt, or after a confirm here.
  document.addEventListener("click", (e) => {
    const a = e.target.closest?.("a[href]");
    if (!a || leaving || e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || a.target || !(a.closest(".hm-tabs") || a.closest(".hm-fd-head"))) return;
    if (leaveMode(saver) === "go") return;
    e.preventDefault();
    saver.save().then(() => {
      const next = afterLeave(saver);
      if (next === "stay" || (next === "ask" && !confirm("This form could not be saved. Leave without saving your changes?"))) return;
      leaving = true;
      location.assign(a.href);
    });
  });
  window.addEventListener("beforeunload", (e) => {
    if (!leaving && saver.dirty) e.preventDefault();
  });

  // Edit | Try it, and the device widths.
  $$(document, "[data-hm-fd-mode]").forEach((b) =>
    b.addEventListener("click", async () => {
      const mode = b.dataset.hmFdMode;
      $$(document, "[data-hm-fd-mode]").forEach((x) => x.setAttribute("aria-pressed", String(x === b)));
      root.dataset.mode = mode;
      sheetEl.hidden = cardEl.hidden = mode === "try";
      previewEl.hidden = mode !== "try";
      syncHistory();
      if (mode !== "try") return;
      select(null); // nothing stays selected behind the preview
      setAdd(false); // the palette is hidden in Try it; don't leave it open for the way back
      try {
        const m = await previewChunk();
        preview ??= m.mountPreview(previewEl, { getDoc: doc, freeEmail: boot.freeEmail ?? [], announce });
        preview.draw();
      } catch {
        previewEl.replaceChildren(h("p", { class: "hm-muted", role: "alert", text: "The preview could not be loaded. Reload the page to try again." }));
      }
    }),
  );
  $$(document, "[data-hm-fd-device]").forEach((b) =>
    b.addEventListener("click", () => {
      $$(document, "[data-hm-fd-device]").forEach((x) => x.setAttribute("aria-pressed", String(x === b)));
      canvasEl.dataset.device = b.dataset.hmFdDevice;
    }),
  );

  // ----- the keyboard -----
  document.addEventListener("keydown", (e) => {
    const t = e.target;
    if (t.closest?.("dialog[open]")) return;
    const text = !!t.closest?.("input, textarea, select, [contenteditable]");
    const control = !!t.closest?.("button, a[href], summary, [role=switch]");
    const key = resolveKey(e, { text, selected: !!selected, control, canvas: canvas.contains(t), preview: root.dataset.mode === "try" });
    if (!key || (settings && key !== "save" && key !== "undo" && key !== "redo")) return;
    e.preventDefault();
    switch (key) {
      case "save":
        return void saveKey();
      case "undo":
        return history(undo);
      case "redo":
        return history(redo);
      case "prev":
        return step(-1);
      case "next":
        return step(1);
      case "deselect":
        select(null);
        return canvas.focusField(sheetEl.querySelector('[tabindex="0"]')?.dataset.id, { quiet: true });
      case "help":
        return void sheetChunk().then((m) => ((sheet ??= m.createSheet(GROUPS)), sheet.open()));
      default:
        return act(key);
    }
  });

  palette.paint();
  showState("idle");
  redraw();
  if (settings) {
    settingsChunk()
      .then((m) => {
        panel = m.mountSettings(root, { getDoc: doc, commit: run, boot, save: () => saver.save(), ago });
        root.hidden = false;
        redraw();
      })
      .catch(() => showState("error", "The settings could not be loaded. Reload the page to try again."));
  }

  if (new URLSearchParams(location.search).get("qa") === "1") {
    root.dataset.hmQa = "";
    window.__hmForms = { getDoc: () => doc(), getSelected: () => selected };
  }
}

const root = document.querySelector("[data-hm-fd]");
const bootEl = document.querySelector("script[data-hm-fd-boot]");
if (root && bootEl) start(root, JSON.parse(bootEl.textContent));
