// Binding helpers: pure op builders and source filtering for the Bind control.
// The vocabulary itself comes from the server (boot.bindSources); this only
// mirrors the class matrix, the server stays the authority. No DOM, no fetch.
// Run: node --test scripts/admin-qa/unit/builder-bind.test.mjs

// A pattern source ("entity.attributes.<path>") reads data of unknown shape.
const isPattern = (source) => source.endsWith(">");
const compatible = (cls, s) => cls === "text" || isPattern(s.source) || cls === s.class;

/** Bind a prop to a source: the literal is dropped in the same commit. */
export const bindOps = (id, key, source) => [
  { op: "setBind", id, key, value: source },
  { op: "setProp", id, key, value: null },
];

/** Remove the binding; the prop falls back to its literal (if any). */
export const unbindOps = (id, key) => [{ op: "setBind", id, key, value: null }];

/** The vocabulary rows a prop of class cls (text|url|image) may bind to. */
export const sourcesFor = (sources, cls, inLoop = true) => (sources || []).filter((s) => compatible(cls, s) && (inLoop || !s.source.startsWith("loop.item.")));

/** Whether the node id sits inside a loop (loop.item.* is only valid there). */
export function inLoop(doc, id) {
  const walk = (ns, inside) => {
    for (const n of ns) {
      if (n.id === id) return inside;
      const r = n.c && walk(n.c, inside || n.t === "loop");
      if (r !== undefined) return r;
    }
  };
  return walk(doc.nodes, false) === true;
}

/** The bound source of a prop, or "". */
export const chipText = (node, key) => node?.b?.[key] ?? "";
