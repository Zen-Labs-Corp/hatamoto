// A query loop's own-attribute sorting and filters (pure, no DOM): the attributes a schema offers, the operators
// per attribute type and the filters a loop stores in p.filters. inspcontent.js draws them.

/** The most filters a query loop takes. */
export const MAX_FILTERS = 3;
/** The longest days value (about ten years). */
export const MAX_DAYS = 3650;
/** The longest text value, in characters. */
export const MAX_VALUE = 200;

/** The operators per attribute type: [stored op, label]. */
export const OPS = {
  date: [["from_today", "is today or later"], ["before_today", "is before today"], ["next_days", "is within the next days"]],
  number: [["eq", "is"], ["lt", "is less than"], ["lte", "is at most"], ["gt", "is more than"], ["gte", "is at least"]],
  enum: [["eq", "is"], ["ne", "is not"]],
  text: [["eq", "is"], ["ne", "is not"]],
};

/** The attributes of a schema; [] for an unknown one. */
export const attrsFor = (schemaAttrs, schema) => (Array.isArray(schemaAttrs?.[schema]) ? schemaAttrs[schema] : []);

/** The attributes a loop can sort by: date, number, text and enum ones. */
export const sortable = (attrs) => (attrs ?? []).filter((a) => a && OPS[a.type]);

/** The operators of an attribute type; [] for an unknown type. */
export const opsFor = (type) => OPS[type] ?? [];

/** The value control an operator needs: "none", "days", "number", "select" or "text". */
export function valueKind(type, op) {
  if (type === "date") return op === "next_days" ? "days" : "none";
  if (type === "number") return "number";
  if (type === "enum") return "select";
  return "text";
}

const num = (v) => (typeof v === "number" ? v : typeof v === "string" && v.trim() !== "" ? Number(v) : NaN);

function clean(row, byKey) {
  const a = byKey.get(row?.attr);
  if (!a || !opsFor(a.type).some(([op]) => op === row.op)) return null;
  const out = { attr: a.key, op: row.op };
  switch (valueKind(a.type, row.op)) {
    case "days": {
      const d = num(row.days);
      if (!Number.isInteger(d) || d < 0 || d > MAX_DAYS) return null;
      out.days = d;
      return out;
    }
    case "number": {
      const n = num(row.value);
      if (!Number.isFinite(n)) return null;
      out.value = n;
      return out;
    }
    case "none":
      return out;
    default:
      if (typeof row.value !== "string" || row.value === "" || row.value.length > MAX_VALUE) return null;
      out.value = row.value;
      return out;
  }
}

/** The filters to store: incomplete rows dropped, values shaped to the loop schema, at most MAX_FILTERS; null when none is left. */
export function cleanFilters(rows, attrs) {
  const byKey = new Map((attrs ?? []).map((a) => [a.key, a]));
  const out = [];
  for (const row of Array.isArray(rows) ? rows : []) {
    const f = clean(row, byKey);
    if (f) out.push(f);
    if (out.length === MAX_FILTERS) break;
  }
  return out.length ? out : null;
}

/** Whether a change of a loop's schema, Context or From leaves sort and filters that no longer fit: the attributes belong to the schema they were set for, and only a query loop takes them. */
export function staleAfterSchemaChange(before, after) {
  const same = (before?.schema ?? "") === (after?.schema ?? "") && !before?.context === !after?.context && (before?.from ?? "query") === (after?.from ?? "query");
  if (same) return false;
  const set = (p) => Boolean(p?.order_by) || (Array.isArray(p?.filters) && p.filters.length > 0);
  return set(before) || set(after);
}
