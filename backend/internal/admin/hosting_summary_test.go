package admin

import (
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/admin/a11y"
)

func TestHostingLast(t *testing.T) {
	t.Parallel()

	at := time.Now().Add(-2 * time.Minute)
	ok := HostingRun{Started: at, Trigger: "manual", Pages: 214, Bytes: 38 << 20, Duration: 9 * time.Second, Result: HostingRunOK}
	bad := HostingRun{Started: at, Trigger: "change", Errors: 1, FirstError: "upload refused.", Result: HostingRunFailed}
	testFail := bad
	testFail.Trigger = HostingTriggerTest
	testOK := ok
	testOK.Trigger = HostingTriggerTest
	running := HostingRun{Started: at, Trigger: "manual", Result: HostingRunRunning}

	t.Run("newest ok", func(t *testing.T) {
		t.Parallel()
		got := hostingLast([]HostingRun{ok, bad})
		if !strings.HasPrefix(got, "214 pages") || strings.Contains(got, "Failed") {
			t.Errorf("got %q", got)
		}
	})
	t.Run("failed over ok", func(t *testing.T) {
		t.Parallel()
		got := hostingLast([]HostingRun{bad, ok})
		for _, want := range []string{"Failed 2 minutes ago", "upload refused", "last good: 214 pages"} {
			if !strings.Contains(got, want) {
				t.Errorf("%q lacks %q", got, want)
			}
		}
		if !strings.HasPrefix(got, "Failed ") || strings.Contains(got, "0 bytes") || strings.Contains(got, "refused.") {
			t.Errorf("got %q", got)
		}
	})
	t.Run("failed only", func(t *testing.T) {
		t.Parallel()
		got := hostingLast([]HostingRun{bad})
		if !strings.HasPrefix(got, "Failed ") || strings.Contains(got, "last good") || strings.Contains(got, "bytes") {
			t.Errorf("got %q", got)
		}
	})
	t.Run("test runs skipped", func(t *testing.T) {
		t.Parallel()
		got := hostingLast([]HostingRun{testFail, running, ok})
		if !strings.HasPrefix(got, "214 pages") {
			t.Errorf("got %q", got)
		}
	})
	t.Run("nothing to show", func(t *testing.T) {
		t.Parallel()
		if got := hostingLast([]HostingRun{running, testOK}); got != "" {
			t.Errorf("got %q", got)
		}
		if got := hostingLast(nil); got != "" {
			t.Errorf("got %q", got)
		}
	})
}

func sftpSetup() HostingSetup {
	return HostingSetup{
		Saved: true, Target: HostingTargetSFTP, Site: "https://example.com", Reach: HostingCheckUnknown,
		HostKey: testSFTPPrint, Remote: HostingCheckOK, RemoteDir: "public_html", HTAccess: true,
		Exact: 3, Wildcard: 1, HTAccessBlock: "# BEGIN Hatamoto\nRedirectMatch 301 ^/old/?$ /new\n# END Hatamoto\n",
	}
}

func setupView(s HostingSetup, tab string) hostingSetupView {
	return hostingSetupFrom(s, tab, "/admin", time.UTC)
}

func tabLabels(v hostingSetupView) (labels []string, active string) {
	for _, t := range v.Tabs {
		labels = append(labels, t.Label)
		if t.Active {
			active = t.Label
		}
	}
	return labels, active
}

func checkDetails(v hostingSetupView) map[string]hostingCheck {
	m := map[string]hostingCheck{}
	for _, c := range v.Checks {
		m[c.Name] = c
	}
	return m
}

func TestHostingSetupTabsIncludeApache(t *testing.T) {
	t.Parallel()

	labels, _ := tabLabels(setupView(HostingSetup{Saved: true, Target: HostingTargetDir}, ""))
	if strings.Join(labels, "|") != "Caddy|nginx|S3 + CDN|Apache / cPanel" {
		t.Errorf("tabs = %v", labels)
	}
	v := setupView(sftpSetup(), "apache")
	for _, tab := range v.Tabs {
		if tab.Label == "Apache / cPanel" && !strings.Contains(tab.Href, "?setup=apache#hosting-setup") {
			t.Errorf("apache tab href = %q", tab.Href)
		}
	}
}

func TestHostingSetupSFTPDefaultsToApache(t *testing.T) {
	t.Parallel()

	v := setupView(sftpSetup(), "")
	if _, active := tabLabels(v); active != "Apache / cPanel" || v.Kind != hostingSetupApache {
		t.Errorf("active %q, kind %q", active, v.Kind)
	}
	if _, active := tabLabels(setupView(sftpSetup(), "bogus")); active != "Apache / cPanel" {
		t.Errorf("a bogus tab leaves %q active", active)
	}
	if !strings.Contains(v.Config, "# BEGIN Hatamoto") || !strings.Contains(v.Help, "Preview from your redirect rules.") {
		t.Errorf("config %q, help %q", v.Config, v.Help)
	}
}

