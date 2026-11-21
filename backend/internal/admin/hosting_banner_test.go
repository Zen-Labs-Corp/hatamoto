package admin

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

const exportBannerMarker = "data-hm-export-banner"

func failedExportHosting() *fakeHosting {
	f := staticHosting()
	at := time.Date(2026, 11, 20, 9, 30, 0, 0, time.UTC)
	f.runs = []HostingRun{
		{Started: at, Trigger: "change", Errors: 1, FirstError: "upload refused", Result: HostingRunFailed},
		{Started: at.Add(-time.Hour), Trigger: "manual", Pages: 3, Result: HostingRunOK},
	}
	return f
}

func TestExportBannerOwnerPages(t *testing.T) {
	t.Parallel()

	for _, path := range []string{"/admin", hostingURL} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			p := newHostingRig(t, failedExportHosting())
			rec := p.do("GET", path, "owner-token", nil, nil)
			if rec.Code != 200 {
				t.Fatalf("GET = %d", rec.Code)
			}
			body := rec.Body.String()
			for _, w := range []string{exportBannerMarker, "Last export failed", "upload refused.", "Your previous export is still live.", "hm-callout--danger", `href="/admin/settings/hosting"`} {
				if !strings.Contains(body, w) {
					t.Errorf("page lacks %q", w)
				}
			}
			if strings.Count(body, exportBannerMarker) != 1 || strings.Contains(body, "data-hm-callout-dismiss") {
				t.Errorf("banner count %d or dismissible", strings.Count(body, exportBannerMarker))
			}
		})
	}
}

func TestExportBannerAbsent(t *testing.T) {
	t.Parallel()

	ok := func() *fakeHosting {
		f := failedExportHosting()
		f.runs = f.runs[1:]
		return f
	}
	running := func() *fakeHosting { // a running row never hides or shows the banner
		f := ok()
		f.runs = append([]HostingRun{{Started: time.Now(), Result: HostingRunRunning}}, f.runs...)
		return f
	}
	for _, tc := range []struct {
		name, path, token string
		svc               func() *fakeHosting
		noSvc             bool
	}{
		{"ok run", "/admin", "owner-token", ok, false},
		{"running over ok", "/admin", "owner-token", running, false},
		{"no run", "/admin", "owner-token", func() *fakeHosting { f := ok(); f.runs = nil; return f }, false},
		{"no service", "/admin", "owner-token", nil, true},
		{"editor dashboard", "/admin", "editor-token", failedExportHosting, false},
		{"served mode", "/admin", "owner-token", func() *fakeHosting { f := failedExportHosting(); f.view.Mode = HostingServed; return f }, false},
		{"other page", "/admin/settings/backups", "owner-token", failedExportHosting, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var f *fakeHosting
			if !tc.noSvc {
				f = tc.svc()
			}
			p := newHostingRig(t, f)
			if body := p.do("GET", tc.path, tc.token, nil, nil).Body.String(); strings.Contains(body, exportBannerMarker) {
				t.Error("banner shown")
			}
		})
	}

	t.Run("post re-render", func(t *testing.T) {
		t.Parallel()
		p := newHostingSaveRig(t, failedExportHosting())
		rec := p.do("POST", hostingURL, "owner-token", hostingForm(map[string][]string{"bucket": {""}}), nil)
		if rec.Code != 422 || strings.Contains(rec.Body.String(), exportBannerMarker) {
			t.Errorf("code %d, banner on a POST re-render", rec.Code)
		}
	})
	t.Run("read error", func(t *testing.T) {
		t.Parallel()
		f := failedExportHosting()
		p := newHostingRig(t, f)
		p.a.deps.Hosting = lastRunErr{f}
		if rec := p.do("GET", "/admin", "owner-token", nil, nil); rec.Code != 200 || strings.Contains(rec.Body.String(), exportBannerMarker) {
			t.Errorf("code %d, banner after a failed read", rec.Code)
		}
	})
}

// lastRunErr fails LastRun.
type lastRunErr struct{ *fakeHosting }

func (lastRunErr) LastRun(_ context.Context) (HostingRun, bool, error) {
	return HostingRun{}, false, errors.New("db down")
}

func TestExportBannerGolden(t *testing.T) {
	t.Parallel()

	p := newHostingRig(t, failedExportHosting())
	rec := p.do("GET", "/admin", "owner-token", nil, nil)
	compareGolden(t, "pages/banner-export_failed.html", normalizePage(rec.Body.String()))
}

// With no successful export ever, nothing is live and the banner must not say so.
func TestExportBannerNoPreviousExport(t *testing.T) {
	t.Parallel()

	f := failedExportHosting()
	f.runs = f.runs[:1]
	body := newHostingRig(t, f).do("GET", "/admin", "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, "Last export failed") || strings.Contains(body, "previous export is still live") {
		t.Errorf("banner wording wrong without an earlier ok run")
	}
}
