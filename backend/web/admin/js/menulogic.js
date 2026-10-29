// Pure tree operations for the menu editor. A menu is a flat list of rows in
// document order, each with a depth; an item's block is the row plus the rows
// right after it that are deeper. Every function takes rows ({depth, ...}) and
// returns new arrays (rows are copied, never mutated). A refused operation
// returns null, so the caller leaves the tree alone.
//
// The results equal the server's applyMenuOp, which backend/internal/admin
// checks against the same cases (testdata/menu_ops_parity.json).

// blockEnd is the index after row i and its sub-items.
export function blockEnd(rows, i) {
  let j = i + 1;
  while (j < rows.length && rows[j].depth > rows[i].depth) j++;
  return j;
}

const shift = (block, delta) => block.map((r) => ({ ...r, depth: r.depth + delta }));
const copy = (rows) => rows.map((r) => ({ ...r }));
const valid = (rows, i) => Number.isInteger(i) && i >= 0 && i < rows.length;

// removeBlock drops the item and everything under it. undo feeds restoreBlock.
export function removeBlock(rows, i) {
  if (!valid(rows, i)) return null;
  const end = blockEnd(rows, i);
  return {
    rows: copy([...rows.slice(0, i), ...rows.slice(end)]),
    undo: { index: i, block: copy(rows.slice(i, end)) },
  };
}

// restoreBlock puts a removed block back at its old index, depths intact.
export function restoreBlock(rows, undo) {
  const at = Math.min(undo.index, rows.length);
  return copy([...rows.slice(0, at), ...undo.block, ...rows.slice(at)]);
}

// outdentBlock moves the block one level up and right after the parent's whole
// group; the siblings that followed it stay under the parent.
export function outdentBlock(rows, i) {
  if (!valid(rows, i) || rows[i].depth === 0) return null;
  const d = rows[i].depth;
  const end = blockEnd(rows, i);
  let p = i - 1;
  while (rows[p].depth >= d) p--;
  const pend = blockEnd(rows, p);
  return copy([...rows.slice(0, i), ...rows.slice(end, pend), ...shift(rows.slice(i, end), -1), ...rows.slice(pend)]);
}

// indentBlock nests the block one level deeper under the item above it.
export function indentBlock(rows, i, max) {
  if (!valid(rows, i) || i === 0) return null;
  const d = rows[i].depth;
  if (d > rows[i - 1].depth || d >= max) return null;
  const end = blockEnd(rows, i);
  return copy([...rows.slice(0, i), ...shift(rows.slice(i, end), 1), ...rows.slice(end)]);
}

// moveBlock swaps the block with its previous (step -1) or next (step 1)
// sibling, taking the sub-items along.
export function moveBlock(rows, i, step) {
  if (!valid(rows, i)) return null;
  const d = rows[i].depth;
  const end = blockEnd(rows, i);
  if (step < 0) {
    let start = -1;
    for (let k = i - 1; k >= 0; k--) {
      if (rows[k].depth < d) break;
      if (rows[k].depth === d) {
        start = k;
        break;
      }
    }
    if (start < 0) return null;
    return copy([...rows.slice(0, start), ...rows.slice(i, end), ...rows.slice(start, i), ...rows.slice(end)]);
  }
  if (end >= rows.length || rows[end].depth !== d) return null;
  const next = blockEnd(rows, end);
  return copy([...rows.slice(0, i), ...rows.slice(end, next), ...rows.slice(i, end), ...rows.slice(next)]);
}

// moveToParent makes the block the last child of the top-level row parentIdx
// (depth 1), or the last top-level item when parentIdx is null. It refuses a
// parent that is not top level, the block itself or its own sub-items, and a
// move that would take a sub-item past max.
export function moveToParent(rows, i, parentIdx, max) {
  if (!valid(rows, i)) return null;
  const end = blockEnd(rows, i);
  const top = parentIdx === null || parentIdx === undefined;
  if (!top) {
    if (!valid(rows, parentIdx) || rows[parentIdx].depth !== 0) return null;
    if (parentIdx >= i && parentIdx < end) return null;
  }
  const delta = (top ? 0 : 1) - rows[i].depth;
  if (rows.slice(i, end).some((r) => r.depth + delta > max)) return null;
  const rest = [...rows.slice(0, i), ...rows.slice(end)];
  let at = rest.length;
  if (!top) {
    const t = parentIdx > i ? parentIdx - (end - i) : parentIdx;
    at = blockEnd(rest, t);
  }
  return copy([...rest.slice(0, at), ...shift(rows.slice(i, end), delta), ...rest.slice(at)]);
}

// duplicateBlock inserts a copy of the block right after the original's group.
export function duplicateBlock(rows, i) {
  if (!valid(rows, i)) return null;
  const end = blockEnd(rows, i);
  return copy([...rows.slice(0, end), ...rows.slice(i, end), ...rows.slice(end)]);
}

// dropTargets lists the places the block at `from` may be dropped. One slot
// per gap of the list without the block: index is where the block would be
// inserted in that list, depth the level nearest pointerDepth that keeps the
// tree valid and within max, and label says where it lands, in the row's
// original numbering: {kind: "inside", index: parent row} or {kind: "top"}.
export function dropTargets(rows, from, pointerDepth, max) {
  if (!valid(rows, from)) return [];
  const end = blockEnd(rows, from);
  const block = rows.slice(from, end);
  const height = Math.max(...block.map((r) => r.depth)) - rows[from].depth;
  const lastRel = block[block.length - 1].depth - rows[from].depth;
  const rest = [...rows.slice(0, from), ...rows.slice(end)];
  const original = (k) => (k < from ? k : k + (end - from));
  const slots = [];
  for (let g = 0; g <= rest.length; g++) {
    const prev = g > 0 ? rest[g - 1].depth : -1;
    const next = g < rest.length ? rest[g].depth : 0;
    const hi = Math.min(prev + 1, max - height);
    // The row after the block may be at most one level below the block's last row.
    const lo = Math.max(0, next - 1 - lastRel);
    if (lo > hi) continue;
    const depth = Math.min(Math.max(pointerDepth, lo), hi);
    let label = { kind: "top" };
    if (depth > 0) {
      let p = g - 1;
      while (rest[p].depth >= depth) p--;
      label = { kind: "inside", index: original(p) };
    }
    slots.push({ index: g, depth, label });
  }
  return slots;
}

// clampDepths keeps the tree valid: the first item is top level and an item is
// at most one level below the one before it, and none is deeper than max.
export function clampDepths(rows, max) {
  let prev = -1;
  return rows.map((r) => {
    let d = Math.max(r.depth, 0);
    if (d > prev + 1) d = prev + 1;
    if (d > max) d = max;
    prev = d;
    return { ...r, depth: d };
  });
}
