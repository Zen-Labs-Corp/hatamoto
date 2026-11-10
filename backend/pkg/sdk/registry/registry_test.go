package registry

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"testing"
)

func testKey() ed25519.PrivateKey {
	seed := sha256.Sum256([]byte("registry-test-seed"))
	return ed25519.NewKeyFromSeed(seed[:])
}

func TestKeyID(t *testing.T) {
	pub := testKey().Public().(ed25519.PublicKey)
	sum := sha256.Sum256(pub)
	want := ""
	for _, b := range sum[:8] {
		want += string("0123456789abcdef"[b>>4]) + string("0123456789abcdef"[b&15])
	}
	if got := KeyID(pub); got != want || len(got) != 16 {
		t.Fatalf("KeyID = %q, want %q", got, want)
	}
}

func TestSignIndexRoundTrip(t *testing.T) {
	priv := testKey()
	in := []byte(`{ "version": 1, "issued_at": "2026-01-01T00:00:00Z", "expires_at": "2026-02-01T00:00:00Z",
	  "packages": [ {"kind":"plugin","name":"acme","version":"1.0.0","url":"a.hmp","sha256":"00","about":"a & b <c>","size":1000,"x":1e3} ] }`)
	doc, err := SignIndex(in, priv)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.HasSuffix(doc, []byte("\n")) {
		t.Fatal("trailing newline")
	}
	var raw struct {
		Index     jsontext.Value `json:"index"`
		Signer    string         `json:"signer"`
		Algorithm string         `json:"algorithm"`
		Signature string         `json:"signature"`
	}
	if err := jsonv2.Unmarshal(doc, &raw); err != nil {
		t.Fatal(err)
	}
	// The envelope shows the index indented; the signature covers its compact form.
	if err := raw.Index.Compact(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw.Index, []byte(`"about":"a \u0026 b \u003cc\u003e"`)) || !bytes.Contains(raw.Index, []byte(`"x":1e3`)) {
		t.Fatalf("index not compact/escaped or number rewritten: %s", raw.Index)
	}
	sig, err := base64.StdEncoding.DecodeString(raw.Signature)
	if err != nil {
		t.Fatal(err)
	}
	pub := priv.Public().(ed25519.PublicKey)
	if !ed25519.Verify(pub, raw.Index, sig) {
		t.Fatal("signature does not verify over the embedded index")
	}
	if raw.Signer != KeyID(pub) || raw.Algorithm != "ed25519" {
		t.Fatalf("signer/algorithm = %q/%q", raw.Signer, raw.Algorithm)
	}
	var idx Index
	if err := jsonv2.Unmarshal(raw.Index, &idx); err != nil || len(idx.Packages) != 1 || idx.Packages[0].Name != "acme" {
		t.Fatalf("index decode: %v %+v", err, idx)
	}
}

func TestSignIndexDeterministic(t *testing.T) {
	in := []byte(`{"version":1,"packages":[]}`)
	a, err := SignIndex(in, testKey())
	if err != nil {
		t.Fatal(err)
	}
	b, _ := SignIndex(in, testKey())
	if !bytes.Equal(a, b) {
		t.Fatal("output differs between runs")
	}
}

func TestSignIndexRefusals(t *testing.T) {
	if _, err := SignIndex([]byte(`{}`), ed25519.PrivateKey("short")); err == nil {
		t.Fatal("bad key length accepted")
	}
	if _, err := SignIndex([]byte(`{`), testKey()); err == nil {
		t.Fatal("invalid JSON accepted")
	}
}
