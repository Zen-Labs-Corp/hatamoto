package packages

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/config"
)

func newKey(t testing.TB) Key {
	t.Helper()
	k, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func trust(t testing.TB, ks ...Key) TrustSet {
	t.Helper()
	ts := TrustSet{}
	for _, k := range ks {
		pub, err := k.PublicKey()
		if err != nil {
			t.Fatal(err)
		}
		ts.Add(pub)
	}
	return ts
}

func parse(t *testing.T, raw []byte) *Package {
	t.Helper()
	p, err := Parse(raw, DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSignedPackageVerifies(t *testing.T) {
	k := newKey(t)
	raw, err := PackFiles(themeFiles(), &k)
	if err != nil {
		t.Fatal(err)
	}
	p := parse(t, raw)
	if p.Signature == nil || p.Signature.Signer != k.ID {
		t.Fatalf("signature = %+v", p.Signature)
	}
	if err := trust(t, k).Verify(p); err != nil {
		t.Fatal(err)
	}
	if p.Has(SignatureFile) {
		t.Fatal("signature.json must not be visible in the package files")
	}
}

func TestVerifyRefusals(t *testing.T) {
	k, other := newKey(t), newKey(t)
	signed, _ := PackFiles(themeFiles(), &k)

	t.Run("unsigned", func(t *testing.T) {
		raw, _ := PackFiles(themeFiles(), nil)
		if err := trust(t, k).Verify(parse(t, raw)); !errors.Is(err, ErrUnsigned) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("wrong signer", func(t *testing.T) {
		if err := trust(t, other).Verify(parse(t, signed)); !errors.Is(err, ErrUntrustedKey) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("bad signature: content changed after signing", func(t *testing.T) {
		files := themeFiles()
		files["assets/css/base.css"] = []byte("body{display:none}")
		sigOf := parse(t, signed).Signature
		files[SignatureFile] = []byte(`{"signer":"` + sigOf.Signer + `","algorithm":"ed25519","signature":"` + sigOf.Signature + `"}`)
		raw, err := zipFiles(files)
		if err != nil {
			t.Fatal(err)
		}
		if err := trust(t, k).Verify(parse(t, raw)); !errors.Is(err, ErrBadSignature) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("bad signature: forged by another key under the trusted id", func(t *testing.T) {
		forged, _ := PackFiles(themeFiles(), &other)
		p := parse(t, forged)
		p.Signature.Signer = k.ID
		if err := trust(t, k).Verify(p); !errors.Is(err, ErrBadSignature) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("garbage signature", func(t *testing.T) {
		p := parse(t, signed)
		p.Signature.Signature = "!!!"
		if err := trust(t, k).Verify(p); !errors.Is(err, ErrBadSignature) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("other algorithm", func(t *testing.T) {
		p := parse(t, signed)
		p.Signature.Algorithm = "rsa"
		if err := trust(t, k).Verify(p); err == nil {
			t.Fatal("expected a refusal")
		}
	})
}

func TestIndexEnvelope(t *testing.T) {
	k, other := newKey(t), newKey(t)
	doc, err := SignIndex([]byte(`{ "version": 1, "packages": [] }`), k)
	if err != nil {
		t.Fatal(err)
	}
	idx, err := trust(t, k).VerifyIndex(doc)
	if err != nil || string(idx) != `{"version":1,"packages":[]}` {
		t.Fatalf("VerifyIndex = %s, %v", idx, err)
	}
	if _, err := trust(t, other).VerifyIndex(doc); !errors.Is(err, ErrUntrustedKey) {
		t.Fatalf("wrong signer: %v", err)
	}
	tampered := bytes.Replace(doc, []byte(`[]`), []byte(`[{"name":"evil"}]`), 1)
	if bytes.Equal(tampered, doc) {
		t.Fatal("test did not tamper")
	}
	if _, err := trust(t, k).VerifyIndex(tampered); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("tampered: %v", err)
	}
	if _, err := trust(t, k).VerifyIndex([]byte(`{"index":{"version":1}}`)); !errors.Is(err, ErrUnsigned) {
		t.Fatalf("unsigned index: %v", err)
	}
}

func TestParseTrustSet(t *testing.T) {
	a, b := newKey(t), newKey(t)
	ts, err := ParseTrustSet(a.Public + ", " + b.Public + "\n")
	if err != nil || len(ts) != 2 || ts[a.ID] == nil {
		t.Fatalf("ts = %v, %v", ts, err)
	}
	if _, err := ParseTrustSet("not-a-key"); err == nil || !strings.Contains(err.Error(), "ed25519") {
		t.Fatalf("err = %v", err)
	}
}

func TestRegistryIndexMustBeFresh(t *testing.T) {
	now := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	r := &Registry{Now: func() time.Time { return now }}
	at := func(d time.Duration) string { return now.Add(d).Format(time.RFC3339) }
	day := 24 * time.Hour
	for name, tc := range map[string]struct {
		idx  Index
		want error
	}{
		"fresh":       {Index{IssuedAt: at(-day), ExpiresAt: at(10 * day)}, nil},
		"expired":     {Index{IssuedAt: at(-40 * day), ExpiresAt: at(-day)}, ErrIndexExpired},
		"no times":    {Index{}, ErrIndexNotFresh},
		"from future": {Index{IssuedAt: at(5 * day), ExpiresAt: at(10 * day)}, ErrIndexNotFresh},
		"huge window": {Index{IssuedAt: at(-day), ExpiresAt: at(900 * day)}, ErrIndexNotFresh},
		"backwards":   {Index{IssuedAt: at(day), ExpiresAt: at(-day)}, ErrIndexNotFresh},
	} {
		err := r.checkFresh(&tc.idx)
		if tc.want == nil && err != nil || tc.want != nil && !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", name, err, tc.want)
		}
	}
}

func TestRegistryRefusesPlainHTTPUnlessOverridden(t *testing.T) {
	r := &Registry{URL: "http://registry.test/registry.json"}
	if _, err := r.Fetch(context.Background()); !errors.Is(err, ErrInsecureURL) {
		t.Fatalf("err = %v", err)
	}
}

// The longest index BuildIndex will sign passes the freshness check servers
// apply, and values outside 1..180 days are refused instead of signing an index
// every server rejects.
func TestBuildIndexValidityRange(t *testing.T) {
	key := newKey(t)
	now := time.Now()
	day := 24 * time.Hour
	for _, d := range []time.Duration{0, -day, 181 * day} {
		if _, err := BuildIndex(t.TempDir(), "p/", key, now, d); err == nil {
			t.Errorf("a validity of %s must be refused", d)
		}
	}
	for _, d := range []time.Duration{day, 90 * day, 180 * day} {
		doc, err := BuildIndex(t.TempDir(), "p/", key, now, d)
		if err != nil {
			t.Fatalf("%s: %v", d, err)
		}
		raw, err := trust(t, key).VerifyIndex(doc)
		if err != nil {
			t.Fatal(err)
		}
		var idx Index
		if err := json.Unmarshal(raw, &idx); err != nil {
			t.Fatal(err)
		}
		if err := (&Registry{Now: func() time.Time { return now }}).checkFresh(&idx); err != nil {
			t.Errorf("an index valid for %s is rejected by servers: %v", d, err)
		}
	}
}

// A3: the default trust list (the first-party key) must reject a package signed
// by an unknown key, and accept it once that key is added to TrustedKeys.
func TestDefaultTrustRejectsUnknownSignerUntilAdded(t *testing.T) {
	k := newKey(t)
	raw, err := PackFiles(themeFiles(), &k)
	if err != nil {
		t.Fatal(err)
	}
	p := parse(t, raw)

	defaults, err := ParseTrustSet(config.PackageConfig{}.TrustedKeyList())
	if err != nil {
		t.Fatal(err)
	}
	if err := defaults.Verify(p); !errors.Is(err, ErrUntrustedKey) {
		t.Fatalf("a freshly generated key must be refused by default, err = %v", err)
	}

	added, err := ParseTrustSet(config.PackageConfig{TrustedKeys: k.Public}.TrustedKeyList())
	if err != nil {
		t.Fatal(err)
	}
	if err := added.Verify(p); err != nil {
		t.Fatalf("a key added to TrustedKeys must be accepted, err = %v", err)
	}
}

func TestRegistryErrorsDoNotLeakURLCredentials(t *testing.T) {
	for _, u := range []string{
		"http://user:pass@registry.test/registry.json", // refused scheme
		"user:pass@registry.test",                      // no scheme, unparsable host
	} {
		r := &Registry{URL: u}
		_, err := r.Fetch(context.Background())
		if err == nil {
			t.Fatalf("%s: want an error", u)
		}
		if strings.Contains(err.Error(), "user:pass") || strings.Contains(err.Error(), "pass") {
			t.Fatalf("error leaks credentials: %v", err)
		}
	}

	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	u := strings.Replace(srv.URL, "http://", "http://user:pass@", 1) + "/registry.json"
	r := &Registry{URL: u, AllowInsecure: true}
	_, err := r.Fetch(context.Background())
	if err == nil || strings.Contains(err.Error(), "pass") || strings.Contains(err.Error(), "user") {
		t.Fatalf("non-200 error = %v", err)
	}
}

func TestSignBytesRoundTrip(t *testing.T) {
	k, other := newKey(t), newKey(t)
	msg := []byte("any bytes, not a digest")
	sig, err := SignBytes(k, msg)
	if err != nil {
		t.Fatal(err)
	}
	ts := trust(t, k)
	if err := ts.VerifyBytes(sig.Signer, sig.Algorithm, sig.Signature, msg); err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if err := trust(t, other).VerifyBytes(sig.Signer, sig.Algorithm, sig.Signature, msg); !errors.Is(err, ErrUntrustedKey) {
		t.Fatalf("wrong key: got %v, want ErrUntrustedKey", err)
	}
	if err := ts.VerifyBytes(sig.Signer, "", sig.Signature, append([]byte("x"), msg...)); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("tampered: got %v, want ErrBadSignature", err)
	}
}

func TestNewSignatureWrapsRawSignature(t *testing.T) {
	k := newKey(t)
	priv, err := k.PrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	msg := []byte("signed elsewhere")
	sig := NewSignature(priv.Public().(ed25519.PublicKey), ed25519.Sign(priv, msg))
	if err := trust(t, k).VerifyBytes(sig.Signer, sig.Algorithm, sig.Signature, msg); err != nil {
		t.Fatal(err)
	}
}
