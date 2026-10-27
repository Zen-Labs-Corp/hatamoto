// The blog editor's glue (v1-13). The server renders one <form> (title, body
// textarea, sidebar) that saves without JavaScript; this module upgrades it in
// place:
//  * the body becomes the shared Tiptap kit (mountRichText) plus slash and bubble
//    menus, callout / FAQ / embed / html blocks, captioned images, paste clean-up
//    and image upload;
//  * drafts autosave in the background (version-checked); every post keeps a
//    local recovery copy; a published post changes only on "Update";
//  * word count, focus mode, keyboard shortcuts, the live SEO/AEO panel (htmx,
//    declared in the markup) and a sandboxed preview.
// Everything the editor writes goes back into the textareas, so the form stays
// the document.
import { hmTime, hmDateTime } from "@hm/dates";
import { createAutosave, createRecovery, recoveryAction } from "@hm/editor/autosave";

/**
 * The HTML as the browser would re-serialise it. The server's sanitiser writes
 * ' and " as entities and the editor writes them as characters; both normalise
 * to the same text, so equal content compares equal.
 */
export function normaliseHTML(html) {
  const t = document.createElement("template");
  t.innerHTML = html;
  return t.innerHTML;
}

// Not @hm/adminpath: the unit-test resolvers stub every other @hm/* module (blog-state.test.mjs), so a named import would fail.
// Keep this expression the same as adminpath.js.
const adminBase = () => (globalThis.document?.querySelector?.('meta[name="admin-base"]')?.content || "/admin").replace(/\/+$/, "");
const csrf = () => document.querySelector('meta[name="csrf-token"]')?.content ?? "";
// Browser-storage key for the formatting toolbar choice ("off" hides it); a UI preference only.
const TOOLBAR_KEY = "hm.blogeditor.toolbar";
// The top bar sits outside the form (in the shared editor layout), so a form lookup falls back to the document.
const $ = (root, sel) => root.querySelector(sel) ?? (root.matches?.("form") ? document.querySelector(sel) : null);

/** Words and reading time (200 words a minute, at least one minute), as the server counts them. */
export function readingStats(text) {
  const words = (text.trim().match(/\S+/g) ?? []).length;
  return { words, minutes: Math.max(1, Math.floor((words + 100) / 200)) };
}

/** The fields autosave and recovery carry, from the form. */
export function draftOf(form) {
  const v = (n) => form.elements.namedItem(n)?.value ?? "";
  return { name: v("name"), content: v("content"), excerpt: v("excerpt"), featured_image: v("featured_image") };
}

/** The request body of one autosave. */
export function autosaveBody(form, version) {
  return new URLSearchParams({ ...draftOf(form), version: String(version) });
}

/** The tab an arrow, Home or End key moves to; null for any other key. Tabs wrap round. */
export function tabTarget(index, key, count) {
  const to = { ArrowRight: index + 1, ArrowLeft: index - 1, Home: 0, End: count - 1 }[key];
  return to === undefined ? null : (to + count) % count;
}

/**
 * The ⋯ menu items that stand in for bar controls exist only where those controls are hidden. They are
 * hidden with the hidden attribute (not CSS alone) so the menu's arrow keys skip them at other widths.
 */
export function showMenuItemsByWidth(doc, mm) {
  for (const [selector, query] of [
    [".hm-be__menu-narrow", "(max-width: 45rem)"],
    [".hm-be__menu-drawer", "(max-width: 45rem)"],
  ]) {
    const q = mm(query);
    const apply = () => doc.querySelectorAll(selector).forEach((el) => (el.hidden = !q.matches));
    apply();
    q.addEventListener("change", apply);
  }
}

/**
 * Below 960px the settings aside is a drawer: Settings (under the editor, or in the ⋯ menu) opens it,
 * Close or Escape closes it and focus returns to the opener. The writing column and the bar are inert
 * while it is open. Wide screens keep the aside beside the text. Opening leaves focus mode first (focus
 * mode hides the aside, which would leave an inert page and no visible drawer). A drawer holding a field
 * the server rejected opens on load, and a summary link to a field inside it opens it too.
 */
export function mountSettingsDrawer({ doc, side, openers, behind, narrow, fallbackFocus, leaveFocus = () => {} }) {
  let from = null;
  const close = (back = true) => {
    if (!side.hasAttribute("data-open")) return;
    side.removeAttribute("data-open");
    for (const el of behind) el.inert = false;
    if (back) (from?.isConnected ? from : fallbackFocus)?.focus();
    from = null;
  };
  const open = (opener) => {
    if (!narrow.matches) return;
    leaveFocus();
    const menu = opener?.closest("[popover]");
    from = menu ? doc.querySelector(`[popovertarget="${menu.id}"]`) : opener;
    menu?.hidePopover?.();
    side.setAttribute("data-open", "");
    for (const el of behind) el.inert = true;
    (side.querySelector("[role=tab][aria-selected=true]") ?? side.querySelector("[data-hm-be-settings-close]"))?.focus();
  };
  const apply = () => {
    side.classList.toggle("hm-be__side--drawer", narrow.matches);
    if (!narrow.matches) close(false);
  };
  apply();
  narrow.addEventListener("change", apply);
  for (const o of openers) o.addEventListener("click", () => open(o));
  side.querySelector("[data-hm-be-settings-close]")?.addEventListener("click", () => close());
  doc.addEventListener("keydown", (e) => {
    if (e.key === "Escape" && side.hasAttribute("data-open") && !side.querySelector(":popover-open")) close();
  });
  doc.addEventListener("click", (e) => {
    const link = e.target?.closest?.('a[href^="#"]');
    const target = link && link.getAttribute("href").length > 1 ? doc.getElementById(link.getAttribute("href").slice(1)) : null;
    if (!target || !side.contains(target) || !narrow.matches) return;
    e.preventDefault();
    open(null);
    // a field in the other settings tab sits in a hidden panel: show that panel first
    const panel = target.closest("[role=tabpanel]");
    if (panel?.hidden) side.querySelector(`[role=tab][aria-controls="${panel.id}"]`)?.click();
    target.focus();
  });
  if (narrow.matches && side.querySelector('[aria-invalid="true"], .hm-field--invalid')) open(null);
}

