package backupcrypt

import (
	"errors"
	"strings"
	"testing"

	"filippo.io/age"
)

func TestSettingsMode(t *testing.T) {
	for _, c := range []struct {
		s    Settings
		want string
	}{
		{Settings{}, "off"},
		{Settings{Recipient: "age1x"}, "key"},
		{Settings{PassphraseSealed: []byte{1}}, "passphrase"},
	} {
		if got := c.s.Mode(); got != c.want {
			t.Errorf("Mode(%+v) = %q, want %q", c.s, got, c.want)
		}
	}
}

func TestSettingsKey(t *testing.T) {
	lowWork(t)
	keyA, keyB := SealKey("server-a"), SealKey("server-b")
	if k, err := (Settings{}).Key(keyA); k != nil || err != nil {
		t.Fatalf("off: %v, %v", k, err)
	}
	ident, _ := age.GenerateX25519Identity()
	k, err := Settings{Recipient: ident.Recipient().String()}.Key(keyA)
	if err != nil || k == nil {
		t.Fatalf("key mode: %v, %v", k, err)
	}
	const pass = "correct horse battery"
	sealed, err := SealPassphrase(keyA, pass)
	if err != nil {
		t.Fatal(err)
	}
	s := Settings{PassphraseSealed: sealed}
	if k, err := s.Key(keyA); err != nil || k == nil || !k.IsPassphrase() {
		t.Fatalf("passphrase mode: %v, %v", k, err)
	}
	_, err = s.Key(keyB)
	if !errors.Is(err, ErrUnreadable) || strings.Contains(err.Error(), pass) {
		t.Fatalf("other server key: %v", err)
	}
	if _, err := (Settings{Recipient: "garbage"}).Key(keyA); !errors.Is(err, ErrUnreadable) {
		t.Fatalf("garbage recipient: %v", err)
	}
}

func TestSealPassphraseRejectsShort(t *testing.T) {
	const short = "tooshort"
	_, err := SealPassphrase(SealKey("s"), short)
	if err == nil || strings.Contains(err.Error(), short) {
		t.Fatalf("err = %v", err)
	}
}
