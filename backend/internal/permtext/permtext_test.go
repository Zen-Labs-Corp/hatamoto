package permtext

import (
	"slices"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
)

func TestEveryPermissionHasHelp(t *testing.T) {
	for _, p := range sdk.Permissions {
		if Lookup(p).Help == "" || Lookup(p).Help == p {
			t.Errorf("permission %q has no help text", p)
		}
	}
}

func TestWarnSet(t *testing.T) {
	var got []string
	for _, p := range sdk.Permissions {
		if tx := Lookup(p); tx.Warn {
			got = append(got, p)
			if tx.Short == "" {
				t.Errorf("%q warns without a short label", p)
			}
		}
	}
	slices.Sort(got)
	want := []string{"builder.island", "http.fetch.any", "site.scripts"}
	if !slices.Equal(got, want) {
		t.Fatalf("warn set = %v, want %v", got, want)
	}
}

func TestUnknownFallsBackToName(t *testing.T) {
	if got := Lookup("future.perm"); got != (Text{Help: "future.perm"}) {
		t.Fatalf("got %+v", got)
	}
}

func TestHelpEmptyForUnknown(t *testing.T) {
	if got := Help("future.perm"); got != "" {
		t.Fatalf("unknown Help = %q, want empty", got)
	}
	if got := Help(sdk.PermDB); got != "Keep its own data tables" {
		t.Fatalf("db Help = %q", got)
	}
}

func TestForCard(t *testing.T) {
	cases := []struct {
		name  string
		perms []string
		count int
		warn  []string
		warns []bool
	}{
		{"nil", nil, 0, nil, nil},
		{"plain", []string{"db"}, 1, nil, []bool{false}},
		{"two risky", []string{"site.scripts", "http.fetch.any"}, 2, []string{"JavaScript on every public page.", "Any web address."}, []bool{true, true}},
		{"duplicate short", []string{"site.scripts", "site.scripts"}, 2, []string{"JavaScript on every public page."}, []bool{true, true}},
		{"unknown", []string{"future.perm"}, 1, nil, []bool{false}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := ForCard(tc.perms)
			if c.Count != tc.count || !slices.Equal(c.Warn, tc.warn) || len(c.Lines) != len(tc.warns) {
				t.Fatalf("ForCard = %+v", c)
			}
			for i, l := range c.Lines {
				if l.Name != tc.perms[i] || l.Warn != tc.warns[i] || l.Text == "" || strings.HasPrefix(l.Text, "WARNING: ") {
					t.Errorf("line %d = %+v", i, l)
				}
			}
		})
	}
	if l := ForCard([]string{"future.perm"}).Lines[0]; l.Text != "future.perm" {
		t.Errorf("unknown text = %q", l.Text)
	}
}
