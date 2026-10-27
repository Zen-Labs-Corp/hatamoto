// The entity (Details) form: the save indicator beside Save, and the parent chip that follows the picker.
// The indicator is the shared hm/savestate: only data-state and the text change.
const SAVED = { unsaved: "Unsaved changes", saving: "Saving…" };

/** The label of the option a select has chosen (the picker's "Services", not its value). */
export function selectedLabel(select) {
  const opt = select?.selectedOptions?.[0] ?? select?.options?.[select.selectedIndex];
  return opt ? opt.textContent.trim() : "";
}

/** Wires one form. */
export function mountEntityForm(form) {
  const state = form.querySelector("[data-hm-ef-status] .hm-savestate");
  const text = state?.querySelector(".hm-savestate__text");
  const chip = form.querySelector("[data-hm-parent-chip] .hm-badge");
  const paint = (name) => {
    if (!state || !text) return;
    state.dataset.state = name;
    text.textContent = SAVED[name];
  };
  const edited = (e) => {
    // the parent search box only filters the picker: it is not an edit
    if (e.target?.name === "parent_q") return;
    paint("unsaved");
    if (e.target?.id === "parent" && chip) chip.textContent = selectedLabel(e.target);
  };
  form.addEventListener("input", edited);
  form.addEventListener("change", edited);
  form.addEventListener("submit", () => paint("saving"));
  // Back restores a page frozen mid-save: the form is editable again and unsaved
  globalThis.addEventListener?.("pageshow", (e) => e.persisted && paint("unsaved"));
  return { paint };
}

const form = globalThis.document?.querySelector("form.hm-entityform");
if (form) mountEntityForm(form);
