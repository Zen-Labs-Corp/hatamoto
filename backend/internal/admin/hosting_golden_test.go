package admin

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

// The Last export line says how long ago the run was, which moves with the clock.
var hostingAgo = regexp.MustCompile(`(?:just now|\d+ (?:minutes?|hours?|days?) ago)`)

func TestHostingGolden(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		svc  func() *fakeHosting
	}{
		{"settings-hosting-served", servedHosting},
		{"settings-hosting-static", func() *fakeHosting {
			f := staticHosting()
			at := time.Date(2026, 11, 20, 9, 30, 0, 0, time.UTC)
			f.runs = []HostingRun{
				{Started: at, Trigger: "manual", Pages: 214, Bytes: 38 << 20, Changed: 5, Duration: 9 * time.Second, Result: HostingRunOK},
				{Started: at.Add(-2 * time.Hour), Trigger: "change", Errors: 1, FirstError: "upload refused", Result: HostingRunFailed},
				{Started: at.Add(-26 * time.Hour), Trigger: "mode", Pages: 214, Bytes: 38 << 20, Changed: 214, Duration: 14 * time.Second, Result: HostingRunOK},
			}
			return f
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := newHostingSaveRig(t, tc.svc())
			rec := p.do("GET", hostingURL, "owner-token", nil, nil)
			if rec.Code != 200 {
				t.Fatalf("GET = %d", rec.Code)
			}
			body := hostingAgo.ReplaceAllString(normalizePage(rec.Body.String()), "AGO")
			if strings.Contains(body, testHostingSecret) {
				t.Error("the page leaks the secret")
			}
			compareGolden(t, "pages/"+tc.name+".html", body)
		})
	}
}
