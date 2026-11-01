// Entity, schema, taxonomy and media screens: slug helpers, the media picker
// dialog, drag-and-drop upload with progress, the bulk-delete confirmation and
// drag reordering of schema fields. Every one of these has a working
// no-JavaScript path on the server; this only makes them nicer.

import { adminBase } from "@hm/adminpath";
import { openConfirm } from "@hm/components";
import { renumberOp, renumberRef, removeFocusIndex } from "@hm/listlogic";

// ---- slugs ---------------------------------------------------------------
function slugify(s, sep) {
  return s
    .normalize("NFKD")
    .replace(/[\u0300-\u036f]/g, "")
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, sep)
    .replace(new RegExp(`^${sep}+|${sep}+$`, "g"), "");
}

// The profile's page address under the public switch follows the Name field
// until the person has been public (no data-hm-page-follows then). It uses the
// ASCII slug; a name with none shows "your-name" until saved.
function followPageAddress(src) {
  const sw = document.querySelector('[data-hm-page-follows="name"]');
  const help = sw && document.getElementById(`${sw.id}-help`);
  if (!help || src.id !== "name") return;
  const slug = slugify(src.value, "-").slice(0, 128).replace(/-+$/, "") || "your-name";
  const path = sw.dataset.hmPagePath.replace(new RegExp(`${sw.dataset.hmPageSlug}$`), slug);
  help.textContent = help.textContent.replace(sw.dataset.hmPagePath, path);
  sw.dataset.hmPagePath = path;
  sw.dataset.hmPageSlug = slug;
}

// <input data-hm-slug-from="name" data-hm-slug-auto="1">: follows the source
// field until the person edits it. data-hm-name-from fills a field name
// (letters and underscores) from the label the same way.
document.addEventListener("input", (evt) => {
  const src = evt.target;
  if (!src.id) return;
  followPageAddress(src);
  for (const dst of document.querySelectorAll(`[data-hm-slug-from="${CSS.escape(src.id)}"]`)) {
    if (dst.dataset.hmSlugAuto === "1" && !dst.dataset.hmTouched) dst.value = slugify(src.value, "-");
  }
  if (src.dataset.hmNameFrom) {
    const dst = document.getElementById(src.dataset.hmNameFrom);
    if (dst && (!dst.value || dst.dataset.hmAuto === dst.value)) {
      dst.value = slugify(src.value, "_");
      dst.dataset.hmAuto = dst.value;
    }
  }
  if (src.dataset.hmSlugFrom && src.dataset.hmSlugAuto === "1") src.dataset.hmTouched = "1";
});

// ---- schema editor: choices that depend on the kind --------------------
function syncKinds(root) {
  root.querySelectorAll("[data-hm-kind]").forEach((sel) => {
    const row = sel.closest("[data-hm-field-row]");
    row?.querySelectorAll("[data-hm-for]").forEach((el) => { el.hidden = el.dataset.hmFor !== sel.value; });
  });
}
document.addEventListener("change", (evt) => { if (evt.target.matches?.("[data-hm-kind]")) syncKinds(document); });

// Move up/down/remove swap only the field list. Rows the person had open
// stay open (the server only knows the moved one).
let openRows = [];
document.addEventListener("htmx:beforeRequest", (evt) => {
  if (!evt.detail.elt.matches?.("[data-hm-reorder]")) return;
  openRows = [...document.querySelectorAll("[data-hm-field-row] > details[open]")].map((d) => d.closest("[data-hm-field-row]").id);
});
document.addEventListener("htmx:afterSettle", (evt) => {
  if (!evt.target.querySelector?.("[data-hm-field-list]") && !evt.target.matches?.("[data-hm-field-list]")) return;
  openRows.forEach((id) => { const d = document.getElementById(id)?.querySelector("details"); if (d) d.open = true; });
  openRows = [];
});

