// Form: show or hide fields from their data-hm-logic-* conditions. Mirrors the
// server rule (form.Visible); the server decides again on submit.
for (const form of document.querySelectorAll("form[data-hm-form-logic]")) {
  const rules = [...form.querySelectorAll("[data-hm-logic-field]")];
  const byName = {};
  for (const el of rules) {
    const name = el.querySelector("[name]")?.name;
    if (name) byName[name] = el;
  }
  const values = (name) => {
    const found = form.elements[name];
    const list = !found ? [] : found instanceof RadioNodeList ? [...found] : [found];
    const out = list
      .filter((i) => !/^(checkbox|radio)$/.test(i.type) || i.checked)
      .map((i) => i.value);
    return out.length ? out : [""];
  };
  const test = (op, got, want) => {
    const filled = got.some((v) => v !== "");
    if (op === "filled") return filled;
    if (op === "empty") return !filled;
    if (op === "contains") return got.some((v) => v.includes(want));
    return got.includes(want) === (op === "is");
  };
  const shown = (el, seen) => {
    if (!el) return true;
    if (seen.has(el)) return false;
    seen.add(el);
    const src = el.dataset.hmLogicField;
    // a hidden source counts as empty
    const got = shown(byName[src], seen) ? values(src) : [""];
    return test(el.dataset.hmLogicOp, got, el.dataset.hmLogicValue);
  };
  const run = () => {
    for (const el of rules) {
      const show = shown(el, new Set());
      el.hidden = !show;
      for (const i of el.querySelectorAll("input,select,textarea")) i.disabled = !show;
    }
  };
  form.addEventListener("input", run);
  form.addEventListener("change", run);
  run();
}
