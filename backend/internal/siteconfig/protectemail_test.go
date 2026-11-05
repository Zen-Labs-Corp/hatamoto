package siteconfig

import "testing"

func TestParseProtectEmailDefaultsOn(t *testing.T) {
	for _, in := range []string{"", "null", "{}", "not json", `{"protectEmail":"x"}`, `{"protectEmail":true}`} {
		if !ParseProtectEmail([]byte(in)) {
			t.Errorf("%q: want on", in)
		}
	}
	if ParseProtectEmail([]byte(`{"protectEmail":false}`)) {
		t.Error("false must read as off")
	}
}

func TestMergeProtectEmailKeepsOtherKeys(t *testing.T) {
	raw, err := MergeProtectEmail([]byte(`{"colors":{"a":1},"contactButton":{"enabled":true}}`), false)
	if err != nil {
		t.Fatal(err)
	}
	if ParseProtectEmail(raw) {
		t.Error("merged value must be off")
	}
	if !ParseContactButton(raw).Enabled {
		t.Errorf("other keys lost: %s", raw)
	}
	raw, err = MergeProtectEmail(nil, true)
	if err != nil || !ParseProtectEmail(raw) {
		t.Errorf("empty document: %s %v", raw, err)
	}
	if _, err := MergeProtectEmail([]byte("[1]"), true); err == nil {
		t.Error("invalid document must fail")
	}
}
