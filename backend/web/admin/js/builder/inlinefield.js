// The inspector's editor for the text of a heading or text node: the Tiptap inline region (bold, italic,
// underline, strike, code, link) in place of the plain text field, a lazy chunk mounted over it. It reads and
// writes the same props as the canvas editor (html, with the plain text kept in step) through inlineOps.
import { h, warnNote } from "@hm/builder/dom";
import { canonHTML, inlineOps, textOf } from "@hm/builder/inlinehtml";

const MARKS = ["underline", "code"]; // on top of the region's bold, italic, strike and link
const MAX_TEXT = 20000; // the schema's maxLength of a heading/text value
const LIMIT_MSG = "This text is over the 20,000-character limit. The extra was not kept.";
const esc = (s) => s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");

/** The inner html the editor opens with: the marked-up html when there is some, else the plain text. */
export const innerOf = (node) => node.p?.html || esc(node.p?.text ?? "");

/** The ops for the editor's output (`<p>…</p>`): plain text when it holds no tag, else html, as the canvas does. */
export function opsFor(node, out) {
  const inner = canonHTML(/^<p>([\s\S]*)<\/p>$/.exec(out)?.[1] ?? out);
  return inlineOps(node, /</.test(inner) ? "html" : "text", /</.test(inner) ? inner : textOf(inner));
}

/** Whether `next` differs from `prev` only in its text/html, so the open editor can show it without a redraw. */
export function onlyText(next, prev) {
  if (!prev || next.id !== prev.id || next.t !== prev.t || next.s !== prev.s || next.b !== prev.b || next.c !== prev.c || next.b?.text) return false;
  const { text: _t, html: _h, ...a } = next.p ?? {};
  const { text: _t2, html: _h2, ...b } = prev.p ?? {};
  return JSON.stringify(a) === JSON.stringify(b);
}

/** The undo key of the inspector editor's edits: one per editing session (it moves on when the editor loses focus), so two sessions are two steps. */
export const fieldSession = (id) => {
  let n = 0;
  return { key: () => `inline:${id}:field:${n}`, end: () => void n++ };
};

/**
 * Replaces the text control of `field` (the plain field the inspector drew, which stays when the kit does
 * not load) with the editor. ctx: {commit(ops, opts), label, live() (false once the panel was redrawn), current() (the node as it is now: it
 * may have changed while the kit loaded)}.
 * Resolves to {destroy, sync(next, prev)}, or null when the panel is gone.
 */
export async function upgrade(field, node, { commit, label, live, current, announce }) {
  const kit = await import("@hm/editor/kit");
  const plain = field.querySelector(".hm-field__control");
  if (!live() || !plain) return null;
  node = current?.() ?? node;
  const box = h("div");
  const hadFocus = plain === document.activeElement; // Enter on the tree row focused the plain field before the kit came: the editor takes the focus over
  plain.replaceWith(box);
  const lab = field.querySelector("label");
  lab?.removeAttribute("for"); // a textbox div is not labelable: the label names it (aria-labelledby) and a click on it focuses it
  const session = fieldSession(node.id);
  const note = warnNote(LIMIT_MSG, { role: "status", hidden: true });
  field.append(note);
  box.addEventListener("focusout", (e) => !box.contains(e.relatedTarget) && session.end());
  const ed = kit.mountProseRegion(box, innerOf(node), {
    inline: true,
    marks: MARKS,
    hardBreak: node.t === "text",
    label,
    onChange: (out) => {
      const ops = opsFor(node, out);
      const v = ops.find((o) => o.value && (o.key === "html" || o.key === "text"))?.value ?? "";
      note.hidden = v.length <= MAX_TEXT;
      if (!note.hidden) {
        announce?.(LIMIT_MSG);
        ed.setHTML(innerOf(current?.() ?? node)); // back to the stored text: the doc never gets more than the schema allows
        return;
      }
      commit(ops, { label: "Change Text", coalesce: session.key() });
    },
  });
  if (lab) {
    lab.id ||= `hm-bs-lab-${node.id}`;
    ed.editor.view.dom.setAttribute("aria-labelledby", lab.id);
    lab.addEventListener("click", () => ed.editor.commands.focus());
  }
  if (hadFocus) ed.editor.commands.focus();
  return {
    destroy: ed.destroy,
    sync(next, prev) {
      if (!onlyText(next, prev)) return false;
      node = next;
      ed.setHTML(innerOf(next));
      return true;
    },
  };
}
