package packages

import (
	"context"
	"errors"
	"testing"
)

func signedKit(t *testing.T, k Key) []byte {
	t.Helper()
	raw, err := PackFiles(kitFiles(kitExampleJSON), &k)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestInstallRefusesAKitBeforeAnyPolicyOrStore(t *testing.T) {
	k := newKey(t)
	// A zero Manager has no store and no trust: reaching either would panic or
	// refuse with another error.
	m := &Manager{}
	for _, src := range []Source{SourceUpload, SourceRegistry, SourceURL} {
		if _, err := m.Install(context.Background(), signedKit(t, k), InstallRequest{Source: src}); !errors.Is(err, ErrKitNotInstallable) {
			t.Fatalf("%s: err = %v, want ErrKitNotInstallable", src, err)
		}
	}
}

func TestVerifyKitTrusted(t *testing.T) {
	k := newKey(t)
	m := &Manager{Trust: trust(t, k)}
	p, origin, err := m.VerifyKit(context.Background(), signedKit(t, k), SourceUpload)
	if err != nil {
		t.Fatal(err)
	}
	if p.Kind != KindKit || p.Name != "community" {
		t.Fatalf("package = %s %s", p.Kind, p.Name)
	}
	if want := Classify("community", k.ID); origin != want {
		t.Fatalf("origin = %v, want %v", origin, want)
	}
}

func TestVerifyKitRefusals(t *testing.T) {
	k, other := newKey(t), newKey(t)
	ctx := context.Background()
	t.Run("untrusted key", func(t *testing.T) {
		m := &Manager{Trust: trust(t, other)}
		if _, _, err := m.VerifyKit(ctx, signedKit(t, k), SourceUpload); !errors.Is(err, ErrUntrustedKey) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("tampered", func(t *testing.T) {
		sig := parse(t, signedKit(t, k)).Signature
		files := kitFiles(kitMinJSON)
		files[SignatureFile] = []byte(`{"signer":"` + sig.Signer + `","algorithm":"ed25519","signature":"` + sig.Signature + `"}`)
		raw, err := zipFiles(files)
		if err != nil {
			t.Fatal(err)
		}
		m := &Manager{Trust: trust(t, k)}
		if _, _, err := m.VerifyKit(ctx, raw, SourceUpload); !errors.Is(err, ErrBadSignature) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("unsigned dev push with dev switched off", func(t *testing.T) {
		raw, err := PackFiles(kitFiles(kitMinJSON), nil)
		if err != nil {
			t.Fatal(err)
		}
		m := &Manager{Trust: trust(t, k)}
		if _, _, err := m.VerifyKit(ctx, raw, SourceDev); !errors.Is(err, ErrDevDisabled) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("signer not allowed", func(t *testing.T) {
		m := &Manager{Trust: trust(t, k, other), OnlySigners: []string{other.ID}}
		if _, _, err := m.VerifyKit(ctx, signedKit(t, k), SourceUpload); !errors.Is(err, ErrSignerNotAllowed) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("a plugin is not a kit", func(t *testing.T) {
		raw, err := PackFiles(pluginFiles(), &k)
		if err != nil {
			t.Fatal(err)
		}
		m := &Manager{Trust: trust(t, k)}
		if _, _, err := m.VerifyKit(ctx, raw, SourceUpload); !errors.Is(err, ErrNotAKit) {
			t.Fatalf("err = %v", err)
		}
	})
}