/** The copy the server delivered in the page ({content, version, name, excerpt, at}), or null when none or malformed. */
export function deliveredCopy(doc = document) {
  try {
    const c = JSON.parse(doc.getElementById("hm-be-recovery")?.textContent ?? "null");
    if (!c || typeof c !== "object" || typeof c.content !== "string") return null;
    const text = (v) => (typeof v === "string" ? v : "");
    return { content: c.content, version: Number(c.version) || 0, name: text(c.name), excerpt: text(c.excerpt), featured_image: text(c.featured_image), at: Number.isFinite(Date.parse(c.at)) ? c.at : "" };
  } catch {
    return null;
  }
}

/** The status line and whether it is an error the writer must not miss (it also goes to the toast region). */
export function stateLine(q, at) {
  const text = { pending: "Unsaved changes", saving: "Saving…", saved: `Saved at ${at}`, error: `${q.message} Retrying.`, conflict: "", stopped: q.message }[q.state];
  return { text, alert: q.state === "error" || q.state === "stopped" };
}

/** Errors go to the toast region as alerts, so they are seen at any width. */
export function alertToast(message, doc = document) {
  doc.dispatchEvent(new CustomEvent("hm:toast", { detail: { kind: "error", message } }));
}

const pad = (n) => String(n).padStart(2, "0");
const stamp = (d) => `${d.getUTCFullYear()}-${pad(d.getUTCMonth() + 1)}-${pad(d.getUTCDate())}T${pad(d.getUTCHours())}:${pad(d.getUTCMinutes())}`;

/** The "Publish at" presets as the UTC datetime-local value each stands for ("" clears the schedule). */
export function schedulePresets(now = new Date()) {
  const at9 = (days) => {
    const d = new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate() + days, 9, 0));
    return stamp(d);
  };
  const toMonday = ((8 - now.getUTCDay()) % 7) || 7;
  return { now: stamp(now), tomorrow: at9(1), monday: at9(toMonday), week: stamp(new Date(now.getTime() + 7 * 864e5)), clear: "" };
}

/** The picker's button text for a datetime-local value. */
export function scheduleLabel(value, zone) {
  return value ? `${value.replace("T", " ")} ${zone}`.trim() : "Not scheduled";
}

/** The suggestions for what was typed: terms not yet chosen that contain it, then "create" when it is new. */
export function termSuggestions(options, chosen, typed, canCreate, max = 8) {
  const q = typed.trim().toLowerCase();
  const left = options.filter((o) => !chosen.has(o.id) && (q === "" || o.name.toLowerCase().includes(q))).slice(0, max);
  const taken = options.some((o) => o.name.toLowerCase() === q);
  return canCreate && q !== "" && !taken ? [...left, { create: true, name: typed.trim() }] : left;
}

export class BlogEditor {
  constructor(form, { fetch: f = globalThis.fetch.bind(globalThis), storage = globalThis.localStorage, timers } = {}) {
    this.form = form;
    this.fetch = f;
    this.id = form.dataset.hmEntity;
    this.status = form.dataset.hmStatus;
    this.version = Number(form.elements.namedItem("version").value);
    this.title = form.elements.namedItem("name");
    this.body = form.elements.namedItem("content");
    this.statusEl = $(form, "[data-hm-be-status] .hm-savestate__text");
    this.stateEl = $(form, "[data-hm-be-status] .hm-savestate");
    this.noticeEl = $(form, "[data-hm-be-notice]");
    this.countEl = $(form, "[data-hm-be-count]");
    this.storage = storage;
    this.opened = draftOf(form);
    this.recovery = createRecovery({
      url: form.dataset.hmRecoveryUrl,
      discardUrl: form.dataset.hmRecoveryDiscardUrl,
      csrf,
      copy: deliveredCopy(),
      fetch: f,
      timers,
      onState: (s) => this.recoveryState(s),
    });
    this.unsaved = false;
    this.ed = null;
    this.autosave =
      this.status === "draft"
        ? createAutosave({ save: (a) => this.saveDraft(a), version: this.version, timers, onState: (q) => this.paintState(q), onSaved: (v) => this.setVersion(v) })
        : null;
    this.wire();
    this.checkRecovery();
  }

  /** The bar's hm/savestate: save state only (text and data-state change). state: saved|unsaved|saving|error. */
  say(text, state) {
    if (this.statusEl) this.statusEl.textContent = text;
    if (state && this.stateEl) this.stateEl.dataset.state = state;
  }

  /** Everything else the editor announces (focus mode, uploads, links) goes to its own polite live region, never the save indicator. */
  notify(text) {
    if (this.noticeEl) this.noticeEl.textContent = text;
  }

  paintState(q) {
    const at = hmTime(new Date());
    const { text, alert } = stateLine(q, at);
    if (text !== undefined) this.say(text, { pending: "unsaved", saving: "saving", saved: "saved", error: "error", stopped: "error", conflict: "error" }[q.state]);
    // One toast per outage: retries pass through "saving" and repeat the error, so
    // the last toasted error is forgotten only when a save succeeds.
    if (alert && text !== this.alerted) {
      this.alerted = text;
      alertToast(text);
    }
    if (q.state === "saved" || q.state === "conflict") this.alerted = "";
    if (q.state === "conflict") this.showConflict(q.conflict);
    if (q.state === "saved") this.unsaved = false;
  }

  setVersion(v) {
    this.version = v;
    this.form.elements.namedItem("version").value = String(v);
    this.form.dataset.hmVersion = String(v);
  }

