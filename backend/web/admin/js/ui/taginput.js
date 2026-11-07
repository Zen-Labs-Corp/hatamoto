// Behaviour for the hm/taginput partial: email chips with an inline error and a live region.
// Nested module (preload only). The caller owns the list; onChange fires for user edits only.
import { parseAddresses } from "@hm/ui/emailaddr";

export function mountTagInput(root, { list = [], onChange } = {}) {
  const chips = root.querySelector("[data-hm-ti-chips]");
  const input = root.querySelector("[data-hm-ti-input]");
  const err = root.querySelector("[data-hm-ti-err]");
  const live = root.querySelector("[data-hm-ti-live]");
  const tpl = root.querySelector("template[data-hm-ti-chip]");
  const errId = err.id;
  const describedBy = input.getAttribute("aria-describedby") || "";
  let items = [];

  const announce = (msg) => {
    live.textContent = "";
    live.textContent = msg;
  };

  function setError(msg) {
    err.textContent = msg;
    err.hidden = false;
    input.setAttribute("aria-invalid", "true");
    input.setAttribute("aria-describedby", `${describedBy} ${errId}`.trim());
    root.setAttribute("data-invalid", "");
    announce(msg);
  }

  function clearError() {
    err.textContent = "";
    err.hidden = true;
    input.removeAttribute("aria-invalid");
    input.setAttribute("aria-describedby", describedBy);
    root.removeAttribute("data-invalid");
  }

  function render() {
    chips.replaceChildren();
    for (const addr of items) {
      const li = tpl.content.firstElementChild.cloneNode(true);
      li.querySelector("[data-hm-ti-text]").textContent = addr;
      const rm = li.querySelector("[data-hm-ti-remove]");
      rm.setAttribute("aria-label", "Remove " + addr);
      rm.addEventListener("click", () => {
        remove(addr);
        input.focus();
      });
      chips.appendChild(li);
    }
  }

  function changed() {
    render();
    if (onChange) onChange(items.slice());
  }

  function remove(addr) {
    items = items.filter((x) => x !== addr);
    changed();
    announce("Removed " + addr);
  }

  // Adds every new valid address; returns how many were added.
  function add(addrs) {
    const have = new Set(items.map((x) => x.toLowerCase()));
    const fresh = addrs.filter((a) => !have.has(a.toLowerCase()));
    if (!fresh.length) return 0;
    items = items.concat(fresh);
    changed();
    announce("Added " + fresh.join(", "));
    return fresh.length;
  }

  function commit() {
    const text = input.value.trim();
    if (!text) return;
    const { valid, bad } = parseAddresses(text);
    if (bad.length) {
      input.value = bad.join(", ");
      add(valid);
      setError(`${bad[0]} is not a valid email address`);
      return;
    }
    input.value = "";
    clearError();
    add(valid);
  }

  input.addEventListener("keydown", (e) => {
    if (e.isComposing) return;
    if (e.key === "Enter" || e.key === ",") {
      e.preventDefault();
      commit();
    } else if (e.key === "Backspace" && input.value === "" && items.length) {
      remove(items[items.length - 1]);
    }
  });
  input.addEventListener("input", clearError);
  input.addEventListener("blur", commit);
  input.addEventListener("paste", (e) => {
    e.preventDefault();
    const text = (e.clipboardData && e.clipboardData.getData("text")) || "";
    const { valid, bad } = parseAddresses(input.value + " " + text);
    input.value = bad.join(", ");
    add(valid);
    if (bad.length) setError(`${bad[0]} is not a valid email address`);
    else clearError();
  });

  // A stored recipient the check cannot parse is still shown: dropping it would lose it on the next edit.
  items = list.slice();
  render();

  return {
    get: () => items.slice(),
    set(next) {
      items = next.slice();
      clearError();
      render();
    },
    focus: () => input.focus(),
  };
}
