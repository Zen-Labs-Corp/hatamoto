// hm/searchselect: type to search, arrow keys to move, Enter to pick. Results
// come from the endpoint in data-src as JSON [{value,label,hint}]. The picked
// value goes into the hidden input and a status line announces the result count.
// The markup works without this script (the server then resolves the typed text).
function init(root) {
  root.querySelectorAll("[data-hm-searchselect]").forEach((box) => {
    if (box.dataset.hmReady) return;
    box.dataset.hmReady = "1";
    const input = box.querySelector("[data-hm-ss-input]");
    const value = box.querySelector("[data-hm-ss-value]");
    const prev = box.querySelector("[data-hm-ss-prev]");
    const list = box.querySelector("[data-hm-ss-list]");
    const status = box.querySelector("[data-hm-ss-status]");
    let timer = 0;
    let seq = 0;

    const close = () => {
      list.hidden = true;
      list.replaceChildren();
      list.removeAttribute("aria-busy");
      status.textContent = "";
      input.setAttribute("aria-expanded", "false");
      input.removeAttribute("aria-activedescendant");
    };
    const options = () => [...list.querySelectorAll("[role=option]")];
    const mark = (i) => {
      const opts = options();
      opts.forEach((o, k) => o.setAttribute("aria-selected", String(k === i)));
      if (opts[i]) {
        input.setAttribute("aria-activedescendant", opts[i].id);
        opts[i].scrollIntoView({ block: "nearest" });
      }
    };
    const pick = (opt) => {
      value.value = opt.dataset.value;
      input.value = opt.dataset.label;
      prev.value = opt.dataset.label;
      close();
      box.dispatchEvent(new CustomEvent("hm:picked", { bubbles: true }));
    };
    const show = (items) => {
      list.replaceChildren();
      if (items.length === 0) {
        const li = document.createElement("li");
        li.className = "hm-searchselect__none";
        li.textContent = "No results";
        li.setAttribute("role", "presentation");
        list.append(li);
      }
      items.forEach((it, k) => {
        const li = document.createElement("li");
        li.id = `${input.id}-opt-${k}`;
        li.setAttribute("role", "option");
        li.setAttribute("aria-selected", "false");
        li.className = "hm-searchselect__option";
        li.dataset.value = it.value;
        li.dataset.label = it.label;
        const name = document.createElement("span");
        name.textContent = it.label;
        li.append(name);
        if (it.hint) {
          const hint = document.createElement("span");
          hint.className = "hm-searchselect__hint";
          hint.textContent = it.hint;
          li.append(hint);
        }
        li.addEventListener("mousedown", (e) => { e.preventDefault(); pick(li); });
        list.append(li);
      });
      list.hidden = false;
      list.removeAttribute("aria-busy");
      status.textContent = items.length === 0 ? "No results" : items.length === 1 ? "1 result" : `${items.length} results`;
      input.setAttribute("aria-expanded", "true");
    };
    const search = async () => {
      const mine = ++seq;
      list.setAttribute("aria-busy", "true");
      const url = `${box.dataset.src}?q=${encodeURIComponent(input.value.trim())}`;
      try {
        const res = await fetch(url, { headers: { Accept: "application/json" }, credentials: "same-origin" });
        if (!res.ok || mine !== seq) return;
        show(await res.json());
      } catch {
        /* the typed text is still resolved by the server on save */
      }
      if (mine === seq) list.removeAttribute("aria-busy");
    };

    input.addEventListener("input", () => {
      value.value = ""; // typing un-picks; the server resolves the text if nothing is picked
      clearTimeout(timer);
      timer = setTimeout(search, 200);
    });
    input.addEventListener("focus", () => { if (!input.value) search(); });
    input.addEventListener("blur", () => setTimeout(close, 100));
    input.addEventListener("keydown", (e) => {
      const opts = options();
      const cur = opts.findIndex((o) => o.getAttribute("aria-selected") === "true");
      if (e.key === "ArrowDown") { e.preventDefault(); if (list.hidden) search(); else mark(Math.min(cur + 1, opts.length - 1)); }
      else if (e.key === "ArrowUp") { e.preventDefault(); mark(Math.max(cur - 1, 0)); }
      else if ((e.key === "Home" || e.key === "End") && !list.hidden && opts.length > 0) { e.preventDefault(); mark(e.key === "Home" ? 0 : opts.length - 1); }
      else if (e.key === "Enter" && !list.hidden && cur >= 0) { e.preventDefault(); pick(opts[cur]); }
      else if (e.key === "Escape" && !list.hidden) { e.preventDefault(); e.stopPropagation(); close(); }
    });
  });
}

document.addEventListener("DOMContentLoaded", () => init(document));
document.addEventListener("htmx:afterSettle", (evt) => init(evt.target));
export { init };