  async saveDraft({ version }) {
    const sent = draftOf(this.form);
    let res;
    try {
      res = await this.fetch(`${adminBase()}/entities/${this.id}/edit/autosave`, {
        method: "POST",
        credentials: "same-origin",
        headers: { "Content-Type": "application/x-www-form-urlencoded", "X-CSRF-Token": csrf(), Accept: "application/json" },
        body: autosaveBody(this.form, version),
      });
    } catch {
      return { ok: false, message: "Not saved. You appear to be offline." };
    }
    let body = {};
    try {
      body = await res.json();
    } catch {
      /* the session ended or a proxy answered */
    }
    if (res.status === 409 && body.conflict) return { conflict: true, version: body.version, message: body.error };
    if (res.status === 409) {
      // Published or scheduled elsewhere: nothing was saved, and nothing will be until the writer acts.
      return { stop: true, message: "This post was published in another tab; your changes are not saved. Update to apply them." };
    }
    if (res.ok && body.ok) {
      // Saved: the kept copy is redundant unless the writer has typed since.
      const now = draftOf(this.form);
      if (Object.keys(sent).every((k) => sent[k] === now[k])) this.recovery.clear();
      return { ok: true, version: body.version };
    }
    return { ok: false, message: body.error || (res.status === 401 || res.status === 403 ? "Your session ended: sign in again in another tab." : "The draft could not be saved.") };
  }

  showConflict(c) {
    let box = $(this.form, "[data-hm-be-conflict]");
    if (!box) {
      box = document.createElement("div");
      box.className = "hm-flash hm-flash--error";
      box.setAttribute("role", "alert");
      box.dataset.hmBeConflict = "";
      const text = document.createElement("span");
      const over = document.createElement("button");
      over.type = "button";
      over.className = "hm-btn hm-btn--sm";
      over.textContent = "Overwrite with my version";
      over.addEventListener("click", () => {
        box.remove();
        this.autosave.resolve("overwrite");
      });
      const reload = document.createElement("button");
      reload.type = "button";
      reload.className = "hm-btn hm-btn--sm hm-btn--ghost";
      reload.textContent = "Reload their version";
      reload.addEventListener("click", () => location.reload());
      box.append(text, " ", over, " ", reload);
      $(this.form, ".hm-be__meta").after(box);
    }
    box.firstChild.textContent = `${c?.message || "This draft was saved by someone else."} `;
  }

  // ---- recovery ---------------------------------------------------------------------

  /** What the status line says about the server copy: a failure is shown until a retry gets through. */
  recoveryState(s) {
    if (this.autosave) return; // a draft has its autosave and its own status line
    if (s === "failed") this.say("Not saved. You appear to be offline.", "unsaved");
    else if (s === "expired") this.say("Your session ended: sign in again in another tab.", "error");
    else if (s === "refused") this.say("This copy could not be kept. Update the post to save your changes.", "error");
    else if (s === "kept" && this.unsaved) this.say("Unsaved changes", "unsaved");
  }

  checkRecovery() {
    const copy = this.recovery.copy;
    const action = recoveryAction(copy, { ...this.opened, version: this.version }, normaliseHTML);
    if (action === "none") {
      if (copy) this.recovery.clear();
      return;
    }
    const box = $(this.form, "[data-hm-be-recover]");
    if (!box) return;
    const when = copy.at ? hmDateTime(new Date(copy.at)) : "earlier";
    $(box, "[data-hm-be-recover-text]").textContent =
      action === "stale"
        ? `A copy of this post from ${when} was kept for you, but the post has been saved since. Restoring it replaces what is in the editor.`
        : `Unsaved changes from ${when} were kept for you.`;
    box.hidden = false;
    $(box, "[data-hm-be-recover-use]").addEventListener("click", () => {
      this.title.value = copy.name ?? this.title.value;
      this.setBody(copy.content);
      const ex = this.form.elements.namedItem("excerpt");
      if (ex) ex.value = copy.excerpt ?? ex.value;
      box.hidden = true;
      this.sizeTitle();
      this.changed();
    });
    $(box, "[data-hm-be-recover-drop]").addEventListener("click", () => {
      this.recovery.clear();
      box.hidden = true;
    });
  }

  setBody(html) {
    this.body.value = html;
    if (this.ed) this.ed.setHTML(html);
    this.body.dispatchEvent(new Event("input", { bubbles: true }));
  }

  // ---- changes ------------------------------------------------------------------------

  changed() {
    const d = draftOf(this.form);
    const same = d.name === this.opened.name && d.content === this.opened.content && d.excerpt === this.opened.excerpt && d.featured_image === this.opened.featured_image;
    this.unsaved = !same;
    if (!same && !this.autosave) this.recovery.write({ version: this.version, ...d }); // a draft has its autosave
    if (this.countEl) {
      const text = this.ed ? this.ed.editor.getText() : this.body.value.replace(/<[^>]*>/g, " ");
      const s = readingStats(text);
      this.countEl.textContent = `${s.words} words · ${s.minutes} min read`;
    }
    if (this.autosave && !same) this.autosave.change();
    else if (!this.autosave && !same) this.say("Unsaved changes", "unsaved");
  }

  sizeTitle() {
    this.title.style.height = "auto";
    this.title.style.height = `${this.title.scrollHeight}px`;
  }

  // ---- wiring -----------------------------------------------------------------------------

