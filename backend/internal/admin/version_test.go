package admin

import (
	"net/http"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin/a11y"
)

func TestDisplayAppVersion(t *testing.T) {
	t.Parallel()

	for raw, want := range map[string]string{
		"v1.4.2":          "v1.4.2",
		"1.4.2":           "v1.4.2",
		"vv1.4.2":         "v1.4.2",
		" v1.4.2 ":        "v1.4.2",
		"v1.5.0-rc.1":     "v1.5.0-rc.1",
		"":                "dev build",
		"dev":             "dev build",
		"(devel)":         "dev build",
		"v1.4.3-dev.7":    "dev build",
		"v1.5.0-rc.dev.2": "dev build",
		"v0.0.0-dev.0":    "dev build",
		"v1.4":            "dev build",
		"v1.4.2+abc":      "dev build",
		"main-abc123":     "dev build",
	} {
		if got := displayAppVersion(raw); got != want {
			t.Errorf("displayAppVersion(%q) = %q, want %q", raw, got, want)
		}
	}
	if got := (&View{}).AppVersion(); got != "dev build" {
		t.Errorf("hand-made View AppVersion = %q, want dev build", got)
	}
}

// colophonOf is the shell's colophon footer in body, "" when there is none.
func colophonOf(body string) string {
	i := strings.Index(body, `<footer class="hm-colophon">`)
	if i < 0 {
		return ""
	}
	j := strings.Index(body[i:], "</footer>")
	if j < 0 {
		return body[i:]
	}
	return body[i : i+j+len("</footer>")]
}

// topbarOf is the shell's top bar in body.
func topbarOf(body string) string {
	i := strings.Index(body, `<div class="hm-topbar">`)
	if i < 0 {
		return ""
	}
	j := strings.Index(body[i:], `<main id="hm-main"`)
	if j < 0 {
		return body[i:]
	}
	return body[i : i+j]
}

func TestShellColophon(t *testing.T) {
	t.Parallel()

	fu := &fakeUpdates{state: UpdatesState{Running: "1.4.2", History: []UpdatesHistoryRow{{When: t0, To: "1.4.2", Phase: "success"}}}}
	g := newRig(t, func(d *Deps) { d.AppVersion = "1.4.2"; d.Updates = fu })

	for _, tc := range []struct{ name, path, token string }{
		{"owner", "/admin/profile", "owner-token"},
		{"editor", "/admin/profile", "editor-token"},
		{"owner updates", "/admin/settings/updates", "owner-token"},
	} {
		rec := g.do("GET", tc.path, tc.token, nil, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d", tc.name, rec.Code)
		}
		body := rec.Body.String()
		col := colophonOf(body)
		for _, want := range []string{
			`<a href="https://hatamoto.dev" rel="noopener" target="_blank" title="Hatamoto v1.4.2">`,
			`Hatamoto v1.4.2 <span aria-hidden="true">&middot;</span> Stand close. Serve well.</a>`,
		} {
			if !strings.Contains(col, want) {
				t.Errorf("%s: colophon lacks %q:\n%s", tc.name, want, col)
			}
		}
		if i, j := strings.Index(body, "</main>"), strings.Index(body, `<footer class="hm-colophon">`); i < 0 || j < i {
			t.Errorf("%s: the colophon must follow </main>", tc.name)
		}
		top := topbarOf(body)
		if top == "" {
			t.Fatalf("%s: no top bar", tc.name)
		}
		if strings.Contains(top, "hm-updchip") || strings.Contains(body, "hm-updchip") || strings.Contains(body, "hm-topbar__chip") {
			t.Errorf("%s: the update chip is gone, but the page still has it:\n%s", tc.name, top)
		}
		if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
			t.Errorf("%s: a11y: %v", tc.name, issues)
		}
	}

	// Pages outside the shell carry no colophon.
	if rec := g.do("GET", "/admin/login", "", nil, nil); colophonOf(rec.Body.String()) != "" {
		t.Error("the sign-in page shows the colophon")
	}
}

func TestShellColophonDevBuild(t *testing.T) {
	t.Parallel()

	g := newRig(t, func(d *Deps) { d.AppVersion = "v1.4.3-dev.7" })
	rec := g.do("GET", "/admin/profile", "owner-token", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if col := colophonOf(rec.Body.String()); !strings.Contains(col, `title="Hatamoto dev build">Hatamoto dev build <span`) {
		t.Errorf("a dev build reads as one:\n%s", col)
	}
}
