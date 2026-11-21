package packages

import (
	"context"
	"errors"
	"testing"

	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/sdk"
)

func commercialPackage(t *testing.T, sdkRange string, signer *Key) *Package {
	t.Helper()
	man, err := jsonx.Marshal(sdk.Manifest{Name: "audit-x", Version: "0.1.0", SDK: sdkRange, License: sdk.LicenseCommercial})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := PackFiles(map[string][]byte{PluginManifest: man, PluginWasm: []byte("\x00asm\x01\x00\x00\x00")}, signer)
	if err != nil {
		t.Fatal(err)
	}
	return parse(t, raw)
}

var allSources = []Source{SourceRegistry, SourceUpload, SourceURL, SourceDev}

func TestCommercialPolicy(t *testing.T) {
	ctx := context.Background()
	site, trusted := newKey(t), newKey(t)
	pub, err := site.PublicKey()
	if err != nil {
		t.Fatal(err)
	}
	yes := func(context.Context, string) bool { return true }
	no := func(context.Context, string) bool { return false }
	opts := func(key []byte, ent func(context.Context, string) bool) *Manager {
		return &Manager{Options: Options{Trust: trust(t, trusted), PluginKey: key, Entitled: ent, AllowDev: true}}
	}
	for _, tc := range []struct {
		name    string
		m       *Manager
		p       *Package
		wantErr error
	}{
		{"no plugin key", opts(nil, yes), commercialPackage(t, "^0.26", &site), ErrCommercialNotEntitled},
		{"entitled, site key", opts(pub, yes), commercialPackage(t, "^0.26", &site), nil},
		{"signed by a trusted key", opts(pub, yes), commercialPackage(t, "^0.26", &trusted), ErrCommercialSigner},
		{"not entitled", opts(pub, no), commercialPackage(t, "^0.26", &site), ErrCommercialNotEntitled},
		{"entitlement unset", opts(pub, nil), commercialPackage(t, "^0.26", &site), ErrCommercialNotEntitled},
		{"unsigned", opts(pub, yes), commercialPackage(t, "^0.26", nil), ErrCommercialSigner},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, src := range allSources {
				want := tc.wantErr
				if src == SourceDev {
					want = ErrCommercialNotEntitled
				}
				err := tc.m.CheckPolicy(ctx, tc.p, src)
				if !errors.Is(err, want) || (want == nil) != (err == nil) {
					t.Errorf("%s: err = %v, want %v", src, err, want)
				}
			}
		})
	}

	t.Run("message", func(t *testing.T) {
		err := opts(nil, yes).CheckPolicy(ctx, commercialPackage(t, "^0.26", &site), SourceUpload)
		if got, want := err.Error(), "audit-x: commercial plugin not entitled on this site"; got != want {
			t.Errorf("err = %q, want %q", got, want)
		}
	})
	t.Run("old sdk range", func(t *testing.T) {
		var pe *ParseError
		if err := opts(pub, yes).CheckPolicy(ctx, commercialPackage(t, "^0.5", &site), SourceUpload); !errors.As(err, &pe) {
			t.Errorf("err = %v, want *ParseError", err)
		}
	})
	t.Run("unsigned opt-in does not apply", func(t *testing.T) {
		// A nil store would panic if the unsigned opt-in were consulted.
		m := opts(pub, yes)
		if err := m.CheckPolicy(ctx, commercialPackage(t, "^0.26", nil), SourceUpload); !errors.Is(err, ErrCommercialSigner) {
			t.Errorf("err = %v, want ErrCommercialSigner", err)
		}
	})
	t.Run("install", func(t *testing.T) {
		raw, err := PackFiles(map[string][]byte{PluginManifest: mustManifest(t), PluginWasm: []byte("\x00asm\x01\x00\x00\x00")}, nil)
		if err != nil {
			t.Fatal(err)
		}
		m := &Manager{}
		if _, err := m.Install(ctx, raw, InstallRequest{Source: SourceUpload}); !errors.Is(err, ErrCommercialNotEntitled) {
			t.Errorf("err = %v, want ErrCommercialNotEntitled", err)
		}
	})
	t.Run("non-commercial keeps its path", func(t *testing.T) {
		raw, err := PackFiles(pluginFiles(), &trusted)
		if err != nil {
			t.Fatal(err)
		}
		m := opts(pub, no)
		if err := m.CheckPolicy(ctx, parse(t, raw), SourceUpload); err != nil {
			t.Errorf("err = %v", err)
		}
	})
}

