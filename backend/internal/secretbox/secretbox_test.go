package secretbox

import (
	"bytes"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	k := DeriveKey("s", "d")
	blob, err := Seal(k, []byte("aad"), []byte("hunter2"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(blob, []byte("hunter2")) {
		t.Fatal("plaintext visible in blob")
	}
	got, err := Open(k, []byte("aad"), blob)
	if err != nil || string(got) != "hunter2" {
		t.Fatalf("Open = %q, %v", got, err)
	}
}

func TestOpenFails(t *testing.T) {
	k := DeriveKey("s", "d")
	blob, _ := Seal(k, []byte("aad"), []byte("x"))
	tampered := append([]byte(nil), blob...)
	tampered[len(tampered)-1] ^= 1
	cases := map[string]struct{ key, aad, blob []byte }{
		"wrong aad": {k, []byte("other"), blob},
		"wrong key": {DeriveKey("s2", "d"), []byte("aad"), blob},
		"wrong dom": {DeriveKey("s", "d2"), []byte("aad"), blob},
		"tampered":  {k, []byte("aad"), tampered},
		"too short": {k, []byte("aad"), []byte{1, 2}},
		"bad key":   {[]byte("short"), []byte("aad"), blob},
	}
	for name, c := range cases {
		if _, err := Open(c.key, c.aad, c.blob); err == nil {
			t.Errorf("%s: Open succeeded", name)
		}
	}
}

func TestNonceDiffers(t *testing.T) {
	k := DeriveKey("s", "d")
	a, _ := Seal(k, nil, []byte("x"))
	b, _ := Seal(k, nil, []byte("x"))
	if bytes.Equal(a, b) {
		t.Fatal("two seals identical")
	}
}
