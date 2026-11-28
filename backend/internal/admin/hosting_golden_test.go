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
		name  string
		svc   func() *fakeHosting
		query string
	}{
		{name: "settings-hosting-served", svc: servedHosting},
		{name: "settings-hosting-static", svc: func() *fakeHosting {
			f := staticHosting()
			at := time.Date(2026, 11, 20, 9, 30, 0, 0, time.UTC)
			f.runs = []HostingRun{
				{Started: at, Trigger: "manual", Pages: 214, Bytes: 38 << 20, Changed: 5, Duration: 9 * time.Second, Result: HostingRunOK},
				{Started: at.Add(-2 * time.Hour), Trigger: "change", Errors: 1, FirstError: "upload refused", Result: HostingRunFailed},
				{Started: at.Add(-26 * time.Hour), Trigger: "mode", Pages: 214, Bytes: 38 << 20, Changed: 214, Duration: 14 * time.Second, Result: HostingRunOK},
			}
			f.setup = HostingSetup{Saved: true, Target: HostingTargetS3, Site: "https://example.com", RedirectsTotal: 4, Reach: HostingCheckUnknown, HeaderNames: []string{"X-Content-Type-Options", "X-Frame-Options"}}
			return f
		}},
		{name: "settings-hosting-static-samehost", svc: func() *fakeHosting {
			f := staticHosting()
			f.view = HostingView{Mode: HostingStatic, Target: HostingTargetDir, Dir: "/srv/site/public"}
			at := time.Date(2026, 11, 20, 9, 30, 0, 0, time.UTC)
			f.runs = []HostingRun{
				{Started: at, Trigger: "manual", Pages: 214, Bytes: 38 << 20, Changed: 5, Duration: 9 * time.Second, Result: HostingRunOK},
			}
			f.live = []HostingIsland{{Label: "Forms", Route: "POST /forms/..", Status: "via proxy"}, {Label: "Search", Route: "/search", Status: "via proxy"}}
			f.setup = HostingSetup{
				Saved: true, Target: HostingTargetDir, SameHost: true, Site: "https://example.com", Upstream: "127.0.0.1:8090",
				Caddy:  "example.com {\n\t@hatamoto path /admin /admin/* /forms/*\n\thandle @hatamoto {\n\t\treverse_proxy 127.0.0.1:8090\n\t}\n\thandle {\n\t\troot * /srv/site/public\n\t\tfile_server\n\t}\n}\n",
				Nginx:  "server {\n\tserver_name example.com;\n\troot /srv/site/public;\n}\n",
				Folder: HostingCheckOK, Exact: 3, Wildcard: 1, RedirectsSplit: true, RedirectsTotal: 4, Reach: HostingCheckUnknown,
				HeaderNames: []string{"X-Content-Type-Options", "X-Frame-Options"},
			}
			return f
		}},
		{name: "settings-hosting-sftp", svc: sftpGoldenHosting},
		{name: "settings-hosting-sftp-apache", svc: sftpGoldenHosting, query: "?setup=apache"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := newHostingSaveRig(t, tc.svc())
			rec := p.do("GET", hostingURL+tc.query, "owner-token", nil, nil)
			if rec.Code != 200 {
				t.Fatalf("GET = %d", rec.Code)
			}
			body := hostingAgo.ReplaceAllString(normalizePage(rec.Body.String()), "AGO")
			for _, secret := range []string{testHostingSecret, testSFTPPassword, "sftp-test-password"} {
				if strings.Contains(body, secret) {
					t.Errorf("the page leaks %q", secret)
				}
			}
			compareGolden(t, "pages/"+tc.name+".html", body)
		})
	}
}

// sftpGoldenHosting is a static site on the SFTP target with a trusted host key and one redirect rule.
func sftpGoldenHosting() *fakeHosting {
	f := sftpHosting()
	f.view.SFTPHost, f.view.SFTPHostKey = "ssh.example.com", "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAtest"
	at := time.Date(2026, 11, 20, 9, 30, 0, 0, time.UTC) // fixed: the default runs move with the clock
	f.runs = []HostingRun{
		{Started: at, Trigger: "manual", Pages: 214, Bytes: 38 << 20, Changed: 5, Duration: 9 * time.Second, Result: HostingRunOK},
		{Started: at.Add(-2 * time.Hour), Trigger: "change", Errors: 1, FirstError: "upload refused", Result: HostingRunFailed},
	}
	f.setup = HostingSetup{
		Saved: true, Target: HostingTargetSFTP, Site: "https://example.com", Reach: HostingCheckUnknown,
		HostKey: f.view.SFTPHostKey, Remote: HostingCheckOK, RemoteDir: "public_html", HTAccess: true,
		HTAccessBlock: "# BEGIN Hatamoto\n<IfModule mod_alias.c>\nRedirectMatch 301 ^/old/?$ /new\n</IfModule>\n# END Hatamoto\n",
		Exact:         1, RedirectsSplit: true, RedirectsTotal: 1,
	}
	return f
}