  wire() {
    const form = this.form;
    $(form, "[data-hm-be-tools]")?.removeAttribute("hidden");
    // On phones the bar's tools live in the ⋯ menu: its items press the tool they stand for.
    for (const item of document.querySelectorAll("[data-hm-be-proxy]")) {
      item.addEventListener("click", () => {
        item.closest("[popover]")?.hidePopover?.();
        $(form, `[data-hm-be-${item.dataset.hmBeProxy}]`)?.click();
      });
    }
    showMenuItemsByWidth(document, matchMedia.bind(window));
    this.sizeTitle();
    this.title.addEventListener("input", () => {
      this.title.value = this.title.value.replace(/[\r\n]+/g, " ");
      this.sizeTitle();
    });
    this.title.addEventListener("keydown", (e) => {
      if (e.key !== "Enter") return;
      e.preventDefault();
      (this.ed ? this.ed.focus() : this.body.focus());
    });
    const slug = form.elements.namedItem("slug");
    // Only JavaScript can say the slug follows the title (content.js does the following), so
    // without it the posted slug is kept. Editing the slug by hand stops it.
    const auto = form.elements.namedItem("slug_auto");
    if (auto && slug?.dataset.hmSlugAuto === "1") auto.value = "1";
    slug?.addEventListener("input", () => auto && (auto.value = ""));
    form.addEventListener("input", (e) => {
      if (["name", "content", "excerpt", "featured_image"].includes(e.target.name)) this.changed();
    });
    form.addEventListener("change", (e) => {
      if (e.target.name === "featured_image") this.changed();
    });
    form.addEventListener("submit", (e) => this.submit(e));
    window.addEventListener("pagehide", () => this.recovery.flush());
    window.addEventListener("beforeunload", (e) => {
      this.recovery.flush();
      if (form.isConnected && (this.unsaved || this.autosave?.dirty)) {
        e.preventDefault();
        e.returnValue = "";
      }
    });
    form.addEventListener("keydown", (e) => this.shortcut(e));

    const focus = $(form, "[data-hm-be-focus]");
    focus?.addEventListener("click", () => this.focusMode(!form.hasAttribute("data-hm-focus")));

    $(form, "[data-hm-be-toolbar]")?.addEventListener("click", () => this.toolbarMode(this.ed?.element.dataset.hmBar === "hidden"));

    this.schedule();
    this.terms();
    // Schedule: the picker lives in the sidebar, so open it from there.
    $(form, "[data-hm-be-schedule]")?.addEventListener("click", () => {
      this.selectTab("post");
      const picker = $(form, "[data-hm-be-sched-button]");
      if (picker && !picker.closest("[hidden]")) picker.click();
      else form.elements.schedule?.focus();
    });

    this.tabs();
    this.drawer();
    this.excerptCount();

    const toggle = $(form, "[data-hm-be-preview-toggle]");
    toggle?.addEventListener("click", () => this.openPreview(toggle));
    const dialog = $(form, "[data-hm-be-preview-pane]");
    $(form, "[data-hm-be-preview-close]")?.addEventListener("click", () => dialog.close());
    // Esc and Close both end here: the focus goes back to the Preview button.
    dialog?.addEventListener("close", () => toggle?.focus());
    for (const b of form.querySelectorAll("[data-hm-be-width]")) {
      b.addEventListener("click", () => (b.dataset.hmBeWidth === "refresh" ? this.refreshPreview() : this.setWidth(b)));
    }
    this.mount();
  }

  /**
   * Save draft, Publish and Update post the form. A background save in flight
   * lands first (it moves the version on), then the form goes out with the
   * latest version, so the writer never conflicts with their own autosave.
   */
  async submit(e) {
    if (this.submitting || e.submitter?.value === "preview") return;
    // The form carries the text: a copy written now could land after the save and bring the old text back.
    this.recovery.suspend();
    const a = this.autosave;
    if (!a) {
      this.unsaved = false;
      return;
    }
    if (a.busy) {
      e.preventDefault();
      await a.stop();
      this.submitting = true;
      this.setVersion(a.q.version);
      this.unsaved = false;
      this.form.requestSubmit(e.submitter);
      return;
    }
    a.stop();
    this.setVersion(a.q.version);
    this.unsaved = false; // the server answers with what was saved, or with the form again
  }

  shortcut(e) {
    const mod = e.metaKey || e.ctrlKey;
    if (mod && !e.shiftKey && e.key.toLowerCase() === "s") {
      e.preventDefault();
      if (this.autosave) this.autosave.flush();
      else this.notify("Press Update to save changes to a published post.");
    } else if (mod && e.shiftKey && e.key.toLowerCase() === "f") {
      e.preventDefault();
      this.focusMode(!this.form.hasAttribute("data-hm-focus"));
    } else if (e.altKey && e.shiftKey && !mod && (e.code === "KeyT" || e.key?.toLowerCase() === "t") && this.ed) {
      e.preventDefault();
      this.toolbarMode(this.ed.element.dataset.hmBar === "hidden");
    } else if (mod && !e.shiftKey && e.key.toLowerCase() === "k" && this.ed) {
      e.preventDefault();
      this.ed.element.querySelector('[aria-label="Link"]')?.click();
    } else if (e.key === "Escape" && this.form.hasAttribute("data-hm-focus")) {
      this.focusMode(false);
    }
  }

  /**
   * Publish at: preset picker over the native datetime-local input, which stays in the form
   * (it is what posts, and what shows without JavaScript). A rejected value keeps the native input.
   */
  schedule() {
    const root = $(this.form, "[data-hm-be-sched-js]");
    const native = this.form.elements.namedItem("schedule");
    const fallback = $(this.form, "[data-hm-be-sched]");
    if (!root || !native || fallback.querySelector(".hm-field--invalid")) return;
    const zone = root.dataset.hmZone ?? "UTC";
    const text = $(root, "[data-hm-be-sched-button] span");
    const date = $(root, "[data-hm-be-sched-date]");
    const time = $(root, "[data-hm-be-sched-time]");
    const pop = $(root, "[data-hm-be-sched-pop]");
    const fill = (v) => ([date.value, time.value] = v ? v.split("T") : ["", ""]);
    const paint = () => (text.textContent = scheduleLabel(native.value, zone));
    root.hidden = false;
    fallback.hidden = true;
    paint();
    fill(native.value);
    pop.addEventListener("toggle", (e) => e.newState === "open" && fill(native.value));
    root.addEventListener("click", (e) => {
      const preset = e.target.closest("[data-hm-be-sched-preset]");
      if (preset) fill(schedulePresets()[preset.dataset.hmBeSchedPreset]);
    });
    $(root, "[data-hm-be-sched-apply]").addEventListener("click", () => {
      native.value = date.value ? `${date.value}T${time.value || "09:00"}` : "";
      const status = this.form.elements.status;
      if (native.value && status) {
        status.value = "scheduled";
        // value= fires no event; the styled Select trigger syncs on change.
        status.dispatchEvent(new Event("change", { bubbles: true }));
      }
      paint();
      pop.hidePopover();
      native.dispatchEvent(new Event("change", { bubbles: true }));
    });
  }

