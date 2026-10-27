// The builder's client document store: the page_doc v2 tree, the same op verbs as
// backend/internal/builder/ops.go (insert, move, remove, setProp, setStyle) and a
// command-log history. Pure and immutable: applyOp path-copies and returns a NEW
// doc, nothing is ever mutated, so a store value can be kept as a snapshot.
// No DOM, no imports. Limits mirror doc.go (depth 8, 1000 nodes, id grammar).

export const MAX_DEPTH = 8;
export const MAX_NODES = 1000;
export const COALESCE_MS = 1000;
const MAX_HISTORY = 200;
const ID_RE = /^[A-Za-z0-9_-]{1,64}$/;
const BPS = ["base", "md", "sm"];
export const hasParentHover = (s) => Object.keys(s ?? {}).some((k) => k.endsWith(":parent-hover"));
export const takesParentHover = (parent) => !!parent && parent.t !== "ref" && parent.t !== "component";
export const STATES = ["hover", "focus", "active", "parent-hover"];
// "md" or "md:hover": a breakpoint, optionally one state (mirrors builder.ParseSKey).
const okBp = (k) => {
  const [b, st, extra] = k.split(":");
  return BPS.includes(b) && extra === undefined && (st === undefined || STATES.includes(st));
};

// Kinds that take children: the server's kinds table (setChildKinds at boot), containers until then.
let childKinds = new Set(["container"]);

/** Sets the kinds that take children, from the boot kinds table ([{kind, children}]). */
export function setChildKinds(kinds) {
  childKinds = new Set(kinds.filter((k) => k.children).map((k) => k.kind));
}

/** Whether nodes of kind t take children. */
export const holdsChildren = (t) => childKinds.has(t);

const hasKeys = (o) => !!o && Object.keys(o).length > 0;
const clone = (v) => (v === undefined ? undefined : structuredClone(v));

/** A fresh node id: "n" + 10 hex digits. */
export function newId() {
  const b = globalThis.crypto.getRandomValues(new Uint8Array(5));
  return "n" + Array.from(b, (x) => x.toString(16).padStart(2, "0")).join("");
}

/** Deep copy with fresh ids for the node and every descendant. */
export function freshCopy(n) {
  const walk = (x) => {
    const c = { ...clone(x), id: newId() };
    if (x.c) c.c = x.c.map(walk);
    return c;
  };
  return walk(n);
}

/** {node, parent (node|null), parentId, index, path, depth (1 = top)} or null. */
export function locate(doc, id) {
  const walk = (ns, parent, path) => {
    for (let i = 0; i < ns.length; i++) {
      const n = ns[i];
      const p = [...path, i];
      if (n.id === id) return { node: n, parent, parentId: parent ? parent.id : "", index: i, path: p, depth: p.length };
      const hit = n.c && walk(n.c, n, p);
      if (hit) return hit;
    }
    return null;
  };
  return walk(doc.nodes, null, []);
}

export const find = (doc, id) => locate(doc, id)?.node ?? null;

/** Children of parentId ("" = top level). */
export const siblings = (doc, parentId) => (parentId ? (find(doc, parentId)?.c ?? []) : doc.nodes);

/** Pre-order list of every node. */
export function flat(doc) {
  const out = [];
  const walk = (ns) => ns.forEach((n) => { out.push(n); if (n.c) walk(n.c); });
  walk(doc.nodes);
  return out;
}

/** The top-level ancestor (or self) of id. */
export const topOf = (doc, id) => {
  const l = locate(doc, id);
  return l ? doc.nodes[l.path[0]] : null;
};

/** Mirrors render.go: container(tag=section only) > one unstyled component. `navlabel` is only a row name. */
export function v1Shaped(n) {
  if (n.t !== "container" || hasKeys(n.s) || (n.c ?? []).length !== 1) return false;
  for (const [k, v] of Object.entries(n.p ?? {})) {
    if (k === "tag" ? v !== "section" : !(k === "hidden" || k === "v1" || k === "navlabel")) return false;
  }
  const c = n.c[0];
  return c.t === "component" && !hasKeys(c.s) && !(c.c ?? []).length;
}

/**
 * The node a canvas key moves as. A v1 section is drawn as its component, but the section is the
 * top-level container around it: moving the component alone would pull it out and leave an empty shell.
 */
export function moveSubject(doc, id) {
  const at = locate(doc, id);
  return at && at.node.t === "component" && at.depth === 2 && v1Shaped(at.parent) ? at.parentId : id;
}

const size = (n) => 1 + (n.c ?? []).reduce((a, c) => a + size(c), 0);
const contains = (n, id) => n.id === id || (n.c ?? []).some((c) => contains(c, id));

// New list for the children at `path` (a path of indexes; [] = top level).
function editList(nodes, path, fn) {
  if (!path.length) return fn(nodes);
  const [i, ...rest] = path;
  const out = nodes.slice();
  const n = { ...nodes[i] };
  const c = editList(nodes[i].c ?? [], rest, fn);
  if (c.length) n.c = c; else delete n.c;
  out[i] = n;
  return out;
}

