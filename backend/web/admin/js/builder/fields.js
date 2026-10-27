// The Content tab of a component node: the theme section's own form, rendered and
// decoded by the server (POST /builder/component, one decoder shared with the no-JS
// outline). This module never reads a field name or value: it inserts the HTML the
// server sent, posts the form's FormData back untouched (plus the type, node id and
// the current content) and applies the content the server decoded.
import { h } from "@hm/builder/dom";

const DEBOUNCE_MS = 300;
const LOADING = "Loading the section form…";

/** Warm the section-form chunks (editor kit included) when idle; errors are swallowed, adopt() retries. */
export function prefetch() {
  import("@hm/editor/mount").then((m) => m.loadKit()).catch(() => {});
  import("@hm/repeater").catch(() => {});
  import("@hm/content").catch(() => {});
}

/**
 * mountFields(host, {node, type, getContent, urls, csrf, onContent}) -> {destroy}
 * onContent(content): a decoded content object to commit as the node's content.
 * destroy() sends any pending edit before it leaves, so a selection change loses nothing.
 */
export function mountFields(host, { node, type, getContent, urls, csrf, onContent }) {
  const form = h("form", { class: "hm-bs-fields", novalidate: true, "aria-busy": "true" });
  const status = h("p", { class: "hm-muted", role: "status", text: LOADING });
  host.append(status, form);
  let timer = 0;
  let dirty = false;
  let gone = false;
  let seq = 0;
  let errKey = "";

  const post = async (fields, extra) => {
    const fd = new FormData(fields);
    fd.set("mode", "decode");
    fd.set("type", type);
    fd.set("node", node);
    fd.set("prev", JSON.stringify(getContent() ?? {}));
    if (extra) fd.set(extra.name, extra.value);
    return fetch(urls.component, { method: "POST", credentials: "same-origin", headers: { "X-CSRF-Token": csrf() }, body: fd });
  };

  // Server HTML goes in as given; the widgets (editors, repeaters, pickers) start on it.
  async function adopt(html) {
    const focus = document.activeElement && form.contains(document.activeElement) ? document.activeElement : null;
    const at = focus?.id;
    const range = focus && "selectionStart" in focus ? [focus.selectionStart, focus.selectionEnd] : null;
    form.innerHTML = html;
    window.htmx?.process(form);
    form.dispatchEvent(new CustomEvent("htmx:afterSettle", { bubbles: true, detail: {} }));
    // Loaded on first use, after the settle event above: initialise them explicitly (the inits are idempotent).
    // Whatever loaded still starts; a failed chunk leaves a note and never a form stuck busy.
    const mods = await Promise.allSettled([import("@hm/editor/mount"), import("@hm/repeater"), import("@hm/content")]);
    const [mount, repeater, content] = mods.map((m) => m.value);
    repeater?.init(form);
    content?.init(form);
    let editorsErr = null;
    try {
      await mount?.enhanceEditors(form);
    } catch (err) {
      editorsErr = err;
    }
    form.removeAttribute("aria-busy");
    const failed = mods.find((m) => m.status === "rejected") ?? (editorsErr && { reason: editorsErr });
    if (failed) {
      console.error("hatamoto builder: section form", failed.reason);
      status.textContent = "Part of the section form could not be loaded. Reload the page to try again.";
      if (!status.isConnected) host.prepend(status);
    }
    if (at) {
      const back = Array.from(form.querySelectorAll("[id]")).find((e) => e.id === at);
      if (back) {
        back.focus();
        if (range && "setSelectionRange" in back) back.setSelectionRange(...range);
      }
    }
  }

  async function decode(extra) {
    clearTimeout(timer);
    dirty = false;
    const mine = ++seq;
    try {
      const res = await post(form, extra);
      if (!res.ok) throw new Error(`component ${res.status}`);
      const body = await res.json();
      if (mine !== seq) return;
      const errors = body.errors && Object.keys(body.errors).length ? JSON.stringify(body.errors) : "";
      // a newer keystroke wins: the error markup is swapped in on the next decode instead
      const swap = body.html && !gone && (body.structural || (errors !== errKey && !dirty));
      if (swap) await adopt(body.html);
      if (swap || !body.html || errors === errKey) errKey = errors;
      if (!errors && body.content) onContent(body.content);
      else if (body.structural && body.content) onContent(body.content);
    } catch (err) {
      console.error("hatamoto builder: section form", err);
    }
  }

  const later = () => {
    dirty = true;
    clearTimeout(timer);
    timer = setTimeout(() => decode(), DEBOUNCE_MS);
  };
  form.addEventListener("input", later);
  form.addEventListener("change", later);
  form.addEventListener("focusout", () => dirty && decode());
  // Repeater buttons (add, remove, move) post as the clicked button: its name/value ride along untouched.
  form.addEventListener("submit", (e) => {
    e.preventDefault();
    const b = e.submitter;
    if (b?.name) decode({ name: b.name, value: b.value });
  });

  (async () => {
    try {
      const fd = new FormData();
      fd.set("mode", "form");
      fd.set("type", type);
      fd.set("node", node);
      fd.set("prev", JSON.stringify(getContent() ?? {}));
      const res = await fetch(urls.component, { method: "POST", credentials: "same-origin", headers: { "X-CSRF-Token": csrf() }, body: fd });
      if (!res.ok) throw new Error(`component ${res.status}`);
      const body = await res.json();
      if (gone) return;
      await adopt(body.html ?? "");
      if (status.textContent === LOADING) status.remove();
    } catch (err) {
      status.textContent = "The section form could not be loaded.";
      console.error("hatamoto builder: section form", err);
    }
  })();

  return {
    destroy() {
      const pending = dirty;
      gone = true;
      if (pending) decode();
    },
  };
}