  /**
   * Categories and Tags as token inputs over the checkbox list (which stays in the form, and is
   * what posts without JavaScript). Chosen terms are the ticked boxes; a term that does not exist
   * yet becomes a hidden new_term="kind:name" the server creates on save.
   */
  terms() {
    const root = $(this.form, "[data-hm-terms]");
    if (!root) return;
    const canCreate = root.hasAttribute("data-hm-terms-create");
    const found = new Map();
    for (const fs of root.querySelectorAll("fieldset[data-hm-terms-kind]")) {
      const options = [...fs.querySelectorAll('input[name="term"]')].map((box) => ({ id: box.value, name: box.nextElementSibling.textContent.trim(), box }));
      found.set(fs.dataset.hmTermsKind, options);
      fs.hidden = true;
    }
    for (const k of ["category", "tag"]) if (!found.has(k)) found.set(k, []);
    const kinds = ["category", "tag", ...[...found.keys()].filter((k) => k !== "category" && k !== "tag")];
    const plural = (k) => (k === "category" ? "Categories" : `${k[0].toUpperCase()}${k.slice(1)}s`);
    root.querySelector("[data-hm-terms-new]")?.setAttribute("hidden", "");
    root.querySelector("p.hm-muted")?.setAttribute("hidden", "");
    const anchor = root.querySelector("input[name=_terms]");
    for (const kind of kinds.reverse()) anchor.after(this.tokenInput(kind, plural(kind), found.get(kind), canCreate));
  }

  tokenInput(kind, label, options, canCreate) {
    const el = (tag, cls, attrs = {}) => Object.assign(document.createElement(tag), { className: cls }, attrs);
    const id = `be-tok-${kind}`;
    const wrap = el("div", "hm-tokens");
    const lab = el("label", "hm-field__label", { htmlFor: id, textContent: label });
    const box = el("div", "hm-tokens__box");
    const chips = el("ul", "hm-tokens__chips", { role: "list" });
    const input = el("input", "hm-tokens__input", { type: "text", id, autocomplete: "off", placeholder: `Add ${label.toLowerCase()}` });
    const list = el("ul", "hm-tokens__list", { id: `${id}-list`, role: "listbox", hidden: true });
    list.setAttribute("aria-label", `${label} suggestions`);
    input.setAttribute("role", "combobox");
    input.setAttribute("aria-autocomplete", "list");
    input.setAttribute("aria-expanded", "false");
    input.setAttribute("aria-controls", list.id);
    const status = el("span", "hm-visually-hidden", { role: "status" });
    box.append(chips, input);
    wrap.append(lab, box, list, status);

    const chosen = new Set(options.filter((o) => o.box.checked).map((o) => o.id));
    const news = new Map(); // lower-case name -> {chip, hidden}
    let shown = [];
    let active = -1;
    const announce = (t) => (status.textContent = t);
    const changed = () => this.form.dispatchEvent(new Event("change", { bubbles: true }));
    const addChip = (name, onRemove) => {
      const li = el("li", "hm-chip");
      const x = el("button", "hm-chip__x", { type: "button", textContent: "×" });
      x.setAttribute("aria-label", `Remove ${name}`);
      li.append(el("span", "", { textContent: name }), x);
      x.addEventListener("click", () => {
        li.remove();
        onRemove();
        announce(`Removed ${name}.`);
        input.focus();
        changed();
      });
      li._remove = () => x.click();
      chips.append(li);
    };
    const addExisting = (o) => {
      o.box.checked = true;
      chosen.add(o.id);
      addChip(o.name, () => {
        o.box.checked = false;
        chosen.delete(o.id);
      });
    };
    const addNew = (name) => {
      const key = name.toLowerCase();
      if (news.has(key)) return;
      const hidden = el("input", "", { type: "hidden", name: "new_term", value: `${kind}:${name}` });
      wrap.append(hidden);
      news.set(key, hidden);
      addChip(name, () => {
        hidden.remove();
        news.delete(key);
      });
    };
    const close = () => {
      list.hidden = true;
      input.setAttribute("aria-expanded", "false");
      input.removeAttribute("aria-activedescendant");
      active = -1;
    };
    const setActive = (i) => {
      active = i;
      [...list.children].forEach((li, n) => li.setAttribute("aria-selected", String(n === i)));
      if (i >= 0) input.setAttribute("aria-activedescendant", list.children[i].id);
      else input.removeAttribute("aria-activedescendant");
    };
    const render = () => {
      shown = termSuggestions(options, chosen, input.value, canCreate).filter((o) => !o.create || !news.has(o.name.toLowerCase()));
      list.textContent = "";
      shown.forEach((o, n) => {
        const li = el("li", "hm-tokens__opt", { id: `${id}-opt-${n}`, role: "option", textContent: o.create ? `Create "${o.name}"` : o.name });
        li.setAttribute("aria-selected", "false");
        li.addEventListener("mousedown", (e) => {
          e.preventDefault();
          pick(o);
        });
        list.append(li);
      });
      list.hidden = shown.length === 0;
      input.setAttribute("aria-expanded", String(!list.hidden));
      setActive(shown.length ? 0 : -1);
      if (shown.length) list.scrollIntoView({ block: "nearest" });
      if (shown.length) announce(`${shown.length} suggestion${shown.length === 1 ? "" : "s"}`);
    };
    const pick = (o) => {
      if (o.create) addNew(o.name);
      else addExisting(o);
      announce(`Added ${o.name}`);
      input.value = "";
      close();
      changed();
    };
    // Enter takes the highlighted suggestion; a comma takes exactly what was typed.
    const commit = (typedOnly) => {
      const typed = input.value.trim();
      if (!typedOnly && active >= 0 && shown[active]) return pick(shown[active]);
      if (!typed) return;
      const hit = options.find((o) => o.name.toLowerCase() === typed.toLowerCase());
      if (hit && !chosen.has(hit.id)) pick(hit);
      else if (!hit && canCreate) pick({ create: true, name: typed });
      else input.value = "";
    };
    input.addEventListener("input", render);
    input.addEventListener("focus", () => input.value && render());
    input.addEventListener("blur", close);
    input.addEventListener("keydown", (e) => {
      if (e.key === "Enter" || e.key === ",") {
        e.preventDefault();
        e.stopPropagation(); // Enter must not submit the post
        commit(e.key === ",");
      } else if (e.key === "Backspace" && input.value === "") {
        chips.lastElementChild?._remove();
      } else if (e.key === "ArrowDown" || e.key === "ArrowUp") {
        e.preventDefault();
        if (list.hidden) render();
        else if (shown.length) setActive((active + (e.key === "ArrowDown" ? 1 : -1) + shown.length) % shown.length);
      } else if (e.key === "Escape" && !list.hidden) {
        e.preventDefault();
        e.stopPropagation();
        close();
      }
    });
    for (const o of options.filter((o) => o.box.checked)) addChip(o.name, () => {
      o.box.checked = false;
      chosen.delete(o.id);
    });
    return wrap;
  }

