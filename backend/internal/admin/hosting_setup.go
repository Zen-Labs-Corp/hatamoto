package admin

import (
	"fmt"
	"strings"
	"time"

	"hatamoto.dev/core/internal/admin/ui"
)

// Tabs of the "Set up your web server" card, chosen by ?setup=.
const (
	hostingSetupCaddy = "caddy"
	hostingSetupNginx = "nginx"
	hostingSetupS3    = "s3"
	// hostingSetupApache is the tab of the SFTP target (cPanel / shared hosting).
	hostingSetupApache = "apache"
)

// hostingCheck is one line of the setup checklist.
type hostingCheck struct {
	Name, Detail string
	Badge        ui.Badge
}

// hostingSetupView is the "Set up your web server" card. It describes the
// saved settings, never the form being edited.
type hostingSetupView struct {
	Show   bool
	Tabs   []ui.Tab
	Kind   string // hostingSetupCaddy, hostingSetupNginx, hostingSetupS3 or hostingSetupApache; "" shows only the note
	Checks []hostingCheck
	// Config is the readonly text of the Caddy or nginx tab; Note says why
	// there is none; Error is a safe refusal from the generator.
	Config, Help, Note, Error string
	Headers                   []string // security headers for a CDN
}

func setupBadge(kind, label string) ui.Badge { return ui.Badge{Label: label, Kind: kind} }

// hostingSetupFrom builds the card. tab is the raw ?setup= value: only the
// four known ones count, anything else gets the default for the target.
func hostingSetupFrom(s HostingSetup, tab, adminPath string, loc *time.Location) hostingSetupView {
	v := hostingSetupView{Show: s.Saved, Headers: s.HeaderNames}
	if !v.Show {
		return v
	}
	switch tab {
	case hostingSetupCaddy, hostingSetupNginx, hostingSetupS3, hostingSetupApache:
	default:
		switch s.Target {
		case HostingTargetS3:
			tab = hostingSetupS3
		case HostingTargetSFTP:
			tab = hostingSetupApache
		default:
			tab = hostingSetupCaddy
		}
	}
	v.Kind = tab
	for _, t := range []struct{ id, label string }{{hostingSetupCaddy, "Caddy"}, {hostingSetupNginx, "nginx"}, {hostingSetupS3, "S3 + CDN"}, {hostingSetupApache, "Apache / cPanel"}} {
		v.Tabs = append(v.Tabs, ui.Tab{Label: t.label, Href: adminPath + hostingPath + "?setup=" + t.id + "#hosting-setup", Active: t.id == tab})
	}
	switch {
	case s.Target == HostingTargetSFTP && tab == hostingSetupApache:
		v.Checks = hostingApacheChecks(s, loc)
		v.Config = s.HTAccessBlock
		if s.HTAccess && s.HTAccessBlock == "" {
			v.Note = "There are no redirect rules, so the export writes no .htaccess block."
		}
	case s.Target == HostingTargetSFTP:
		// Another web server's tab: the files go to the host, which serves them.
		v.Kind = ""
		v.Note = "Your files go to your host over SFTP and its web server serves them. See the Apache / cPanel tab."
	case tab == hostingSetupApache:
		v.Kind = ""
		v.Note = "The Apache / cPanel setup is for the SFTP target. Choose SFTP under Publish to."
	default:
		v.Checks = hostingChecks(s, tab == hostingSetupS3, loc)
		switch {
		case tab == hostingSetupS3:
		case s.Target == HostingTargetS3:
			v.Note = "The files are in a bucket, so there is no web server config to copy. See S3 + CDN."
		case s.GenError != "":
			v.Error = s.GenError
		case tab == hostingSetupNginx:
			v.Config = s.Nginx
		default:
			v.Config = s.Caddy
		}
	}
	if v.Kind == hostingSetupApache && v.Config != "" {
		v.Help = "Preview from your redirect rules. Each export writes this block between # BEGIN Hatamoto and # END Hatamoto and keeps the rest of .htaccess."
	}
	if v.Config != "" && s.Upstream != "" {
		v.Help = fmt.Sprintf("Upstream %s is this server's listen address. Change it if your web server reaches it differently. Copy the config again after you change captcha, plugins or the theme.", s.Upstream)
	}
	return v
}

