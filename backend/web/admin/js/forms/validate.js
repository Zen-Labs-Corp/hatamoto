// The designer's preview validator: a port of the server's form logic and validation
// (backend/internal/form/logic.go, validate.go), checked against the same fixture the Go
// tests use. Pure and DOM-free, no imports. Messages are the server's, label based.
// Rules the preview shows must be the rules a visitor meets, so change both together.

const MAX_ANSWER = 20000; // runes, when a field sets no longest answer
const MAX_HIDDEN = 500; // runes of a resolved hidden value

const runes = (s) => [...s].length;
const isObj = (v) => v !== null && typeof v === "object" && !Array.isArray(v);
const own = (o, k) => (isObj(o) && Object.hasOwn(o, k) ? o[k] : undefined);
const label = (f) => (f.label ? f.label : f.name);
const isLayout = (f) => f.type === "heading" || f.type === "paragraph" || f.type === "divider";

function isEmptyAnswer(v) {
  if (v === null || v === undefined) return true;
  if (typeof v === "string") return v.trim() === "";
  if (Array.isArray(v)) return v.length === 0;
  return false;
}

const emptyForLogic = (v) => (typeof v === "boolean" ? !v : isEmptyAnswer(v));

function logicText(v) {
  if (typeof v === "string") return v.trim();
  if (typeof v === "boolean") return String(v);
  if (typeof v === "number") return String(v);
  return "";
}

/** A text answer matches by trimmed equality, or substring when sub is set; a list matches when any member does. */
function answerHas(v, want, sub) {
  let list = Array.isArray(v) ? v : [v];
  if (Array.isArray(v) && v.length === 0) list = [""];
  return list.some((it) => {
    const s = logicText(it);
    return s === want || (sub && s.includes(want));
  });
}

function evalLogic(l, src) {
  switch (l.op) {
    case "filled":
      return !emptyForLogic(src);
    case "empty":
      return emptyForLogic(src);
    case "is":
      return answerHas(src, l.value ?? "", false);
    case "is_not":
      return !answerHas(src, l.value ?? "", false);
    case "contains":
      return answerHas(src, l.value ?? "", true);
  }
  return false;
}

/** {fieldId: shown} for every field with an id. A hidden source reads as empty; a logic cycle counts as hidden. */
export function visible(fields, data) {
  const byId = new Map();
  for (const f of fields) if (f.id) byId.set(f.id, f);
  const state = new Map(); // 1 running, 2 shown, 3 hidden
  const shownOf = (id) => {
    const st = state.get(id);
    if (st === 1 || st === 3) return false;
    if (st === 2) return true;
    const f = byId.get(id);
    state.set(id, 1);
    let show = true;
    if (f.logic) {
      let src;
      const s = byId.get(f.logic.field_id);
      if (s && shownOf(s.id)) src = own(data, s.name);
      show = evalLogic(f.logic, src);
    }
    state.set(id, show ? 2 : 3);
    return show;
  };
  const out = {};
  for (const id of byId.keys()) out[id] = shownOf(id);
  return out;
}

function resolveHidden(h, ctx) {
  if (!h) return "";
  let v = "";
  switch (h.source) {
    case "param":
      try {
        v = new URL(ctx.page_url ?? "", "http://x.invalid").searchParams.get(h.param ?? "") ?? "";
      } catch {
        v = "";
      }
      break;
    case "page":
      v = ctx.page_url ?? "";
      break;
    case "referrer":
      v = ctx.referrer ?? "";
      break;
    case "static":
      v = h.value ?? "";
      break;
  }
  v = v.trim();
  return runes(v) > MAX_HIDDEN ? [...v].slice(0, MAX_HIDDEN).join("") : v;
}

const ruleMessage = (f, def) => (typeof f.validation?.message === "string" && f.validation.message.trim() !== "" ? f.validation.message : def);
const hasOption = (opts, v) => (opts ?? []).some((o) => o.value === v);

