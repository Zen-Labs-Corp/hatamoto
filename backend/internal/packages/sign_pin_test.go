package packages

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"os"
	"testing"
)

// pinKey is a throwaway deterministic signing key (ed25519 is deterministic: the
// same seed and message always give the same signature). It guards nothing.
func pinKey() Key {
	seed := sha256.Sum256([]byte("hatamoto-json-v2-4-pin"))
	priv := ed25519.NewKeyFromSeed(seed[:])
	pub := priv.Public().(ed25519.PublicKey)
	return Key{ID: KeyID(pub), Public: base64.StdEncoding.EncodeToString(pub), Private: base64.StdEncoding.EncodeToString(priv)}
}

func pinTrust(t *testing.T) TrustSet {
	t.Helper()
	pub, err := pinKey().PublicKey()
	if err != nil {
		t.Fatal(err)
	}
	ts := TrustSet{}
	ts.Add(pub)
	return ts
}

// pinIndexInput is the index JSON the HEAD envelope was signed from: escaped
// \u0026 \u003c \u2028, an exponent number and free whitespace.
const pinIndexInput = "{\n  \"version\": 1,\n  \"issued_at\": \"2030-01-01T00:00:00Z\",\n  \"expires_at\": \"2030-02-01T00:00:00Z\",\n  \"packages\": [ {\"kind\":\"plugin\",\"name\":\"hello\",\"version\":\"1.0.0\",\"url\":\"p/hello.hmp\",\"sha256\":\"0000000000000000000000000000000000000000000000000000000000000007\",\"description\":\"a \\u0026 b \\u003c c \\u003e d \\u2028 e  f\",\"size\":1e3} ]\n}\n"

// pinCompactIndex is Compact(pinIndexInput): the bytes that are signed.
const pinCompactIndex = `{"version":1,"issued_at":"2030-01-01T00:00:00Z","expires_at":"2030-02-01T00:00:00Z","packages":[{"kind":"plugin","name":"hello","version":"1.0.0","url":"p/hello.hmp","sha256":"0000000000000000000000000000000000000000000000000000000000000007","description":"a \u0026 b \u003c c \u003e d \u2028 e  f","size":1e3}]}`

func readHeadRegistry(t *testing.T) []byte {
	t.Helper()
	doc, err := os.ReadFile("testdata/head_registry.json")
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

// A registry.json signed by the previous encoder still verifies and hands back
// the same compact bytes.
func TestHeadSignedRegistryVerifies(t *testing.T) {
	raw, err := pinTrust(t).VerifyIndex(readHeadRegistry(t))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != pinCompactIndex {
		t.Fatalf("compact index changed:\n got %s\nwant %s", raw, pinCompactIndex)
	}
}

// Signing the same index with the same key reproduces the published envelope.
func TestSignIndexReproducesHeadEnvelope(t *testing.T) {
	doc, err := SignIndex([]byte(pinIndexInput), pinKey())
	if err != nil {
		t.Fatal(err)
	}
	if want := readHeadRegistry(t); !bytes.Equal(doc, want) {
		t.Fatalf("envelope changed:\n got %s\nwant %s", doc, want)
	}
}

// rawCase is false in the pin phase: at HEAD an index holding raw & < > U+2028
// is signed over bytes that differ from the embedded ones (the encoder escapes
// the embedded copy). The platform cutover fixes that and flips this to true.
const rawCase = true

func TestSignIndexRawHTMLCharsVerify(t *testing.T) {
	if !rawCase {
		t.Log("raw HTML characters in an index: enabled by the packages cutover")
		return
	}
	idx := "{\"version\":1,\"issued_at\":\"2030-01-01T00:00:00Z\",\"expires_at\":\"2030-02-01T00:00:00Z\",\"packages\":[{\"description\":\"a & b < c > d \u2028 e\"}]}"
	doc, err := SignIndex([]byte(idx), pinKey())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pinTrust(t).VerifyIndex(doc); err != nil {
		t.Fatalf("signed index does not verify: %v", err)
	}
}