// hostingChecks lists the rows of the card. onS3Tab is true for the S3 + CDN
// tab, which has no folder and no config to point at.
func hostingChecks(s HostingSetup, onS3Tab bool, loc *time.Location) []hostingCheck {
	var out []hostingCheck
	site := s.Site
	if site == "" {
		site = "The site address"
	}
	switch s.Reach {
	case HostingCheckOK:
		out = append(out, hostingCheck{"Site address", site + " answers." + hostingCheckedAt(s.ReachAt, loc), setupBadge("success", "OK")})
	case HostingCheckFailed:
		out = append(out, hostingCheck{"Site address", site + " did not answer." + hostingCheckedAt(s.ReachAt, loc) + " Check the DNS record and the web server.", setupBadge("warning", "Check")})
	default:
		out = append(out, hostingCheck{"Site address", "Not checked yet. It runs after the next export on this server.", setupBadge("info", "Not checked")})
	}
	if s.Target != HostingTargetS3 && !onS3Tab {
		switch s.Folder {
		case HostingCheckOK:
			out = append(out, hostingCheck{"Folder", "The folder exists and the service user can write to it and to its parent.", setupBadge("success", "OK")})
		case HostingCheckMissing:
			out = append(out, hostingCheck{"Folder", "The folder does not exist yet. The first export creates it.", setupBadge("info", "Missing")})
		case HostingCheckNotWritable:
			out = append(out, hostingCheck{"Folder", "The service user cannot write to the folder's parent, where each export is built and swapped in, or to the folder itself.", setupBadge("warning", "Check")})
		default:
			out = append(out, hostingCheck{"Folder", "The folder is not checked on this system.", setupBadge("info", "Not checked")})
		}
	}
	headers := "Static files need the security headers Hatamoto normally sends."
	if s.Target == HostingTargetS3 || onS3Tab {
		headers += " Add them at your CDN"
		if !onS3Tab {
			headers += " (see S3 + CDN)"
		}
		headers += "."
	} else {
		headers += " They are included in the config below."
	}
	out = append(out, hostingCheck{"Security headers", headers, setupBadge("warning", "Add")})
	out = append(out, hostingRedirectsCheck(s, onS3Tab))
	return out
}

// hostingApacheChecks lists the rows of the Apache / cPanel tab. It never
// connects: the remote folder row reads the last run to this target.
func hostingApacheChecks(s HostingSetup, loc *time.Location) []hostingCheck {
	var out []hostingCheck
	if s.HostKey != "" {
		out = append(out, hostingCheck{"Host key", "Trusted: " + s.HostKey + ".", setupBadge("success", "OK")})
	} else {
		out = append(out, hostingCheck{"Host key", "Not trusted. Use Trust host key under Target.", setupBadge("warning", "Check")})
	}
	switch s.Remote {
	case HostingCheckOK:
		out = append(out, hostingCheck{"Remote folder", "The last export wrote to " + s.RemoteDir + ".", setupBadge("success", "OK")})
	case HostingCheckFailed:
		out = append(out, hostingCheck{"Remote folder", "The last export to " + s.RemoteDir + " failed. See the log below.", setupBadge("warning", "Check")})
	default:
		out = append(out, hostingCheck{"Remote folder", "Run Test export to check.", setupBadge("info", "Not checked")})
	}
	if s.HTAccess {
		out = append(out, hostingCheck{"Redirects", fmt.Sprintf("Written to .htaccess as real redirects: %d exact, %d wildcard. mod_rewrite is needed only for wildcard rules.", s.Exact, s.Wildcard), setupBadge("success", "OK")})
	} else {
		out = append(out, hostingCheck{"Redirects", "Write .htaccess is off, so redirects are meta-refresh pages.", setupBadge("warning", "Check")})
	}
	return out
}

func hostingRedirectsCheck(s HostingSetup, onS3Tab bool) hostingCheck {
	const name = "Redirects"
	if s.RedirectsSplit {
		if s.Exact+s.Wildcard == 0 {
			return hostingCheck{name, "The last export has no redirect rules.", setupBadge("success", "OK")}
		}
		d := fmt.Sprintf("A plain web server ignores _redirects: %d exact %s become meta-refresh pages and %d wildcard %s lost.",
			s.Exact, plural(s.Exact, "rule", "rules"), s.Wildcard, plural(s.Wildcard, "rule is", "rules are"))
		if !onS3Tab {
			d += " The config below includes them."
		}
		if len(s.Skipped) > 0 {
			d += " Left out: " + strings.Join(s.Skipped, "; ") + "."
		}
		return hostingCheck{name, d, setupBadge("warning", "Check")}
	}
	if s.RedirectsTotal == 0 {
		return hostingCheck{name, "The last export has no redirect rules.", setupBadge("success", "OK")}
	}
	return hostingCheck{name, fmt.Sprintf("The last export has %d redirect %s. A plain bucket ignores _redirects: exact rules fall back to meta-refresh pages and wildcard rules are lost.",
		s.RedirectsTotal, plural(s.RedirectsTotal, "rule", "rules")), setupBadge("warning", "Check")}
}

func hostingCheckedAt(at time.Time, loc *time.Location) string {
	if at.IsZero() {
		return ""
	}
	return " Checked " + at.In(loc).Format("2 Jan 15:04") + "."
}
