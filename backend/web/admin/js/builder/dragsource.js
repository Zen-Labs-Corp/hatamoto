// The press-and-move drag source: a pointer press that travels past a small threshold becomes a
// drag start. Generic (no store, no DOM building); shared by the page builder and the forms designer.

const THRESHOLD = 4; // px of pointer travel before a press becomes a drag

/**
 * Turns a press-and-move on el into a drag start. pick(event) returns a function
 * that makes the drag spec ({id} or {node}), or null when the press is not on a source.
 * Touch is left alone (it scrolls); touch users have the keyboard commands and click.
 */
export function dragSource(el, { pick, start }) {
  let down = null;
  el.addEventListener("pointerdown", (e) => {
    const make = e.button === 0 && e.pointerType !== "touch" ? pick(e) : null;
    down = make ? { x: e.clientX, y: e.clientY, id: e.pointerId, make } : null;
  });
  el.addEventListener("pointermove", (e) => {
    if (!down || e.pointerId !== down.id) return;
    if (e.buttons === 0) down = null;
    else if (Math.hypot(e.clientX - down.x, e.clientY - down.y) > THRESHOLD) {
      const d = down;
      down = null;
      start(e, d.make());
    }
  });
  for (const ev of ["pointerup", "pointercancel"]) el.addEventListener(ev, () => (down = null));
}
