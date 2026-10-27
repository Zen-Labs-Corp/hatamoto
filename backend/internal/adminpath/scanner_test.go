package adminpath

import (
	"context"
	"slices"
	"testing"
)

// Entries shorter than 4 characters fail the format rule first, so they are
// invalid rather than weak; the scanner matcher itself still knows them.
func TestScannerListAllWeak(t *testing.T) {
	for _, w := range ScannerList {
		if !onScannerList(w) {
			t.Errorf("%q not matched by the scanner matcher", w)
		}
		got := Assess(context.Background(), "/"+w, nil)
		if len(w) < minSegment {
			if got.Level != LevelInvalid {
				t.Errorf("/%s: level %v, want invalid (too short)", w, got.Level)
			}
			continue
		}
		if w == "auth" { // also a fixed reserved prefix, which ranks first
			if got.Level != LevelReserved {
				t.Errorf("/auth: level %v, want reserved", got.Level)
			}
			continue
		}
		if got.Level != LevelWeak {
			t.Errorf("/%s: level %v, want weak", w, got.Level)
			continue
		}
		if !hasCode(got, "scanner") {
			t.Errorf("/%s: no scanner reason: %+v", w, got.Reasons)
		}
	}
}

func TestWeakCandidates(t *testing.T) {
	for in, want := range map[string]string{
		"my-admin-panel2": "admin",
		"site-login":      "login",
		"admin2":          "admin",
		"login-2026":      "login",
		"my-cpanel2026":   "cpanel",
		"my-controlpanel": "controlpanel",
	} {
		if !slices.Contains(weakCandidates(in), want) {
			t.Errorf("weakCandidates(%q) = %q, want it to hold %q", in, weakCandidates(in), want)
		}
	}
}

func TestWordListSize(t *testing.T) {
	if len(wordSet) < 300 {
		t.Errorf("word list has %d entries", len(wordSet))
	}
}

func hasCode(r Result, code string) bool {
	for _, x := range r.Reasons {
		if x.Code == code {
			return true
		}
	}
	return false
}
