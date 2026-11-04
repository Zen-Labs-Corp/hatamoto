package tags

import (
	"regexp"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/consent"
)

// samples holds one accepted id per vendor.
var samples = map[string]string{
	"ga4":       "G-ABC123XYZ9",
	"gtm":       "GTM-ABC123",
	"gads":      "AW-123456789",
	"meta":      "123456789012345",
	"clarity":   "k1a2b3c4d5",
	"uet":       "12345678",
	"linkedin":  "1234567",
	"tiktok":    "C1A2B3D4E5F6G7H8I9J0",
	"pinterest": "2612345678901",
	"tawk":      "5f0a1b2c3d4e5f6a7b8c9d0e/1abcdefgh",
	"crisp":     "12345678-1234-1234-1234-123456789abc",
	"hubspot":   "1234567",
	"adsense":   "ca-pub-1234567890123456",
}

func TestThirteenVendorsWithUniqueIDs(t *testing.T) {
	t.Parallel()
	if len(Vendors) != 13 {
		t.Fatalf("vendors = %d, want 13", len(Vendors))
	}
	seen := map[string]bool{}
	for _, v := range Vendors {
		if seen[v.ID] {
			t.Errorf("duplicate vendor id %s", v.ID)
		}
		seen[v.ID] = true
		if got, ok := ByID(v.ID); !ok || got.ID != v.ID {
			t.Errorf("ByID(%s) failed", v.ID)
		}
	}
}

func TestPatternsAcceptSamples(t *testing.T) {
	t.Parallel()
	for _, v := range Vendors {
		if v.Pattern == nil {
			t.Errorf("%s: no pattern", v.ID)
			continue
		}
		s, ok := samples[v.ID]
		if !ok {
			t.Errorf("%s: no sample", v.ID)
			continue
		}
		if !v.ValidID(v.NormalizeID(s)) {
			t.Errorf("%s: sample %q rejected", v.ID, s)
		}
		for _, bad := range []string{"", "x", "G-<script>", s + "\n", " " + s + " x"} {
			if v.ValidID(bad) && bad != s {
				t.Errorf("%s: %q accepted", v.ID, bad)
			}
		}
		if !strings.HasPrefix(v.Pattern.String(), "^") || !strings.HasSuffix(v.Pattern.String(), "$") {
			t.Errorf("%s: pattern %s not anchored", v.ID, v.Pattern)
		}
	}
}

var hostRE = regexp.MustCompile(`^(https|wss)://(\*\.)?[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)

func TestHostsAreWellFormed(t *testing.T) {
	t.Parallel()
	for _, v := range Vendors {
		for name, list := range map[string][]string{"script": v.Script, "connect": v.Connect, "style": v.Style, "font": v.Font} {
			for _, h := range list {
				if !hostRE.MatchString(h) {
					t.Errorf("%s %s: bad host %q", v.ID, name, h)
				}
				if (name == "script" || name == "style" || name == "font") && strings.HasPrefix(h, "wss://") {
					t.Errorf("%s %s: wss host %q", v.ID, name, h)
				}
			}
		}
		if len(v.Script) == 0 && v.Src != "" {
			t.Errorf("%s: loads a script but allows no script host", v.ID)
		}
	}
}

func TestCategoriesAndTemplates(t *testing.T) {
	t.Parallel()
	ph := regexp.MustCompile(`\{[^}]*\}`)
	for _, v := range Vendors {
		ok := false
		for _, c := range consent.Categories {
			ok = ok || c == v.Category
		}
		if !ok {
			t.Errorf("%s: category %q not in consent.Categories", v.ID, v.Category)
		}
		if v.Boot == "" || v.Label == "" || v.IDLabel == "" {
			t.Errorf("%s: missing boot, label or id label", v.ID)
		}
		for _, tmpl := range []string{v.Src, v.Noscript} {
			for _, m := range ph.FindAllString(tmpl, -1) {
				if m != "{id}" {
					t.Errorf("%s: placeholder %s", v.ID, m)
				}
			}
		}
		if v.Src != "" && !strings.HasPrefix(v.Src, "https://") {
			t.Errorf("%s: Src not https", v.ID)
		}
	}
}

func TestExpandKeepsSlashEscapesRest(t *testing.T) {
	t.Parallel()
	if got := Expand("https://e/{id}", "abc/def"); got != "https://e/abc/def" {
		t.Errorf("got %s", got)
	}
	if got := Expand("https://e/?id={id}", "a b&c"); strings.ContainsAny(got[len("https://e/?id="):], " &") {
		t.Errorf("not escaped: %s", got)
	}
}
