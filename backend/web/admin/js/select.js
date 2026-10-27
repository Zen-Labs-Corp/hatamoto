// hm/select: upgrades [data-hm-select] > select to a combobox trigger and a listbox.
// The native select stays in the DOM, visually hidden and out of the tab order, and
// remains the source of truth: a pick sets selectedIndex and dispatches bubbling
// input and change events on it, so htmx triggers, form state and validation work as
// they do for a plain select. Options are read from select.options on every open, so
// options swapped in by htmx show up. Without script the native select is used.
const HINT = " · ";
const TYPEAHEAD_MS = 700;
let uid = 0;

function enhance(select) {
  const root = select.closest("[data-hm-select]");
  if (!root || select.dataset.hmReady) return;
  select.dataset.hmReady = "1";
  const id = select.id || `hm-select-${++uid}`;

  // The accessible name is the label (or aria-label) plus the current value.
  let nameEl = select.labels?.[0] ?? null;
  if (nameEl) nameEl.id ||= `${id}-label`;
  else if (select.getAttribute("aria-label")) {
    nameEl = document.createElement("span");
    nameEl.className = "hm-visually-hidden";
    nameEl.id = `${id}-label`;
    nameEl.textContent = select.getAttribute("aria-label");
    root.append(nameEl);
  }

  const trigger = document.createElement("button");
  trigger.type = "button";
  trigger.id = `${id}-trigger`;
  trigger.className = "hm-field__control hm-select__trigger";
  trigger.setAttribute("role", "combobox");
  trigger.setAttribute("aria-haspopup", "listbox");
  trigger.setAttribute("aria-expanded", "false");
  trigger.setAttribute("aria-controls", `${id}-list`);
  const value = document.createElement("span");
  value.className = "hm-select__value";
  value.id = `${id}-value`;
  trigger.append(value);
  trigger.setAttribute("aria-labelledby", nameEl ? `${nameEl.id} ${value.id}` : value.id);

  const list = document.createElement("ul");
  list.id = `${id}-list`;
  list.className = "hm-select__list";
  list.setAttribute("role", "listbox");
  if (nameEl) list.setAttribute("aria-labelledby", nameEl.id);
  list.hidden = true;

  select.classList.add("hm-visually-hidden");
  select.tabIndex = -1;
  select.setAttribute("aria-hidden", "true");
  select.before(trigger);
  root.append(list);

  const split = (text) => {
    const i = text.indexOf(HINT);
    return i < 0 ? [text, ""] : [text.slice(0, i), text.slice(i + HINT.length)];
  };
  const isOpen = () => !list.hidden;
  const items = () => [...list.children];
  const enabled = (li) => li.getAttribute("aria-disabled") !== "true";

  // Mirror the native state onto the trigger and the label.
  const sync = () => {
    const opt = select.selectedOptions[0];
    const [name] = split(opt?.text ?? "");
    value.textContent = name || "\u00a0";
    if (opt && opt.value === "") value.setAttribute("data-placeholder", "");
    else value.removeAttribute("data-placeholder");
    trigger.disabled = select.matches(":disabled");
    for (const [from, to] of [["aria-invalid", "aria-invalid"], ["aria-describedby", "aria-describedby"]]) {
      const v = select.getAttribute(from);
      if (v === null) trigger.removeAttribute(to);
      else trigger.setAttribute(to, v);
    }
    if (select.required) trigger.setAttribute("aria-required", "true");
    else trigger.removeAttribute("aria-required");
  };

  const setActive = (i) => {
    items().forEach((li, k) => {
      if (k === i) li.setAttribute("data-active", "");
      else li.removeAttribute("data-active");
    });
    const li = items()[i];
    if (li) {
      trigger.setAttribute("aria-activedescendant", li.id);
      li.scrollIntoView({ block: "nearest" });
    } else trigger.removeAttribute("aria-activedescendant");
  };
  const activeIndex = () => items().findIndex((li) => li.hasAttribute("data-active"));
  // Next enabled index from i in direction d (no wrap), or i when there is none.
  const step = (i, d) => {
    const all = items();
    for (let k = i + d; k >= 0 && k < all.length; k += d) if (enabled(all[k])) return k;
    return i;
  };

  // A scrolling ancestor (dialog body) clips the list as well as the viewport.
  const clipBottom = () => {
    let bottom = window.innerHeight;
    for (let el = root.parentElement; el; el = el.parentElement) {
      const o = getComputedStyle(el).overflowY;
      if (o === "auto" || o === "scroll" || o === "hidden") bottom = Math.min(bottom, el.getBoundingClientRect().bottom);
    }
    return bottom;
  };

  const onOutside = (e) => { if (!root.contains(e.target)) close(false); };
  const open = () => {
    if (isOpen() || select.matches(":disabled")) return;
    list.replaceChildren();
    [...select.options].forEach((o, i) => {
      const li = document.createElement("li");
      li.id = `${id}-opt-${i}`;
      li.className = "hm-select__option";
      li.setAttribute("role", "option");
      li.setAttribute("aria-selected", String(o.selected));
      if (o.disabled || o.parentElement?.disabled) li.setAttribute("aria-disabled", "true");
      if (o.value === "") li.setAttribute("data-placeholder", "");
      const [name, hint] = split(o.text);
      const label = document.createElement("span");
      label.textContent = name;
      li.append(label);
      if (hint) {
        const h = document.createElement("span");
        h.className = "hm-select__hint";
        h.textContent = hint;
        li.append(h);
      }
      li.addEventListener("mousedown", (e) => { e.preventDefault(); pick(i); });
      list.append(li);
    });
    list.classList.remove("hm-select__list--up");
    list.hidden = false;
    trigger.setAttribute("aria-expanded", "true");
    const box = list.getBoundingClientRect();
    const tbox = trigger.getBoundingClientRect();
    if (box.bottom > clipBottom() && tbox.top - box.height > 0 && tbox.top > window.innerHeight - tbox.bottom) list.classList.add("hm-select__list--up");
    setActive(Math.max(select.selectedIndex, 0));
    document.addEventListener("pointerdown", onOutside, true);
  };
  function close(refocus = true) {
    if (!isOpen()) return;
    list.hidden = true;
    trigger.setAttribute("aria-expanded", "false");
    trigger.removeAttribute("aria-activedescendant");
    document.removeEventListener("pointerdown", onOutside, true);
    if (refocus) trigger.focus();
  }
  function pick(i, refocus = true) {
    const opt = select.options[i];
    if (!opt || opt.disabled) return;
    const changed = select.selectedIndex !== i;
    select.selectedIndex = i;
    sync();
    close(refocus);
    if (changed) {
      select.dispatchEvent(new Event("input", { bubbles: true }));
      select.dispatchEvent(new Event("change", { bubbles: true }));
    }
  }

  let typed = "";
  let typedTimer = 0;
  const typeahead = (ch) => {
    clearTimeout(typedTimer);
    typedTimer = setTimeout(() => { typed = ""; }, TYPEAHEAD_MS);
    typed += ch.toLowerCase();
    const all = items();
    const from = activeIndex() + (typed.length === 1 ? 1 : 0);
    for (let k = 0; k < all.length; k++) {
      const i = (Math.max(from, 0) + k) % all.length;
      if (enabled(all[i]) && all[i].firstChild.textContent.toLowerCase().startsWith(typed)) { setActive(i); return; }
    }
  };

  trigger.addEventListener("click", () => (isOpen() ? close() : open()));
  trigger.addEventListener("keydown", (e) => {
    if (e.altKey || e.ctrlKey || e.metaKey) return;
    const k = e.key;
    if (!isOpen()) {
      if (k === "ArrowDown" || k === "ArrowUp" || k === "Home" || k === "End" || k === "Enter" || k === " ") {
        e.preventDefault();
        open();
        if (k === "Home") setActive(step(-1, 1));
        if (k === "End") setActive(step(items().length, -1));
      } else if (k.length === 1) {
        e.preventDefault();
        open();
        typeahead(k);
      }
      return;
    }
    if (k === "ArrowDown") { e.preventDefault(); setActive(step(activeIndex(), 1)); }
    else if (k === "ArrowUp") { e.preventDefault(); setActive(step(activeIndex(), -1)); }
    else if (k === "Home") { e.preventDefault(); setActive(step(-1, 1)); }
    else if (k === "End") { e.preventDefault(); setActive(step(items().length, -1)); }
    else if (k === "Enter" || (k === " " && !typed)) { e.preventDefault(); const i = activeIndex(); if (i >= 0 && enabled(items()[i])) pick(i); else close(); }
    else if (k === "Escape") { e.preventDefault(); e.stopPropagation(); close(); }
    else if (k === "Tab") { const i = activeIndex(); if (i >= 0 && enabled(items()[i])) pick(i, false); else close(false); }
    else if (k.length === 1) { e.preventDefault(); typeahead(k); }
  });
  root.addEventListener("focusout", (e) => { if (!root.contains(e.relatedTarget)) close(false); });

  // Labels, programmatic focus and failed validation all land on the trigger.
  select.addEventListener("focus", () => trigger.focus());
  select.addEventListener("invalid", () => trigger.focus());
  select.addEventListener("change", sync);
  select.form?.addEventListener("reset", () => setTimeout(sync));
  // Options swapped in by htmx, and disabled/aria-* changes made by other scripts.
  new MutationObserver(sync).observe(select, {
    childList: true, subtree: true, characterData: true,
    attributes: true, attributeFilter: ["disabled", "required", "aria-invalid", "aria-describedby"],
  });
  select.hmSelectSync = sync;
  sync();
}

