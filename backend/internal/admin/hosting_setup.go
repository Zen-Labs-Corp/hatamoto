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
	Kind   string // hostingSetupCaddy, hostingSetupNginx or hostingSetupS3
	Checks []hostingCheck
	// Config is the readonly text of the Caddy or nginx tab; Note says why
	// there is none; Error is a safe refusal from the generator.
	Config, Help, Note, Error string
	Headers                   []string // security headers for a CDN
}

func setupBadge(kind, label string) ui.Badge { return ui.Badge{Label: label, Kind: kind} }

// hostingSetupFrom builds the card. tab is the raw ?setup= value: only the
// three known ones count, anything else gets the default for the target.
func hostingSetupFrom(s HostingSetup, tab, adminPath string, loc *time.Location) hostingSetupView {
	v := hostingSetupView{Show: s.Saved, Headers: s.HeaderNames}
	if !v.Show {
		return v
	}
	switch tab {
	case hostingSetupCaddy, hostingSetupNginx, hostingSetupS3:
		v.Kind = tab
	default:
		v.Kind = hostingSetupCaddy
		if s.Target == HostingTargetS3 {
			v.Kind = hostingSetupS3
		}
	}
	for _, t := range []struct{ id, label string }{{hostingSetupCaddy, "Caddy"}, {hostingSetupNginx, "nginx"}, {hostingSetupS3, "S3 + CDN"}} {
		v.Tabs = append(v.Tabs, ui.Tab{Label: t.label, Href: adminPath + hostingPath + "?setup=" + t.id + "#hosting-setup", Active: t.id == v.Kind})
	}
	v.Checks = hostingChecks(s, loc)
	switch {
	case v.Kind == hostingSetupS3:
	case s.Target == HostingTargetS3:
		v.Note = "The files are in a bucket, so there is no web server config to copy. See S3 + CDN."
	case s.GenError != "":
		v.Error = s.GenError
	case v.Kind == hostingSetupNginx:
		v.Config = s.Nginx
	default:
		v.Config = s.Caddy
	}
	if v.Config != "" && s.Upstream != "" {
		v.Help = fmt.Sprintf("Upstream %s is this server's listen address. Change it if your web server reaches it differently. Copy the config again after you change captcha, plugins or the theme.", s.Upstream)
	}
	return v
}

func hostingChecks(s HostingSetup, loc *time.Location) []hostingCheck {
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
	if s.Target != HostingTargetS3 {
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
	if s.Target == HostingTargetS3 {
		headers += " Add them at your CDN (see S3 + CDN)."
	} else {
		headers += " They are included in the config below."
	}
	out = append(out, hostingCheck{"Security headers", headers, setupBadge("warning", "Add")})
	out = append(out, hostingRedirectsCheck(s))
	return out
}

func hostingRedirectsCheck(s HostingSetup) hostingCheck {
	const name = "Redirects"
	if s.RedirectsSplit {
		if s.Exact+s.Wildcard == 0 {
			return hostingCheck{name, "The last export has no redirect rules.", setupBadge("success", "OK")}
		}
		d := fmt.Sprintf("A plain web server ignores _redirects: %d exact %s become meta-refresh pages and %d wildcard %s lost. The config below includes them.",
			s.Exact, plural(s.Exact, "rule", "rules"), s.Wildcard, plural(s.Wildcard, "rule is", "rules are"))
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
