// The forms designer's document and ops: a form definition {name, description, status, fields,
// settings} edited by immutable ops on the shared command log. Same envelope as the page store:
// applyOp(doc, op) -> {doc, inv}, nothing is mutated. Rules mirror ValidateDefinitionStrict
// (backend/internal/form/definition.go): at most 60 fields, names ^[a-z][a-z0-9_]{0,63}$.
//
// ops: addField{index, field} removeField{id} moveField{id, to} setField{id, key, value}
//      setSettings{key, value} setMeta{key, value} replaceField{id, field} batch{ops}
// A null value deletes the key. removeField also clears the logic of every field that listed the
// removed one as its source; its inverse is one batch that puts everything back.

import { commitLog } from "@hm/builder/cmdlog";
import { TYPES, isLayout } from "@hm/forms/types";

export const MAX_FIELDS = 60;
const META_KEYS = ["name", "description", "status"];

const clone = (v) => (v === undefined ? undefined : structuredClone(v));
const indexOf = (doc, id) => {
  const i = doc.fields.findIndex((f) => f.id === id);
  if (i < 0) throw new Error(`no such field ${id}`);
  return i;
};
const withKey = (o, key, value) => {
  const out = { ...o };
  if (value === null || value === undefined) delete out[key];
  else out[key] = clone(value);
  return out;
};
const prior = (o, key) => (o && Object.hasOwn(o, key) ? clone(o[key]) : null);

/** A fresh field id: "f_" + 10 hex digits. */
export function newId() {
  const b = globalThis.crypto.getRandomValues(new Uint8Array(5));
  return "f_" + Array.from(b, (x) => x.toString(16).padStart(2, "0")).join("");
}

export function applyOp(doc, op) {
  switch (op.op) {
    case "addField": {
      if (doc.fields.length >= MAX_FIELDS) throw new Error(`a form has at most ${MAX_FIELDS} fields`);
      if (doc.fields.some((f) => f.id === op.field.id)) throw new Error(`field id ${op.field.id} is taken`);
      const at = Math.min(Math.max(op.index, 0), doc.fields.length);
      const fields = [...doc.fields.slice(0, at), clone(op.field), ...doc.fields.slice(at)];
      return { doc: { ...doc, fields }, inv: { op: "removeField", id: op.field.id } };
    }
    case "removeField": {
      const at = indexOf(doc, op.id);
      const gone = doc.fields[at];
      const restore = [{ op: "addField", index: at, field: clone(gone) }];
      const fields = doc.fields
        .filter((f) => f.id !== op.id)
        .map((f) => {
          if (f.logic?.field_id !== op.id) return f;
          restore.push({ op: "setField", id: f.id, key: "logic", value: clone(f.logic) });
          return withKey(f, "logic", null);
        });
      return { doc: { ...doc, fields }, inv: restore.length === 1 ? restore[0] : { op: "batch", ops: restore } };
    }
    case "moveField": {
      const from = indexOf(doc, op.id);
      const rest = doc.fields.filter((f) => f.id !== op.id);
      const to = Math.min(Math.max(op.to, 0), rest.length);
      const fields = [...rest.slice(0, to), doc.fields[from], ...rest.slice(to)];
      return { doc: { ...doc, fields }, inv: { op: "moveField", id: op.id, to: from } };
    }
    case "setField": {
      if (op.key === "id") throw new Error("a field id never changes");
      const at = indexOf(doc, op.id);
      const inv = { op: "setField", id: op.id, key: op.key, value: prior(doc.fields[at], op.key) };
      const fields = doc.fields.map((f, i) => (i === at ? withKey(f, op.key, op.value) : f));
      return { doc: { ...doc, fields }, inv };
    }
    case "replaceField": {
      const at = indexOf(doc, op.id);
      const fields = doc.fields.map((f, i) => (i === at ? clone(op.field) : f));
      return { doc: { ...doc, fields }, inv: { op: "replaceField", id: op.field.id, field: clone(doc.fields[at]) } };
    }
    case "setSettings":
      return { doc: { ...doc, settings: withKey(doc.settings ?? {}, op.key, op.value) }, inv: { op: "setSettings", key: op.key, value: prior(doc.settings, op.key) } };
    case "setMeta": {
      if (!META_KEYS.includes(op.key)) throw new Error(`unknown form property ${op.key}`);
      return { doc: withKey(doc, op.key, op.value), inv: { op: "setMeta", key: op.key, value: prior(doc, op.key) } };
    }
    case "batch": {
      const invs = [];
      for (const o of op.ops) {
        const r = applyOp(doc, o);
        doc = r.doc;
        invs.unshift(r.inv);
      }
      return { doc, inv: { op: "batch", ops: invs } };
    }
    default:
      throw new Error(`unknown op ${op.op}`);
  }
}