func mustManifest(t *testing.T) []byte {
	t.Helper()
	b, err := jsonx.Marshal(sdk.Manifest{Name: "audit-x", Version: "0.1.0", SDK: "^0.26", License: sdk.LicenseCommercial})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestCommercialUsable(t *testing.T) {
	ctx := context.Background()
	site, other := newKey(t), newKey(t)
	pub, err := site.PublicKey()
	if err != nil {
		t.Fatal(err)
	}
	yes := func(context.Context, string) bool { return true }
	no := func(context.Context, string) bool { return false }
	row := func(edit func(*Row)) Row {
		r := Row{Kind: KindPlugin, Name: "audit-x", Source: SourceUpload, SignerKeyID: site.ID, Manifest: mustManifest(t)}
		if edit != nil {
			edit(&r)
		}
		return r
	}
	m := func(key []byte, ent func(context.Context, string) bool) *Manager {
		return &Manager{Options: Options{PluginKey: key, Entitled: ent, Trust: trust(t, other)}}
	}
	for _, tc := range []struct {
		name string
		m    *Manager
		r    Row
		want error
	}{
		{"entitled", m(pub, yes), row(nil), nil},
		{"no plugin key", m(nil, yes), row(nil), ErrCommercialNotEntitled},
		{"other signer", m(pub, yes), row(func(r *Row) { r.SignerKeyID = other.ID }), ErrCommercialSigner},
		{"developer row", m(pub, yes), row(func(r *Row) { r.Developer = true }), ErrCommercialNotEntitled},
		{"dev source", m(pub, yes), row(func(r *Row) { r.Source = SourceDev }), ErrCommercialNotEntitled},
		{"unsigned", m(pub, yes), row(func(r *Row) { r.SignerKeyID = "" }), ErrCommercialNotEntitled},
		{"not entitled", m(pub, no), row(nil), ErrCommercialNotEntitled},
		{"entitlement unset", m(pub, nil), row(nil), ErrCommercialNotEntitled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.m.usable(ctx, tc.r); !errors.Is(err, tc.want) || (tc.want == nil) != (err == nil) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}
	t.Run("non-commercial row keeps today's result", func(t *testing.T) {
		free := row(func(r *Row) { r.Manifest = []byte(pluginJSON); r.SignerKeyID = other.ID })
		if err := m(nil, nil).usable(ctx, free); err != nil {
			t.Errorf("trusted signer: %v", err)
		}
		free.SignerKeyID = site.ID
		if err := m(nil, nil).usable(ctx, free); !errors.Is(err, ErrUntrustedKey) {
			t.Errorf("untrusted signer: %v, want ErrUntrustedKey", err)
		}
	})
	t.Run("undecodable plugin manifest is refused", func(t *testing.T) {
		if err := m(pub, yes).usable(ctx, row(func(r *Row) { r.Manifest = []byte("{") })); err == nil {
			t.Error("an undecodable manifest must not be usable")
		}
	})
	t.Run("theme row is not commercial", func(t *testing.T) {
		if err := m(nil, nil).usable(ctx, Row{Kind: KindTheme, Name: "sunrise", Manifest: []byte("{")}); err != nil {
			t.Errorf("err = %v", err)
		}
	})
}

// Existing plugins may carry any license member (older hosts ignored it): Parse
// reads them, only ParseNew refuses a value other than "" or "commercial".
func TestLicenseValueIsStrictOnlyForNewInstalls(t *testing.T) {
	for _, tc := range []struct {
		name, license string
		new           bool
	}{
		{"empty", `""`, true},
		{"commercial", `"commercial"`, true},
		{"case variant", `"Commercial"`, false},
		{"spdx id", `"MIT"`, false},
		{"npm object", `{"type":"MIT"}`, false},
		{"array", `["MIT"]`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string][]byte{
				PluginManifest: []byte(`{"name":"hello","version":"1.0.0","sdk":"^0.26","license":` + tc.license + `}`),
				PluginWasm:     []byte("\x00asm\x01\x00\x00\x00"),
			}
			raw, err := PackFiles(files, nil)
			if err != nil {
				t.Fatal(err)
			}
			p, err := Parse(raw, DefaultLimits)
			if err != nil {
				t.Fatalf("Parse must keep reading it: %v", err)
			}
			if got := isCommercial(p); got != (tc.license == `"commercial"`) {
				t.Errorf("isCommercial = %v", got)
			}
			var pe *ParseError
			_, err = ParseNew(raw, DefaultLimits)
			if tc.new && err != nil || !tc.new && !errors.As(err, &pe) {
				t.Errorf("ParseNew err = %v, want ok=%v", err, tc.new)
			}
			row := Row{Kind: KindPlugin, Name: "hello", Manifest: p.Manifest}
			if handled, err := (&Manager{}).commercialUsable(context.Background(), row); handled != (tc.license == `"commercial"`) {
				t.Errorf("commercialUsable handled=%v err=%v", handled, err)
			}
		})
	}
}
