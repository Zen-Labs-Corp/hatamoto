package server

import (
	"bytes"
	"context"
	"testing"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/oidcsignin"
)

type fakeSignInStore struct {
	set   oidcsignin.Settings
	found bool
}

func (f *fakeSignInStore) Get(context.Context) (oidcsignin.Settings, bool, error) {
	return f.set, f.found, nil
}

func (f *fakeSignInStore) Save(_ context.Context, s oidcsignin.Settings) error {
	if err := s.Validate(); err != nil {
		return err
	}
	f.set, f.found = s, true
	return nil
}

type fakeSignInProv struct{ invalidated int }

func (f *fakeSignInProv) Invalidate()                                      { f.invalidated++ }
func (f *fakeSignInProv) Check(context.Context, oidcsignin.Settings) error { return nil }

func TestSignInAdminSecret(t *testing.T) {
	ctx := context.Background()
	key := oidcsignin.SecretKey("server-secret")
	st, pr := &fakeSignInStore{}, &fakeSignInProv{}
	a := signInAdmin{store: st, key: key, prov: pr}
	in := admin.SignInOIDCInput{
		Enabled: true, Issuer: "https://idp.example.com", ClientID: "c", Secret: "s3cret",
		Domains: []string{"example.com"}, DefaultRole: "editor",
	}
	if err := a.Save(ctx, in, false); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(st.set.SecretSealed, []byte("s3cret")) {
		t.Fatal("secret stored in clear")
	}
	if got, ok := st.set.Secret(key); !ok || got != "s3cret" {
		t.Fatalf("stored secret does not open to the secret: %q %v", got, ok)
	}
	if pr.invalidated != 1 {
		t.Fatalf("invalidated %d times, want 1", pr.invalidated)
	}
	v, err := a.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if v.Secret != "" || !v.SecretSaved || v.SecretLost {
		t.Fatalf("view = %+v", v)
	}

	old := append([]byte(nil), st.set.SecretSealed...)
	in.Secret, in.Label = "", "Acme"
	if err := a.Save(ctx, in, true); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(st.set.SecretSealed, old) || st.set.Label != "Acme" {
		t.Fatal("keepSecret must keep the sealed secret and save the rest")
	}

	other := signInAdmin{store: st, key: oidcsignin.SecretKey("changed"), prov: pr}
	if v, _ = other.Get(ctx); !v.SecretLost || v.SecretSaved {
		t.Fatalf("a secret sealed under another key must read as lost: %+v", v)
	}
	if err := other.Save(ctx, in, true); err == nil {
		t.Fatal("keeping an unreadable secret must fail")
	}
}
