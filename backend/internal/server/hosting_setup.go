package server

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/form"
	"hatamoto.dev/core/internal/importjob"
	"hatamoto.dev/core/internal/redirect"
	"hatamoto.dev/core/internal/staticexport"
	"hatamoto.dev/core/internal/statichost"
	"hatamoto.dev/core/internal/webconf"
)

// maxSetupRedirectsBody bounds the _redirects read on the Hosting page.
const maxSetupRedirectsBody = 1 << 20

// hostingSetupDeps are the server values the setup card is built from. A nil
// func means "not checked".
type hostingSetupDeps struct {
	adminPath func() string
	apiPrefix string
	upstream  string // dialable host:port
	maxBodyMB int    // largest public upload the web server must pass; 0 = unknown
	adminMB   int    // largest admin upload (import, package, media); 0 = unknown
	headers   func(ctx context.Context, live string) []secHeader
	folder    func(dir string) string
	reach     func() (string, time.Time)
	// rules lists the active redirect rules for the .htaccess preview; nil = no preview.
	rules func(ctx context.Context) ([]*redirect.Redirect, error)
}

// uploadLimitMB is the request body limit a web server in front of Hatamoto
// needs on every path: the largest public upload (a media or form file), plus
// room for the multipart framing and the other fields. Hatamoto enforces each
// endpoint's own limit; the web server must only not refuse first.
func uploadLimitMB(mediaMaxMB int) int {
	return max(mediaMaxMB, form.MaxFileUploadMB) + 5
}

// adminUploadLimitMB is the limit for the admin path only. The WordPress export
// is larger than the plugin, theme and CSV uploads, so it sets the floor.
func adminUploadLimitMB(mediaMaxMB int) int {
	return max(uploadLimitMB(mediaMaxMB), importjob.MaxWXRBytes>>20+5)
}

// upstreamAddr is the address a web server on the same box dials to reach
// Hatamoto: an unspecified listen address becomes loopback.
func upstreamAddr(host string, port int) string {
	switch host {
	case "", "0.0.0.0", "::", "[::]":
		host = "127.0.0.1"
	}
	host = strings.Trim(host, "[]")
	return net.JoinHostPort(host, strconv.Itoa(port))
}

// Setup describes the web-server setup for the SAVED settings. It reads only
// the saved directory's _redirects file and makes no network call.
func (h *hostingAdmin) Setup(ctx context.Context) (admin.HostingSetup, error) {
	set, err := h.load(ctx)
	if err != nil {
		return admin.HostingSetup{}, err
	}
	d := h.setup
	site, _ := statichost.NormalizeOrigin(h.siteURL(ctx))
	out := admin.HostingSetup{
		Saved:    hostingTargetSaved(set),
		Target:   set.Target,
		SameHost: strings.TrimSpace(set.LiveOrigin) == "",
		Site:     site,
		Upstream: d.upstream,
		Folder:   admin.HostingCheckUnknown,
		Reach:    admin.HostingCheckUnknown,
	}
	if d.reach != nil {
		out.Reach, out.ReachAt = d.reach()
	}
	live := ""
	if !out.SameHost {
		live, _ = statichost.NormalizeOrigin(set.LiveOrigin)
	}
	var headers []webconf.Header
	if d.headers != nil {
		for _, x := range d.headers(ctx, live) {
			headers = append(headers, webconf.Header{Name: x.Name, Value: x.Value})
			out.HeaderNames = append(out.HeaderNames, x.Name)
		}
	}
	if set.Target == staticexport.TargetSFTP {
		h.setupSFTP(ctx, set, &out)
		return out, nil
	}
	if set.Target != staticexport.TargetS3 && set.DirPath != "" {
		if d.folder != nil {
			out.Folder = d.folder(set.DirPath)
		}
		body := readRedirects(set.DirPath)
		rules, exact, wild := webconf.ParseRedirects(body)
		out.Exact, out.Wildcard, out.RedirectsSplit = exact, wild, true
		out.RedirectsTotal = exact + wild
		if site == "" {
			out.GenError = "Set the site address in Site settings first."
			return out, nil
		}
		gen, err := webconf.Generate(webconf.Input{
			Site: site, Live: live, Root: set.DirPath, Upstream: d.upstream, MaxBodyMB: d.maxBodyMB,
			AdminPath: d.adminPath(), AdminBodyMB: d.adminMB,
			Proxy:   statichost.ProxyPaths(d.adminPath(), d.apiPrefix),
			Headers: headers, Redirects: rules,
		})
		if errors.Is(err, webconf.ErrUnsafe) {
			what := strings.TrimPrefix(err.Error(), webconf.ErrUnsafe.Error()+": ") // generator text, safe to show
			out.GenError = "The " + what + " contains characters a web server config cannot hold."
		} else if err == nil {
			out.Caddy, out.Nginx, out.Skipped = gen.Caddy, gen.Nginx, gen.Skipped
		}
		return out, nil
	}
	// S3: there is no folder to read; the last good run knows the rule count.
	if runs, err := h.runs.List(ctx, 20); err == nil {
		for _, r := range runs {
			if r.Status == admin.HostingRunOK {
				out.RedirectsTotal = r.Redirects
				break
			}
		}
	}
	return out, nil
}

// setupSFTP fills the SFTP part of the setup card. It makes no network call:
// the remote check reads the run log and the preview comes from the saved
// redirect rules (the export also writes redirects it finds while crawling).
func (h *hostingAdmin) setupSFTP(ctx context.Context, set staticexport.Settings, out *admin.HostingSetup) {
	out.HostKey, out.HTAccess, out.RemoteDir = set.SFTPHostKey, set.SFTPHTAccess, set.SFTPDir
	out.Remote = admin.HostingCheckUnknown
	// Runs record the cleaned folder (public_html/ is public_html).
	dir := set.SFTPDir
	if clean, err := staticexport.ValidateRemoteDir(dir); err == nil {
		dir = clean
	}
	key := staticexport.SFTPKey(set.SFTPUser, set.SFTPHost, set.SFTPPort, dir)
	if runs, err := h.runs.List(ctx, 20); err == nil {
		for _, r := range runs {
			if r.Target != key {
				continue
			}
			switch r.Status {
			case admin.HostingRunOK:
				out.Remote = admin.HostingCheckOK
			case admin.HostingRunFailed:
				out.Remote = admin.HostingCheckFailed
			default:
				continue // still running: look at the one before
			}
			break
		}
	}
	if h.setup.rules == nil {
		return
	}
	rules, err := h.setup.rules(ctx)
	if err != nil {
		return
	}
	var exact []staticexport.Line
	var patterns []*redirect.Redirect
	for _, r := range rules {
		if redirect.IsPatternSource(r.SourcePath) {
			patterns = append(patterns, r)
		} else {
			exact = append(exact, staticexport.Line{From: r.SourcePath, To: r.TargetURL, Status: r.StatusCode})
		}
	}
	block, skipped := staticexport.RenderHTAccess(exact, patterns)
	out.HTAccessBlock, out.Skipped = string(block), skipped
	out.Exact, out.Wildcard, out.RedirectsSplit = len(exact), len(patterns), true
	out.RedirectsTotal = len(exact) + len(patterns)
}

// readRedirects returns the _redirects file of the last export in dir, "" when
// it is missing or unreadable (the card then counts no rules).
func readRedirects(dir string) []byte {
	f, err := os.Open(filepath.Join(dir, staticexport.RedirectsFile))
	if err != nil {
		return nil
	}
	defer f.Close()
	body, err := io.ReadAll(io.LimitReader(f, maxSetupRedirectsBody))
	if err != nil {
		return nil
	}
	return body
}
