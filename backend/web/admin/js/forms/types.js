// The field types the designer offers: label, hint, palette group, icon key and the family a
// field can be switched within. The keys are form.FieldType* (backend/internal/form/model.go);
// icon keys are names of the server's icon set. Pure data, no imports.

export const GROUP_ORDER = ["fields", "choices", "layout", "advanced"];

export const TYPES = {
  text: { label: "Short text", hint: "One line: a name, a subject", group: "fields", icon: "pen", family: "input" },
  email: { label: "Email", hint: "An email address, checked", group: "fields", icon: "mail", family: "input" },
  phone: { label: "Phone", hint: "A phone number", group: "fields", icon: "smartphone", family: "input" },
  textarea: { label: "Long text", hint: "Several lines: a message", group: "fields", icon: "file-text", family: "input" },
  number: { label: "Number", hint: "A number, with an optional range", group: "fields", icon: "list-ordered", family: "input" },
  url: { label: "Web address", hint: "A link starting with http", group: "fields", icon: "link", family: "input" },
  date: { label: "Date", hint: "A calendar date", group: "fields", icon: "calendar", family: "input" },
  select: { label: "Dropdown", hint: "Pick one from a list", group: "choices", icon: "chevron-down", family: "choice" },
  radio: { label: "Single choice", hint: "Pick one, all options visible", group: "choices", icon: "list", family: "choice" },
  checkbox: { label: "Multiple choice", hint: "Pick any number of options", group: "choices", icon: "check-circle", family: "choice" },
  heading: { label: "Heading", hint: "A title between fields", group: "layout", icon: "heading", family: "text-block" },
  paragraph: { label: "Paragraph", hint: "A few lines of explanation", group: "layout", icon: "align-left", family: "text-block" },
  divider: { label: "Divider", hint: "A line between sections", group: "layout", icon: "minus", family: "divider" },
  hidden: { label: "Hidden value", hint: "Filled in for you, not shown", group: "advanced", icon: "eye-off", family: "hidden" },
  consent: { label: "Consent", hint: "A box the visitor must tick", group: "advanced", icon: "shield-check", family: "consent" },
};

/** The types a field of this type can be switched to without losing its answer shape (itself included). */
export const compatible = (type) => Object.keys(TYPES).filter((t) => TYPES[t].family === TYPES[type].family && t !== "file");

/** Display-only types: no answer, no name. */
export const isLayout = (type) => TYPES[type]?.group === "layout";