  /**
   * The formatting toolbar shows by default (not on phones); the Toolbar button (Alt+Shift+T) hides
   * or shows it and the choice is remembered in this browser.
   */
  toolbarMode(on, { quiet = false } = {}) {
    this.ed?.setBar(on);
    $(this.form, "[data-hm-be-toolbar]")?.setAttribute("aria-pressed", String(on));
    if (quiet) return;
    try {
      this.storage?.setItem(TOOLBAR_KEY, on ? "on" : "off");
    } catch {
      /* storage full or blocked: the choice lasts for this page only */
    }
    this.notify(on ? "Formatting toolbar shown." : "Formatting toolbar hidden.");
  }

  toolbarWanted() {
    try {
      const saved = this.storage?.getItem(TOOLBAR_KEY);
      if (saved === "on" || saved === "off") return saved === "on";
    } catch {
      /* storage blocked: use the width default */
    }
    // Phones start with the toolbar folded away (⋯ brings it back); the choice, once made, sticks.
    return !matchMedia("(max-width: 45rem)").matches;
  }

  // ---- link suggestions ---------------------------------------------------------------------------

  /** POSTs the live body to a link endpoint; answers the JSON body, or {error}. */
  async linkCall(path, fields) {
    const body = new URLSearchParams({ content: this.ed.getHTML(), ...fields });
    try {
      const res = await this.fetch(`${adminBase()}/entities/${this.id}/edit/links/${path}`, {
        method: "POST",
        credentials: "same-origin",
        headers: { "Content-Type": "application/x-www-form-urlencoded", "X-CSRF-Token": csrf(), Accept: "application/json" },
        body,
      });
      const json = await res.json().catch(() => ({}));
      if (!res.ok) return { error: json.error || "Link suggestions are not available right now." };
      return json;
    } catch {
      return { error: "You appear to be offline." };
    }
  }

  /**
   * "Suggest links": the link rules whose phrase is in the draft (unsaved edits count).
   * Insert links the first usable mention in the editor itself; the link is saved with
   * the post (autosave for drafts, Update for published posts), never behind its back.
   */
  async openLinks() {
    if (!this.ed) return;
    const dlg = this.linksDialog();
    const list = dlg.querySelector("[data-hm-links-list]");
    const note = dlg.querySelector("[data-hm-links-note]");
    list.replaceChildren();
    note.textContent = "Looking for matches in this post…";
    if (!dlg.open) dlg.showModal();
    const res = await this.linkCall("suggest", {});
    if (res.error) {
      note.textContent = res.error;
      return;
    }
    const rows = res.suggestions ?? [];
    const ready = rows.filter((r) => r.insertable).length;
    note.textContent = rows.length === 0
      ? "No link rule matches this post yet. Rules live under Settings, Internal linking."
      : `${ready} of ${rows.length} matching ${rows.length === 1 ? "rule" : "rules"} can be linked here.`;
    for (const r of rows) list.append(this.linkRow(r, note));
    list.querySelector("button")?.focus();
  }

  linkRow(r, note) {
    const li = document.createElement("li");
    li.className = "hm-be__link";
    const text = document.createElement("span");
    text.className = "hm-be__link-text";
    const anchor = document.createElement("span");
    anchor.className = "hm-be__link-anchor";
    anchor.textContent = `“${r.anchor}”`;
    const target = document.createElement("span");
    target.className = "hm-be__link-target";
    target.textContent = `${r.target_name}${r.url ? ` · ${r.url}` : ""}`;
    target.title = target.textContent;
    text.append(anchor, target);
    li.append(text);
    if (!r.insertable) {
      const why = document.createElement("span");
      why.className = "hm-be__link-why";
      why.textContent = r.reason || "Cannot be linked";
      li.append(why);
      return li;
    }
    const btn = document.createElement("button");
    btn.type = "button";
    btn.className = "hm-btn hm-btn--sm";
    btn.textContent = "Insert";
    btn.setAttribute("aria-label", `Insert a link on “${r.anchor}” to ${r.target_name}`);
    btn.addEventListener("click", async () => {
      btn.disabled = true;
      btn.textContent = "Inserting…";
      const res = await this.linkCall("insert", { target: r.target, anchor: r.anchor });
      if (res.error || !res.inserted) {
        btn.textContent = "Insert";
        btn.disabled = false;
        note.textContent = res.error || `Not inserted: ${res.reason || "the phrase is no longer linkable"}.`;
        return;
      }
      // One undoable step; onUpdate writes the textarea and marks the post changed.
      const top = document.scrollingElement?.scrollTop ?? 0;
      const ed = this.ed.editor;
      const at = ed.state.selection.from;
      ed.chain().setContent(res.html, { emitUpdate: true }).run();
      ed.commands.setTextSelection(Math.min(at, ed.state.doc.content.size - 1));
      if (document.scrollingElement) document.scrollingElement.scrollTop = top;
      li.dataset.done = "";
      btn.replaceWith(Object.assign(document.createElement("span"), { className: "hm-be__link-why", textContent: "Linked" }));
      this.notify(`Linked “${r.anchor}” to ${r.target_name}. ${this.status === "draft" ? "It saves with the draft." : "Press Update to save it."}`);
      li.parentElement?.querySelector("button")?.focus();
    });
    li.append(btn);
    return li;
  }

