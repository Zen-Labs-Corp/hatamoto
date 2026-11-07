// A generic command-log history: commit/undo/redo/jump over any immutable document, given the
// document's own apply(doc, op) -> {doc, inv}. Shared by the page builder and the forms designer.
// Pure, no DOM, no imports.

export const COALESCE_MS = 1000;
export const MAX_HISTORY = 200;

/**
 * apply(doc, op) -> {doc, inv}; coalesceKey(ops) -> string|null is the key of a lone-op typing
 * merge (null: this commit never merges on the timer).
 */
export function commitLog({ apply, coalesceKey = () => null }) {
  // Apply ops in order: {doc, invs} with invs already reversed (ready to undo).
  function applyAll(doc, ops) {
    const invs = [];
    for (const op of ops) {
      const r = apply(doc, op);
      doc = r.doc;
      invs.unshift(r.inv);
    }
    return { doc, invs };
  }

  /** A store value: {doc, past, future, applied}. Every function below returns a new one. */
  const createStore = (doc) => ({ doc, past: [], future: [], applied: [] });
  const canUndo = (store) => store.past.length > 0;
  const canRedo = (store) => store.future.length > 0;

  /**
   * Run ops atomically as one history entry (a throw leaves the store as it was).
   * `applied` on the result lists the ops that ran, for the bridge planner.
   * coalesce: true merges a lone op with the same coalesceKey within 1 s of the last entry
   * into it (typing), keeping the original inverse. A string is an explicit session key (a
   * slider or colour drag): every commit with the same key merges, however long the pause,
   * for any ops, so the drag is one undo step. setStyle with true never merges.
   */
  function commit(store, ops, { label = "", coalesce = false, now = Date.now() } = {}) {
    const { doc, invs } = applyAll(store.doc, ops);
    const session = typeof coalesce === "string";
    const ckey = session ? coalesce : coalesce ? coalesceKey(ops) : null;
    const last = store.past[store.past.length - 1];
    let past;
    if (ckey && last && last.ckey === ckey && (session || now - last.t <= COALESCE_MS)) {
      past = [...store.past.slice(0, -1), { ...last, fwd: ops, t: now }];
    } else {
      past = [...store.past, { label, inv: invs, fwd: ops, ckey, t: now }].slice(-MAX_HISTORY);
    }
    return { doc, past, future: [], applied: ops };
  }

  function undo(store) {
    const e = store.past[store.past.length - 1];
    if (!e) return { ...store, applied: [] };
    const { doc } = applyAll(store.doc, e.inv);
    return { doc, past: store.past.slice(0, -1), future: [...store.future, { ...e, ckey: null }], applied: e.inv };
  }

  function redo(store) {
    const e = store.future[store.future.length - 1];
    if (!e) return { ...store, applied: [] };
    const { doc } = applyAll(store.doc, e.fwd);
    return { doc, past: [...store.past, { ...e, ckey: null }], future: store.future.slice(0, -1), applied: e.fwd };
  }

  /**
   * Move n steps through the history in one go: n < 0 undoes |n| entries, n > 0 redoes n (clamped to
   * what exists). The result is the store after all steps, with `applied` the concatenation of the
   * ops they ran, so the caller repaints once. n = 0 changes nothing.
   */
  function jump(store, n) {
    const step = n < 0 ? undo : redo;
    let s = { ...store, applied: [] };
    const applied = [];
    for (let i = Math.abs(Math.trunc(n)); i > 0; i--) {
      const next = step(s);
      if (next.applied.length === 0) break;
      applied.push(...next.applied);
      s = next;
    }
    return { ...s, applied };
  }

  return { createStore, canUndo, canRedo, commit, undo, redo, jump };
}
