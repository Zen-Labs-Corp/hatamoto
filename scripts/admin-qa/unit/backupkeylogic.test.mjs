// Age key pair glue, checked against the vendored curve and bech32 code.
// Run: node --test scripts/admin-qa/unit/backupkeylogic.test.mjs
import { register } from "node:module";
import { readFileSync } from "node:fs";
import { webcrypto } from "node:crypto";
import { test } from "node:test";
import assert from "node:assert/strict";

const assets = new URL("../../../backend/web/assets/", import.meta.url).href;
const manifest = JSON.parse(readFileSync(new URL("vendor.json", assets), "utf8"));
const map = Object.fromEntries(
  manifest.assets.flatMap((a) => Object.entries(a.importmap ?? {})).map(([k, v]) => [k, assets + v]),
);
register(
  "data:text/javascript," +
    encodeURIComponent(
      `const map=${JSON.stringify(map)};export async function resolve(s,c,next){if(map[s])return{url:map[s],shortCircuit:true};return next(s,c);}`,
    ),
);

const logic = await import("../../../backend/web/admin/js/backupkeylogic.js");
const { x25519 } = await import("@noble/curves/ed25519.js");

const hex = (s) => Uint8Array.from(s.match(/../g), (b) => parseInt(b, 16));
const toHex = (b) => Buffer.from(b).toString("hex");

const SECRET = "AGE-SECRET-KEY-1WEFSEYUAH90W6PF5EMZEL2F5PTN02MRLKENQVAK5XHCLREF2DWMSJ0C3QX"; // gitleaks:allow (test-only key)
const RECIPIENT = "age158ul58f6rupqf67spp5pvt9zf036kd7qtn3vmwwddcg66najya7sh6gwsr";

test("RFC 7748 section 5.2 single-iteration vectors", () => {
  assert.equal(
    toHex(
      x25519.scalarMult(
        hex("a546e36bf0527c9d3b16154b82465edd62144c0ac1fc5a18506a2244ba449ac4"),
        hex("e6db6867583030db3594c1a424b15f7c726624ec26b3353b10a903a6d0ab1c4c"),
      ),
    ),
    "c3da55379de9c6908e94ea4df28d084f32eccf03491c71f754b4075577a28552",
  );
  assert.equal(
    toHex(
      x25519.scalarMult(
        hex("4b66e9d4d1b4673c5ad22691957d6af5c11b6421e0ea01d42ca4169e7918ba0d"),
        hex("e5210f12786811d3f4b7959d0538ae2c31dbe7106fc03c3efc4cd549c715a493"),
      ),
    ),
    "95cbde9476e8907d7aade45cb4b873f88b595a68799fa152e6f8f7647aac7957",
  );
});

test("RFC 7748 section 6.1 public keys", () => {
  assert.equal(
    toHex(x25519.getPublicKey(hex("77076d0a7318a57d3c16c17251b26645df4c2f87ebc0992ab177fba51db92c2a"))),
    "8520f0098930a754748b7ddcb43ef75a0dbf3a0d26381af4eba4a98eaa9b4e6a",
  );
  assert.equal(
    toHex(x25519.getPublicKey(hex("5dab087e624a8a4b79e17f8b83800ee66f3bb1292618b6fd1c2f8b27ff88e0eb"))),
    "de9edb7d7b7dc1b4d35b61c2ece435373f8343c85b78674dadfc7e146f882b4f",
  );
});

test("a key pair made by the reference age implementation round-trips", () => {
  assert.equal(logic.recipientOf(SECRET), RECIPIENT);
  assert.equal(logic.encodeIdentity(logic.decodeIdentity(SECRET)), SECRET);
});

test("generateKeyPair has the age lengths and a valid identity", () => {
  const pair = logic.generateKeyPair((n) => Uint8Array.from({ length: n }, (_, i) => i + 1));
  assert.equal(pair.publicKey.length, 62);
  assert.ok(pair.publicKey.startsWith("age1"));
  assert.equal(pair.privateKey.length, 74);
  assert.ok(pair.privateKey.startsWith("AGE-SECRET-KEY-1"));
  assert.equal(logic.recipientOf(pair.privateKey), pair.publicKey);
});

test("generateKeyPair defaults to getRandomValues", () => {
  const a = logic.generateKeyPair();
  const b = logic.generateKeyPair();
  assert.notEqual(a.privateKey, b.privateKey);
});

test("decodeIdentity rejects a bad checksum and a recipient", () => {
  const last = SECRET.slice(-1) === "X" ? "Q" : "X";
  assert.throws(() => logic.decodeIdentity(SECRET.slice(0, -1) + last));
  assert.throws(() => logic.decodeIdentity(RECIPIENT));
});

test("the derived public key matches WebCrypto X25519", async (t) => {
  if (!webcrypto?.subtle) return t.skip("WebCrypto unavailable");
  const sk = webcrypto.getRandomValues(new Uint8Array(32));
  const pkcs8 = Buffer.concat([Buffer.from("302e020100300506032b656e04220420", "hex"), sk]);
  let jwk;
  try {
    const key = await webcrypto.subtle.importKey("pkcs8", pkcs8, { name: "X25519" }, true, ["deriveBits"]);
    jwk = await webcrypto.subtle.exportKey("jwk", key);
  } catch {
    return t.skip("X25519 not supported by this node");
  }
  assert.equal(toHex(x25519.getPublicKey(sk)), toHex(Buffer.from(jwk.x, "base64url")));
});

test("keyFileText has the age-keygen lines", () => {
  const pair = { publicKey: RECIPIENT, privateKey: SECRET };
  assert.equal(
    logic.keyFileText(pair, "2026-11-28T00:00:00.000Z"),
    `# created: 2026-11-28T00:00:00.000Z\n# public key: ${RECIPIENT}\n${SECRET}\n`,
  );
});