// An address with no display name: dot-atoms on both sides of the @, where an atom char is ASCII atext or any
// non-ASCII character (the server's net/mail rules, narrowed to no quoted parts or comments). The domain may also
// be a literal: `[1.2.3.4]` (an IPv4 address, or an IPv4-mapped IPv6 one) or `[IPv6:...]` (any IP address).
const ATOM = "[A-Za-z0-9!#$%&'*+/=?^_`{|}~\\-]|[^\\x00-\\x7f]";
const DOT_ATOM = `(?:${ATOM})+(?:\\.(?:${ATOM})+)*`;
const EMAIL_RE = new RegExp(`^${DOT_ATOM}@(?:${DOT_ATOM}|\\[([\\x21-\\x5a\\x5e-\\x7e]+)\\])$`, "u");

const V4_RE = /^(?:(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)\.){3}(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)$/;

/** Go's net.ParseIP: "v4" for a dotted quad, "mapped" for an IPv6 address inside ::ffff:0:0/96, "v6" for any other IPv6 one, "" for no address. */
function ipKind(s) {
  if (V4_RE.test(s)) return "v4";
  if (!s.includes(":")) return "";
  const cut = s.lastIndexOf(":") + 1;
  let tail = null; // a trailing dotted quad stands for the last two groups
  if (s.slice(cut).includes(".")) {
    if (!V4_RE.test(s.slice(cut))) return "";
    const q = s.slice(cut).split(".").map(Number);
    tail = [q[0] * 256 + q[1], q[2] * 256 + q[3]];
    s = `${s.slice(0, cut)}0:0`;
  }
  const parts = s.split("::");
  if (parts.length > 2) return "";
  const side = (x) => (x === "" ? [] : x.split(":"));
  const [head, rest] = [side(parts[0]), side(parts[1] ?? "")];
  if (![...head, ...rest].every((x) => /^[0-9A-Fa-f]{1,4}$/.test(x))) return "";
  const n = head.length + rest.length;
  if (parts.length === 2 ? n > 7 : n !== 8) return "";
  const g = [...head, ...Array(parts.length === 2 ? 8 - n : 0).fill("0"), ...rest].map((x) => parseInt(x, 16));
  if (tail) g.splice(6, 2, ...tail);
  return g.slice(0, 5).every((x) => x === 0) && g[5] === 0xffff ? "mapped" : "v6";
}

function validEmail(s) {
  const m = EMAIL_RE.exec(s);
  if (!m || new TextEncoder().encode(s).length > 254) return false;
  const lit = m[1];
  if (lit === undefined) return true;
  if (lit.startsWith("IPv6:")) return ipKind(lit.slice(5)) !== "";
  const k = ipKind(lit);
  return k === "v4" || k === "mapped";
}
const NUM_RE = /^[+-]?(\d+\.?\d*|\.\d+)([eE][+-]?\d+)?$/;

function validDate(s) {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(s);
  if (!m) return false;
  const [y, mo, d] = [+m[1], +m[2], +m[3]];
  const t = new Date(Date.UTC(2000, mo - 1, d));
  t.setUTCFullYear(y);
  return mo >= 1 && mo <= 12 && t.getUTCFullYear() === y && t.getUTCMonth() === mo - 1 && t.getUTCDate() === d;
}

