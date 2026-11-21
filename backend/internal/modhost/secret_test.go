package modhost

import (
	"bytes"
	"errors"
	"testing"

	"hatamoto.dev/core/internal/secretbox"
)

func TestModuleKey(t *testing.T) {
	t.Cleanup(Reset)
	if _, err := ModuleKey("x", "y"); !errors.Is(err, ErrNoSecret) {
		t.Fatalf("no source: %v", err)
	}
	SetSecretSource(func() (string, error) { return "s3cret", nil })
	k, err := ModuleKey("x", "y")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(k, secretbox.DeriveKey("s3cret", "module/x/y")) {
		t.Fatal("key is not under the module domain")
	}
	if bytes.Equal(k, secretbox.DeriveKey("s3cret", "hatamoto/spam/timing-token")) {
		t.Fatal("module key equals a core key")
	}
	for _, c := range [][2]string{{"X", "y"}, {"", "y"}, {"x", ""}, {"x", "../core"}, {"x", "A"}} {
		if _, err := ModuleKey(c[0], c[1]); err == nil {
			t.Errorf("ModuleKey(%q,%q) accepted", c[0], c[1])
		}
	}
	other, _ := ModuleKey("x", "z")
	if bytes.Equal(k, other) {
		t.Fatal("purposes share a key")
	}
}

func TestModuleKeySourceErrors(t *testing.T) {
	t.Cleanup(Reset)
	SetSecretSource(func() (string, error) { return "", errors.New("unreadable") })
	if _, err := ModuleKey("x", "y"); err == nil {
		t.Fatal("source error swallowed")
	}
	SetSecretSource(func() (string, error) { return "", nil })
	if _, err := ModuleKey("x", "y"); !errors.Is(err, ErrNoSecret) {
		t.Fatalf("empty secret: %v", err)
	}
}
