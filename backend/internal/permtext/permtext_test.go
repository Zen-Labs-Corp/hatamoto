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

func TestCSPLines(t *testing.T) {
	if got := CSPLines(nil); got != nil {
		t.Fatalf("nil csp: %v", got)
	}
	got := CSPLines(&sdk.CSP{ImgSrc: []string{"https://*.example.com"}, ScriptSrc: []string{"https://js.example.com"}})
	if len(got) != 2 || got[0].Name != "Scripts" || got[1].Name != "Images" || got[1].Hosts[0] != "https://*.example.com" {
		t.Fatalf("lines = %+v", got)
	}
}

func TestContentTypesLine(t *testing.T) {
	for _, c := range []struct {
		in   []string
		want string
	}{
		{nil, ""},
		{[]string{"Product"}, "Create the content type Product"},
		{[]string{"Product", "Order"}, "Create the content types Product and Order"},
		{[]string{"A", "B", "C"}, "Create the content types A, B and C"},
	} {
		if got := ContentTypesLine(c.in); got != c.want {
			t.Errorf("ContentTypesLine(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestTagLinesSayItRunsNoCode(t *testing.T) {
	ls := TagLines("Site ID", []string{"a.example", "*.b.example"}, "marketing", true)
	if len(ls) != 3 {
		t.Fatalf("lines = %+v", ls)
	}
	for _, want := range []string{"runs no code", "a.example, *.b.example", "WebSocket"} {
		if !strings.Contains(ls[0].Text, want) {
			t.Errorf("script line lacks %q: %s", want, ls[0].Text)
		}
	}
	if !strings.Contains(ls[1].Text, "Marketing") || !strings.Contains(ls[2].Text, "Site ID") {
		t.Errorf("lines = %+v", ls)
	}
	if got := TagLines("", []string{"a.example"}, "analytics", false); len(got) != 2 || strings.Contains(got[0].Text, "WebSocket") {
		t.Errorf("minimal = %+v", got)
	}
}
