//go:build integration

package wpimport

import (
	"context"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/render"
)

func customCSSRunner(t *testing.T, d *Deps, mode SiteMode, css string) *Runner {
	t.Helper()
	r, err := NewRunner(d, nil, &Dump{Site: Site{URL: "https://old.example", Title: "Old"}, CustomCSS: css}, Options{AllowPrivate: true})
	if err != nil {
		t.Fatal(err)
	}
	r.state = &SiteState{Mode: mode}
	r.res.Report = &Report{CustomCSS: "found"}
	return r
}

func storedCSS(t *testing.T, d *Deps) string {
	t.Helper()
	d.Site.Invalidate()
	cfg, err := d.Site.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return render.AdditionalCSS(cfg.ThemeExtra)
}

func TestImportSettingsCustomCSS(t *testing.T) {
	ctx := context.Background()
	d, _ := newSEODeps(t)
	reset := func() {
		_, _ = d.DB.Pool().Exec(ctx, `UPDATE hatamoto_site_config SET theme_extra = theme_extra - 'additional_css'`)
		d.Site.Invalidate()
	}
	reset()
	t.Cleanup(reset)

	t.Run("empty site stores it", func(t *testing.T) {
		r := customCSSRunner(t, d, ModeEmpty, "a{color:red}")
		if err := r.importSettings(ctx); err != nil {
			t.Fatal(err)
		}
		if got := storedCSS(t, d); got != "a{color:red}" {
			t.Errorf("stored %q", got)
		}
		if got := r.res.Report.CustomCSS; got == "found" || got == "" {
			t.Errorf("report line %q was not updated", got)
		}
	})
	t.Run("refused CSS is kept only as a comment", func(t *testing.T) {
		reset()
		r := customCSSRunner(t, d, ModeEmpty, "@import url(//evil.example/x.css);")
		if err := r.importSettings(ctx); err != nil {
			t.Fatal(err)
		}
		got := storedCSS(t, d)
		if !strings.HasPrefix(got, "/* Skipped by the WordPress import") || render.ValidateCustomCSS(got) != nil {
			t.Errorf("stored %q", got)
		}
		if got := r.res.Report.CustomCSS; got == "found" || got == "" {
			t.Errorf("report line %q was not updated", got)
		}
	})
	t.Run("existing site keeps its CSS", func(t *testing.T) {
		reset()
		if err := d.Site.PatchThemeExtra(ctx, render.AdditionalCSSKey, []byte(`"b{color:blue}"`)); err != nil {
			t.Fatal(err)
		}
		r := customCSSRunner(t, d, ModeExisting, "a{color:red}")
		if err := r.importSettings(ctx); err != nil {
			t.Fatal(err)
		}
		if got := storedCSS(t, d); got != "b{color:blue}" {
			t.Errorf("stored %q, want the existing CSS kept", got)
		}
		if !strings.HasPrefix(r.res.Report.CustomCSS, "not imported") {
			t.Errorf("report line %q", r.res.Report.CustomCSS)
		}
		if len(r.res.Warnings) == 0 {
			t.Error("no warning for the kept CSS")
		}
	})
}
