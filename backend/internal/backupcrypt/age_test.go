package backupcrypt

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
)

const plainMarker = "secrets.env core.dump payload"

func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func lowWork(t *testing.T) {
	old := scryptWorkFactor
	scryptWorkFactor = 10
	t.Cleanup(func() { scryptWorkFactor = old })
}

func roundTrip(t *testing.T, k *Key, id *Identity) {
	t.Helper()
	dir := t.TempDir()
	src := writeFile(t, dir, "a.tar", plainMarker)
	enc := filepath.Join(dir, "a.tar.age")
	if err := k.EncryptFile(src, enc); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(enc)
	if !strings.HasPrefix(string(raw), Magic) || strings.Contains(string(raw), "secrets.env") {
		t.Fatal("ciphertext must start with the age header and hide the plaintext")
	}
	dst := filepath.Join(dir, "out.tar")
	if err := id.DecryptFile(enc, dst); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(dst); string(got) != plainMarker {
		t.Fatalf("round trip = %q", got)
	}
}

func TestRoundTripX25519(t *testing.T) {
	ident, _ := age.GenerateX25519Identity()
	k, err := ParseRecipient(" " + ident.Recipient().String() + "\n")
	if err != nil {
		t.Fatal(err)
	}
	id, err := ParseIdentityFile(strings.NewReader("# created\n# public key: x\n" + ident.String() + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	roundTrip(t, k, id)
}

func TestRoundTripPassphrase(t *testing.T) {
	lowWork(t)
	k, err := PassphraseKey("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	id, _ := PassphraseIdentity("correct horse battery")
	roundTrip(t, k, id)
}

func TestWrongKeyIsErrWrongKey(t *testing.T) {
	lowWork(t)
	ident, _ := age.GenerateX25519Identity()
	other, _ := age.GenerateX25519Identity()
	pass, _ := PassphraseKey("correct horse battery")
	xk, _ := ParseRecipient(ident.Recipient().String())
	wrongID, _ := ParseIdentityFile(strings.NewReader(other.String()))
	wrongPass, _ := PassphraseIdentity("another long passphrase")
	for name, c := range map[string]struct {
		k  *Key
		id *Identity
	}{"identity": {xk, wrongID}, "passphrase": {pass, wrongPass}} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			src := writeFile(t, dir, "a.tar", plainMarker)
			enc := filepath.Join(dir, "a.tar.age")
			if err := c.k.EncryptFile(src, enc); err != nil {
				t.Fatal(err)
			}
			dst := filepath.Join(dir, "out.tar")
			if err := c.id.DecryptFile(enc, dst); !errors.Is(err, ErrWrongKey) {
				t.Fatalf("err = %v, want ErrWrongKey", err)
			}
			if _, err := os.Stat(dst); !os.IsNotExist(err) {
				t.Fatal("dst must not exist after a failed decrypt")
			}
		})
	}
}

func TestParseRecipientRefusesPrivateKey(t *testing.T) {
	ident, _ := age.GenerateX25519Identity()
	for _, in := range []string{ident.String(), strings.ToLower(ident.String())} {
		_, err := ParseRecipient(in)
		if !errors.Is(err, ErrPrivateKey) {
			t.Fatalf("err = %v, want ErrPrivateKey", err)
		}
		if strings.Contains(err.Error(), in) {
			t.Fatal("error echoes the key")
		}
	}
	if _, err := ParseRecipient("nonsense"); err == nil || strings.Contains(err.Error(), "nonsense") {
		t.Fatalf("garbage err = %v", err)
	}
}

func TestParseIdentityFile(t *testing.T) {
	ident, _ := age.GenerateX25519Identity()
	if _, err := ParseIdentityFile(strings.NewReader("# c\n\n" + ident.String() + "\n# tail\n")); err != nil {
		t.Fatal(err)
	}
	_, err := ParseIdentityFile(strings.NewReader(ident.Recipient().String() + "\n"))
	if err == nil || !strings.Contains(err.Error(), "public key") {
		t.Fatalf("public-only err = %v", err)
	}
}

func TestIsEncryptedAndExclusiveDst(t *testing.T) {
	dir := t.TempDir()
	ident, _ := age.GenerateX25519Identity()
	k, _ := ParseRecipient(ident.Recipient().String())
	src := writeFile(t, dir, "a.tar", plainMarker)
	enc := filepath.Join(dir, "a.tar.age")
	if err := k.EncryptFile(src, enc); err != nil {
		t.Fatal(err)
	}
	short := writeFile(t, dir, "short", "age")
	for path, want := range map[string]bool{enc: true, src: false, short: false} {
		got, err := IsEncrypted(path)
		if err != nil || got != want {
			t.Errorf("IsEncrypted(%s) = %v, %v; want %v", filepath.Base(path), got, err, want)
		}
	}
	if err := k.EncryptFile(src, enc); err == nil {
		t.Fatal("EncryptFile must refuse an existing dst")
	}
	if _, err := os.Stat(enc); err != nil {
		t.Fatal("existing dst must survive a refused EncryptFile")
	}
}

func TestValidatePassphrase(t *testing.T) {
	if ValidatePassphrase(strings.Repeat("a", 11)) == nil {
		t.Error("11 chars accepted")
	}
	if err := ValidatePassphrase(strings.Repeat("a", 12)); err != nil {
		t.Error(err)
	}
}
