package admin

import (
	"strings"
	"testing"
)

func TestStaticNoteFor(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		mode   string
		ingest bool
		want   bool
	}{
		{HostingServed, false, false},
		{HostingServed, true, false},
		{HostingStatic, true, false},
		{HostingStatic, false, true},
	} {
		n := staticNoteFor(tc.mode, tc.ingest)
		if (n != nil) != tc.want {
			t.Errorf("staticNoteFor(%q, %v) = %v, want note %v", tc.mode, tc.ingest, n, tc.want)
		}
		if n != nil && (n.Href != ingestDocsURL || !strings.Contains(n.Body, "not counted here")) {
			t.Errorf("note = %+v", n)
		}
	}
}

func TestCrawlersStaticWarning(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		svc    *fakeHosting
		ingest bool
		want   bool
	}{
		{"static, no ingest", staticHosting(), false, true},
		{"static, ingest", staticHosting(), true, false},
		{"served", servedHosting(), false, false},
		{"no service", nil, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g, _ := newAnalyticsRig(t)
			if tc.svc != nil {
				g.a.deps.Hosting = tc.svc
			}
			g.a.deps.AgentLogIngest = tc.ingest
			rec := g.do("GET", "/admin/analytics/crawlers", "owner-token", nil, nil)
			if rec.Code != 200 {
				t.Fatalf("status %d", rec.Code)
			}
			body := rec.Body.String()
			if got := strings.Contains(body, `id="w-cr-static"`); got != tc.want {
				t.Errorf("warning shown = %v, want %v", got, tc.want)
			}
			if tc.want && (!strings.Contains(body, "hm-callout--warning") || !strings.Contains(body, `href="`+ingestDocsURL+`"`)) {
				t.Error("warning lacks its tone or link")
			}
		})
	}
}