function initFields(root) {
  root.querySelectorAll(".hm-fieldrow__handle").forEach((h) => { h.hidden = false; });
  syncKinds(root);
  if (!window.Sortable) return;
  root.querySelectorAll("[data-hm-field-list]").forEach((list) => {
    if (list.dataset.hmReady) return;
    list.dataset.hmReady = "1";
    // Rows post their key in document order, so the new order is read from
    // the DOM when the form is submitted: nothing to renumber.
    window.Sortable.create(list, { draggable: "[data-hm-field-row]", handle: ".hm-fieldrow__handle", animation: 120 });
  });
}

// ---- form builder: collapsible rows, drag to reorder -----------------------
// Fields post as f.N.*, and the server reads them by N, so after a drag every
// name, id and Move/Remove value is renumbered to the DOM order.
function renumberFormFields(list) {
  list.querySelectorAll(":scope > [data-hm-field]").forEach((li, i) => {
    li.querySelectorAll("*").forEach((el) => {
      for (const a of ["name", "id", "for", "aria-describedby", "aria-controls", "aria-activedescendant", "popovertarget"]) {
        const v = el.getAttribute(a);
        if (v === null) continue;
        const nv = renumberRef(v, "f", i);
        if (nv !== v) el.setAttribute(a, nv);
      }
      if (el.getAttribute("name") === "_op") el.value = renumberOp(el.value, i);
    });
    li.querySelectorAll("[aria-label]").forEach((el) => {
      const v = el.getAttribute("aria-label");
      if (/ field \d+$/.test(v)) el.setAttribute("aria-label", v.replace(/\d+$/, String(i + 1)));
    });
    li.querySelectorAll(".hm-visually-hidden").forEach((el) => {
      if (/^ field \d+/.test(el.textContent)) el.textContent = ` field ${i + 1}`;
    });
  });
}

function initFormFields(root) {
  root.querySelectorAll("[data-hm-fieldlist]").forEach((list) => {
    if (list.dataset.hmReady) return;
    list.dataset.hmReady = "1";
    list.querySelectorAll("[data-hm-handle]").forEach((h) => { h.hidden = false; });
    // A closed <details> hides its invalid input and no browser opens it, so
    // validation would fail silently. Open those rows before the browser validates.
    list.closest("form")?.addEventListener("click", (evt) => {
      const b = evt.target.closest("button[type=submit]");
      if (!b || b.formNoValidate) return;
      list.querySelectorAll("details:not([open])").forEach((d) => { if (d.querySelector(":invalid")) d.open = true; });
    }, true);
    list.addEventListener("change", (evt) => {
      if (evt.target.matches('select[name$=".type"]')) evt.target.closest("[data-hm-field]").dataset.type = evt.target.value;
    });
    list.addEventListener("input", (evt) => {
      if (!evt.target.matches('input[name$=".label"]')) return;
      const t = evt.target.closest("[data-hm-field]").querySelector("[data-hm-field-label]");
      if (t) t.textContent = evt.target.value.trim() || "Untitled field";
    });
    if (window.Sortable) {
      window.Sortable.create(list, { draggable: "[data-hm-field]", handle: "[data-hm-handle]", animation: 120, onEnd: () => renumberFormFields(list) });
    }
  });
}

// ---- media grid: the bulk bar's Clear button is a reset button; the unticked boxes
// do not fire change, so tell the shared bulk handler to refresh the count.
document.addEventListener("reset", (evt) => {
  const form = evt.target;
  if (!form.matches?.("[data-hm-bulk-toolbar]")) return;
  setTimeout(() => {
    const first = document.querySelector(`[data-hm-row-select][form="${form.id}"]`);
    first?.dispatchEvent(new Event("change", { bubbles: true }));
    first?.focus();
  }, 0);
});

// ---- media picker ---------------------------------------------------------
let pickerTarget = null;

function pickerDialog() {
  let dlg = document.getElementById("hm-media-picker");
  if (dlg) return dlg;
  dlg = document.createElement("dialog");
  dlg.id = "hm-media-picker";
  dlg.className = "hm-dialog";
  dlg.setAttribute("aria-labelledby", "hm-media-picker-title");
  dlg.innerHTML =
    '<div class="hm-dialog__panel"><h2 class="hm-dialog__title" id="hm-media-picker-title">Choose an image</h2>' +
    '<div class="hm-dialog__body" id="hm-media-picker-body" aria-live="polite"></div>' +
    '<div class="hm-dialog__foot"><button class="hm-btn" type="button" data-hm-picker-close>Cancel</button></div></div>';
  document.body.appendChild(dlg);
  return dlg;
}