export const { createStore, canUndo, canRedo, commit, undo, redo, jump } = commitLog({
  apply: applyOp,
  coalesceKey: (ops) => (ops.length === 1 && ops[0].op === "setField" ? `${ops[0].id}\u0000${ops[0].key}` : null),
});

/** A snake_case field name from a label (the server's name grammar), never empty. */
export function nameFrom(label) {
  let s = String(label ?? "")
    .normalize("NFKD")
    .replace(/[\u0300-\u036f]/g, "")
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "_")
    .replace(/^_+|_+$/g, "");
  if (!/^[a-z]/.test(s)) s = s ? "field_" + s : "field";
  return s.slice(0, 56).replace(/_+$/, "");
}

/** base, base_2, base_3 ... the first not already a name of fields. */
export function uniqueName(base, fields) {
  const taken = new Set(fields.map((f) => f.name));
  if (!taken.has(base)) return base;
  for (let n = 2; ; n++) {
    const name = `${base.slice(0, 58)}_${n}`;
    if (!taken.has(name)) return name;
  }
}

const options = (n) => Array.from({ length: n }, (_, i) => ({ label: `Option ${i + 1}`, value: `option_${i + 1}` }));

/** A new field of a type, with a fresh id and a name unique among fields. */
export function newField(type, fields) {
  const t = TYPES[type];
  if (!t) throw new Error(`unknown field type ${type}`);
  const f = { id: newId(), type, label: t.label, name: "", required: false, position: fields.length };
  switch (type) {
    case "heading":
      f.label = "";
      f.text = "Heading";
      return f;
    case "paragraph":
      f.label = "";
      f.text = "Add a few lines of text.";
      return f;
    case "divider":
      f.label = "";
      return f;
    case "select":
    case "radio":
    case "checkbox":
      f.options = options(2);
      break;
    case "consent":
      f.label = "I agree";
      break;
    case "hidden":
      f.hidden = { source: "static", value: "" };
      break;
    case "file":
      f.validation = { accept: ["pdf"] };
      break;
  }
  f.name = uniqueName(nameFrom(f.label), fields);
  return f;
}

/** The ops that insert a copy of a field right after it: fresh id, a unique name, no pinned name. */
export function duplicateOps(doc, id) {
  const at = indexOf(doc, id);
  const copy = clone(doc.fields[at]);
  copy.id = newId();
  if (!isLayout(copy.type)) copy.name = uniqueName(copy.name.replace(/_\d+$/, ""), doc.fields);
  delete copy.name_locked;
  return [{ op: "addField", index: at + 1, field: copy }];
}

/** The ops that set a field's label; its name follows the label until the name is pinned (name_locked) or it is a layout block. */
export function labelOps(doc, id, label) {
  const f = doc.fields[indexOf(doc, id)];
  const ops = [{ op: "setField", id, key: "label", value: label }];
  if (!isLayout(f.type) && !f.name_locked) {
    const others = doc.fields.filter((x) => x.id !== id);
    ops.push({ op: "setField", id, key: "name", value: uniqueName(nameFrom(label || TYPES[f.type].label), others) });
  }
  return ops;
}
