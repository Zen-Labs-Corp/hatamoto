// Element restore: the ops that put an earlier version of one element back (pure, lazy with noderestore.js).
// They are ordinary ops, so one commit is one undo step.
const same = (a, b) => JSON.stringify(a ?? null) === JSON.stringify(b ?? null);
const keys = (a, b) => [...new Set([...Object.keys(a ?? {}), ...Object.keys(b ?? {})])];

/**
 * restoreOps(cur, old, at) -> ops. cur is the element now, old the earlier {t, p, s, b}, at = {parentId, index}
 * where cur sits. The same kind changes only what differs; another kind is removed and inserted again at the
 * same place under the same id, with the children it has now. No ops means nothing differs.
 */
export function restoreOps(cur, old, at) {
  const id = cur.id;
  if (cur.t !== old.t) {
    const node = { id, t: old.t, ...(old.p && { p: old.p }), ...(old.s && { s: old.s }), ...(old.b && { b: old.b }), ...(cur.c && { c: cur.c }) };
    return [{ op: "remove", id }, { op: "insert", parent: at.parentId, index: at.index, node }];
  }
  const ops = [];
  for (const key of keys(cur.p, old.p)) if (!same(cur.p?.[key], old.p?.[key])) ops.push({ op: "setProp", id, key, value: old.p?.[key] ?? null });
  for (const bp of keys(cur.s, old.s)) for (const key of keys(cur.s?.[bp], old.s?.[bp])) if (cur.s?.[bp]?.[key] !== old.s?.[bp]?.[key]) ops.push({ op: "setStyle", id, bp, key, value: old.s?.[bp]?.[key] ?? null });
  for (const key of keys(cur.b, old.b)) if (cur.b?.[key] !== old.b?.[key]) ops.push({ op: "setBind", id, key, value: old.b?.[key] ?? null });
  return ops;
}
