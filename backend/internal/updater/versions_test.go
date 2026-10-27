package updater

import "testing"

func TestSameMajor(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"v1.4.2", "v1.9.0", true},
		{"v1.4.2", "v2.0.0", false},
		{"v10.0.0", "v1.0.0", false},
		{"v1.0.0-rc.1", "v1.0.0", true},
		{"dev", "v1.0.0", false},
		{"v1.0.0", "", false},
	} {
		if got := SameMajor(tc.a, tc.b); got != tc.want {
			t.Errorf("SameMajor(%q,%q) = %v", tc.a, tc.b, got)
		}
	}
}

func TestPinMatches(t *testing.T) {
	for _, tc := range []struct {
		pin, v string
		want   bool
	}{
		{"", "v3.0.0", true},
		{"v1.4", "v1.4.0", true},
		{"v1.4", "v1.4.17", true},
		{"v1.4", "v1.5.0", false},
		{"v1.4", "v1.40.0", false},
		{"v1.4", "v2.4.0", false},
		{"v1.4", "garbage", false},
		{"1.4", "v1.4.0", false},
	} {
		if got := PinMatches(tc.pin, tc.v); got != tc.want {
			t.Errorf("PinMatches(%q,%q) = %v", tc.pin, tc.v, got)
		}
	}
}
