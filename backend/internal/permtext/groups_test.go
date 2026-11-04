package permtext

import (
	"slices"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
)

func titles(s []Statement) []string {
	var out []string
	for _, x := range s {
		out = append(out, x.Title)
	}
	return out
}

func TestGroup(t *testing.T) {
	cases := []struct {
		name     string
		perms    []string
		runs     bool
		proposes bool
		want     []string
		kinds    []Kind
	}{
		{"none", nil, false, false, nil, nil},
		{"own notes collapse", []string{sdk.PermSettings, sdk.PermLog, sdk.PermKV, sdk.PermDB}, false, false,
			[]string{"Keep its own notes", "Write to the server log"}, []Kind{KindOK, KindOK}},
		{"network", []string{sdk.PermHTTPFetch}, false, false,
			[]string{"Call other web addresses it lists"}, []Kind{KindOK}},
		{"runs", []string{sdk.PermLog}, true, false,
			[]string{"Write to the server log", "Prepare pages, posts, images, menus and redirects for you to review"}, []Kind{KindOK, KindReview}},
		{"proposes", nil, true, true,
			[]string{"Prepare pages, posts, images, menus and redirects for you to review", "Suggest site-wide changes (site title, link structure, home page)"}, []Kind{KindReview, KindReview}},
		{"unknown stays raw", []string{"future.perm"}, false, false, []string{"future.perm"}, []Kind{KindOK}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := Group(c.perms, c.runs, c.proposes)
			if got := titles(g.Can); !slices.Equal(got, c.want) {
				t.Fatalf("Can = %v, want %v", got, c.want)
			}
			for i, s := range g.Can {
				if s.Kind != c.kinds[i] {
					t.Errorf("Can[%d].Kind = %q, want %q", i, s.Kind, c.kinds[i])
				}
			}
			if len(g.Never) != 4 {
				t.Errorf("Never = %d items, want 4", len(g.Never))
			}
		})
	}
}

func TestGroupKeepsWarn(t *testing.T) {
	g := Group([]string{sdk.PermSiteScripts, sdk.PermDB}, false, false)
	if len(g.Can) != 2 || !g.Can[0].Warn || g.Can[1].Warn {
		t.Fatalf("warn flags wrong: %+v", g.Can)
	}
	if strings.HasPrefix(g.Can[0].Title, "WARNING") || strings.HasPrefix(g.Can[0].Sub, "WARNING") {
		t.Errorf("text keeps the prefix: %+v", g.Can[0])
	}
}

// A permission that reaches beyond the plugin's own data is never an "ok"
// sentence: the never list says plugins cannot run code on public pages, so a
// plugin that was granted that must show as something to review.
func TestGroupNeverShowsReachingPermissionsAsOK(t *testing.T) {
	for _, p := range []string{sdk.PermSiteScripts, sdk.PermHTTPFetchAny, sdk.PermBuilderIsland} {
		g := Group([]string{p}, false, false)
		if len(g.Can) != 1 || g.Can[0].Kind != KindReview || !g.Can[0].Warn || g.Can[0].Sub == "" {
			t.Errorf("%s: %+v, want one review statement with a warning and a detail line", p, g.Can)
		}
	}
	if g := Group([]string{sdk.PermSiteScripts}, false, false); !strings.Contains(g.Can[0].Title, "JavaScript on every public page") {
		t.Errorf("site.scripts title = %q", g.Can[0].Title)
	}
}

func TestNeverList(t *testing.T) {
	want := []string{
		"Change your live site directly",
		"Edit or delete pages you wrote",
		"Touch user accounts, passwords, email settings or your site address",
		"Run code on your public pages",
	}
	got := NeverList()
	if !slices.Equal(titles(got), want) {
		t.Fatalf("never = %v", titles(got))
	}
	for _, s := range got {
		if s.Kind != KindNo {
			t.Errorf("%q kind = %q", s.Title, s.Kind)
		}
	}
}