const editNode = (doc, path, fn) => ({
  ...doc,
  nodes: editList(doc.nodes, path.slice(0, -1), (l) => l.map((n, i) => (i === path[path.length - 1] ? fn(n) : n))),
});

function insertAt(doc, parentId, index, node) {
  let path = [];
  if (parentId) {
    const p = locate(doc, parentId);
    if (!p) throw new Error(`parent ${parentId} not found`);
    if (!childKinds.has(p.node.t)) throw new Error(`${p.node.t} cannot hold other elements`);
    path = p.path;
  }
  const len = siblings(doc, parentId).length;
  if (!Number.isInteger(index) || index < 0 || index > len) throw new Error("index out of range");
  return { ...doc, nodes: editList(doc.nodes, path, (l) => [...l.slice(0, index), node, ...l.slice(index)]) };
}

function removeAt(doc, loc) {
  return { ...doc, nodes: editList(doc.nodes, loc.path.slice(0, -1), (l) => l.filter((_, i) => i !== loc.index)) };
}

// Throws unless the doc is within the doc.go limits and shaped like a valid tree.
function check(doc) {
  const seen = new Set();
  const walk = (ns, depth, parent) => {
    for (const n of ns) {
      if (!takesParentHover(parent) && hasParentHover(n.s)) throw new Error("Parent hover needs a parent");
      if (depth > MAX_DEPTH) throw new Error(`depth > ${MAX_DEPTH}`);
      if (typeof n.id !== "string" || !ID_RE.test(n.id)) throw new Error("bad node id");
      if (seen.has(n.id)) throw new Error(`duplicate id ${n.id}`);
      seen.add(n.id);
      if (seen.size > MAX_NODES) throw new Error(`more than ${MAX_NODES} nodes`);
      if ((n.c ?? []).length && !childKinds.has(n.t)) throw new Error(`${n.t} cannot hold other elements`);
      if (n.c) walk(n.c, depth + 1, n);
    }
  };
  walk(doc.nodes, 1, null);
  return doc;
}

/** Apply one op to doc. Returns {doc, inv}; throws (doc untouched) on an invalid op. */
export function applyOp(doc, op) {
  switch (op.op) {
    case "insert": {
      if (!op.node) throw new Error("insert without node");
      const node = clone(op.node);
      return { doc: check(insertAt(doc, op.parent ?? "", op.index, node)), inv: { op: "remove", id: node.id } };
    }
    case "remove": {
      const loc = locate(doc, op.id);
      if (!loc) throw new Error(`no such node ${op.id}`);
      return { doc: removeAt(doc, loc), inv: { op: "insert", parent: loc.parentId, index: loc.index, node: loc.node } };
    }
    case "move": {
      const loc = locate(doc, op.id);
      if (!loc) throw new Error(`no such node ${op.id}`);
      const parent = op.parent ?? "";
      if (parent && contains(loc.node, parent)) throw new Error("cannot move a node into itself");
      const out = insertAt(removeAt(doc, loc), parent, op.index, loc.node);
      return { doc: check(out), inv: { op: "move", id: op.id, parent: loc.parentId, index: loc.index } };
    }
    case "setProp": {
      const loc = locate(doc, op.id);
      if (!loc) throw new Error(`no such node ${op.id}`);
      const had = loc.node.p && Object.hasOwn(loc.node.p, op.key);
      const inv = { op: "setProp", id: op.id, key: op.key, value: had ? clone(loc.node.p[op.key]) : null };
      const value = op.value === undefined ? null : clone(op.value);
      return {
        inv,
        doc: editNode(doc, loc.path, (n) => {
          const p = { ...n.p };
          if (value === null) delete p[op.key]; else p[op.key] = value;
          const out = { ...n, p };
          if (!hasKeys(p)) delete out.p;
          return out;
        }),
      };
    }
    case "setStyle": {
      const loc = locate(doc, op.id);
      if (!loc) throw new Error(`no such node ${op.id}`);
      if (!okBp(op.bp)) throw new Error(`bad breakpoint ${op.bp}`);
      if (op.value !== null && typeof op.value !== "string") throw new Error("style value must be a string or null");
      const old = loc.node.s?.[op.bp]?.[op.key];
      const inv = { op: "setStyle", id: op.id, bp: op.bp, key: op.key, value: old ?? null };
      return {
        inv,
        doc: editNode(doc, loc.path, (n) => {
          const s = { ...n.s };
          const bp = { ...s[op.bp] };
          if (op.value === null) delete bp[op.key]; else bp[op.key] = op.value;
          if (hasKeys(bp)) s[op.bp] = bp; else delete s[op.bp];
          const out = { ...n, s };
          if (!hasKeys(s)) delete out.s;
          return out;
        }),
      };
    }
    case "setBind": {
      const loc = locate(doc, op.id);
      if (!loc) throw new Error(`no such node ${op.id}`);
      const value = op.value === undefined ? null : op.value;
      if (value !== null && (typeof value !== "string" || value === "")) throw new Error("bind source must be a non-empty string or null");
      const inv = { op: "setBind", id: op.id, key: op.key, value: loc.node.b?.[op.key] ?? null };
      return {
        inv,
        doc: editNode(doc, loc.path, (n) => {
          const b = { ...n.b };
          if (value === null) delete b[op.key]; else b[op.key] = value;
          const out = { ...n, b };
          if (!hasKeys(b)) delete out.b;
          return out;
        }),
      };
    }
    default:
      throw new Error(`unknown op ${op.op}`);
  }
}

