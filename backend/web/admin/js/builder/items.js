// The Items panel of the Content tab: the entries of Tabs, Accordion and Gallery (child elements) and of a List (its lines).
// Add item, Move up, Move down and Delete are the existing insert / move / remove / setProp ops, one commit each.
// A lazy chunk. Run: node --test scripts/admin-qa/unit/builder-items.test.mjs
import { h, actButton } from "@hm/builder/dom";
import { find } from "@hm/builder/store";
import { moveOp } from "@hm/builder/navigator";
import { makeNode } from "@hm/builder/palette";

/** Which kinds have child items, with the prop that names an item and the word for it. */
export const ITEM_KINDS = { tabs: { child: "tabpanel", prop: "label", word: "tab" }, accordion: { child: "accorditem", prop: "title", word: "item" }, gallery: { child: "image", prop: "alt", word: "image" } };

/** The visible name of the i-th child item. */
export const itemName = (kid, prop, i) => String(kid.p?.[prop] ?? "").trim() || (kid.t === "image" ? String(kid.p?.src ?? "").split("/").pop() : "") || `Item ${i + 1}`;

/** The lines of a List's items prop, and back. */
export const linesOf = (text) => (text === "" || text == null ? [] : String(text).split("\n"));
export const swapLines = (lines, i, by) => {
  const j = i + by;
  if (j < 0 || j >= lines.length) return lines;
  const out = [...lines];
  [out[i], out[j]] = [out[j], out[i]];
  return out;
};

/**
 * itemsPanel(node, {api, commit, draw}) -> element | null (kinds without items).
 * Child items: Add item (the kind's default child) / Move up / Move down / Delete; a name button selects the child.
 * List: one text field per line with the same buttons, written back as the items prop.
 */
export function itemsPanel(node, { api, commit, draw }) {
  const spec = ITEM_KINDS[node.t];
  if (!spec && node.t !== "list") return null;
  const word = spec?.word ?? "item";
  const box = h("div", { class: "hm-bs-items", role: "group", "aria-label": "Items" });
  const again = (focus) => {
    draw();
    document.querySelector?.(`[data-hm-items-focus="${focus}"]`)?.focus?.();
  };
  const btn = (label, name, disabled, onclick) => {
    const b = actButton(label, { class: "hm-bs-item__b", "aria-label": name, onclick });
    if (disabled) b.disabled = true;
    return b;
  };
  const list = h("ol", { class: "hm-bs-items__list" });

  if (node.t === "list") {
    const now = () => linesOf(find(api.getDoc(), node.id)?.p?.items); // read at click time: a line edit commits without a redraw
    const lines = now();
    const write = (next, label, say) => {
      if (commit([{ op: "setProp", id: node.id, key: "items", value: next.length ? next.join("\n") : null }], { label })) {
        say && api.announce(say);
        again("add");
      }
    };
    lines.forEach((text, i) => {
      const id = `hm-bs-li${i}-${node.id}`;
      const input = h("input", { id, type: "text", class: "hm-field__control", "aria-label": `Item ${i + 1}`, "data-hm-items-focus": `i${i}` });
      input.value = text;
      input.addEventListener("change", () => {
        const next = [...linesOf(find(api.getDoc(), node.id)?.p?.items)];
        next[i] = input.value;
        commit([{ op: "setProp", id: node.id, key: "items", value: next.join("\n") }], { label: "Change Items" });
      });
      list.append(
        h("li", { class: "hm-bs-item" }, input,
          btn("Move up", `Move item ${i + 1} up`, i === 0, () => write(swapLines(now(), i, -1), "Move item", `Item ${i + 1} moved up`)),
          btn("Move down", `Move item ${i + 1} down`, i === lines.length - 1, () => write(swapLines(now(), i, 1), "Move item", `Item ${i + 1} moved down`)),
          btn("Delete", `Delete item ${i + 1}`, false, () => write(now().filter((_, k) => k !== i), "Delete item", `Item ${i + 1} deleted`))),
      );
    });
    box.append(list, actButton("Add item", { "data-hm-items-focus": "add", onclick: () => write([...now(), ""], "Add item", "Item added") }));
    return box;
  }

  const kids = node.c ?? [];
  const go = (ops, label, say) => commit(ops, { label }) && (api.announce(say), again("add"));
  kids.forEach((kid, i) => {
    const name = itemName(kid, spec.prop, i);
    const pick = actButton(name, { class: "hm-bs-item__name", "aria-label": `Select ${word} ${name}`, onclick: () => api.select(kid.id) });
    const move = (action, by) => {
      const op = moveOp(api.getDoc(), kid.id, action);
      return btn(by < 0 ? "Move up" : "Move down", `Move ${word} ${name} ${by < 0 ? "up" : "down"}`, !op, () => go([op], `Move ${word}`, `${name} moved ${by < 0 ? "up" : "down"}`));
    };
    list.append(h("li", { class: "hm-bs-item" }, pick, move("moveup", -1), move("movedown", 1), btn("Delete", `Delete ${word} ${name}`, false, () => go([{ op: "remove", id: kid.id }], `Delete ${word}`, `${name} deleted`))));
  });
  box.append(list, actButton(`Add ${word}`, { "data-hm-items-focus": "add", onclick: () => go([{ op: "insert", parent: node.id, index: kids.length, node: makeNode({ kind: spec.child }, api.kinds) }], `Add ${word}`, `${word[0].toUpperCase()}${word.slice(1)} added`) }));
  return box;
}
