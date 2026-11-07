// The Settings tab's pure logic, unit-tested: how a form's settings map to the controls and back,
// the subject-token filler (mirrors form.FillTokens in backend/internal/form/tokens.go) and the
// confirmation preview. No DOM here.

const TOKEN = /\{\{\s*([a-z][a-z0-9_]*)\s*\}\}/g;
const MAX_LINE = 200;
const DEFAULT_CONFIRMATION = "Thank you. We have received your submission.";

export const RETENTION_DAYS = [30, 90, 365];
/** The "Delete after" choices; 0 is Never (the key is absent). */
export const retentionOptions = [
  { days: 30, label: "30 days" },
  { days: 90, label: "90 days" },
  { days: 365, label: "1 year" },
  { days: 0, label: "Never" },
];

const BOOLEANS = ["enable_captcha", "confirmation_email", "no_ip"];

/**
 * Replaces {{field_name}} with the field's value in data. A token that names no field of the form,
 * or has no value, becomes nothing. One line, at most 200 characters, no CR or LF.
 */
export function fillTokens(tpl, fields, data) {
  const known = new Set((fields ?? []).map((f) => f.name));
  const out = String(tpl ?? "").replace(TOKEN, (_, name) => {
    const v = data?.[name];
    return known.has(name) && v !== undefined && v !== null ? String(v) : "";
  });
  return Array.from(out.replace(/[\r\n]+/g, " ").split(/\s+/).filter(Boolean).join(" ")).slice(0, MAX_LINE).join("");
}

/** The stored recipients string ("a@x.com, b@y.com") as a list of addresses. */
export function parseStoredRecipients(str) {
  return String(str ?? "")
    .split(/[,;\s]+/)
    .filter(Boolean);
}

/** The list as the stored string, or null (delete the key) when empty. */
export function joinRecipients(list) {
  return list.length ? list.join(", ") : null;
}

/** The value a setSettings op stores for a control's value; null deletes the key; undefined means the value is not valid yet. */
export function settingsPatch(key, value) {
  if (BOOLEANS.includes(key)) return Boolean(value);
  switch (key) {
    case "store_off":
    case "honeypot_off":
      return value ? null : true; // the control is "trap field on"; the key is inverted so absent keeps it on
    case "rate_limit_per_hour": {
      const s = String(value ?? "").trim();
      if (s === "") return null;
      if (!/^\d{1,4}$/.test(s)) return undefined;
      return Number(s) === 0 ? null : Number(s);
    }
    case "blocked_words": {
      const words = String(value ?? "")
        .split(/\r?\n/)
        .map((w) => w.trim())
        .filter(Boolean);
      return words.length ? words : null;
    }
    case "retention_days": {
      const n = Number(value);
      return n === 0 ? null : RETENTION_DAYS.includes(n) ? n : undefined;
    }
    case "notify_email":
      return Array.isArray(value) ? joinRecipients(value) : value === "" ? null : value;
    default: {
      const s = String(value ?? "");
      return s === "" ? null : s;
    }
  }
}

/** The inverse of settingsPatch: what a control shows for the stored settings. */
export function controlValue(key, settings) {
  const v = settings?.[key];
  if (BOOLEANS.includes(key)) return Boolean(v);
  switch (key) {
    case "store_off":
    case "honeypot_off":
      return !v;
    case "rate_limit_per_hour":
      return v ? String(v) : "";
    case "blocked_words":
      return Array.isArray(v) ? v.join("\n") : "";
    case "retention_days":
      return Number(v) || 0;
    case "notify_email":
      return parseStoredRecipients(v);
    default:
      return v ?? "";
  }
}

/** Whether a `data-hm-fs-when` block shows: "key" (the setting is on), "!key", or "@after:redirect" (a screen choice). */
export function whenMatches(expr, settings, ui = {}) {
  if (expr.startsWith("@")) {
    const [name, want] = expr.slice(1).split(":");
    return ui[name] === want;
  }
  const off = expr.startsWith("!");
  const key = off ? expr.slice(1) : expr;
  const on = controlValue(key, settings) === true;
  return off ? !on : on;
}

/** The sample answers the preview is filled from: the latest real submission, else each field's label. */
export function sampleData(fields, sample) {
  if (sample && Object.keys(sample).length) return sample;
  return Object.fromEntries((fields ?? []).map((f) => [f.name, f.label || f.name]));
}

/**
 * What the visitor's confirmation email looks like: To, Subject and the message, as the server sends
 * them (the subject and message are used as written; only the notification subject takes tokens).
 */
export function confirmationPreview(doc, sample) {
  const s = doc.settings ?? {};
  const email = (doc.fields ?? []).find((f) => f.type === "email");
  const subject = String(s.confirmation_subject ?? "").replace(/\s+/g, " ").trim() || `We received your submission — ${doc.name ?? ""}`;
  const message = String(s.confirmation_message ?? "").trim() || String(s.success_message ?? "").trim() || DEFAULT_CONFIRMATION;
  return { to: (email && sample?.[email.name]) || "visitor@example.com", subject, message };
}

/** True when two lists hold the same addresses in the same order. */
export const sameList = (a, b) => a.length === b.length && a.every((x, i) => x === b[i]);
