// Age X25519 key pairs, made in the browser with the vendored @noble/curves and @scure/base.
// The private key never leaves the page.
import { x25519 } from "@noble/curves/ed25519.js";
import { bech32 } from "@scure/base";

const RECIPIENT = "age";
const IDENTITY = "age-secret-key-";

// age strings are longer than bech32's 90-character default, so the limit is off (false).
export function encodeRecipient(pub) {
  return bech32.encode(RECIPIENT, bech32.toWords(pub), false);
}

export function encodeIdentity(sk) {
  return bech32.encode(IDENTITY, bech32.toWords(sk), false).toUpperCase();
}

// decodeIdentity throws on a bad checksum, mixed case or anything that is not an age private key.
export function decodeIdentity(s) {
  const { prefix, bytes } = bech32.decodeToBytes(String(s).trim(), false);
  if (prefix !== IDENTITY || bytes.length !== 32) throw new Error("not an age private key");
  return bytes;
}

export function recipientOf(identity) {
  return encodeRecipient(x25519.getPublicKey(decodeIdentity(identity)));
}

// x25519.getPublicKey clamps internally, so the raw 32 random bytes are the age secret.
export function generateKeyPair(random = (n) => crypto.getRandomValues(new Uint8Array(n))) {
  const sk = random(32);
  return { publicKey: encodeRecipient(x25519.getPublicKey(sk)), privateKey: encodeIdentity(sk) };
}

// The age-keygen file shape; `--identity` reads it.
export function keyFileText(pair, createdISO) {
  return `# created: ${createdISO}\n# public key: ${pair.publicKey}\n${pair.privateKey}\n`;
}
