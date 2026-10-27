// History labels (a lazy chunk, loaded when idle): "Add Heading", "Delete Container", "Move Button",
// "Change Text colour (Tablet)". Derived from the ops and the document BEFORE the commit, so a removed
// element can still be named. Callers that pass a specific label ("Paste 3 elements") keep it.
import { find } from "@hm/builder/store";
import { BP_NAME, NAMES } from "@hm/builder/bp";

const GENERIC = new Set(["Add", "Move", "Remove", "Delete", "Duplicate", "Style", "Change"]);

/** Whether a label is missing or only a verb the derivation replaces ("Edit text" too). */
export const isGeneric = (l) => !l || GENERIC.has(l) || /^Edit \w+$/.test(l);

const cap = (s) => s.charAt(0).toUpperCase() + s.slice(1).replace(/_/g, " ");

/**
 * The label for ops committed on `before`. ctx = {kindName(kind), label} where label is the caller's own (generic) one.
 * The first op decides the verb; several elements of one kind of op read "<verb> N elements".
 */
export function derive(ops, before, { kindName, label = "" }) {
  const o = ops[0];
  if (!o) return label;
  const nameOf = (n) => (n ? kindName(n.t) : "element");
  const many = (verb, ids) => (ids.size > 1 ? `${verb} ${ids.size} elements` : null);
  const ids = (op) => new Set(ops.filter((x) => x.op === op).map((x) => x.id ?? x.node?.id));
  switch (o.op) {
    case "insert": {
      const verb = label === "Duplicate" ? "Duplicate" : "Add";
      return many(verb, ids("insert")) ?? `${verb} ${nameOf(o.node)}`;
    }
    case "remove":
      return many("Delete", ids("remove")) ?? `Delete ${nameOf(find(before, o.id))}`;
    case "move":
      return many("Move", ids("move")) ?? `Move ${nameOf(find(before, o.id))}`;
    case "setStyle": {
      const keys = new Set(ops.filter((x) => x.op === "setStyle").map((x) => x.key));
      if (keys.size > 1) return "Change style";
      return `Change ${NAMES[o.key] ?? o.key}${o.bp && o.bp !== "base" ? ` (${BP_NAME[o.bp] ?? o.bp})` : ""}`;
    }
    case "setProp":
      return `Change ${o.key === "text" ? "text" : cap(o.key).toLowerCase()}`;
    case "setBind":
      return `Bind ${cap(o.key).toLowerCase()}`;
    default:
      return label;
  }
}