// The search box is where a keyboard user starts: on open, and again after a
// search or page swap replaced it.
function focusPickerSearch() {
  const q = document.getElementById("picker-q");
  if (!q) return;
  q.focus();
  q.setSelectionRange(q.value.length, q.value.length);
}

// The tile whose address is already in the field shows as chosen.
function markPicked(root) {
  root.querySelectorAll("[data-hm-pick]").forEach((b) => b.setAttribute("aria-pressed", String(!!pickerTarget && b.dataset.hmPick === pickerTarget.value)));
}
document.addEventListener("htmx:afterSettle", (evt) => { if (evt.target.closest?.("#hm-media-picker")) markPicked(evt.target); });

async function openPicker(opener) {
  const dlg = pickerDialog();
  pickerTarget = document.getElementById(opener.dataset.hmMediaPicker);
  const body = dlg.querySelector("#hm-media-picker-body");
  body.innerHTML = '<div class="hm-media-skeleton" role="status"><span class="hm-visually-hidden">Loading images…</span>' + "<i></i>".repeat(8) + "</div>";
  dlg.showModal();
  try {
    const res = await fetch(adminBase() + "/media/picker", { headers: { "HX-Request": "true", "HX-Target": "hm-media-picker-body" }, credentials: "same-origin" });
    if (!res.ok) throw new Error(String(res.status));
    body.innerHTML = await res.text();
    window.htmx?.process(body);
    initUpload(body);
    markPicked(body);
    focusPickerSearch();
  } catch {
    body.textContent = "The library could not be loaded. Close this and try again.";
  }
}

document.addEventListener("click", (evt) => {
  const opener = evt.target.closest?.("[data-hm-media-picker]");
  if (opener) {
    evt.preventDefault();
    openPicker(opener);
    return;
  }
  const pick = evt.target.closest?.("[data-hm-pick]");
  if (pick && pickerTarget) {
    pickerTarget.value = pick.dataset.hmPick;
    pickerTarget.dispatchEvent(new Event("input", { bubbles: true }));
    pickerTarget.dispatchEvent(new Event("change", { bubbles: true }));
    const img = pickerTarget.closest(".hm-field")?.querySelector("img.hm-sf-image");
    if (img) { img.src = pick.dataset.hmPick; img.hidden = false; }
    document.getElementById("hm-media-picker")?.close();
    pickerTarget.focus();
    return;
  }
  if (evt.target.closest?.("[data-hm-picker-close]")) document.getElementById("hm-media-picker")?.close();
});

document.addEventListener("htmx:afterSettle", (evt) => {
  if (evt.target.closest?.("#hm-media-picker-body") || evt.target.id === "hm-picker") focusPickerSearch();
});

// The parent picker's search box is only useful with htmx; without JS the
// select lists the current parent and the first matches. Enter in it must not
// save the form.
function initParentSearch(root) {
  root.querySelectorAll("[data-hm-noenter]").forEach((q) => {
    if (q.dataset.hmReady) return;
    q.dataset.hmReady = "1";
    q.hidden = !window.htmx;
    q.addEventListener("keydown", (e) => { if (e.key === "Enter") e.preventDefault(); });
  });
}

function initPickers(root) {
  initParentSearch(root);
  if (typeof HTMLDialogElement === "undefined") return;
  root.querySelectorAll("[data-hm-media-picker]").forEach((b) => { b.hidden = false; });
  root.querySelectorAll("[data-hm-photo-address]").forEach((w) => { w.hidden = true; });
}

// ---- upload: choose or drop files, list them, progress ---------------------
// Choosing or dropping only fills the list; the person presses Upload. A file
// the input does not accept, or that is too large, shows its reason and is
// left out of the upload.
const sizeText = (n) => (n >= 1048576 ? `${(n / 1048576).toFixed(1)} MB` : `${Math.max(1, Math.round(n / 1024))} KB`);