function checkText(f, lab, s) {
  const n = runes(s);
  switch (f.type) {
    case "email":
      if (!validEmail(s)) return `${lab} must be a valid email address`;
      break;
    case "url": {
      let u = null;
      try {
        // new URL also takes "https:example.com"; the server's parser leaves that without a host
        if (/^https?:\/\//i.test(s)) u = new URL(s);
      } catch {}
      if (!u || (u.protocol !== "http:" && u.protocol !== "https:") || u.host === "") return `${lab} must be a web address starting with http:// or https://`;
      break;
    }
    case "date":
      if (!validDate(s)) return `${lab} must be a date like 2026-01-31`;
      break;
  }
  const v = f.validation;
  if (!v) return n > MAX_ANSWER ? `${lab} is too long` : "";
  if (v.min_length != null && n < v.min_length) return ruleMessage(f, `${lab} must be at least ${v.min_length} characters`);
  if ((v.max_length != null && n > v.max_length) || (v.max_length == null && n > MAX_ANSWER)) return ruleMessage(f, `${lab} is too long`);
  if (v.pattern) {
    // The server matches the whole answer with RE2. JS syntax that RE2 lacks (or the reverse) fails to compile here,
    // and a pattern that does not compile adds no error, exactly as on the server.
    let re = null;
    try {
      re = new RegExp(`^(?:${v.pattern})$`, "u");
    } catch {}
    if (re && !re.test(s)) return ruleMessage(f, `${lab} is not in the expected format`);
  }
  return "";
}

function validateNumber(f, lab, raw) {
  let n;
  if (typeof raw === "number") n = raw;
  else if (typeof raw === "string" && NUM_RE.test(raw.trim())) n = Number(raw.trim());
  else return [null, `${lab} must be a number`];
  if (!Number.isFinite(n)) return [null, `${lab} must be a number`];
  const v = f.validation;
  if (v && ((v.min != null && n < v.min) || (v.max != null && n > v.max))) return [null, ruleMessage(f, `${lab} is out of range`)];
  return [n, ""];
}

function validateTickbox(f, lab, raw) {
  let ticked = false;
  if (typeof raw === "boolean") ticked = raw;
  else if (typeof raw === "string") ticked = !["", "0", "false", "off", "no"].includes(raw.trim().toLowerCase());
  else if (raw !== null && raw !== undefined) return [null, `${lab} must be yes or no`];
  if (!ticked) return [null, f.required ? `${lab} is required` : ""];
  return [true, ""];
}

function validateConsent(lab, raw) {
  if (raw === true) return ["Yes", ""];
  if (typeof raw === "string" && ["yes", "on", "true", "1"].includes(raw.trim().toLowerCase())) return ["Yes", ""];
  return [null, `${lab} is required`];
}

/** [value|null, message]: the normalised answer (null for none) or an error message. */
function validateAnswer(f, raw) {
  const lab = label(f);
  if (f.type === "consent") return validateConsent(lab, raw);
  if (f.type === "checkbox" && (f.options ?? []).length === 0) return validateTickbox(f, lab, raw);
  if (isEmptyAnswer(raw)) return [null, f.required ? `${lab} is required` : ""];
  switch (f.type) {
    case "select":
    case "radio":
      return typeof raw === "string" && hasOption(f.options, raw) ? [raw, ""] : [null, `Choose one of the listed options for ${lab}`];
    case "checkbox": {
      const list = Array.isArray(raw) ? raw : [raw];
      for (const it of list) if (typeof it !== "string" || !hasOption(f.options, it)) return [null, `Choose only the listed options for ${lab}`];
      return [[...list], ""];
    }
    case "number":
      return validateNumber(f, lab, raw);
  }
  if (typeof raw !== "string") return [null, `${lab} must be text`];
  const s = raw.trim();
  const msg = checkText(f, lab, s);
  return msg ? [null, msg] : [s, ""];
}

/**
 * Validate answers against the fields: {data, errors}. errors is keyed by field name; data is
 * null when there is any error. ctx is {page_url, referrer}; freeEmail the lowercase free-provider
 * domains (the caller passes them: the list is not embedded here).
 */
export function validate(fields, data, ctx = {}, freeEmail = []) {
  const vis = visible(fields, data);
  const clean = {};
  const errors = {};
  for (const f of fields) {
    if (isLayout(f) || !f.name) continue;
    if (f.id && !vis[f.id]) continue;
    if (f.type === "hidden") {
      const v = resolveHidden(f.hidden, ctx);
      if (v !== "") clean[f.name] = v;
      continue;
    }
    const [val, msg0] = validateAnswer(f, own(data, f.name));
    let msg = msg0;
    if (msg === "" && f.type === "email" && f.block_free_email && typeof val === "string") {
      const domain = val.slice(val.lastIndexOf("@") + 1).trim().toLowerCase();
      if (val.includes("@") && freeEmail.includes(domain)) msg = `${label(f)} must be a work email address`;
    }
    if (msg !== "") errors[f.name] = msg;
    else if (val !== null) clean[f.name] = val;
  }
  return Object.keys(errors).length ? { data: null, errors } : { data: clean, errors: {} };
}
