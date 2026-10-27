// The history panel (a lazy chunk): every change of this session with its label and age; picking
// one jumps there in one step (store.jump). historyRows is the pure model. The panel re-reads the
// store on every open and every change (the screen replaces the store when it adopts a server doc).
import { h, focusKept } from "@hm/builder/dom";

/** "just now", "40 s ago", "5 min ago", "2 h ago". */
export function ago(ms) {
  if (ms < 5000) return "just now";
  const s = Math.round(ms / 1000);
  if (s < 60) return `${s} s ago`;
  if (s < 3600) return `${Math.round(s / 60)} min ago`;
  return `${Math.round(s / 3600)} h ago`;
}

/**
 * The rows, oldest first: {n, label, ago, current}. n is the jump that lands on that state (negative undoes,
 * positive redoes, 0 is where the page is now). The first row is the state before the first change.
 */
export function historyRows(st, now) {
  const past = st.past.map((e, i) => ({ n: i + 1 - st.past.length, label: e.label || "Change", ago: ago(now - e.t) }));
  const future = [...st.future].reverse().map((e, i) => ({ n: i + 1, label: e.label || "Change", ago: ago(now - e.t) }));
  return [{ n: -st.past.length, label: "Original", ago: "" }, ...past, ...future].map((r) => ({ ...r, current: r.n === 0 }));
}

/**
 * Opens the panel under anchor. getStore() is the current store; onJump(n) applies the jump; onClose() runs when it closes.
 * Returns {refresh, close}: refresh redraws from getStore() (call after every change).
 */
export function openHistory({ anchor, getStore, onJump, onClose }) {
  const list = h("ul", { role: "listbox", "aria-label": "Changes, oldest first", class: "hm-bs-history__list" });
  const m = h("div", { class: "hm-bs-history", popover: "auto", role: "dialog", "aria-label": "History" }, h("p", { class: "hm-muted", text: "Pick a step to go back or forward to it." }), list);
  const options = () => [...list.children];

  function refresh() {
    const had = list.contains(document.activeElement);
    const rows = historyRows(getStore(), Date.now());
    list.replaceChildren(
      ...rows.map((r) =>
        h("li", { role: "option", tabindex: r.current ? "0" : "-1", "aria-selected": String(r.current), "data-n": String(r.n), class: "hm-bs-history__row" }, h("span", { text: r.label }), h("span", { class: "hm-muted", text: r.current ? "now" : r.ago })),
      ),
    );
    const cur = list.querySelector('[tabindex="0"]');
    cur?.scrollIntoView?.({ block: "nearest" });
    if (had) cur?.focus();
  }

  const pick = (el) => el && Number(el.dataset.n) !== 0 && onJump(Number(el.dataset.n));
  list.addEventListener("click", (e) => pick(e.target.closest("[data-n]")));
  list.addEventListener("keydown", (e) => {
    const all = options();
    const i = all.indexOf(document.activeElement);
    const to = { ArrowDown: i + 1, ArrowUp: i - 1, Home: 0, End: all.length - 1 }[e.key];
    if (to !== undefined) {
      e.preventDefault();
      all[Math.min(all.length - 1, Math.max(0, to))].focus();
    } else if (e.key === "Enter" || e.key === " ") {
      e.preventDefault();
      pick(all[i]);
    }
  });
  const keptFocus = focusKept(m);
  m.addEventListener("toggle", (e) => {
    if (e.newState !== "closed") return;
    m.remove();
    if (keptFocus()) anchor?.focus();
    onClose();
  });
  refresh();
  document.body.append(m);
  m.showPopover();
  const r = anchor?.getBoundingClientRect();
  const vw = document.documentElement.clientWidth;
  m.style.left = `${Math.max(8, Math.min(r?.width ? r.left : 8, vw - m.offsetWidth - 8))}px`;
  m.style.top = `${Math.max(8, Math.min(r?.width ? r.bottom + 4 : 56, innerHeight - m.offsetHeight - 8))}px`;
  list.querySelector('[tabindex="0"]').focus();
  return { refresh, close: () => m.hidePopover() };
}