func TestHostingSetupApacheChecks(t *testing.T) {
	t.Parallel()

	c := checkDetails(setupView(sftpSetup(), ""))
	if len(c) != 3 {
		t.Fatalf("%d checks, want 3", len(c))
	}
	if h := c["Host key"]; h.Detail != "Trusted: "+testSFTPPrint+"." || h.Badge.Kind != "success" {
		t.Errorf("trusted: %+v", h)
	}
	if r := c["Remote folder"]; r.Detail != "The last export wrote to public_html." || r.Badge.Kind != "success" {
		t.Errorf("remote ok: %+v", r)
	}
	if r := c["Redirects"]; r.Detail != "Written to .htaccess as real redirects: 3 exact, 1 wildcard. mod_rewrite is needed only for wildcard rules." || r.Badge.Kind != "success" {
		t.Errorf("redirects on: %+v", r)
	}

	s := sftpSetup()
	s.HostKey, s.Remote, s.HTAccess = "", HostingCheckFailed, false
	c = checkDetails(setupView(s, ""))
	if h := c["Host key"]; h.Detail != "Not trusted. Use Trust host key under Target." || h.Badge.Kind != "warning" {
		t.Errorf("untrusted: %+v", h)
	}
	if r := c["Remote folder"]; r.Detail != "The last export to public_html failed. See the log below." || r.Badge.Kind != "warning" {
		t.Errorf("remote failed: %+v", r)
	}
	if r := c["Redirects"]; r.Detail != "Write .htaccess is off, so redirects are meta-refresh pages." || r.Badge.Kind != "warning" {
		t.Errorf("redirects off: %+v", r)
	}

	s.Remote = HostingCheckUnknown
	if r := checkDetails(setupView(s, ""))["Remote folder"]; r.Detail != "Run Test export to check." || r.Badge.Kind != "info" {
		t.Errorf("remote unknown: %+v", r)
	}
	for _, c := range setupView(s, "").Checks {
		if c.Badge.Label == "" {
			t.Errorf("%s has no badge label", c.Name)
		}
	}
}

func TestHostingSetupApacheOtherTargetNote(t *testing.T) {
	t.Parallel()

	for _, target := range []string{HostingTargetDir, HostingTargetS3} {
		v := setupView(HostingSetup{Saved: true, Target: target}, "apache")
		if v.Note != "The Apache / cPanel setup is for the SFTP target. Choose SFTP under Publish to." || len(v.Checks) != 0 || v.Config != "" || v.Kind != "" {
			t.Errorf("%s: %+v", target, v)
		}
		if _, active := tabLabels(v); active != "Apache / cPanel" {
			t.Errorf("%s: active %q", target, active)
		}
	}
}

func TestHostingSetupSFTPOnCaddyTabNote(t *testing.T) {
	t.Parallel()

	for _, tab := range []string{"caddy", "nginx", "s3"} {
		s := sftpSetup()
		s.Caddy, s.Nginx = "caddy config", "nginx config"
		v := setupView(s, tab)
		if v.Note != "Your files go to your host over SFTP and its web server serves them. See the Apache / cPanel tab." || v.Config != "" || v.Kind != "" || len(v.Checks) != 0 {
			t.Errorf("%s: %+v", tab, v)
		}
	}
}

// The page: the SFTP fields, the Trust form, the three-way target options and the
// Apache tab all render, and neither the password nor the key reaches the page.
func TestHostingPageSFTP(t *testing.T) {
	t.Parallel()

	f := sftpHosting()
	f.setup = sftpSetup()
	body := newHostingRig(t, f).do("GET", hostingURL+"?setup=apache", "owner-token", nil, nil).Body.String()
	for _, want := range []string{
		`SFTP (cPanel / shared hosting)`, `value="sftp" checked`, `name="sftp_host" value="ssh.example.net"`, `name="sftp_port" value="22"`,
		`name="sftp_user" value="alice"`, `name="sftp_auth"`, `name="sftp_password"`, `name="sftp_key"`, `name="sftp_dir" value="public_html"`,
		`name="sftp_htaccess"`, `Saved. Leave blank to keep the saved password.`, testSFTPPrint,
		`id="hostkey-form" method="get" action="/admin/settings/hosting/host-key"`, `form="hostkey-form"`,
		"Apache / cPanel", "# BEGIN Hatamoto", "Host key", "Remote folder",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(body, testSFTPPassword) || strings.Contains(body, "sftp-key-body") {
		t.Error("the page holds a secret")
	}
	if strings.Contains(body, `aria-describedby="hosting-trust-why"`) {
		t.Error("Trust host key is disabled with a saved SFTP host")
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
}
