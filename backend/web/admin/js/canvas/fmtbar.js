// The formatting bar over a heading/text being edited on the canvas, and its link dialog: a lazy chunk,
// loaded on the first `selection` report. The bar is a sibling of the outline marks inside the stage
// (drawMarks rebuilds the marks, never this). Its buttons keep focus in the frame (pointerdown is
// prevented); only the link field takes focus, and the frame keeps the edit and its saved selection
// meanwhile. Format commands go out through canvas.format; nothing here reads or writes the page text.
import { h } from "@hm/builder/dom";
import { validateLinkURL } from "@hm/editor/links";

const GAP = 6;
// [command, label, mark it shows as pressed, accessible name]
const BUTTONS = [
  ["bold", "B", "bold", "Bold"],
  ["italic", "I", "italic", "Italic"],
  ["underline", "U", "underline", "Underline"],
  ["strike", "S", "strike", "Strikethrough"],
  ["code", "</>", "code", "Code"],
  ["link", "Link", "link", "Link"],
  ["clear", "Clear", null, "Clear formatting"],
];
/** A heading takes only these; a text takes them all. */
export const HEADING_CMDS = ["bold", "italic", "link", "clear"];
export const cmdsFor = (kind) => (kind === "heading" ? HEADING_CMDS : BUTTONS.map(([cmd]) => cmd));

/**
 * Where the bar goes: above the edited element's box (document space, times the preview scale), below it when
 * there is no room above in the visible part of the stage (view: {top, height}, stage space), and kept inside
 * the view and the stage's right edge. It follows the element, not the caret, so it never covers the line
 * being typed. Pure.
 */
export function placeBar({ rect, scale, barW, barH, stageW, view = { top: 0, height: Infinity } }) {
  const above = rect.y * scale - barH - GAP;
  let top = above >= view.top ? above : (rect.y + rect.h) * scale + GAP;
  top = Math.max(view.top, Math.min(top, view.top + view.height - barH));
  return { top, left: Math.max(0, Math.min(rect.x * scale, stageW - barW)) };
}

/** What the link dialog does with the typed address: {href} to apply, or {error} to show. Pure. */
export function linkInput(raw) {
  const v = validateLinkURL(raw);
  return v.ok ? { href: v.href } : { error: v.error };
}

