package packages

import (
	"bytes"
	"crypto/ed25519"
	"testing"

	"hatamoto.dev/sdk/registry"
)

func pinPrivate(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	priv, err := pinKey().PrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	return priv
}

// The public registry helper must sign exactly what the core signs.
func TestRegistryParitySignIndex(t *testing.T) {
	k := pinKey()
	priv := pinPrivate(t)

	got, err := registry.SignIndex([]byte(pinIndexInput), priv)
	if err != nil {
		t.Fatal(err)
	}
	core, err := SignIndex([]byte(pinIndexInput), k)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, core) {
		t.Fatalf("helper and core differ:\n got %s\ncore %s", got, core)
	}
	if want := readHeadRegistry(t); !bytes.Equal(got, want) {
		t.Fatalf("helper does not reproduce the HEAD envelope:\n got %s\nwant %s", got, want)
	}
	if _, err := pinTrust(t).VerifyIndex(got); err != nil {
		t.Fatalf("core refuses the helper's envelope: %v", err)
	}
}

func TestRegistryParityRawHTMLChars(t *testing.T) {
	priv := pinPrivate(t)
	idx := "{\"version\":1,\"issued_at\":\"2030-01-01T00:00:00Z\",\"expires_at\":\"2030-02-01T00:00:00Z\",\"packages\":[{\"description\":\"a & b < c > d \u2028 e\"}]}"
	doc, err := registry.SignIndex([]byte(idx), priv)
	if err != nil {
		t.Fatal(err)
	}
	core, err := SignIndex([]byte(idx), pinKey())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(doc, core) {
		t.Fatalf("helper and core differ:\n got %s\ncore %s", doc, core)
	}
	if _, err := pinTrust(t).VerifyIndex(doc); err != nil {
		t.Fatalf("core refuses the helper's envelope: %v", err)
	}
}

func TestRegistryParityKeyID(t *testing.T) {
	pub, err := pinKey().PublicKey()
	if err != nil {
		t.Fatal(err)
	}
	if registry.KeyID(pub) != KeyID(pub) {
		t.Fatalf("key id %q != %q", registry.KeyID(pub), KeyID(pub))
	}
}