function rejection(file, accept, maxMB) {
  const types = accept.split(",").map((a) => a.trim().toLowerCase()).filter(Boolean);
  const name = file.name.toLowerCase();
  const typeOK = !types.length || types.some((a) => (a.startsWith(".") ? name.endsWith(a) : a.endsWith("/*") ? file.type.startsWith(a.slice(0, -1)) : file.type === a));
  if (!typeOK) return "This file type is not allowed.";
  if (maxMB > 0 && file.size > maxMB * 1048576) return `Too large (the limit is ${maxMB} MB).`;
  return "";
}

function fileRow(file, error) {
  const li = document.createElement("li");
  li.className = "hm-dropzone__file";
  const name = document.createElement("span");
  name.textContent = file.name;
  const note = document.createElement("span");
  note.className = error ? "hm-field__error" : "hm-muted";
  note.textContent = error || sizeText(file.size);
  li.append(name, note);
  return li;
}

function initUpload(root) {
  root.querySelectorAll("[data-hm-dropzone]").forEach((form) => {
    if (form.dataset.hmReady) return;
    form.dataset.hmReady = "1";
    const area = form.querySelector("[data-hm-dropzone-area]");
    const input = form.querySelector('input[type="file"]');
    const list = form.querySelector("[data-hm-dropzone-files]");
    const bar = form.querySelector("[data-hm-progress]");
    if (!area || !input) return;
    const accept = input.getAttribute("accept") ?? "";
    const maxMB = Number(input.dataset.hmMaxMb) || 0;
    const text = form.querySelector("[data-hm-progress-text]");
    // The dialog's Upload button sits in its footer, outside the form, tied to it by form="id".
    const submit = form.querySelector('button[type="submit"]') ?? (form.id ? document.querySelector(`button[form="${form.id}"]`) : null);
    const label = submit?.querySelector("span") ?? submit;
    const idle = label?.textContent ?? "";
    const tpl = (sel) => form.querySelector("[data-hm-dropzone-tpl]").content.querySelector(sel).cloneNode(true);
    let rows = [];

    // focusAt: after a Remove, focus moves to the row now at that place (or the one before it), else the file input.
    const render = (chosen, focusAt) => {
      const keep = new DataTransfer();
      const failed = [];
      list?.replaceChildren();
      rows = [];
      for (const f of chosen) {
        const err = rejection(f, accept, maxMB);
        const li = fileRow(f, err);
        if (err) {
          failed.push(li);
        } else {
          keep.items.add(f);
          const remove = tpl("[data-hm-remove]");
          remove.setAttribute("aria-label", `Remove ${f.name}`);
          const at = keep.files.length - 1;
          remove.addEventListener("click", () => render([...keep.files].filter((_, i) => i !== at), at));
          li.append(remove);
          rows.push(li);
        }
        list?.append(li);
      }
      input.files = keep.files;
      if (focusAt !== undefined) {
        const buttons = list?.querySelectorAll("[data-hm-remove]") ?? [];
        (buttons[removeFocusIndex(focusAt, buttons.length)] ?? input).focus();
      }
      if (label) label.textContent = keep.files.length ? `Upload ${keep.files.length} ${keep.files.length === 1 ? "file" : "files"}` : idle;
    };

    ["dragenter", "dragover"].forEach((t) => area.addEventListener(t, (e) => { e.preventDefault(); area.classList.add("hm-dropzone--over"); }));
    ["dragleave", "drop"].forEach((t) => area.addEventListener(t, (e) => { e.preventDefault(); area.classList.remove("hm-dropzone--over"); }));
    area.addEventListener("drop", (e) => {
      if (!e.dataTransfer?.files?.length) return;
      render([...input.files, ...e.dataTransfer.files]);
    });
    input.addEventListener("change", () => render([...input.files]));

    const names = () => {
      const f = [...(input.files ?? [])];
      return f.length === 1 ? f[0].name : `${f.length} files`;
    };
    const status = (msg) => rows.forEach((li) => {
      let s = li.querySelector("[data-hm-file-status]");
      if (!s) { s = document.createElement("span"); s.className = "hm-muted"; s.dataset.hmFileStatus = ""; li.querySelector("button")?.before(s); }
      s.textContent = msg;
    });
    form.addEventListener("htmx:xhr:loadstart", () => {
      form.querySelector("[data-hm-upload-failed]")?.remove();
      if (bar) { bar.hidden = false; bar.value = 0; }
      if (submit) { submit.disabled = true; submit.setAttribute("aria-disabled", "true"); }
      if (text) text.textContent = `Uploading ${names()}…`;
      status("Waiting…");
    });
    form.addEventListener("htmx:xhr:progress", (e) => {
      if (!e.detail.lengthComputable) return;
      const pct = Math.round((e.detail.loaded / e.detail.total) * 100);
      if (bar) bar.value = pct;
      if (text) text.textContent = `Uploading ${names()}… ${pct}%`;
      status(`${pct}%`);
    });
    // htmx:xhr:loadend carries no xhr, so the outcome comes from htmx:afterRequest.
    form.addEventListener("htmx:xhr:loadend", () => {
      if (submit) { submit.disabled = false; submit.removeAttribute("aria-disabled"); }
    });
    form.addEventListener("htmx:afterRequest", (e) => {
      if (e.target !== form || e.detail.successful) return;
      if (bar) bar.hidden = true;
      if (text) text.textContent = "";
      status("");
      list?.querySelectorAll("[data-hm-upload-failed]").forEach((old) => old.remove());
      const li = document.createElement("li");
      li.className = "hm-dropzone__file";
      li.dataset.hmUploadFailed = "";
      const msg = document.createElement("span");
      msg.className = "hm-field__error";
      msg.textContent = "The upload failed.";
      const retry = tpl("[data-hm-retry]");
      retry.addEventListener("click", () => form.requestSubmit());
      li.append(msg, retry);
      list?.append(li);
    });
  });
}