  linksDialog() {
    if (this.links) return this.links;
    const dlg = document.createElement("dialog");
    dlg.className = "hm-dialog hm-be__links";
    dlg.setAttribute("aria-labelledby", "be-links-t");
    dlg.innerHTML = `<div class="hm-be__dialog-head"><h2 class="hm-dialog__title" id="be-links-t">Link suggestions</h2></div>
<div class="hm-be__links-body"><p class="hm-muted hm-be__links-note" role="status" aria-live="polite" data-hm-links-note></p><ul class="hm-be__links-list" data-hm-links-list></ul></div>
<div class="hm-dialog__foot"><button type="button" class="hm-btn hm-btn--ghost" data-hm-links-refresh>Refresh</button><button type="button" class="hm-btn hm-btn--primary" data-hm-links-close>Close</button></div>`;
    dlg.querySelector("[data-hm-links-close]").addEventListener("click", () => dlg.close());
    dlg.querySelector("[data-hm-links-refresh]").addEventListener("click", () => this.openLinks());
    dlg.addEventListener("close", () => this.ed?.focus());
    document.body.append(dlg);
    this.links = dlg;
    return dlg;
  }

  /** Wires the settings drawer (see mountSettingsDrawer) for this form. */
  drawer() {
    const side = $(this.form, ".hm-be__side");
    const openers = document.querySelectorAll("[data-hm-be-settings]");
    if (!side || !openers.length) return;
    for (const el of document.querySelectorAll("[data-hm-be-drawer-js]")) el.hidden = false;
    mountSettingsDrawer({
      doc: document,
      side,
      openers,
      behind: [$(this.form, ".hm-be__main"), document.querySelector(".hm-editor-bar")].filter(Boolean),
      narrow: matchMedia("(max-width: 59.99rem)"),
      fallbackFocus: document.querySelector("[popovertarget=be-more]"),
      leaveFocus: () => this.form.hasAttribute("data-hm-focus") && this.focusMode(false),
    });
  }

  /** Post and SEO and AEO tabs. Without JavaScript both panels show, stacked, under their headings. */
  tabs() {
    const list = $(this.form, "[data-hm-be-tabs]");
    if (!list) return;
    list.hidden = false;
    for (const h of this.form.querySelectorAll("[data-hm-be-panel-h]")) h.hidden = true;
    const tabs = [...list.querySelectorAll('[role="tab"]')];
    list.addEventListener("click", (e) => {
      const tab = e.target.closest('[role="tab"]');
      if (tab) this.selectTab(tab.id.replace("be-tab-", ""));
    });
    list.addEventListener("keydown", (e) => {
      const to = tabTarget(tabs.indexOf(document.activeElement), e.key, tabs.length);
      if (to === null || !tabs.includes(document.activeElement)) return;
      e.preventDefault();
      this.selectTab(tabs[to].id.replace("be-tab-", ""), true);
    });
    // A field the server rejected sits in the SEO panel: open it, so the error is seen.
    const seoOnly = !document.getElementById("be-tabpanel-post").querySelector(".hm-field--invalid") && document.getElementById("be-tabpanel-seo").querySelector(".hm-field--invalid");
    this.selectTab(seoOnly ? "seo" : "post");
  }

  /** Shows one panel. Hidden panels stay in the form, so everything still posts and the checks still refresh. */
  selectTab(name, focus = false) {
    if (!$(this.form, "[data-hm-be-tabs]")) return;
    for (const tab of this.form.querySelectorAll('[data-hm-be-tabs] [role="tab"]')) {
      const on = tab.id === `be-tab-${name}`;
      tab.setAttribute("aria-selected", String(on));
      tab.tabIndex = on ? 0 : -1;
      if (on && focus) tab.focus();
      document.getElementById(tab.getAttribute("aria-controls")).hidden = !on;
    }
  }

  excerptCount() {
    const out = $(this.form, "[data-hm-be-excerpt-count]");
    const field = this.form.elements.namedItem("excerpt");
    if (!out || !field) return;
    const show = () => (out.textContent = `${[...field.value].length} / 160`);
    out.hidden = false;
    field.addEventListener("input", show);
    show();
  }

  focusMode(on) {
    this.form.toggleAttribute("data-hm-focus", on);
    $(this.form, "[data-hm-be-focus]")?.setAttribute("aria-pressed", String(on));
    this.notify(on ? "Focus mode on. Press Escape to leave." : "Focus mode off.");
  }

