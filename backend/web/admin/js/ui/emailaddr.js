// Pure address parsing for the tag input. Mirrors validEmailList in
// backend/internal/admin/forms.go (net/mail ParseAddress, bare address only): a display name
// ("Name <a@b.c>") is not accepted. The server stays the authority; this is the early hint.
const ATEXT = "[A-Za-z0-9!#$%&'*+/=?^_`{|}~-]+";
const LABEL = "[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?";
const ADDRESS = new RegExp(`^${ATEXT}(?:\\.${ATEXT})*@${LABEL}(?:\\.${LABEL})*$`);

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
