package pluginhost

import (
	"errors"
	"strings"
	"testing"

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
	for _, rng := range []string{"^0.5", "^0.11", "^0.12", "^0.13", "^0.14", "^0.15", "^99.0", ">=0.11.2 <0.12.0", ""} {
		m := sdk.Manifest{Name: "p", SDK: rng}
		ok, err := m.Compatible()
		if refusal := SDKRefusal(m); (refusal == nil) != (err == nil && ok) {
			t.Errorf("%q: SDKRefusal = %v, Compatible = %v, %v", rng, refusal, ok, err)
		}
	}
}
