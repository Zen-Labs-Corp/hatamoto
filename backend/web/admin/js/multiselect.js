// MultiSelect (after Tremor's): a fieldset of checkboxes marked
// data-hm-multiselect becomes a select-like trigger showing the chosen items
// as chips, opening a popover with a search box and the same checkboxes. The
// boxes never leave the form, so what posts is what posted without this
// script; only where they sit changes. components.js places the popover under
// the trigger, focuses the search and returns focus on close.
const MAX_CHIPS = 3;
let seq = 0;

const text = (el) => el.textContent.trim();

function enhance(fs) {
  if (fs.dataset.hmMsel) return;
  const legend = fs.querySelector("legend");
  const list = fs.querySelector("ul");
  if (!legend || !list) return;
  const boxes = [...list.querySelectorAll('input[type="checkbox"]')];
  if (boxes.length === 0) return;
  fs.dataset.hmMsel = "1";
  const n = ++seq;
  const name = text(legend);
  legend.id ||= `msel-${n}-legend`;

  const el = (tag, cls, props = {}) => Object.assign(document.createElement(tag), { className: cls }, props);
  const trigger = el("button", "hm-msel__trigger", { type: "button", id: `msel-${n}-trigger` });
  trigger.setAttribute("popovertarget", `msel-${n}`);
  trigger.setAttribute("aria-haspopup", "dialog");
  const value = el("span", "hm-msel__value", { id: `msel-${n}-value` });
  trigger.setAttribute("aria-labelledby", `${legend.id} ${value.id}`);
  trigger.append(value);
  trigger.insertAdjacentHTML("beforeend", '<svg class="hm-msel__caret" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false"><path d="m6 9 6 6 6-6"/></svg>');

  const pop = el("div", "hm-popover-panel hm-msel__pop", { id: `msel-${n}` });
  pop.setAttribute("popover", "");
  pop.setAttribute("role", "dialog");
  pop.setAttribute("aria-label", `Choose ${name.toLowerCase()}`);
  const filter = el("input", "hm-field__control hm-msel__filter", { type: "search", placeholder: `Search ${name.toLowerCase()}`, autocomplete: "off" });
  filter.setAttribute("aria-label", `Search ${name.toLowerCase()}`);
  const empty = el("p", "hm-msel__empty", { textContent: "No match", hidden: true });
  const foot = el("div", "hm-msel__foot");
  const count = el("span", "hm-msel__count", { role: "status" });
  const clear = el("button", "hm-msel__clear", { type: "button", textContent: "Clear" });
  foot.append(count, clear);
  list.classList.add("hm-msel__list");
  if (boxes.length > 7) pop.append(filter);
  pop.append(list, empty, foot);
  fs.append(trigger, pop);

  const paint = () => {
    const on = boxes.filter((b) => b.checked);
    value.replaceChildren();
    if (on.length === 0) {
      value.append(el("span", "hm-msel__placeholder", { textContent: `Select ${name.toLowerCase()}` }));
    } else {
      for (const b of on.slice(0, MAX_CHIPS)) value.append(el("span", "hm-msel__chip", { textContent: text(b.closest("label")) }));
      if (on.length > MAX_CHIPS) value.append(el("span", "hm-msel__more", { textContent: `+${on.length - MAX_CHIPS}` }));
    }
    count.textContent = on.length ? `${on.length} selected` : "None selected";
    clear.disabled = on.length === 0;
  };
  list.addEventListener("change", paint);
  clear.addEventListener("click", () => {
    for (const b of boxes) b.checked = false;
    fs.dispatchEvent(new Event("change", { bubbles: true })); // the editors' unsaved-changes tracking
    paint();
  });
  filter.addEventListener("input", () => {
    const q = filter.value.trim().toLowerCase();
    let shown = 0;
    for (const li of list.children) {
      const hit = q === "" || text(li).toLowerCase().includes(q);
      li.hidden = !hit;
      shown += hit ? 1 : 0;
    }
    empty.hidden = shown > 0;
  });
  pop.addEventListener("toggle", (evt) => {
    if (evt.newState === "closed" && filter.value) {
      filter.value = "";
      filter.dispatchEvent(new Event("input"));
    }
  });
  paint();
}

function init(root) {
  for (const fs of root.querySelectorAll("fieldset[data-hm-multiselect]")) enhance(fs);
}

if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", () => init(document));
else init(document);
document.addEventListener("htmx:load", (evt) => init(evt.target));
export { init };
