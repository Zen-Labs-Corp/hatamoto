// Keyboard and screen reader layer for a server-rendered tracker (hm/tracker
// inside [data-hm-aia-tracker]). The blocks, their labels and their CSS
// tooltips already exist; this makes them one tab stop (roving tabindex) and
// moves between them with ArrowLeft/Right/Home/End. Each block is role=img with
// an aria-label, so focusing it already reads the label; a live region would
// read it twice.
import { stepIndex } from "@hm/aia/model";

const BLOCK = ".hm-tracker__block";

// "Show data": the table is server-rendered; with script it folds away behind
// the button (like the charts' Show data), which reports its state.
function enhanceData(root) {
  const sd = root.querySelector("[data-aia-sd]");
  const table = root.querySelector("[data-aia-data]");
  if (!sd || !table) return;
  const set = (open) => {
    table.hidden = !open;
    sd.setAttribute("aria-expanded", String(open));
  };
  sd.hidden = false;
  sd.addEventListener("click", () => set(sd.getAttribute("aria-expanded") !== "true"));
  set(false);
}

export function enhanceTracker(root) {
  enhanceData(root);
  const blocks = [...root.querySelectorAll(BLOCK)];
  if (!blocks.length) return null;
  let cur = blocks.length - 1; // like the prototype, start at the latest day
  const rove = (i) => {
    cur = i;
    blocks.forEach((b, k) => b.setAttribute("tabindex", k === i ? "0" : "-1"));
  };
  rove(cur);

  blocks.forEach((b, i) => {
    b.addEventListener("focus", () => {
      rove(i);
    });
  });
  root.addEventListener("keydown", (e) => {
    const i = blocks.indexOf(document.activeElement);
    if (i < 0) return;
    const next = stepIndex(i, e.key, blocks.length);
    if (next === undefined || next === null) return; // Escape is the shared tooltip handler's
    e.preventDefault();
    blocks[next].focus();
  });
  return { current: () => cur };
}
