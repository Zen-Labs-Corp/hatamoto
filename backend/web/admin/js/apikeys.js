// API key form: reveal the Select all / Clear all button above the scope
// checkboxes. The server renders it hidden; the boxes stay in the form, so what
// posts is what posted without this script. Select all skips the "*" (Everything)
// scope: full access, including future scopes, is never granted by a shortcut.
function enhance(fs) {
  const btn = fs.querySelector("[data-hm-scopes-all]");
  const boxes = [...fs.querySelectorAll('input[type="checkbox"]:not([value="*"])')];
  if (!btn || boxes.length < 2) return;
  const text = btn.querySelector("span");
  const label = () => {
    text.textContent = boxes.every((b) => b.checked) ? "Clear all" : "Select all";
  };
  btn.addEventListener("click", () => {
    const on = !boxes.every((b) => b.checked);
    for (const b of boxes) b.checked = on;
    label();
  });
  fs.addEventListener("change", label);
  label();
  btn.hidden = false;
}

for (const fs of document.querySelectorAll("fieldset[data-hm-scopes]")) enhance(fs);
