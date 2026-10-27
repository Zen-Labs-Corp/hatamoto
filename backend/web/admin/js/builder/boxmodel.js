// The spacing rings as a box model (a lazy chunk): padding sits inside margin, the element in the middle. Each
// ring is a cross of four compact length controls in top, right, bottom, left order (the DOM and tab order; CSS grid
// areas only place them), each side with quick "0" (and "auto" on margins) buttons. Three icon toggles link sides:
// "Same on all sides", "Same top and bottom", "Same left and right"; a linked pair or ring is written in one batch
// (one undo step). Each side keeps its own row state: its input is outlined when set here and described by its
// source in text; one "Reset <ring>" button clears the ring's sides set here.
// The pure parts (ring order, link sets, which keys pair up) are controls.js.
import { h, actButton } from "@hm/builder/dom";
import { linkSets, verticalHorizontal, writesFor, sourceOf } from "@hm/builder/controls";
import { lengthControl } from "@hm/builder/controlsui";

let ids = 0;
const title = (s) => s[0].toUpperCase() + s.slice(1);
const SIDES = ["top", "right", "bottom", "left"];
const LINKS = [["all", "Same on all sides"], ["vertical", "Same top and bottom"], ["horizontal", "Same left and right"]];

/**
 * boxModel({entries, name, palette, stateOf, commit, commitBatch, valid, hint}) -> {el, keys, paint()}.
 * entries: the table entries of the group; name(key) its label ("Padding top"); stateOf(key) -> controls.rowState;
 * commit(key, value, opts) and commitBatch([{key, value}], opts) -> bool write at the style-map key being edited;
 * valid(entry, value) -> bool the table's verdict; hint the text of a refused value. keys: the keys it draws (the rest of the group stays single rows).
 * With both rings the padding ring is drawn inside the margin ring.
 */
export function boxModel({ entries, name, palette, stateOf, commit, commitBatch, valid, hint }) {
  const by = new Map(entries.map((e) => [e.key, e]));
  const rings = linkSets(entries.map((e) => e.key));
  const own = (k) => {
    const s = stateOf(k);
    return s.dot === "set" ? s.value : null;
  };
  const painters = [];
  const built = rings.map(({ name: ring, keys }) => {
    const rid = `hm-bs-b${++ids}`;
    const label = title(ring);
    const link = { all: false, vertical: false, horizontal: false };
    const initial = keys.map(own);
    const [vert, horiz] = [verticalHorizontal(keys[0]).vertical, verticalHorizontal(keys[0]).horizontal];
    const same = (pair) => own(pair[0]) !== null && own(pair[0]) === own(pair[1]);
    link.all = initial[0] !== null && initial.every((v) => v === initial[0]);
    link.vertical = !link.all && same(vert);
    link.horizontal = !link.all && same(horiz);
    const err = h("p", { class: "hm-bs-err", id: `${rid}-e`, hidden: true, text: `That value is not allowed here. ${hint}` });
    /** The sides a write to side i also writes: the ring, or its linked pair, or just itself. */
    const targets = (i) => (link.all ? keys : link.vertical && i % 2 === 0 ? vert : link.horizontal && i % 2 === 1 ? horiz : [keys[i]]);
    const toggles = LINKS.map(([mode, text]) => {
      const b = actButton("", { class: "hm-bs-linkbtn", "data-link": mode, "aria-label": text, "aria-pressed": String(link[mode]), title: text });
      b.addEventListener("click", () => {
        link[mode] = !link[mode];
        if (link[mode]) for (const other of Object.keys(link)) if (other !== mode) link[other] = false;
        for (const t of toggles) t.setAttribute("aria-pressed", String(link[t.dataset.link]));
        if (!link[mode] || mode === "all") return;
        const [from, to] = mode === "vertical" ? vert : horiz; // linking a pair makes the second side match the first
        const v = own(from);
        if (v !== null && own(to) !== v) commitBatch(writesFor([from, to], v));
      });
      return b;
    });
    const reset = actButton("", { class: "hm-bs-reset", "aria-label": `Reset ${ring}`, hidden: true });
    reset.addEventListener("click", () => commitBatch(keys.filter((k) => own(k) !== null).map((key) => ({ key, value: null }))));

    const cells = keys.map((key, i) => {
      const entry = by.get(key);
      const cid = `${rid}-${SIDES[i]}`;
      const src = h("span", { class: "hm-visually-hidden", id: `${cid}-s` });
      const apply = (v) => {
        const list = v !== null ? targets(i) : [key];
        const bad = v !== null && !list.every((k) => valid(by.get(k), v));
        if (bad) ctl.num.setAttribute("aria-invalid", "true");
        else ctl.num.removeAttribute("aria-invalid");
        err.hidden = !bad;
        if (bad) return false;
        return list.length > 1 ? commitBatch(writesFor(list, v)) : commit(key, v);
      };
      const ctl = lengthControl({ id: cid, entry, name: name(key), palette, describe: `${cid}-s ${rid}-e`, compact: true, apply });
      const quick = [["0", "0"], ...(ring === "margin" ? [["auto", "auto"]] : [])].map(([text, v]) => {
        const b = h("button", { type: "button", class: "hm-bs-opt hm-bs-quick", "aria-label": `${name(key)}: ${text}`, text });
        b.addEventListener("click", () => apply(v));
        return b;
      });
      const cell = h("div", { class: `hm-bs-side hm-bs-side--${SIDES[i]}` }, ctl.el, h("div", { class: "hm-bs-side__q" }, ...quick), src);
      painters.push(() => {
        const s = stateOf(key);
        ctl.sync(s.dot === "set" ? s.value : null, s.dot === "inherited" ? String(s.value) : "");
        cell.dataset.dot = s.dot;
        src.textContent = `${name(key)}: ${sourceOf(s).text || "not set"}`;
      });
      return cell;
    });
    painters.push(() => (reset.hidden = !keys.some((k) => own(k) !== null)));
    const core = h("div", { class: "hm-bs-ring__c" }); // the element, or the padding ring inside the margin ring
    const el = h("div", { class: `hm-bs-ring hm-bs-ring--${ring}`, role: "group", "aria-label": label },
      h("div", { class: "hm-bs-ring__h" }, h("span", { class: "hm-bs-ring__t", text: label }), h("span", { class: "hm-bs-links" }, ...toggles), reset),
      h("div", { class: "hm-bs-ring__g" }, ...cells, core), // the cells are in top, right, bottom, left order: that is the tab order
      err);
    return { el, core };
  });
  const element = () => h("div", { class: "hm-bs-ring__el", "aria-hidden": "true", text: "Element" });
  const [outer, inner] = built;
  if (inner) {
    outer.core.append(inner.el);
    outer.el.classList.add("hm-bs-ring--outer");
    inner.core.append(element());
  } else if (outer) outer.core.append(element());
  const el = h("div", { class: "hm-bs-box" }, ...(outer ? [outer.el] : []));
  return { el, keys: rings.flatMap((r) => r.keys), paint: () => painters.forEach((p) => p()) };
}