// ---- bulk delete confirmation ----------------------------------------------
document.addEventListener("click", (evt) => {
  const btn = evt.target.closest?.("[data-hm-bulk-confirm]");
  if (!btn) return;
  const ids = [...btn.closest(".hm-tablecard").querySelectorAll("[data-hm-row-select]:checked")].map((c) => c.value);
  if (ids.length === 0) return;
  const opened = openConfirm({
    action: btn.getAttribute("formaction"), message: btn.dataset.hmBulkConfirm.replace("{n}", String(ids.length)),
    okLabel: btn.dataset.hmConfirmLabel || "Confirm", title: btn.dataset.hmConfirmTitle, danger: true, opener: btn,
    fields: [["action", btn.value], ["confirmed", "1"], ["back", location.pathname + location.search], ...ids.map((id) => ["id", id])],
  });
  if (opened) evt.preventDefault();
});

// <form data-hm-autosubmit>: a change in any of its fields submits it. Arrow keys move a radio
// group's selection and fire change too (WCAG 3.2.2), so a radio submits only on a pointer click
// (detail > 0; a keyboard-driven click has detail 0). Keyboard users press Apply or Enter.
// content.js and aia/page.js carry the same copy; the first one loaded binds, the other skips.
const autosubmit = (evt) => evt.target.closest?.("form[data-hm-autosubmit]")?.requestSubmit();
if (!document.hmAutosubmit) {
  document.hmAutosubmit = true;
  document.addEventListener("change", (evt) => {
    if (evt.target.type !== "radio") autosubmit(evt);
  });
  document.addEventListener("click", (evt) => {
    if (evt.target.type === "radio" && evt.detail > 0) autosubmit(evt);
  });
}

function init(root) {
  initFields(root);
  initFormFields(root);
  initPickers(root);
  initUpload(root);
}
document.addEventListener("DOMContentLoaded", () => init(document));
document.addEventListener("htmx:afterSettle", (evt) => init(evt.target));
export { init };
