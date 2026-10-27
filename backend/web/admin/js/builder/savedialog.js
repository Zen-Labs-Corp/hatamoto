// The name dialog for "Save as block…" and "Save as template…" (lazy, loaded with reuse.js): a small
// modal with the name field focused, Cancel then the primary action, Enter submits, Esc cancels.
// Focus goes back to the opener (native <dialog>). Buttons are clones of the server templates.
import { h } from "@hm/builder/dom";

export const clone = (name, text, props) => {
  const b = document.querySelector(`template[data-hm-bs-${name}]`)?.content.firstElementChild?.cloneNode(true);
  if (!b) return h("button", { text, ...props });
  b.querySelector("span").textContent = text;
  for (const [k, v] of Object.entries(props)) b.setAttribute(k, v);
  return b;
};

/**
 * ask({title, label, ok, initial, onSubmit}) -> Promise<string | null>: the trimmed name, or null when cancelled.
 * onSubmit(name) is optional and may be async: while it runs the dialog stays open with its buttons off; if it
 * throws, the message shows in the dialog and the person can try again; if it returns, the dialog closes.
 */
export function ask({ title, label, ok, initial = "", onSubmit }) {
  return new Promise((resolve) => {
    let result = null;
    const input = h("input", { type: "text", class: "hm-field__control", id: "hm-bs-save-name", maxlength: "255", autocomplete: "off", value: initial });
    const problem = h("p", { class: "hm-field__error", role: "alert", hidden: true });
    const cancel = clone("act", "Cancel", { type: "button" });
    const save = clone("actp", ok, { type: "submit" });
    const form = h(
      "form",
      { method: "dialog", class: "hm-dialog__panel" },
      h("h2", { class: "hm-dialog__title", id: "hm-bs-save-t", text: title }),
      h("div", { class: "hm-field" }, h("label", { class: "hm-field__label", for: "hm-bs-save-name", text: label }), input, problem),
      h("div", { class: "hm-dialog__foot" }, cancel, save),
    );
    const dlg = h("dialog", { class: "hm-dialog", "aria-labelledby": "hm-bs-save-t", "data-size": "sm" }, form);
    const busy = (b) => {
      for (const el of [input, cancel, save]) el.disabled = b;
      save.setAttribute("aria-busy", String(b));
    };
    const fail = (msg) => {
      problem.textContent = msg;
      problem.hidden = false;
      input.setAttribute("aria-invalid", "true");
      input.focus();
    };
    form.addEventListener("submit", async (e) => {
      e.preventDefault();
      const name = input.value.trim();
      if (!name) return fail("Enter a name.");
      problem.hidden = true;
      input.removeAttribute("aria-invalid");
      busy(true);
      try {
        await onSubmit?.(name);
        result = name;
        dlg.close();
      } catch (err) {
        busy(false);
        fail(err.message);
      }
    });
    cancel.addEventListener("click", () => dlg.close());
    dlg.addEventListener("close", () => {
      dlg.remove();
      resolve(result);
    });
    document.body.append(dlg);
    dlg.showModal();
    input.focus();
  });
}
