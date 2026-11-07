// The Settings tab's panel: binds the server-rendered [data-hm-fs] controls to the designer's store.
// No store, saver or history of its own: every edit is a setSettings op through the screen's commit,
// so Undo, Redo, Ctrl+S, the leave guard and the conflict state are the Build tab's. Lazy chunk of screen.js.
import { mountTagInput } from "@hm/ui/taginput";
import { confirmationPreview, controlValue, fillTokens, joinRecipients, sameList, sampleData, settingsPatch, whenMatches } from "@hm/forms/settingslogic";

const RATE_MESSAGE = "Enter a number from 0 to 1000";
const MAX_RATE = 1000;

/**
 * mountSettings(root, {getDoc, commit, boot, save, ago}) -> {sync(), showErrors(map)}.
 * commit(ops, opts) runs ops as one history entry (false when refused); sync() redraws the controls from
 * the document after every change, Undo and Redo included; save() saves now; ago(ms) is "4 min ago".
 */
export function mountSettings(root, { getDoc, commit, boot, save, ago }) {
  const doc = getDoc;
  const ui = { after: doc().settings?.success_redirect_url ? "redirect" : "message" };
  const tags = new Map();
  const local = new Map(); // keys whose last typed value was refused: the message stays until it is fixed

  const slot = (key) => root.querySelector(`[data-hm-fs-err="settings.${key}"]`);
  const controlOf = (key) => root.querySelector(`[data-hm-fs="${key}"]`);
  const focusEl = (key) => {
    const c = controlOf(key);
    return c?.matches("[data-hm-ti]") ? c.querySelector("input") : c;
  };

  function setError(key, message) {
    const el = slot(key);
    const control = focusEl(key);
    if (!el || !control) return;
    el.id ||= `fs-err-${key}`;
    const ids = (control.getAttribute("aria-describedby") ?? "").split(" ").filter((x) => x && x !== el.id);
    el.textContent = message;
    el.hidden = !message;
    if (message) {
      control.setAttribute("aria-invalid", "true");
      control.setAttribute("aria-describedby", [...ids, el.id].join(" "));
    } else {
      control.removeAttribute("aria-invalid");
      if (ids.length) control.setAttribute("aria-describedby", ids.join(" "));
      else control.removeAttribute("aria-describedby");
    }
  }

  function edit(key, value, coalesce = true) {
    const patch = settingsPatch(key, value);
    if (patch === undefined || (key === "rate_limit_per_hour" && patch > MAX_RATE)) {
      local.set(key, true);
      setError(key, key === "rate_limit_per_hour" ? RATE_MESSAGE : "That value is not valid.");
      return;
    }
    if (local.delete(key)) setError(key, "");
    const same = JSON.stringify(patch ?? null) === JSON.stringify(doc().settings?.[key] ?? null);
    if (same) return;
    commit([{ op: "setSettings", key, value: patch }], { label: "Change settings", ...(coalesce ? { coalesce: `settings:${key}` } : {}) });
  }

  // ----- text, number, select and switch controls -----
  for (const el of root.querySelectorAll("input[data-hm-fs], textarea[data-hm-fs], select[data-hm-fs]")) {
    const key = el.dataset.hmFs;
    if (el.type === "checkbox") el.addEventListener("change", () => edit(key, el.checked, false));
    else if (el.tagName === "SELECT") el.addEventListener("change", () => edit(key, el.value, false));
    else el.addEventListener("input", () => edit(key, el.value));
  }

  // ----- recipients -----
  for (const el of root.querySelectorAll("[data-hm-ti][data-hm-fs]")) {
    const key = el.dataset.hmFs;
    tags.set(key, mountTagInput(el, { list: controlValue(key, doc().settings), onChange: (list) => edit(key, joinRecipients(list), false) }));
  }

  // ----- segmented choices: "after" is a screen choice, the others are settings -----
  for (const group of root.querySelectorAll("[data-hm-fs-seg]")) {
    const key = group.dataset.hmFsSeg;
    for (const b of group.querySelectorAll("button[data-value]")) {
      b.addEventListener("click", () => {
        if (key === "after") {
          ui.after = b.dataset.value;
          if (ui.after === "message") edit("success_redirect_url", "", false);
          sync();
          return;
        }
        edit(key, b.dataset.value, false);
      });
    }
  }

  // ----- subject tokens go in at the cursor -----
  for (const b of root.querySelectorAll("[data-hm-fs-token]")) {
    b.addEventListener("click", () => {
      const input = document.getElementById(b.dataset.hmFsTarget);
      const at = input.selectionStart ?? input.value.length;
      const end = input.selectionEnd ?? at;
      const token = b.dataset.hmFsToken;
      input.value = input.value.slice(0, at) + token + input.value.slice(end);
      input.focus();
      input.setSelectionRange(at + token.length, at + token.length);
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
  }

  // ----- danger zone -----
  const archive = root.querySelector("[data-hm-fs-archive]");
  archive.addEventListener("click", () => {
    const archived = doc().status === "archived";
    if (commit([{ op: "setMeta", key: "status", value: archived ? "active" : "archived" }], { label: archived ? "Restore form" : "Archive form" })) save();
  });

  // ----- webhooks: one row per hook, cloned from the page's template -----
  const hookList = root.querySelector("[data-hm-fs-hooks]");
  const hookTpl = root.querySelector("template[data-hm-fs-hook]");
  const hooks = boot.webhooks ?? [];
  root.querySelector("[data-hm-fs-nohooks]").hidden = hooks.length > 0;
  for (const w of hooks) {
    const li = hookTpl.content.firstElementChild.cloneNode(true);
    li.querySelector(".hm-fs-hook__name").textContent = w.name;
    const dot = li.querySelector(".hm-fs-hook__dot");
    dot.dataset.state = !w.active ? "" : w.lastStatus === "delivered" ? "ok" : w.lastStatus === "failed" ? "bad" : "";
    const last = !w.active ? "Off" : w.lastStatus ? `${w.lastStatus[0].toUpperCase()}${w.lastStatus.slice(1)} · ${ago(Date.now() - Date.parse(w.lastAt))}` : "No deliveries yet";
    li.querySelector(".hm-fs-hook__last").textContent = last;
    hookList.append(li);
  }

  // ----- redrawing from the document -----
  const subjectPreview = root.querySelector("[data-hm-fs-subject-preview]");
  const mail = {
    to: root.querySelector("[data-hm-fs-mail-to]"),
    subject: root.querySelector("[data-hm-fs-mail-subject]"),
    body: root.querySelector("[data-hm-fs-mail-body]"),
  };

  function sync() {
    const d = doc();
    const settings = d.settings ?? {};
    if (settings.success_redirect_url) ui.after = "redirect";
    for (const el of root.querySelectorAll("input[data-hm-fs], textarea[data-hm-fs], select[data-hm-fs]")) {
      const key = el.dataset.hmFs;
      if (el === document.activeElement || local.has(key)) continue;
      const v = controlValue(key, settings);
      if (el.type === "checkbox") el.checked = v;
      else if (el.value !== String(v)) el.value = v;
    }
    for (const [key, t] of tags) {
      const list = controlValue(key, settings);
      if (!sameList(t.get(), list)) t.set(list);
    }
    for (const group of root.querySelectorAll("[data-hm-fs-seg]")) {
      const key = group.dataset.hmFsSeg;
      const now = key === "after" ? ui.after : String(controlValue(key, settings));
      for (const b of group.querySelectorAll("button[data-value]")) b.setAttribute("aria-pressed", String(b.dataset.value === now));
    }
    for (const el of root.querySelectorAll("[data-hm-fs-when]")) el.hidden = !whenMatches(el.dataset.hmFsWhen, settings, ui);
    archive.querySelector("span").textContent = d.status === "archived" ? "Restore" : "Archive";

    const p = confirmationPreview(d, boot.sample);
    mail.to.textContent = p.to;
    mail.subject.textContent = p.subject;
    mail.body.textContent = p.message;
    if (subjectPreview) {
      const filled = fillTokens(settings.notify_subject, d.fields, sampleData(d.fields, boot.sample));
      subjectPreview.textContent = filled ? `Looks like: ${filled}` : "";
      subjectPreview.hidden = !filled;
    }
  }

  function showErrors(map) {
    for (const el of root.querySelectorAll("[data-hm-fs-err]")) {
      const key = el.dataset.hmFsErr.replace(/^settings\./, "");
      if (!local.has(key)) setError(key, map[el.dataset.hmFsErr] ?? "");
    }
  }

  sync();
  return { sync, showErrors };
}
