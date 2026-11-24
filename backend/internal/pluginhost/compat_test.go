package pluginhost

import (
	"context"
	"errors"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/pluginhost/plugintest"
	"hatamoto.dev/sdk"
)

func TestSDKRefusalOldHost(t *testing.T) {
	list := []string{"0.5.0", "0.6.0", "0.7.0", "0.8.0", "0.9.0", "0.10.0"}
	for _, tc := range []struct {
		rng    string
		refuse bool
	}{{"^0.12", true}, {"^0.11", false}, {"^0.10", false}, {"^0.5", false}} {
		err := sdkRefusal("gtm", tc.rng, "0.11.0", list, !tc.refuse, nil)
		if (err != nil) != tc.refuse {
			t.Errorf("%s: err = %v, refuse want %v", tc.rng, err, tc.refuse)
		}
		if err == nil {
			continue
		}
		want := `plugin "gtm" requires SDK "^0.12" but this host provides 0.11.0 (it serves plugins built for SDK 0.5 to 0.11)`
		if err.Error() != want {
			t.Errorf("message = %q, want %q", err, want)
		}
	}
}

func TestSDKRefusalEmptyList(t *testing.T) {
	err := sdkRefusal("p", "^99.0", "0.12.0", nil, false, nil)
	if err == nil || !strings.Contains(err.Error(), "SDK 0.12 to 0.12") {
		t.Fatalf("err = %v", err)
	}
}

func TestSDKRefusalInvalidRange(t *testing.T) {
	err := sdkRefusal("broken", "", "0.12.0", nil, false, errors.New("empty range"))
	if err == nil || !strings.Contains(err.Error(), `"broken"`) {
		t.Fatalf("err = %v, want one naming the plugin", err)
	}
}

// SDKRefusal must agree with Manifest.Compatible, the single accept rule for
// load and install. (The update preflight, updater.SDKBlockers, applies the
// same rule to a target release's version and list separately.)
func TestSDKRefusalMatchesCompatible(t *testing.T) {
	for _, rng := range []string{"^0.5", "^0.11", "^0.12", "^0.13", "^0.14", "^0.15", "^0.16", "^0.17", "^0.18", "^0.19", "^0.20", "^0.21", "^99.0", ">=0.11.2 <0.12.0", ""} {
		m := sdk.Manifest{Name: "p", SDK: rng}
		ok, err := m.Compatible()
		if refusal := SDKRefusal(m); (refusal == nil) != (err == nil && ok) {
			t.Errorf("%q: SDKRefusal = %v, Compatible = %v, %v", rng, refusal, ok, err)
		}
	}
}

func TestCommercialRangeRefusal(t *testing.T) {
	for _, tc := range []struct {
		license, rng string
		refuse       bool
	}{
		{sdk.LicenseCommercial, "^0.26", false},
		{sdk.LicenseCommercial, "^0.5", true},
		{sdk.LicenseCommercial, ">=0.25.0", true},
		{"", "^0.5", false},
	} {
		err := CommercialRangeRefusal(sdk.Manifest{Name: "pro", SDK: tc.rng, License: sdk.License(tc.license)})
		if (err != nil) != tc.refuse {
			t.Errorf("license %q sdk %q: err = %v, refuse want %v", tc.license, tc.rng, err, tc.refuse)
		}
	}
}

func TestPagePathPrefixesRefusal(t *testing.T) {
	for _, p := range []string{"/item/", "/stories/x/"} {
		if err := PagePathPrefixesRefusal(sdk.Manifest{Name: "board", PagePathPrefixes: []string{p}}); err != nil {
			t.Errorf("%s refused: %v", p, err)
		}
	}
	for _, p := range []string{"/", "/p/", "/admin/", "/api/v1/", "/uploads/", "/media/", "/theme/", "/-/", "/sitemap.xml/", "/robots.txt/", "/mcp/"} {
		if err := PagePathPrefixesRefusal(sdk.Manifest{Name: "board", PagePathPrefixes: []string{p}}); err == nil {
			t.Errorf("%s accepted", p)
		}
	}
}

func TestEntitySchemaRefusal(t *testing.T) {
	for _, s := range []string{"story", ""} {
		if err := EntitySchemaRefusal(sdk.Manifest{Name: "board", EntitySchema: s}); err != nil {
			t.Errorf("%q refused: %v", s, err)
		}
	}
	for _, s := range []string{"page", "post", "person", "block"} {
		if err := EntitySchemaRefusal(sdk.Manifest{Name: "board", EntitySchema: s}); err == nil {
			t.Errorf("%q accepted", s)
		}
	}
}

func TestContractRefusedAtLoad(t *testing.T) {
	r := newCapaRig(t)
	for want, mutate := range map[string]func(*sdk.Manifest){
		"/p/": func(m *sdk.Manifest) { m.PagePathPrefixes = []string{"/p/"} },
		"entity_schema": func(m *sdk.Manifest) {
			m.Permissions = append(m.Permissions, sdk.PermEntitiesWriteOwn)
			m.EntitySchema = "page"
		},
	} {
		st := r.rt.Load(context.Background(), capaManifest(mutate), plugintest.Build(t, "capa"))
		if st.Loaded || !strings.Contains(st.Error, want) {
			t.Errorf("%s: loaded=%v error=%q", want, st.Loaded, st.Error)
		}
	}
}
