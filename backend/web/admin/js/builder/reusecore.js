// Reuse parts the first paint needs: the palette's Patterns and Blocks items, whether
// a part wants a Content slot, and the ref node's state. The UI (reuse.js) is a lazy chunk.
import { reuseItems } from "@hm/builder/reuseops";

export const UUID = /^[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}$/;

/** What the ref inspector shows for a ref node: the block's name (blocks is [{id,name}]) and its edit link; a malformed id is never linked. */
export function refState(node, blocks) {
  const id = String(node?.p?.block ?? "");
  const known = UUID.test(id) ? (blocks ?? []).find((b) => b.id === id) : null;
  return { id, valid: UUID.test(id), name: known?.name ?? "", missing: !known };
}

/** Palette items for the template and block groups (never the block being edited). */
export const paletteExtra = (reuse, self = "") =>
  reuseItems({ templates: reuse.templates, blocks: (reuse.blocks ?? []).filter((b) => b.id !== self) }).map((i) => ({ ...i, kind: i.key.startsWith("tpl:") ? "template" : "block" }));

/** A layout part that wraps the page content must hold exactly one Content slot. */
export const wantsSlot = (reuse) => reuse.role === "part" && (reuse.slot === "single" || reuse.slot === "archive");