function init(root) {
  root.querySelectorAll("[data-hm-select] > select").forEach(enhance);
}

// Programmatic select.value = … fires no event; call refresh(select) afterwards.
function refresh(select) {
  select.hmSelectSync?.();
}

document.addEventListener("DOMContentLoaded", () => init(document));
// htmx restores focus by id during the swap, before the trigger exists again (it is built in
// init), and a select without an id gets a new generated id on every init. So remember the
// focused trigger by the select's own id when it has one, else by where it sits (form id,
// select name, index among the same-named selects), and re-resolve that after the swap is enhanced.
let focusedTrigger = null; // {id} | {form, name, index} | null
const selectsNamed = (form, name) =>
  [...document.querySelectorAll(`[data-hm-select]`)].filter((r) => {
    const s = r.querySelector("select");
    return s && (s.getAttribute("name") ?? "") === name && (s.form?.id ?? "") === form;
  });
function describeTrigger(a) {
  const select = a.closest("[data-hm-select]")?.querySelector("select");
  if (!select) return null;
  if (select.id) return { id: select.id };
  const form = select.form?.id ?? "";
  const name = select.getAttribute("name") ?? "";
  return { form, name, index: selectsNamed(form, name).indexOf(a.closest("[data-hm-select]")) };
}
const resolveTrigger = (d) =>
  d.id ? document.getElementById(`${d.id}-trigger`) : selectsNamed(d.form, d.name)[d.index]?.querySelector(".hm-select__trigger");
// No swap (an error response, a 204) means no afterSettle: drop the descriptor so it cannot refocus later.
document.addEventListener("htmx:beforeSwap", (evt) => {
  const a = document.activeElement;
  focusedTrigger = evt.detail?.shouldSwap !== false && a?.classList?.contains("hm-select__trigger") ? describeTrigger(a) : null;
});
document.addEventListener("htmx:responseError", () => { focusedTrigger = null; });
document.addEventListener("htmx:sendError", () => { focusedTrigger = null; });
document.addEventListener("htmx:afterSettle", (evt) => {
  init(evt.target);
  const d = focusedTrigger;
  focusedTrigger = null;
  if (d && (!document.activeElement || document.activeElement === document.body)) resolveTrigger(d)?.focus();
});
export { init, refresh };
