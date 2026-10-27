// hm:edit from a theme component's field cannot be applied by path (fields.js decodes through the server). The screen
// shows the stored content on the canvas again and sends the person here. A theme component's field was edited or opened on the canvas (the v1 messages hm:edit, hm:edit-text, hm:rich-open),
// which the builder cannot apply by path: it selects the component, opens its inspector and moves focus to the field
// there. A lazy chunk: the section form arrives from the server after the panel draws, so this waits for the field.

/** The form field name of a component's content path (the outline form and the inspector post under the same prefix). */
export const fieldName = (id, path) => `n.${id}.c.${path}`;

const TRIES = 50; // 5 s at 100 ms: the form is fetched after the panel draws

/** Opens the Content tab of the inspector `root` and focuses the field at `path` of component `id` (the rich editor in front of its textarea) once the form has it. */
export function focusField(root, id, path, announce) {
  root.querySelector('[data-tab="content"]')?.click();
  const name = fieldName(id, path);
  let left = TRIES;
  const timer = setInterval(() => {
    const field = [...root.querySelectorAll("[name]")].find((el) => el.getAttribute("name") === name);
    if (!field && --left > 0) return;
    clearInterval(timer);
    if (!field) return announce("Edit this field in the inspector");
    const target = field.closest(".hm-field")?.querySelector('[contenteditable="true"]') ?? field;
    target.scrollIntoView?.({ block: "nearest" });
    target.focus();
    announce("Edit this field in the inspector");
  }, 100);
}
