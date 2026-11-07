// Command-log history (commit/undo/redo/jump) over any immutable doc. Shared with the forms designer.

export const COALESCE_MS = 1000;
export const MAX_HISTORY = 200;

// apply(doc, op) -> {doc, inv}; coalesceKey(ops) -> typing-merge key or null.
export function commitLog({ apply, coalesceKey = () => null }) {
  function applyAll(doc, ops) {
    const invs = [];
    for (const op of ops) {
      const r = apply(doc, op);
      doc = r.doc;
      invs.unshift(r.inv);
    }
    return { doc, invs };
  }

  const createStore = (doc) => ({ doc, past: [], future: [], applied: [] });
  const canUndo = (store) => store.past.length > 0;
  const canRedo = (store) => store.future.length > 0;

  // One history entry per call; coalesce: true merges typing (same key, within 1 s), a string is a session key.
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

  // n < 0 undoes |n| entries, n > 0 redoes n; one result, `applied` is every op run.
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
