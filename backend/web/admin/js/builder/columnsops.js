// The "Number of columns" control of a Columns element: the count prop and its child containers change together, in one
// commit (one undo step). Adding appends empty containers; removing takes empty trailing containers only (a column that is not a container is content too), so no content is lost.
// Pure; a lazy chunk. Run: node --test scripts/admin-qa/unit/builder-columnsops.test.mjs
import { seedNode } from "@hm/builder/palette";

export const REFUSE = "Move or delete what is inside the last column first.";

/** columnsOps(node, n) -> {ops} (setProp count + inserts or removes), {ops: []} when nothing changes, or {refuse: message}. */
export function columnsOps(node, n) {
  const kids = node.c ?? [];
  if (n === kids.length && node.p?.count === n) return { ops: [] };
  const drop = kids.slice(n);
  if (drop.some((k) => k.t !== "container" || (k.c ?? []).length)) return { refuse: REFUSE };
  const grow = Array.from({ length: Math.max(0, n - kids.length) }, (_, i) => ({ op: "insert", parent: node.id, index: kids.length + i, node: seedNode({ t: "container" }) }));
  return { ops: [{ op: "setProp", id: node.id, key: "count", value: n }, ...grow, ...drop.map((k) => ({ op: "remove", id: k.id }))] };
}