/** Mounts the bar in canvas.stage. pages() lists the addresses the link field offers (pages, then #anchors). Returns {show(selection), hide(), link()}. */
export function mountBar(canvas, { announce = () => {}, pages = () => [] } = {}) {
  let sel = null; // the latest selection report {id, rect, marks, el?, link?}
  let applied = 0; // when the link dialog last applied (ms), until the frame reports the result
  const frame = () => canvas.stage.querySelector("iframe:not(.hm-pe__frame--idle)");
  // Focusing a frame that already has focus blurs its editor in Chromium, which ends the edit: only after the link field had it.
  const refocus = () => {
    const f = frame();
    if (f && document.activeElement !== f) f.focus();
  };
  const send = (cmd, opts) => sel && canvas.format(sel.id, cmd, opts);

  const buttons = BUTTONS.map(([cmd, text, mark, name]) =>
    h("button", { type: "button", class: "hm-btn hm-btn--sm", "data-cmd": cmd, "aria-label": name, "aria-pressed": mark ? "false" : null, "aria-expanded": cmd === "link" ? "false" : null, "aria-controls": cmd === "link" ? "hm-fmt-link" : null, text }),
  );
  const linkBtn = buttons[BUTTONS.findIndex(([cmd]) => cmd === "link")];
  const href = h("input", { type: "text", id: "hm-fmt-href", class: "hm-field__control", inputmode: "url", autocomplete: "off", "aria-describedby": "hm-fmt-err", placeholder: "https:// or /page" });
  const choices = h("datalist", { id: "hm-fmt-pages" });
  href.setAttribute("list", "hm-fmt-pages");
  const tab = h("input", { type: "checkbox", id: "hm-fmt-tab", class: "hm-field__box", role: "switch" });
  const error = h("p", { class: "hm-field__error", id: "hm-fmt-err", role: "alert", hidden: true });
  const form = h("form", { class: "hm-pe__fmt-link", id: "hm-fmt-link", hidden: true, "aria-label": "Link" },
    h("label", { class: "hm-pe__fmt-addr", for: "hm-fmt-href", text: "Link address" }), href, choices,
    h("label", { class: "hm-field__check hm-switch hm-pe__fmt-tab", for: "hm-fmt-tab" }, tab, h("span", { text: "Open in new tab" })),
    h("button", { type: "submit", class: "hm-btn hm-btn--sm hm-btn--primary", text: "Apply" }),
    h("button", { type: "button", class: "hm-btn hm-btn--sm", "data-cmd": "unlink", text: "Remove link" }),
    error,
  );
  const bar = h("div", { class: "hm-pe__fmt", role: "toolbar", "aria-label": "Text formatting", hidden: true }, h("div", { class: "hm-pe__fmt-row" }, ...buttons), form);

  const place = () => {
    if (!sel || bar.hidden) return;
    const scroller = canvas.stage.parentElement;
    const inset = canvas.inset ?? 0; // the sticky strip covers the top of the scroller
    const view = scroller ? { top: scroller.scrollTop - canvas.stage.offsetTop + inset, height: scroller.clientHeight - inset } : undefined;
    const { top, left } = placeBar({ rect: sel.el ?? sel.rect, scale: canvas.scale, barW: bar.offsetWidth, barH: bar.offsetHeight, stageW: canvas.stage.clientWidth, view });
    bar.style.top = `${top}px`;
    bar.style.left = `${left}px`;
  };
  const closeLink = (back) => {
    form.hidden = true;
    error.hidden = true;
    href.removeAttribute("aria-invalid");
    linkBtn.setAttribute("aria-expanded", "false");
    if (back) refocus();
  };
  const hide = () => {
    sel = null;
    bar.hidden = true;
    closeLink(false);
  };

  // Buttons and the dialog's labels and padding must not take focus: a press on a label would move it to <main>, which the
  // focusout below reads as leaving the dialog. The label still activates its checkbox, which takes the focus itself.
  const keepFocus = (e) => {
    if (e.target !== href && e.target !== tab) e.preventDefault();
  };
  bar.addEventListener("pointerdown", keepFocus);
  bar.addEventListener("mousedown", keepFocus);
  bar.addEventListener("click", (e) => {
    const cmd = e.target.closest?.("button[data-cmd]")?.dataset.cmd;
    if (!cmd) return;
    if (cmd === "link") return openLink();
    if (cmd === "unlink") closeLink(true);
    send(cmd);
    if (cmd !== "unlink") refocus();
  });
  form.addEventListener("submit", (e) => {
    e.preventDefault();
    const r = linkInput(href.value);
    if (r.error) {
      error.textContent = r.error;
      error.hidden = false;
      href.setAttribute("aria-invalid", "true");
      href.focus();
      return;
    }
    send("link", { href: r.href, newTab: tab.checked });
    closeLink(true);
    applied = Date.now(); // announced when the frame reports the link under the selection (a collapsed caret outside a link applies nothing)
  });
  // tabbing out of the dialog into the page ends the edit instead of leaving it open behind the focus
  form.addEventListener("focusout", (e) => {
    const to = e.relatedTarget;
    if (to && !bar.contains(to) && to !== frame()) {
      send("done");
      hide();
    }
  });
  form.addEventListener("keydown", (e) => {
    if (e.key !== "Escape") return;
    e.preventDefault();
    e.stopPropagation();
    closeLink(true);
  });
  // a press anywhere outside the bar and the preview ends the edit (a press in the frame never reaches this document)
  document.addEventListener("pointerdown", (e) => {
    if (sel && !bar.contains(e.target) && !canvas.stage.contains(e.target)) {
      send("done");
      hide();
    }
  }, true);
  if (typeof ResizeObserver !== "undefined") new ResizeObserver(place).observe(canvas.stage);
  canvas.stage.parentElement?.addEventListener("scroll", place, { passive: true }); // the visible part of the stage moved

  function openLink() {
    if (!sel) return;
    error.hidden = true;
    href.removeAttribute("aria-invalid");
    href.value = sel.link?.href ?? ""; // the link under the selection, else nothing carried over from the last one
    choices.replaceChildren(...pages().map((o) => h("option", { value: o })));
    tab.checked = !!sel.link?.blank;
    linkBtn.setAttribute("aria-expanded", "true");
    form.hidden = false;
    place();
    href.focus();
    href.select();
  }
  canvas.stage.append(bar);

  return {
    show(s) {
      sel = s;
      const shows = cmdsFor(s.kind);
      buttons.forEach((b, i) => (b.hidden = !shows.includes(BUTTONS[i][0])));
      bar.hidden = false;
      buttons.forEach((b, i) => BUTTONS[i][2] && b.setAttribute("aria-pressed", String(s.marks.includes(BUTTONS[i][2]))));
      place();
      if (applied && Date.now() - applied < 1500 && s.marks.includes("link")) announce("Link applied");
      applied = 0;
    },
    hide,
    link: openLink,
  };
}
