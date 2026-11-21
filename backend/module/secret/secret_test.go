package secret_test

import (
	"bytes"
	"errors"
	"testing"

	"hatamoto.dev/core/module/secret"
)

func TestSealOpen(t *testing.T) {
	t.Cleanup(secret.ResetForTest)
	if _, err := secret.Seal("x", "p", nil, []byte("v")); !errors.Is(err, secret.ErrNoSecret) {
		t.Fatalf("no source: %v", err)
	}
	secret.SetSourceForTest(func() (string, error) { return "server-secret", nil })
	blob, err := secret.Seal("x", "p", []byte("aad"), []byte("value"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := secret.Open("x", "p", []byte("aad"), blob)
	if err != nil || !bytes.Equal(got, []byte("value")) {
		t.Fatalf("round trip: %q %v", got, err)
	}
	for name, c := range map[string][3]string{
		"other module":  {"y", "p", "aad"},
		"other purpose": {"x", "q", "aad"},
		"other aad":     {"x", "p", "nope"},
	} {
		if _, err := secret.Open(c[0], c[1], []byte(c[2]), blob); err == nil {
			t.Errorf("%s opened the blob", name)
		}
	}
	if _, err := secret.Seal("Bad", "p", nil, nil); err == nil {
		t.Error("invalid module accepted")
	}
	if _, err := secret.Open("x", "../p", nil, blob); err == nil {
		t.Error("invalid purpose accepted")
	}
}
