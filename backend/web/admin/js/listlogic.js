// Pure helpers of the form builder, the menu editor and the dropzone list.
// They hold the logic that loses data or focus when it is wrong, so node can test
// it without a DOM (scripts/admin-qa/unit/listlogic.test.mjs).

// renumberRef rewrites the row index in a name, id or id-ref list: "f.3.label"
// (prefix "f"), "f-3-label-error" and the space-separated "x f-3-hint" form.
export function renumberRef(v, prefix, i) {
  return v
    .replace(new RegExp(`^${prefix}\\.\\d+\\.`), `${prefix}.${i}.`)
    .replace(new RegExp(`^${prefix}-\\d+-`), `${prefix}-${i}-`)
    .replace(new RegExp(`(\\s)${prefix}-\\d+-`, "g"), `$1${prefix}-${i}-`);
}

// renumberOp rewrites the trailing row index of a Move/Remove value ("up.3" -> "up.1").
export const renumberOp = (v, i) => v.replace(/\.\d+$/, `.${i}`);

// removeDepths drops the item at idx from a list of depths. The deeper items right
// after it stay and move up one level; promoted is how many did.
export function removeDepths(depths, idx) {
  const d = depths[idx];
  let promoted = 0;
  while (idx + 1 + promoted < depths.length && depths[idx + 1 + promoted] > d) promoted++;
  const out = depths.filter((_, i) => i !== idx);
  for (let k = 0; k < promoted; k++) out[idx + k] -= 1;
  return { depths: out, promoted };
}

// restoreDepths undoes removeDepths: the item goes back at idx with its depth and
// the first `promoted` items after it go one level deeper again. When the tree
// has changed since so that those items are no longer a run at or below the
// item's own depth, nothing but the item itself is touched.
export function restoreDepths(depths, idx, depth, promoted) {
  const out = [...depths];
  out.splice(idx, 0, depth);
  const run = out.slice(idx + 1, idx + 1 + promoted);
  if (run.length === promoted && run.every((x) => x >= depth)) for (let k = 1; k <= promoted; k++) out[idx + k] += 1;
  return out;
}

// removeFocusIndex picks where focus goes after a file Remove: the Remove button now at
// that place, else the one before it, else -1 for the file input.
export const removeFocusIndex = (at, count) => (count === 0 ? -1 : Math.min(at, count - 1));