// Apply ops in order: {doc, invs} with invs already reversed (ready to undo).
function applyAll(doc, ops) {
  const invs = [];
  for (const op of ops) {
    const r = applyOp(doc, op);
    doc = r.doc;
    invs.unshift(r.inv);
  }
  return { doc, invs };
}

/** A store value: {doc, past, future, applied}. Every function below returns a new one. */
export const createStore = (doc) => ({ doc, past: [], future: [], applied: [] });
export const canUndo = (store) => store.past.length > 0;
export const canRedo = (store) => store.future.length > 0;

/**
 * Run ops atomically as one history entry (a throw leaves the store as it was).
 * `applied` on the result lists the ops that ran, for the bridge planner.
 * coalesce: true merges a lone setProp on the same id+key within 1 s of the last entry
 * into it (typing), keeping the original inverse. A string is an explicit session key (a
 * slider or colour drag): every commit with the same key merges, however long the pause,
 * for any ops, so the drag is one undo step. setStyle with true never merges.
 */
export function commit(store, ops, { label = "", coalesce = false, now = Date.now() } = {}) {
  const { doc, invs } = applyAll(store.doc, ops);
  const session = typeof coalesce === "string";
  const ckey = session ? coalesce : coalesce && ops.length === 1 && ops[0].op === "setProp" ? `${ops[0].id}\u0000${ops[0].key}` : null;
  const last = store.past[store.past.length - 1];
  let past;
  if (ckey && last && last.ckey === ckey && (session || now - last.t <= COALESCE_MS)) {
    past = [...store.past.slice(0, -1), { ...last, fwd: ops, t: now }];
  } else {
    past = [...store.past, { label, inv: invs, fwd: ops, ckey, t: now }].slice(-MAX_HISTORY);
  }
  return { doc, past, future: [], applied: ops };
}

export function undo(store) {
  const e = store.past[store.past.length - 1];
  if (!e) return { ...store, applied: [] };
  const { doc } = applyAll(store.doc, e.inv);
  return { doc, past: store.past.slice(0, -1), future: [...store.future, { ...e, ckey: null }], applied: e.inv };
}

export function redo(store) {
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
export function jump(store, n) {
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

/** Insert a fresh-id copy of id right after it. */
export function duplicateOps(doc, id) {
  const loc = locate(doc, id);
  if (!loc) throw new Error(`no such node ${id}`);
  return [{ op: "insert", parent: loc.parentId, index: loc.index + 1, node: freshCopy(loc.node) }];
}

/** Insert an empty wrapper (kind, default container) where id is, then move id into it. */
export function wrapOps(doc, id, kind = "container") {
  const loc = locate(doc, id);
  if (!loc) throw new Error(`no such node ${id}`);
  const wrapper = { id: newId(), t: kind };
  return [
    { op: "insert", parent: loc.parentId, index: loc.index, node: wrapper },
    { op: "move", id, parent: wrapper.id, index: 0 },
  ];
}

/** Move a container's children out after it, then remove it. */
export function unwrapOps(doc, id) {
  const loc = locate(doc, id);
  if (!loc) throw new Error(`no such node ${id}`);
  const moves = (loc.node.c ?? []).map((c, k) => ({ op: "move", id: c.id, parent: loc.parentId, index: loc.index + 1 + k }));
  return [...moves, { op: "remove", id }];
}

/** True when setting key to value on node id would change nothing (null and absent are the same). */
export function unchanged(doc, id, key, value) {
  return (find(doc, id)?.p?.[key] ?? null) === value;
}

/** Canvas inline edits may only set a text or html prop the node's kind has. */
export function canEditText(kinds, node, prop) {
  return !!node && (prop === "text" || (prop === "html" && (node.t === "heading" || node.t === "text"))) && !!kinds.find((k) => k.kind === node.t)?.props?.some((p) => p.key === prop);
}

/** True when the canvas renders node's own text as an editable prop (heading, text, button; a bound text is not): Enter edits it, a container's descendant text is never taken for it. */
export function ownsText(node) {
  return !!node && (node.t === "heading" || node.t === "text" || node.t === "button") && !node.b?.text;
}