  async mount() {
    let mods;
    try {
      mods = await Promise.all([import("@hm/editor/kit"), import("@hm/editor/blocks"), import("@hm/editor/slash"), import("@hm/editor/bubble"), import("@hm/editor/upload"), import("@hm/editor/paste"), import("@hm/editor/embed")]);
    } catch (err) {
      console.error("hatamoto: the editor could not be loaded; the plain fields still work", err);
      return;
    }
    const [kit, blocks, slash, bubble, upload, paste, embed] = mods;
    const ctx = {
      askLink: () => this.ed?.element.querySelector('[aria-label="Link"]')?.click(),
      pickImage: () => this.ed?.element.querySelector('[aria-label="Insert image from the library"]')?.click(),
      uploadImage: () => files.click(),
      askEmbed: () => ask.open("", () => this.ed.focus()),
    };
    const files = Object.assign(document.createElement("input"), { type: "file", accept: "image/png,image/jpeg,image/gif,image/webp", multiple: true, hidden: true });
    files.setAttribute("aria-hidden", "true");
    files.tabIndex = -1;
    files.addEventListener("change", () => {
      upload.uploadFilesAt(this.ed.editor, files.files);
      files.value = "";
    });
    const ask = kit.askRow("Video address (YouTube or Vimeo)", {
      validate: embed.validateEmbedURL,
      onApply: (src) => this.ed.editor.chain().focus().insertContent([{ type: "embed", attrs: { src, title: embed.parseEmbedURL(src).title } }, { type: "paragraph" }]).run(),
    });
    const label = this.body.labels?.[0]?.textContent?.trim() || "Body";
    try {
      this.ed = kit.mountRichText(this.body, {
        label,
        bar: "hidden",
        placeholder: "Start writing. Type / for blocks.",
        siteHosts: (this.body.dataset.hmSiteHosts ?? "").split(",").filter(Boolean),
        image: blocks.CaptionedImage,
        extensions: [
          blocks.Callout, blocks.Faq, blocks.FaqItem, blocks.FaqQuestion, blocks.FaqAnswer, blocks.Embed, blocks.HtmlBlock, blocks.CurrentBlock,
          paste.PasteClean,
          slash.SlashMenu.configure({ ctx }),
          bubble.BubbleMenu.configure({ ctx }),
          upload.ImageUpload.configure({
            csrf,
            onError: (m) => {
              this.notify(m);
              alertToast(m);
            },
            onBusy: (n) => this.notify(n > 0 ? `Uploading ${n} image${n === 1 ? "" : "s"}…` : "Upload finished."),
          }),
        ],
        toolbar: (specs) => {
          const add = (title, label2, run) => ({ title, label: label2, run, active: null });
          const at = specs.findIndex((s) => s.title === "Insert table");
          const extra = [
            add("Callout", "Callout", () => slash.runSlashCommand(this.ed.editor, slash.SLASH_COMMANDS.find((c) => c.id === "callout"), selectionRange(this.ed.editor), ctx)),
            add("FAQ block", "FAQ", () => slash.runSlashCommand(this.ed.editor, slash.SLASH_COMMANDS.find((c) => c.id === "faq"), selectionRange(this.ed.editor), ctx)),
            add("Video embed", "Video", () => ctx.askEmbed()),
            add("Upload image", "Upload", () => ctx.uploadImage()),
          ];
          return [...specs.slice(0, at + 1), ...extra, ...specs.slice(at + 1), add("Suggest links from your link rules", "Suggest links", () => this.openLinks())];
        },
        onChange: () => this.changed(),
      });
    } catch (err) {
      console.error("hatamoto: the editor failed to start; the plain field is used", err);
      this.body.hidden = false;
      return;
    }
    this.ed.element.append(files, ask.form);
    this.ed.element.classList.add("hm-ed--blog");
    this.ed.element.querySelector('[aria-label="Suggest links from your link rules"]')?.setAttribute("data-hm-suggest", "");
    this.toolbarMode(this.toolbarWanted(), { quiet: true });
    this.changed();
    this.countOnly();
  }

  countOnly() {
    const s = readingStats(this.ed.editor.getText());
    if (this.countEl) this.countEl.textContent = `${s.words} words · ${s.minutes} min read`;
  }

  // ---- preview ------------------------------------------------------------------------------------

  async openPreview() {
    const dialog = $(this.form, "[data-hm-be-preview-pane]");
    if (!dialog || dialog.open) return;
    dialog.showModal();
    await this.refreshPreview();
  }

  setWidth(button) {
    for (const b of this.form.querySelectorAll("[data-hm-be-width]")) b.setAttribute("aria-pressed", String(b === button));
    this.width = Number(button.dataset.hmBeWidth);
    this.sizeFrame();
  }

  sizeFrame() {
    const frame = $(this.form, "iframe.hm-be__frame");
    if (frame) frame.style.width = `${this.width ?? 1100}px`;
  }

  async refreshPreview() {
    const stage = $(this.form, "[data-hm-be-stage]");
    const url = this.form.dataset.hmPreview;
    if (!stage || !url) return;
    stage.textContent = "Loading the preview…";
    try {
      const body = new URLSearchParams();
      for (const [k, v] of new FormData(this.form)) if (typeof v === "string" && k !== "_op") body.append(k, v);
      const res = await this.fetch(url, { method: "POST", credentials: "same-origin", headers: { "Content-Type": "application/x-www-form-urlencoded", "X-CSRF-Token": csrf(), "X-HM-Stage": "1" }, body });
      if (!res.ok) throw new Error(String(res.status));
      const { url: frameURL } = await res.json();
      const frame = document.createElement("iframe");
      frame.className = "hm-be__frame";
      frame.title = "Preview of the post in your theme";
      // Scripts for the theme, never the admin's origin: no cookies, no access to this page.
      frame.setAttribute("sandbox", "allow-scripts");
      frame.src = frameURL;
      stage.textContent = "";
      stage.append(frame);
      this.sizeFrame();
    } catch {
      stage.textContent = "The preview could not be built. Save the draft and try again.";
    }
  }
}

function selectionRange(editor) {
  const { from, to } = editor.state.selection;
  return { from, to };
}

function boot() {
  const form = document.querySelector("form[data-hm-blog-editor]");
  if (form && !form.dataset.hmReady) {
    form.dataset.hmReady = "1";
    new BlogEditor(form);
  }
}
if (typeof document !== "undefined") {
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", boot);
  else boot();
  document.addEventListener("htmx:afterSettle", boot);
}
