package admin

import (
	"regexp"
	"slices"
	"strings"
	"testing"
)

var (
	moduleScriptRx  = regexp.MustCompile(`<script type="module" nonce="[^"]*" src="/admin/_/js/([^."]+(?:/[^."]+)?)\.[0-9a-f]{12}\.js"(?: integrity="sha256-[^"]+")?></script>`)
	modulePreloadRx = regexp.MustCompile(`<link rel="modulepreload" href="/admin/_/js/([^."]+(?:/[^."]+)?)\.[0-9a-f]{12}\.js" integrity="sha256-[^"]+">`)
)

// moduleTags returns the <script type=module> and modulepreload module names
// of a page, read from the raw response so the nonce and integrity are real.
func moduleTags(body string) (scripts, preloads []string) {
	for _, m := range moduleScriptRx.FindAllStringSubmatch(body, -1) {
		scripts = append(scripts, m[1])
	}
	for _, m := range modulePreloadRx.FindAllStringSubmatch(body, -1) {
		preloads = append(preloads, m[1])
	}
	return
}

// Per page type: the module tags it carries. Editors boot on editor pages only.
func TestPagesLoadOnlyTheirModules(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	blog := newBlogRig(t)
	pages := []struct {
		name         string
		get          func() string
		scripts      []string // module scripts that must be present besides admin
		preloads     []string // modulepreloads that must be present
		absentPrefix []string // no module script or preload may start with these
	}{
		{"entities list", func() string { return c.do("GET", "/admin/entities", "owner-token", nil, nil).Body.String() },
			[]string{"content"}, []string{"admin", "components", "recovery", "content"}, []string{"editors", "blogeditor", "editor/", "canvas/", "repeater", "menutree", "charts"}},
		{"media list", func() string { return c.do("GET", "/admin/settings/media", "owner-token", nil, nil).Body.String() },
			[]string{"content"}, []string{"content"}, []string{"editors", "blogeditor", "editor/", "canvas/"}},
		{"blog editor", func() string { return blog.do("GET", blog.path(), "owner-token", nil, nil).Body.String() },
			[]string{"blogeditor", "repeater", "content"}, []string{"editor/autosave", "editor/slash", "editor/kit"}, []string{"editors", "canvas/"}},
	}
	for _, p := range pages {
		t.Run(p.name, func(t *testing.T) {
			body := p.get()
			scripts, preloads := moduleTags(body)
			if !slices.Contains(scripts, "admin") {
				t.Errorf("admin entry missing: %v", scripts)
			}
			for _, s := range p.scripts {
				if !slices.Contains(scripts, s) {
					t.Errorf("module script %s missing: %v", s, scripts)
				}
			}
			for _, s := range p.preloads {
				if !slices.Contains(preloads, s) {
					t.Errorf("modulepreload %s missing: %v", s, preloads)
				}
			}
			for _, name := range append(append([]string{}, scripts...), preloads...) {
				for _, bad := range p.absentPrefix {
					if strings.HasPrefix(name, bad) {
						t.Errorf("%s must not load %s", p.name, name)
					}
				}
			}
			if !strings.Contains(body, `<script type="importmap" nonce="`) {
				t.Error("import map missing")
			}
		})
	}
}
