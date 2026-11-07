// Pure address parsing for the tag input. Mirrors validEmailList in
// backend/internal/admin/forms.go (net/mail ParseAddress, bare address only): a display name
// ("Name <a@b.c>") is not accepted. The server stays the authority; this is the early hint.
// net/mail takes any atext in the local part and the domain, and RFC 6532 UTF-8 (jörg@müller.de, a_b.example).
const ATEXT = "[A-Za-z0-9!#$%&'*+/=?^_`{|}~\\u0080-\\u{10FFFF}-]+";
const DOTATOM = `${ATEXT}(?:\\.${ATEXT})*`;
const ADDRESS = new RegExp(`^${DOTATOM}@${DOTATOM}$`, "u");

export function isEmail(s) {
  return s.length <= 254 && ADDRESS.test(s);
}

// Splits on commas, semicolons and whitespace; valid addresses are deduped case-insensitively.
export function parseAddresses(text) {
  const valid = [];
  const bad = [];
  const seen = new Set();
  for (const part of String(text).split(/[,;\s]+/)) {
    if (!part) continue;
    if (!isEmail(part)) {
      bad.push(part);
      continue;
    }
    const k = part.toLowerCase();
    if (seen.has(k)) continue;
    seen.add(k);
    valid.push(part);
  }
  return { valid, bad };
}
