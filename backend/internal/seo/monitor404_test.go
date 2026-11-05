package seo

import (
	"strings"
	"testing"

	"hatamoto.dev/core/internal/jsonx"
)

func TestMonitor404LegacyDecode(t *testing.T) {
	for _, doc := range []string{`{}`, `{"separator":"-","indexnow":{"enabled":true}}`} {
		s, err := Decode([]byte(doc))
		if err != nil {
			t.Fatal(err)
		}
		m := s.Monitor404
		if m.Disabled || m.Days() != 90 || len(m.IgnorePatterns) != 0 || !m.IsZero() {
			t.Fatalf("%s: %+v", doc, m)
		}
	}
}

func TestMonitor404DaysAndOmitZero(t *testing.T) {
	for in, want := range map[int]int{0: 90, 7: 90, 30: 30, 90: 90, 180: 180, 365: 365} {
		if got := (Monitor404{RetentionDays: in}).Days(); got != want {
			t.Errorf("Days(%d)=%d want %d", in, got, want)
		}
	}
	b, err := jsonx.Marshal(Settings{})
	if err != nil || strings.Contains(string(b), "monitor404") {
		t.Fatalf("zero value must be omitted: %s %v", b, err)
	}
}

func TestMonitor404CloneIndependent(t *testing.T) {
	s := Settings{Monitor404: Monitor404{IgnorePatterns: []string{"/a", "/b"}}}
	c := s.Clone()
	c.Monitor404.IgnorePatterns[0] = "/changed"
	if s.Monitor404.IgnorePatterns[0] != "/a" {
		t.Fatal("Clone shares the pattern slice")
	}
}

func TestValidateMonitor404(t *testing.T) {
	many := make([]string, MaxIgnorePatterns404+1)
	for i := range many {
		many[i] = "/x"
	}
	cases := []struct {
		name string
		m    Monitor404
		want string
	}{
		{"zero", Monitor404{}, ""},
		{"ok", Monitor404{RetentionDays: 30, IgnorePatterns: []string{"/wp-admin/*", "*.php"}}, ""},
		{"fifty", Monitor404{IgnorePatterns: many[:MaxIgnorePatterns404]}, ""},
		{"retention", Monitor404{RetentionDays: 7}, "monitor404[retention_days]"},
		{"too many", Monitor404{IgnorePatterns: many}, "monitor404[ignore_patterns]"},
		{"blank", Monitor404{IgnorePatterns: []string{"  "}}, "monitor404[ignore_patterns]"},
		{"no slash", Monitor404{IgnorePatterns: []string{"admin"}}, "monitor404[ignore_patterns]"},
		{"long", Monitor404{IgnorePatterns: []string{"/" + strings.Repeat("a", 200)}}, "monitor404[ignore_patterns]"},
		{"control", Monitor404{IgnorePatterns: []string{"/a\x00b"}}, "monitor404[ignore_patterns]"},
	}
	for _, c := range cases {
		errs := validateMonitor404(c.m)
		if c.want == "" {
			if len(errs) != 0 {
				t.Errorf("%s: %v", c.name, errs)
			}
			continue
		}
		if len(errs) == 0 || errs[0].Field != c.want {
			t.Errorf("%s: %v", c.name, errs)
		}
	}
}
