package analytics

import "testing"

func TestDeviceClass(t *testing.T) {
	tests := map[string]string{
		"Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) Mobile/15E148":     "mobile",
		"Mozilla/5.0 (iPad; CPU OS 17_0 like Mac OS X)":                            "tablet",
		"Mozilla/5.0 (Linux; Android 14; Pixel 8) Chrome/120 Mobile Safari/537.36": "mobile",
		"Mozilla/5.0 (Linux; Android 13; SM-X700) Chrome/120 Safari/537.36":        "tablet",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) Safari/605.1.15":          "desktop",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/120":                     "desktop",
		"Mozilla/5.0 (X11; Linux x86_64) Firefox/120":                              "desktop",
		"":         "other",
		"curl/8.0": "other",
	}
	for ua, want := range tests {
		if got := DeviceClass(ua); got != want {
			t.Errorf("DeviceClass(%q) = %q, want %q", ua, got, want)
		}
	}
}
